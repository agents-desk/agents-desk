package conversation

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
)

// ClosureDTO 是接口中会话一次关闭的轮次记录，时长由原始时间戳实时算出。
type ClosureDTO struct {
	ID                string     `json:"id"`
	Round             int        `json:"round" doc:"该会话的第几次关闭，从 1 递增"`
	TeamID            *string    `json:"team_id,omitempty" doc:"关闭时归属的团队收件箱 ID"`
	Assignee          *Actor     `json:"assignee,omitempty" doc:"关闭时的负责人"`
	ClosedBy          Actor      `json:"closed_by" doc:"执行关闭的发起者"`
	OpenedAt          time.Time  `json:"opened_at" doc:"本轮打开或重新打开的时间"`
	FirstReplyAt      *time.Time `json:"first_reply_at,omitempty" doc:"本轮首次回复的时间，未回复即关闭时为空"`
	ClosedAt          time.Time  `json:"closed_at" doc:"本轮关闭的时间"`
	FirstReplySeconds *int64     `json:"first_reply_seconds,omitempty" doc:"本轮首次回复时长（秒），未回复即关闭时为空"`
	ResolutionSeconds int64      `json:"resolution_seconds" doc:"本轮解决时长（秒）"`
	Rating            *Rating    `json:"rating,omitempty" doc:"客户对本轮关闭的评价"`
}

// ClosuresOutput 是会话关闭轮次列表的响应。
type ClosuresOutput struct {
	Body []ClosureDTO
}

// ListClosures 读取一个会话的历次关闭记录，按轮次从小到大排列。
type ListClosures struct {
	db *bun.DB
}

// NewListClosures 返回读取会话关闭记录的动作。
func NewListClosures(db *bun.DB) *ListClosures { return &ListClosures{db: db} }

// Handle 返回 sub 所在工作区中会话 id 的历次关闭记录；会话不存在或看不到时返回 ErrNotFound，没有权限时返回 ErrForbidden。
func (a *ListClosures) Handle(ctx context.Context, sub identity.Subject, id string) ([]ClosureDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	if _, err := findVisible(ctx, a.db, sub, id, false); err != nil {
		return nil, err
	}
	var rows []Closure
	err := a.db.NewSelect().Model(&rows).
		Where("tenant_id = ? AND conversation_id = ?", sub.Tenant, id).
		OrderExpr("round ASC").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list conversation closures")
	}
	out := make([]ClosureDTO, len(rows))
	for i, r := range rows {
		d := ClosureDTO{
			ID:                r.ID,
			Round:             r.Round,
			TeamID:            r.TeamID,
			ClosedBy:          Actor{Type: r.ClosedByType, ID: r.ClosedByID},
			OpenedAt:          r.OpenedAt,
			FirstReplyAt:      r.FirstReplyAt,
			ClosedAt:          r.ClosedAt,
			ResolutionSeconds: max(int64(r.ClosedAt.Sub(r.OpenedAt).Seconds()), 0),
		}
		if r.AssigneeType != "" {
			d.Assignee = &Actor{Type: r.AssigneeType, ID: r.AssigneeID}
		}
		if r.FirstReplyAt != nil {
			sec := max(int64(r.FirstReplyAt.Sub(r.OpenedAt).Seconds()), 0)
			d.FirstReplySeconds = &sec
		}
		if r.Rating != nil && r.RatedAt != nil {
			d.Rating = &Rating{Score: *r.Rating, Comment: r.RatingComment, RatedAt: *r.RatedAt}
		}
		out[i] = d
	}
	return out, nil
}

// HTTP 处理 GET /t/{slug}/api/conversations/{id}/closures。
func (a *ListClosures) HTTP(ctx context.Context, in *IDInput) (*ClosuresOutput, error) {
	sub, _ := identity.FromContext(ctx)
	out, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &ClosuresOutput{Body: out}, nil
}
