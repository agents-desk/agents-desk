package member

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

// AlertChannel 是某一类实时提醒的渠道开关：浏览器通知和提示音。
type AlertChannel struct {
	Browser bool `json:"browser" doc:"是否发送浏览器桌面通知"`
	Sound   bool `json:"sound" doc:"是否播放提示音"`
}

// NotificationPreferences 是数据库中保存的成员实时提醒偏好，用指针区分未配置（默认全开）和显式关闭。
type NotificationPreferences struct {
	Assigned   *AlertChannel `json:"assigned,omitempty"`
	NewMessage *AlertChannel `json:"new_message,omitempty"`
	Mention    *AlertChannel `json:"mention,omitempty"`
}

// NotificationSettings 是接口中返回的完整实时提醒设置，属于账号本身，在所有工作区中共用。
type NotificationSettings struct {
	Assigned   AlertChannel `json:"assigned" doc:"新会话指派给我时的提醒"`
	NewMessage AlertChannel `json:"new_message" doc:"我负责的会话有新消息时的提醒"`
	Mention    AlertChannel `json:"mention" doc:"有人在内部备注中 @ 我时的提醒"`
}

// DefaultNotificationPreferences 返回默认的实时提醒偏好：全部事件开启浏览器通知和提示音。
func DefaultNotificationPreferences() NotificationPreferences {
	on := func() *AlertChannel { return &AlertChannel{Browser: true, Sound: true} }
	return NotificationPreferences{Assigned: on(), NewMessage: on(), Mention: on()}
}

// Settings 把数据库中保存的实时提醒偏好展开为完整的接口结构，未配置的项默认开启。
func (p NotificationPreferences) Settings() NotificationSettings {
	resolve := func(ch *AlertChannel) AlertChannel {
		if ch == nil {
			return AlertChannel{Browser: true, Sound: true}
		}
		return *ch
	}
	return NotificationSettings{
		Assigned:   resolve(p.Assigned),
		NewMessage: resolve(p.NewMessage),
		Mention:    resolve(p.Mention),
	}
}

// toStored 把接口提交的完整实时提醒设置转换为数据库存储结构。
func (s NotificationSettings) toStored() NotificationPreferences {
	assigned := s.Assigned
	newMessage := s.NewMessage
	mention := s.Mention
	return NotificationPreferences{Assigned: &assigned, NewMessage: &newMessage, Mention: &mention}
}

// NotificationsInput 是修改实时提醒设置的 HTTP 请求。
type NotificationsInput struct {
	Body NotificationSettings
}

// UpdateNotifications 修改成员自己的实时提醒设置。
type UpdateNotifications struct {
	db     *bun.DB
	logins *login.Realm
	files  *filesystem.Service
}

// NewUpdateNotifications 返回修改实时提醒设置的动作；logins、files 用于返回修改后的个人资料。
func NewUpdateNotifications(db *bun.DB, logins *login.Realm, files *filesystem.Service) *UpdateNotifications {
	return &UpdateNotifications{db: db, logins: logins, files: files}
}

// Handle 保存 sub 的实时提醒设置并记录操作日志，返回修改后的个人资料。
func (a *UpdateNotifications) Handle(ctx context.Context, sub identity.Subject, s NotificationSettings) (*Profile, error) {
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		m := new(Member)
		if err := tx.NewSelect().Model(m).Where("id = ?", sub.ID).For("UPDATE").Scan(ctx); err != nil {
			return errors.Wrap(err, "load member")
		}
		if m.NotificationPreferences.Settings() == s {
			return nil
		}
		m.NotificationPreferences = s.toStored()
		m.UpdatedAt = time.Now().UTC()
		if _, err := tx.NewUpdate().Model(m).Column("notification_preferences", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update member notifications")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "member.notifications_updated", TargetType: string(Type), TargetID: sub.ID,
			Properties: map[string]any{"assigned": s.Assigned, "new_message": s.NewMessage, "mention": s.Mention},
		})
	})
	if err != nil {
		return nil, err
	}
	return loadProfile(ctx, a.db, a.logins, a.files, sub)
}

// HTTP 处理 PUT /me/notifications。
func (a *UpdateNotifications) HTTP(ctx context.Context, in *NotificationsInput) (*ProfileOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &ProfileOutput{Body: *p}, nil
}
