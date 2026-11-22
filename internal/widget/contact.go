package widget

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
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
