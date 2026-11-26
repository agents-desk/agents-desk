package conversation

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/tickettype"
)

// UpdateTicketParams 是修改工单标题、描述和可见范围的请求体；不传的字段保持不变。
type UpdateTicketParams struct {
	Title       *string                `json:"title,omitempty" minLength:"1" maxLength:"120" doc:"新的工单标题"`
	Description *string                `json:"description,omitempty" maxLength:"5000" doc:"新的工单描述"`
	Visibility  *tickettype.Visibility `json:"visibility,omitempty" doc:"新的可见范围（all 或 members）"`
}

// UpdateTicketInput 是修改工单信息的 HTTP 请求。
type UpdateTicketInput struct {
	ID   string `path:"id" format:"uuid"`
	Body UpdateTicketParams
}

// UpdateTicket 修改工单的标题、描述和可见范围，并在时间线记入系统事件。
type UpdateTicket struct {
	db   *bun.DB
	down Downstream
}

// NewUpdateTicket 返回修改工单信息的动作。
func NewUpdateTicket(db *bun.DB, down Downstream) *UpdateTicket {
	return &UpdateTicket{db: db, down: down}
}

// Handle 修改 sub 在工作区中可见工单 id 的标题、描述和可见范围；无变化时什么也不做。
func (a *UpdateTicket) Handle(ctx context.Context, sub identity.Subject, id string, p UpdateTicketParams) (*TicketDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, tk, err := findVisibleTicket(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		changed := map[string]any{}
		cols := make([]string, 0, 4)
		if p.Title != nil {
			v := strings.TrimSpace(*p.Title)
			if v != tk.Title {
				changed["title"] = map[string]any{"from": tk.Title, "to": v}
				tk.Title = v
				cols = append(cols, "title")
			}
		}
		if p.Description != nil {
			v := strings.TrimSpace(*p.Description)
			if v != tk.Description {
				changed["description"] = true
				tk.Description = v
				cols = append(cols, "description")
			}
		}
		if p.Visibility != nil && *p.Visibility != tk.Visibility {
			changed["visibility"] = map[string]any{"from": string(tk.Visibility), "to": string(*p.Visibility)}
			tk.Visibility = *p.Visibility
			cols = append(cols, "visibility")
		}
		if len(cols) == 0 {
			return nil
		}
		cols = append(cols, "updated_at")
		if _, err := tx.NewUpdate().Model(tk).Column(cols...).WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update ticket meta")
		}
		c.LastMessageAt = now()
		ev := event(sub, "ticket.updated", map[string]any{"changes": changed})
		return commit(ctx, tx, a.down, c, ev)
	})
	if err != nil {
		return nil, err
	}
	return NewGetTicket(a.db).Handle(ctx, sub, id)
}

// HTTP 处理 PATCH /t/{slug}/api/tickets/{id}。
func (a *UpdateTicket) HTTP(ctx context.Context, in *UpdateTicketInput) (*TicketOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &TicketOutput{Body: *dto}, nil
}

// TicketStatusInput 是修改工单状态的 HTTP 请求。
type TicketStatusInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Status TicketStatus `json:"status" doc:"目标状态：submitted、in_progress、waiting_on_customer 或 resolved"`
	}
}

// SetTicketStatus 修改工单状态，记录解决时间并在时间线追加 ticket.status_changed 系统事件。
type SetTicketStatus struct {
	db   *bun.DB
	down Downstream
}

// NewSetTicketStatus 返回修改工单状态的动作。
func NewSetTicketStatus(db *bun.DB, down Downstream) *SetTicketStatus {
	return &SetTicketStatus{db: db, down: down}
}

// Handle 修改 sub 在工作区中可见工单 id 的状态为 status；无变化时什么也不做。
// 切到 resolved 时记录 resolved_at，从 resolved 切回未解决状态时清空 resolved_at。
func (a *SetTicketStatus) Handle(ctx context.Context, sub identity.Subject, id string, status TicketStatus) (*TicketDTO, error) {
	if err := authorize(sub, auth.ConversationsReply); err != nil {
		return nil, err
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, tk, err := findVisibleTicket(ctx, tx, sub, id, true)
		if err != nil {
			return err
		}
		from := TicketStatus(c.Status)
		if from == status {
			return nil
		}
		at := now()
		c.Status = Status(status)
		c.LastMessageAt = at
		if status == TicketResolved {
			tk.ResolvedAt = &at
		} else {
			tk.ResolvedAt = nil
		}
		if _, err := tx.NewUpdate().Model(tk).Column("resolved_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update ticket resolved_at")
		}
		ev := event(sub, "ticket.status_changed", map[string]any{
			"from": string(from),
			"to":   string(status),
		})
		return commit(ctx, tx, a.down, c, ev)
	})
	if err != nil {
		return nil, err
	}
	return NewGetTicket(a.db).Handle(ctx, sub, id)
}

// HTTP 处理 PUT /t/{slug}/api/tickets/{id}/status。
func (a *SetTicketStatus) HTTP(ctx context.Context, in *TicketStatusInput) (*TicketOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body.Status)
	if err != nil {
		return nil, err
	}
	return &TicketOutput{Body: *dto}, nil
}
