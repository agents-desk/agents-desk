package widget

import (
	"context"
	"net/mail"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/lang"
)

// ContactEmailBody 是线索在聊天窗口中留下邮箱的请求体。
type ContactEmailBody struct {
	Email          string `json:"email" format:"email" maxLength:"254" doc:"线索留下的邮箱"`
	ConversationID string `json:"conversation_id,omitempty" format:"uuid" doc:"当前所在的会话；填写时在会话时间线中记下事件并通知成员刷新"`
}

// ContactEmailInput 是线索留邮箱的 HTTP 请求。
type ContactEmailInput struct {
	Body ContactEmailBody
}

// ContactIntakeBody 是商务联系人在商务沟通链接开场留资表单提交的身份资料。
type ContactIntakeBody struct {
	Name    string `json:"name,omitempty" maxLength:"120" doc:"对方称呼或姓名"`
	Company string `json:"company,omitempty" maxLength:"120" doc:"对方所属公司或组织名称"`
	Email   string `json:"email,omitempty" maxLength:"254" doc:"对方联系邮箱"`
	Note    string `json:"note,omitempty" maxLength:"500" doc:"合作意向或需求简述"`
}

// ContactIntakeInput 是开场留资表单的 HTTP 请求。
type ContactIntakeInput struct {
	Body ContactIntakeBody
}

// ContactOutput 是返回更新后的联系人概要的响应。
type ContactOutput struct {
	Body contact.Summary
}

// LeaveEmail 是线索在聊天窗口中提交自己的邮箱：更新联系人的邮箱，并在提供会话 ID 时通知会话的成员刷新。
type LeaveEmail struct {
	leave  *contact.LeaveEmail
	record *conversation.RecordContactEmail
}

// NewLeaveEmail 返回聊天插件中线索留邮箱的动作。
func NewLeaveEmail(db *bun.DB, down conversation.Downstream) *LeaveEmail {
	return &LeaveEmail{
		leave:  contact.NewLeaveEmail(db),
		record: conversation.NewRecordContactEmail(db, down),
	}
}

// Handle 以联系人 sub 的身份在聊天插件 channelID 中保存邮箱 b.Email；填了 b.ConversationID 时，
// 在该会话中记录一条仅成员可见的系统事件并推送给能看到会话的成员。
func (a *LeaveEmail) Handle(ctx context.Context, sub identity.Subject, channelID string, b ContactEmailBody) (*contact.Summary, error) {
	c, changed, err := a.leave.Handle(ctx, sub, b.Email)
	if err != nil {
		return nil, err
	}
	if changed && b.ConversationID != "" {
		if err := a.record.Handle(ctx, sub, channelID, b.ConversationID, c.Email); err != nil {
			return nil, err
		}
	}
	s := c.Summary()
	return &s, nil
}

// SubmitIntake 处理商务沟通链接的开场留资提交：按渠道配置校验必填字段，写入联系人资料并通知相关会话刷新。
type SubmitIntake struct {
	db   *bun.DB
	down conversation.Downstream
}

// NewSubmitIntake 返回开场留资提交动作。
func NewSubmitIntake(db *bun.DB, down conversation.Downstream) *SubmitIntake {
	return &SubmitIntake{db: db, down: down}
}

// Handle 按当前商务链接的开场留资规则校验并保存联系人资料。
func (a *SubmitIntake) Handle(ctx context.Context, sub identity.Subject, b ContactIntakeBody) (*contact.Summary, error) {
	ch, err := channelFrom(ctx)
	if err != nil {
		return nil, err
	}
	s, err := settingsOf(ch)
	if err != nil {
		return nil, err
	}
	intake := s.resolvedIntake()
	name := strings.TrimSpace(b.Name)
	company := strings.TrimSpace(b.Company)
	email := strings.TrimSpace(b.Email)
	note := strings.TrimSpace(b.Note)

	if intake.Enabled {
		if intake.Name == IntakeRequired && name == "" {
			return nil, ErrIntakeRequired.New(errorx.Field("name"))
		}
		if intake.Company == IntakeRequired && company == "" {
			return nil, ErrIntakeRequired.New(errorx.Field("company"))
		}
		if intake.Email == IntakeRequired && email == "" {
			return nil, ErrIntakeRequired.New(errorx.Field("email"))
		}
		if intake.Note == IntakeRequired && note == "" {
			return nil, ErrIntakeRequired.New(errorx.Field("note"))
		}
	}
	if email != "" {
		parsed, err := mail.ParseAddress(email)
		if err != nil || parsed.Address != email {
			return nil, contact.ErrInvalidEmail.New(errorx.Field("email"))
		}
	}

	var updated *contact.Contact
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := contact.Find(ctx, tx, sub.Tenant, sub.ID)
		if err != nil {
			return err
		}
		var cols []string
		if name != "" && name != c.Name {
			c.Name, cols = name, append(cols, "name")
		}
		if company != "" && company != c.Company {
			c.Company, cols = company, append(cols, "company")
		}
		if email != "" && email != c.Email {
			c.Email, cols = email, append(cols, "email")
		}
		if note != "" {
			if c.Notes == "" {
				c.Notes = note
			} else if !strings.Contains(c.Notes, note) {
				c.Notes = c.Notes + "\n" + note
			}
			cols = append(cols, "notes")
		}
		if len(cols) > 0 {
			c.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
			if _, err := tx.NewUpdate().Model(c).Column(append(cols, "updated_at")...).WherePK().Exec(ctx); err != nil {
				return errors.Wrap(err, "update contact from intake")
			}
			var convIDs []string
			if err := tx.NewSelect().Table("conversations").Column("id").
				Where("tenant_id = ? AND contact_id = ? AND channel_id = ?", sub.Tenant, sub.ID, ch.ID).
				Scan(ctx, &convIDs); err == nil && len(convIDs) > 0 {
				if err := conversation.NotifyUpdated(ctx, tx, a.down, sub.Tenant, convIDs); err != nil {
					return err
				}
			}
		}
		updated = c
		return nil
	})
	if err != nil {
		return nil, err
	}
	summary := updated.Summary()
	return &summary, nil
}

// ContactLanguageBody 是访客在聊天窗口手动切换语言时上报的语言偏好。
type ContactLanguageBody struct {
	Language string `json:"language" minLength:"2" maxLength:"35" doc:"访客选择的语言（BCP 47），如 zh-CN、en、hi-Latn"`
}

// ContactLanguageInput 是访客更新语言偏好的 HTTP 请求。
type ContactLanguageInput struct {
	Body ContactLanguageBody
}

// UpdateLanguage 在访客手动切换聊天窗口语言时更新联系人的冷启动语言先验：
// 若该联系人尚未发过任何真实消息，则将 contacts.language 同步为所选语言；若已有真实消息识别出的语言则保持不变。
type UpdateLanguage struct {
	db *bun.DB
}

// NewUpdateLanguage 返回更新访客冷启动语言先验的动作。
func NewUpdateLanguage(db *bun.DB) *UpdateLanguage { return &UpdateLanguage{db: db} }

// Handle 校验语言标签，仅在联系人尚未发送过消息时更新其 contacts.language，并返回最新联系人概要。
func (a *UpdateLanguage) Handle(ctx context.Context, sub identity.Subject, b ContactLanguageBody) (*contact.Summary, error) {
	canon, ok := lang.Canonical(b.Language)
	if !ok {
		return nil, ErrInvalidLanguage.New(errorx.Field("language"), errorx.Public("language", b.Language))
	}
	var updated *contact.Contact
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c, err := contact.Find(ctx, tx, sub.Tenant, sub.ID)
		if err != nil {
			return err
		}
		hasMessage, err := tx.NewSelect().Table("timeline_items").
			Where("tenant_id = ? AND kind = ? AND actor_type = ? AND actor_id = ?", sub.Tenant, conversation.Message, conversation.ContactActor, sub.ID).
			Exists(ctx)
		if err != nil {
			return errors.Wrap(err, "check contact messages for language update")
		}
		if !hasMessage && c.Language != canon {
			c.Language = canon
			c.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
			if _, err := tx.NewUpdate().Model(c).Column("language", "updated_at").WherePK().Exec(ctx); err != nil {
				return errors.Wrap(err, "update contact language")
			}
		}
		updated = c
		return nil
	})
	if err != nil {
		return nil, err
	}
	summary := updated.Summary()
	return &summary, nil
}
