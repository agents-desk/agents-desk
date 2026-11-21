package conversation

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/member"
	"agents-desk/internal/team"
)

// Assignment 是指派的请求：团队收件箱和负责人，不传的保持原样。
type Assignment struct {
	TeamID     *string `json:"team_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"团队收件箱的 ID；空字符串表示移出团队收件箱；不传表示保持原样"`
	AssigneeID *string `json:"assignee_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"负责人（成员 ID）；空字符串表示没有负责人；不传时，转到别的团队收件箱则清空负责人，否则保持原样"`
}

// AssignInput 是指派的 HTTP 请求。
type AssignInput struct {
	ID   string `path:"id" format:"uuid"`
	Body Assignment
}

// Assign 指派会话：修改它的团队收件箱和负责人。自动分配（E1）也调用它。
type Assign struct {
	db   *bun.DB
	down Downstream
}

// NewAssign 返回指派会话的动作；down 是时间线有新内容时通知的下游。
func NewAssign(db *bun.DB, down Downstream) *Assign { return &Assign{db: db, down: down} }

// Handle 由 sub 按 p 指派他所在工作区中的会话 id，时间线中追加 conversation.assigned，记下原来和新的团队收件箱、负责人；
// 没有变化时什么也不做。转到别的团队收件箱而没有指定负责人时，清空负责人，由新的团队收件箱领取或分配。
//
// 权限：领取没有负责人的会话（只把负责人设为自己），以及处理自己负责的会话，有 conversations:reply 就可以；
// 其他情况要有 conversations:assign（把会话改派给别人），否则返回 ErrForbidden。
// 团队收件箱不属于这个工作区时返回 team.ErrNotFound（字段 team_id），负责人不是这个工作区的成员时返回
// member.ErrNotFound（字段 assignee_id）。
func (a *Assign) Handle(ctx context.Context, sub identity.Subject, id string, p Assignment) (*ConversationDTO, error) {
	return change(ctx, a.db, a.down, sub, id, auth.ConversationsReply, func(ctx context.Context, tx bun.IDB, c *Conversation) ([]*Item, error) {
		// 新的团队收件箱
		toTeam := c.TeamID
		if p.TeamID != nil {
			toTeam = nil
			if *p.TeamID != "" {
				n, err := tx.NewSelect().Table("teams").Where("id = ? AND tenant_id = ?", *p.TeamID, sub.Tenant).Count(ctx)
				if err != nil {
					return nil, errors.Wrap(err, "find team")
				}
				if n == 0 {
					return nil, team.ErrNotFound.New(errorx.Field("team_id"))
				}
				toTeam = p.TeamID
			}
		}
		teamChanged := !sameID(c.TeamID, toTeam)

		// 新的负责人：只能是这个工作区的成员
		fromType, fromID := c.AssigneeType, c.AssigneeID
		toType, toID := fromType, fromID
		switch {
		case p.AssigneeID != nil && *p.AssigneeID == "":
			toType, toID = "", ""
		case p.AssigneeID != nil:
			ref := identity.Ref{Type: member.Type, ID: *p.AssigneeID}
			if _, err := member.RoleOf(ctx, authz.NewStore(tx, auth.Catalog), ref, sub.Tenant); err != nil {
				if errors.Is(err, member.ErrNotFound) {
					return nil, member.ErrNotFound.New(errorx.Field("assignee_id"))
				}
				return nil, err
			}
			toType, toID = MemberActor, *p.AssigneeID
		case teamChanged:
			toType, toID = "", ""
		}
		if !teamChanged && toType == fromType && toID == fromID {
			return nil, nil
		}

		// 权限：领取和处理自己负责的会话不需要改派的权限。要领取的会话已经被别人领取时（两人同时领取，先到者生效），
		// 没有改派权限的成员得到 ErrAlreadyAssigned
		mine := fromType == MemberActor && fromID == sub.ID
		claim := fromType == "" && toType == MemberActor && toID == sub.ID && !teamChanged
		if sub.Type != identity.System && !mine && !claim && !sub.Can(string(auth.ConversationsAssign)) {
			if toType == MemberActor && toID == sub.ID && fromType != "" {
				return nil, ErrAlreadyAssigned.New()
			}
			return nil, ErrForbidden.New()
		}

		fromTeam := c.TeamID
		c.TeamID, c.AssigneeType, c.AssigneeID = toTeam, toType, toID
		ev := assignedEvent(sub, c, fromTeam, fromType, fromID, ReasonManual)
		// 转到别的团队收件箱而没有负责人时，按新团队收件箱的分配方式分配；系统的分配记在后面
		if teamChanged && toType == "" {
			next, err := roundRobin(ctx, tx, c)
			if err != nil || next == nil {
				return []*Item{ev}, err
			}
			return []*Item{ev, next}, nil
		}
		return []*Item{ev}, nil
	})
}

// sameID 报告两个可以为空的 ID 是否相同。
func sameID(x, y *string) bool {
	if x == nil || y == nil {
		return x == y
	}
	return *x == *y
}

// actorData 返回系统事件中的负责人：没有负责人时为 nil。
func actorData(typ ActorType, id string) any {
	if typ == "" {
		return nil
	}
	return map[string]any{"type": string(typ), "id": id}
}

// HTTP 处理 PUT /t/{slug}/api/conversations/{id}/assignment。
func (a *Assign) HTTP(ctx context.Context, in *AssignInput) (*ConversationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &ConversationOutput{Body: *dto}, nil
}
