package conversation

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// RoundDTO 是接口中服务型会话的一轮服务记录，包含进行中和已关闭的轮次，时长由原始时间戳实时算出。
type RoundDTO struct {
	ID                string     `json:"id"`
	Round             int        `json:"round" doc:"该会话的第几轮服务，从 1 递增"`
	TeamID            *string    `json:"team_id,omitempty" doc:"本轮归属的团队收件箱 ID"`
	Assignee          *Actor     `json:"assignee,omitempty" doc:"本轮负责人"`
	FirstReplyBy      *Actor     `json:"first_reply_by,omitempty" doc:"本轮首次回复的发起者"`
	ClosedBy          *Actor     `json:"closed_by,omitempty" doc:"执行关闭的发起者，未关闭时为空"`
	OpenedAt          time.Time  `json:"opened_at" doc:"本轮打开或重新打开的时间"`
	FirstReplyAt      *time.Time `json:"first_reply_at,omitempty" doc:"本轮首次回复的时间，尚未回复时为空"`
	ClosedAt          *time.Time `json:"closed_at,omitempty" doc:"本轮关闭的时间，进行中时为空"`
	FirstReplySeconds *int64     `json:"first_reply_seconds,omitempty" doc:"本轮首次回复时长（秒），尚未回复时为空"`
	ResolutionSeconds *int64     `json:"resolution_seconds,omitempty" doc:"本轮解决时长（秒），尚未关闭时为空"`
	Rating            *Rating    `json:"rating,omitempty" doc:"客户对本轮服务的评价"`
}

// RoundsOutput 是会话服务轮次列表的响应。
type RoundsOutput struct {
	Body []RoundDTO
}

// ListRounds 读取一个会话的全部服务轮次记录（含当前进行中的轮次），按轮次从小到大排列。
type ListRounds struct {
	db *bun.DB
}

// NewListRounds 返回读取会话服务轮次的动作。
func NewListRounds(db *bun.DB) *ListRounds { return &ListRounds{db: db} }

// Handle 返回 sub 所在工作区中会话 id 的全部服务轮次记录；会话不存在或看不到时返回 ErrNotFound，没有权限时返回 ErrForbidden。
func (a *ListRounds) Handle(ctx context.Context, sub identity.Subject, id string) ([]RoundDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	if _, err := findVisible(ctx, a.db, sub, id, false); err != nil {
		return nil, err
	}
	var rows []Round
	err := a.db.NewSelect().Model(&rows).
		Where("tenant_id = ? AND conversation_id = ?", sub.Tenant, id).
		OrderExpr("round ASC").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list conversation rounds")
	}
	out := make([]RoundDTO, len(rows))
	for i, r := range rows {
		d := RoundDTO{
			ID:           r.ID,
			Round:        r.Round,
			TeamID:       r.TeamID,
			OpenedAt:     r.OpenedAt,
			FirstReplyAt: r.FirstReplyAt,
			ClosedAt:     r.ClosedAt,
		}
		if r.AssigneeType != "" {
			d.Assignee = &Actor{Type: r.AssigneeType, ID: r.AssigneeID}
		}
		if r.FirstReplyByType != "" {
			d.FirstReplyBy = &Actor{Type: r.FirstReplyByType, ID: r.FirstReplyByID}
		}
		if r.ClosedByType != "" {
			d.ClosedBy = &Actor{Type: r.ClosedByType, ID: r.ClosedByID}
		}
		if r.FirstReplyAt != nil {
			sec := max(int64(r.FirstReplyAt.Sub(r.OpenedAt).Seconds()), 0)
			d.FirstReplySeconds = &sec
		}
		if r.ClosedAt != nil {
			sec := max(int64(r.ClosedAt.Sub(r.OpenedAt).Seconds()), 0)
			d.ResolutionSeconds = &sec
		}
		if r.Rating != nil && r.RatedAt != nil {
			d.Rating = &Rating{Score: *r.Rating, Comment: r.RatingComment, RatedAt: *r.RatedAt}
		}
		out[i] = d
	}
	return out, nil
}

// HTTP 处理 GET /t/{slug}/api/conversations/{id}/rounds。
func (a *ListRounds) HTTP(ctx context.Context, in *IDInput) (*RoundsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	out, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &RoundsOutput{Body: out}, nil
}
