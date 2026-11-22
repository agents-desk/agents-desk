package contact

import (
	"context"
	"database/sql"
	"net/mail"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/lang"
)

// UpdateBody 是修改联系人资料的请求体：只更新传入的非 nil 字段，支持资料页完整保存和侧栏单独保存备注。
type UpdateBody struct {
	Name     *string `json:"name,omitempty" maxLength:"120" doc:"名字"`
	Email    *string `json:"email,omitempty" maxLength:"254" doc:"邮箱"`
	Language *string `json:"language,omitempty" maxLength:"35" doc:"对方的语言（BCP 47）；传空字符串恢复自动识别"`
	Location *string `json:"location,omitempty" maxLength:"120" doc:"所在地"`
	TimeZone *string `json:"time_zone,omitempty" maxLength:"64" doc:"IANA 时区名，例如 Asia/Shanghai；传空字符串清除"`
	Notes    *string `json:"notes,omitempty" maxLength:"4000" doc:"成员填写的内部备注"`
}

// UpdateInput 是修改联系人的 HTTP 请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body UpdateBody
}

// Update 修改工作区中联系人的资料和备注。
type Update struct {
	db *bun.DB
}

// NewUpdate 返回修改联系人的动作。
func NewUpdate(db *bun.DB) *Update { return &Update{db: db} }

// Handle 由 sub 修改他所在工作区中联系人 id 的资料并记录操作日志，返回修改后的完整资料。
// 修改语言时同步更新该联系人未关闭会话的语言：传合法 BCP 47 记为手动设置，传空字符串恢复按最近消息自动识别。
func (a *Update) Handle(ctx context.Context, sub identity.Subject, id string, b UpdateBody) (*ContactDetailDTO, error) {
	var email string
	if b.Email != nil {
		email = strings.TrimSpace(*b.Email)
		if email != "" {
			parsed, err := mail.ParseAddress(email)
			if err != nil || parsed.Address != email {
				return nil, ErrInvalidEmail.New(errorx.Field("email"))
			}
		}
	}
	var language string
	if b.Language != nil {
		language = strings.TrimSpace(*b.Language)
		if language != "" {
			canonical, ok := lang.Canonical(language)
			if !ok {
				return nil, ErrInvalidLanguage.New(errorx.Field("language"))
			}
			language = canonical
		}
	}
	var tz string
	if b.TimeZone != nil {
		tz = strings.TrimSpace(*b.TimeZone)
		if tz != "" && !validTimeZone(tz) {
			return nil, ErrInvalidTimeZone.New(errorx.Field("time_zone"), errorx.Public("time_zone", tz))
		}
	}

	var updated *Contact
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		c := new(Contact)
		err := tx.NewSelect().Model(c).Where("id = ? AND tenant_id = ?", id, sub.Tenant).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound.New()
		}
		if err != nil {
			return errors.Wrap(err, "load contact")
		}
		var cols []string
		props := map[string]any{}
		if b.Name != nil {
			v := strings.TrimSpace(*b.Name)
			if v != c.Name {
				c.Name, cols, props["name"] = v, append(cols, "name"), v
			}
		}
		if b.Email != nil && email != c.Email {
			c.Email, cols, props["email"] = email, append(cols, "email"), email
		}
		if b.Location != nil {
			v := strings.TrimSpace(*b.Location)
			if v != c.Location {
				c.Location, cols, props["location"] = v, append(cols, "location"), v
			}
		}
		if b.TimeZone != nil && tz != c.TimeZone {
			c.TimeZone, cols, props["time_zone"] = tz, append(cols, "time_zone"), tz
		}
		if b.Notes != nil && *b.Notes != c.Notes {
			c.Notes, cols, props["notes"] = *b.Notes, append(cols, "notes"), true
		}
		syncConvLang := false
		if b.Language != nil {
			if language == "" {
				if c.LanguageManual {
					detected, err := detectContactLanguage(ctx, tx, c.TenantID, c.ID)
					if err != nil {
						return err
					}
					c.Language, c.LanguageManual = detected, false
					cols = append(cols, "language", "language_manual")
					props["language"], props["language_manual"] = detected, false
					syncConvLang = true
				}
			} else if c.Language != language || !c.LanguageManual {
				c.Language, c.LanguageManual = language, true
				cols = append(cols, "language", "language_manual")
				props["language"], props["language_manual"] = language, true
				syncConvLang = true
			}
		}
		if len(cols) == 0 {
			updated = c
			return nil
		}
		at := now()
		c.UpdatedAt = at
		if _, err := tx.NewUpdate().Model(c).Column(append(cols, "updated_at")...).WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update contact")
		}
		if syncConvLang {
			if _, err := tx.NewUpdate().Table("conversations").
				Set("language = ?", c.Language).Set("updated_at = ?", at).
				Where("tenant_id = ? AND contact_id = ? AND status <> ?", c.TenantID, c.ID, "closed").Exec(ctx); err != nil {
				return errors.Wrap(err, "sync conversation language")
			}
		}
		if err := activity.Record(ctx, tx, sub, activity.Activity{
			Event: "contact.updated", TargetType: string(Type), TargetID: c.ID, Properties: props,
		}); err != nil {
			return err
		}
		updated = c
		return nil
	})
	if err != nil {
		return nil, err
	}
	return loadDetail(ctx, a.db, sub.Tenant, updated)
}

// detectContactLanguage 返回联系人 contactID 最近三条确定了语言的消息中出现最多的语言（与 conversation.CustomerLanguage 规则一致）。
func detectContactLanguage(ctx context.Context, db bun.IDB, tenant, contactID string) (string, error) {
	var recent []string
	err := db.NewSelect().TableExpr("timeline_items AS t").ColumnExpr("t.derived->>'language'").
		Where("t.tenant_id = ? AND t.kind = ? AND t.actor_type = ?", tenant, "message", "contact").
		Where("COALESCE(t.derived->>'language', '') NOT IN ('', ?)", "und").
		Where("t.conversation_id IN (SELECT id FROM conversations WHERE tenant_id = ? AND contact_id = ?)", tenant, contactID).
		OrderExpr("t.created_at DESC, t.seq DESC").Limit(3).Scan(ctx, &recent)
	if err != nil {
		return "", errors.Wrap(err, "detect contact language")
	}
	best, count := "", map[string]int{}
	for _, l := range recent {
		count[l]++
		if best == "" || count[l] > count[best] {
			best = l
		}
	}
	return best, nil
}

// HTTP 处理 PUT /t/{slug}/api/contacts/{id}。
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*ContactDetailOutput, error) {
	sub, _ := identity.FromContext(ctx)
	d, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &ContactDetailOutput{Body: *d}, nil
}
