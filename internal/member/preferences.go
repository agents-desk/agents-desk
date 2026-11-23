package member

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
)

// ErrInvalidTimeZone 是不认识的时区名（422），提示显示在“时区”下面。
var ErrInvalidTimeZone = errorx.Define(errorx.InvalidArgument, "member.invalid_time_zone")

// Language 是成员的语言：界面的语言，也是把客户的消息译成的语言。
type Language string

const (
	SimplifiedChinese Language = "zh-CN"
	English           Language = "en"
)

// Languages 是可以选的语言，与界面的翻译文件（locales/web.<语言>.yaml）的语言相同；显示文字是翻译文件中
// member.language.<语言> 的消息。
var Languages = enum.New("member.language", enum.Item(SimplifiedChinese), enum.Item(English))

// Schema 让 OpenAPI 中的 Language 只接受上面的取值，并带有显示文字。
func (Language) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Languages) }

// SendShortcut 是回复框的发送快捷键：回车发送，或者 Ctrl/Cmd + 回车发送。
type SendShortcut string

const (
	SendEnter     SendShortcut = "enter"
	SendCtrlEnter SendShortcut = "ctrl_enter"
)

// SendShortcuts 是可选的发送快捷键，显示文字是翻译文件中 member.send_shortcut.<方式> 的消息。
var SendShortcuts = enum.New("member.send_shortcut", enum.Item(SendEnter), enum.Item(SendCtrlEnter))

// Schema 让 OpenAPI 中的 SendShortcut 只接受上面的取值，并带有显示文字。
func (SendShortcut) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, SendShortcuts)
}

// Preferences 是成员的偏好设置，属于账号本身，在所有工作区中一样。
type Preferences struct {
	Language           Language     `json:"language"`
	TimeZone           string       `json:"time_zone,omitempty" maxLength:"64" doc:"IANA 时区名，例如 Asia/Shanghai；不传表示跟随浏览器"`
	SendShortcut       SendShortcut `json:"send_shortcut,omitempty" doc:"回复框发送快捷键：enter 回车发送，ctrl_enter Ctrl/Cmd + 回车发送；不传默认为 enter"`
	OpenNextAfterReply bool         `json:"open_next_after_reply,omitempty" doc:"发送回复后是否自动打开下一个待回复的会话"`
}

// PreferencesInput 是修改偏好设置的 HTTP 请求。
type PreferencesInput struct {
	Body Preferences
}

// UpdatePreferences 修改成员自己的偏好设置。
type UpdatePreferences struct {
	db     *bun.DB
	logins *login.Realm
	files  *filesystem.Service
}

// NewUpdatePreferences 返回修改偏好设置的动作；logins、files 用于返回修改后的个人资料。
func NewUpdatePreferences(db *bun.DB, logins *login.Realm, files *filesystem.Service) *UpdatePreferences {
	return &UpdatePreferences{db: db, logins: logins, files: files}
}

// Handle 保存 sub 的语言、时区、发送方式和发送后自动跳转偏好并记录操作日志，返回修改后的个人资料。
// 时区不是 IANA 时区名时返回 ErrInvalidTimeZone（字段 time_zone）；语言与发送快捷键的取值由接口枚举校验。
func (a *UpdatePreferences) Handle(ctx context.Context, sub identity.Subject, p Preferences) (*Profile, error) {
	if p.TimeZone != "" {
		// time.LoadLocation 把 "Local" 当作服务器的时区，它不是一个时区名
		if _, err := time.LoadLocation(p.TimeZone); err != nil || p.TimeZone == "Local" {
			return nil, ErrInvalidTimeZone.New(errorx.Field("time_zone"), errorx.Public("time_zone", p.TimeZone))
		}
	}
	if p.SendShortcut == "" {
		p.SendShortcut = SendEnter
	}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		m := new(Member)
		if err := tx.NewSelect().Model(m).Where("id = ?", sub.ID).For("UPDATE").Scan(ctx); err != nil {
			return errors.Wrap(err, "load member")
		}
		if m.Language == p.Language && m.TimeZone == p.TimeZone && m.SendShortcut == p.SendShortcut && m.OpenNextAfterReply == p.OpenNextAfterReply {
			return nil
		}
		m.Language = p.Language
		m.TimeZone = p.TimeZone
		m.SendShortcut = p.SendShortcut
		m.OpenNextAfterReply = p.OpenNextAfterReply
		m.UpdatedAt = time.Now().UTC()
		if _, err := tx.NewUpdate().Model(m).Column("language", "time_zone", "send_shortcut", "open_next_after_reply", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update member preferences")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "member.preferences_updated", TargetType: string(Type), TargetID: sub.ID,
			Properties: map[string]any{
				"language":              p.Language,
				"time_zone":             p.TimeZone,
				"send_shortcut":         p.SendShortcut,
				"open_next_after_reply": p.OpenNextAfterReply,
			},
		})
	})
	if err != nil {
		return nil, err
	}
	return loadProfile(ctx, a.db, a.logins, a.files, sub)
}

// HTTP 处理 PUT /me/preferences。
func (a *UpdatePreferences) HTTP(ctx context.Context, in *PreferencesInput) (*ProfileOutput, error) {
	sub, _ := identity.FromContext(ctx)
	p, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &ProfileOutput{Body: *p}, nil
}
