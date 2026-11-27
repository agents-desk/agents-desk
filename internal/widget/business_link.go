package widget

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/member"
)

// BusinessLinkDTO 是成员个人网页商务沟通链接的接口视图。
type BusinessLinkDTO struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name" doc:"商务沟通链接名称，显示在对方打开的聊天窗口顶部"`
	PublicCode string                 `json:"public_code" doc:"网页商务链接的公开短码，用于 /c/{code} 访问"`
	Appearance Appearance             `json:"appearance" doc:"聊天窗口外观配置（主题色与问候语）"`
	Business   channel.BusinessPolicy `json:"business" doc:"通用商务策略（默认协作成员、自动标签与翻译覆写）"`
	ChatURL    string                 `json:"chat_url" doc:"发给外部商户直接打开的网页沟通地址"`
	Enabled    bool                   `json:"enabled" doc:"是否启用；停用后对方无法继续打开或发消息"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
}

// BusinessLinkOutput 是返回单个商务沟通链接的响应。
type BusinessLinkOutput struct {
	Body BusinessLinkDTO
}

// BusinessLinkListOutput 是返回当前成员全部商务沟通链接的响应。
type BusinessLinkListOutput struct {
	Body []BusinessLinkDTO
}

// CreateBusinessLinkBody 是新建个人商务沟通链接的请求体。
type CreateBusinessLinkBody struct {
	Name       string                  `json:"name" minLength:"1" maxLength:"60" doc:"链接名称，例如：Alex 商务合作专线"`
	Appearance Appearance              `json:"appearance,omitempty" doc:"聊天窗口外观配置"`
	Business   *channel.BusinessPolicy `json:"business,omitempty" doc:"可选：通用商务策略"`
}

// CreateBusinessLinkInput 是新建个人商务沟通链接的 HTTP 请求。
type CreateBusinessLinkInput struct {
	Body CreateBusinessLinkBody
}

// UpdateBusinessLinkBody 是修改个人商务沟通链接的请求体。
type UpdateBusinessLinkBody struct {
	Name       string                  `json:"name" minLength:"1" maxLength:"60"`
	Appearance Appearance              `json:"appearance,omitempty"`
	Business   *channel.BusinessPolicy `json:"business,omitempty"`
	Enabled    bool                    `json:"enabled"`
}

// UpdateBusinessLinkInput 是修改个人商务沟通链接的 HTTP 请求。
type UpdateBusinessLinkInput struct {
	ID   string `path:"id" format:"uuid"`
	Body UpdateBusinessLinkBody
}

// BusinessLinkIDInput 是指定个人商务沟通链接的路径参数。
type BusinessLinkIDInput struct {
	ID string `path:"id" format:"uuid"`
}

// businessLinkURL 返回成员个人商务沟通链接的完整访问地址。
func businessLinkURL(publicURL, channelID string) string {
	base := strings.TrimRight(strings.TrimSpace(publicURL), "/")
	return base + "/widget/?channel=" + channelID
}

// toBusinessLinkDTO 将个人商务链接渠道模型转为接口视图。
func toBusinessLinkDTO(ch *channel.Channel, publicURL string) (BusinessLinkDTO, error) {
	s, err := settingsOf(ch)
	if err != nil {
		return BusinessLinkDTO{}, err
	}
	code := ""
	if ch.PublicCode != nil {
		code = *ch.PublicCode
	}
	return BusinessLinkDTO{
		ID:         ch.ID,
		Name:       ch.Name,
		PublicCode: code,
		Appearance: s.appearance(),
		Business:   channel.BusinessPolicyOf(ch.Settings),
		ChatURL:    businessLinkURL(publicURL, ch.ID),
		Enabled:    ch.Enabled,
		CreatedAt:  ch.CreatedAt,
		UpdatedAt:  ch.UpdatedAt,
	}, nil
}

// findOwnedBusinessLink 读取工作区 tenant 中属于 memberID 的个人商务沟通链接 id。
func findOwnedBusinessLink(ctx context.Context, db bun.IDB, tenant, memberID, id string, lock bool) (*channel.Channel, error) {
	ch := new(channel.Channel)
	q := db.NewSelect().Model(ch).
		Where("id = ? AND tenant_id = ? AND kind = ? AND owner_member_id = ?", id, tenant, channel.BusinessLink, memberID)
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, channel.ErrNotFound.New()
	}
	return ch, errors.Wrap(err, "load business link")
}

// ListBusinessLinks 列出当前成员在工作区中的个人网页商务沟通链接。
type ListBusinessLinks struct {
	db        *bun.DB
	publicURL string
}

// NewListBusinessLinks 返回列出个人商务沟通链接的动作。
func NewListBusinessLinks(db *bun.DB, publicURL string) *ListBusinessLinks {
	return &ListBusinessLinks{db: db, publicURL: publicURL}
}

// Handle 返回 sub 在当前工作区名下的全部个人商务沟通链接。
func (a *ListBusinessLinks) Handle(ctx context.Context, sub identity.Subject) ([]BusinessLinkDTO, error) {
	if sub.Type != member.Type {
		return nil, channel.ErrNotFound.New()
	}
	var rows []channel.Channel
	err := a.db.NewSelect().Model(&rows).
		Where("tenant_id = ? AND kind = ? AND owner_member_id = ?", sub.Tenant, channel.BusinessLink, sub.ID).
		OrderExpr("created_at ASC, id ASC").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "list business links")
	}
	out := make([]BusinessLinkDTO, len(rows))
	for i := range rows {
		dto, err := toBusinessLinkDTO(&rows[i], a.publicURL)
		if err != nil {
			return nil, err
		}
		out[i] = dto
	}
	return out, nil
}

// HTTP 处理 GET /t/{slug}/api/my/business-links。
func (a *ListBusinessLinks) HTTP(ctx context.Context, _ *struct{}) (*BusinessLinkListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	items, err := a.Handle(ctx, sub)
	if err != nil {
		return nil, err
	}
	return &BusinessLinkListOutput{Body: items}, nil
}

// CreateBusinessLink 为当前成员新建一条个人网页商务沟通链接。
type CreateBusinessLink struct {
	db         *bun.DB
	publicURL  string
	checkQuota func(ctx context.Context, sub identity.Subject) error
}

// NewCreateBusinessLink 返回新建个人商务沟通链接的动作；可选传入配额与策略校验函数。
func NewCreateBusinessLink(db *bun.DB, publicURL string, checkQuota ...func(ctx context.Context, sub identity.Subject) error) *CreateBusinessLink {
	var fn func(ctx context.Context, sub identity.Subject) error
	if len(checkQuota) > 0 {
		fn = checkQuota[0]
	}
	return &CreateBusinessLink{db: db, publicURL: publicURL, checkQuota: fn}
}

// Handle 在 sub 所在工作区为 sub 新建一条启用中的个人网页商务沟通链接。
func (a *CreateBusinessLink) Handle(ctx context.Context, sub identity.Subject, b CreateBusinessLinkBody) (*BusinessLinkDTO, error) {
	if sub.Type != member.Type {
		return nil, channel.ErrNotFound.New()
	}
	if a.checkQuota != nil {
		if err := a.checkQuota(ctx, sub); err != nil {
			return nil, err
		}
	}
	appearance, err := normalizeAppearance(b.Appearance)
	if err != nil {
		return nil, err
	}
	settings, err := Settings{Appearance: appearance, AllowedOrigins: []string{}}.toMap()
	if err != nil {
		return nil, err
	}
	if b.Business != nil {
		bp, err := channel.NormalizeBusinessPolicy(*b.Business)
		if err != nil {
			return nil, err
		}
		settings = channel.SetBusinessPolicy(settings, bp)
	}
	ownerID := sub.ID
	code := channel.GeneratePublicCode()
	ch := &channel.Channel{
		ID:            newID(),
		TenantID:      sub.Tenant,
		Kind:          channel.BusinessLink,
		Name:          strings.TrimSpace(b.Name),
		Settings:      settings,
		OwnerMemberID: &ownerID,
		PublicCode:    &code,
		Enabled:       true,
	}
	if _, err := a.db.NewInsert().Model(ch).Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "insert business link")
	}
	dto, err := toBusinessLinkDTO(ch, a.publicURL)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

// HTTP 处理 POST /t/{slug}/api/my/business-links。
func (a *CreateBusinessLink) HTTP(ctx context.Context, in *CreateBusinessLinkInput) (*BusinessLinkOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &BusinessLinkOutput{Body: *dto}, nil
}

// UpdateBusinessLink 修改当前成员的个人网页商务沟通链接。
type UpdateBusinessLink struct {
	db        *bun.DB
	publicURL string
}

// NewUpdateBusinessLink 返回修改个人商务沟通链接的动作。
func NewUpdateBusinessLink(db *bun.DB, publicURL string) *UpdateBusinessLink {
	return &UpdateBusinessLink{db: db, publicURL: publicURL}
}

// Handle 修改属于 sub 的个人商务沟通链接 id；不存在或不属于本人时返回 channel.ErrNotFound。
func (a *UpdateBusinessLink) Handle(ctx context.Context, sub identity.Subject, id string, b UpdateBusinessLinkBody) (*BusinessLinkDTO, error) {
	if sub.Type != member.Type {
		return nil, channel.ErrNotFound.New()
	}
	appearance, err := normalizeAppearance(b.Appearance)
	if err != nil {
		return nil, err
	}
	var updated *channel.Channel
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		ch, err := findOwnedBusinessLink(ctx, tx, sub.Tenant, sub.ID, id, true)
		if err != nil {
			return err
		}
		s, err := settingsOf(ch)
		if err != nil {
			return err
		}
		s.Appearance = appearance
		settings, err := s.toMap()
		if err != nil {
			return err
		}
		if b.Business != nil {
			bp, err := channel.NormalizeBusinessPolicy(*b.Business)
			if err != nil {
				return err
			}
			settings = channel.SetBusinessPolicy(settings, bp)
		} else if existingBP := channel.BusinessPolicyOf(ch.Settings); len(existingBP.DefaultCollaboratorIDs) > 0 || len(existingBP.DefaultTagIDs) > 0 || existingBP.TranslationTone != "" || existingBP.AutoTranslate != nil {
			settings = channel.SetBusinessPolicy(settings, existingBP)
		}
		ch.Name = strings.TrimSpace(b.Name)
		ch.Enabled = b.Enabled
		ch.Settings = settings
		if _, err := tx.NewUpdate().Model(ch).Column("name", "enabled", "settings", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update business link")
		}
		updated = ch
		return nil
	})
	if err != nil {
		return nil, err
	}
	dto, err := toBusinessLinkDTO(updated, a.publicURL)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

// HTTP 处理 PUT /t/{slug}/api/my/business-links/{id}。
func (a *UpdateBusinessLink) HTTP(ctx context.Context, in *UpdateBusinessLinkInput) (*BusinessLinkOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &BusinessLinkOutput{Body: *dto}, nil
}

// DeleteBusinessLink 删除当前成员的个人网页商务沟通链接（历史聊天记录保留）。
type DeleteBusinessLink struct {
	db *bun.DB
}

// NewDeleteBusinessLink 返回删除个人商务沟通链接的动作。
func NewDeleteBusinessLink(db *bun.DB) *DeleteBusinessLink { return &DeleteBusinessLink{db: db} }

// Handle 删除属于 sub 的未使用个人商务沟通链接 id；不存在或不属于本人时返回 channel.ErrNotFound，
// 已有联系人或会话记录时返回 ErrBusinessLinkInUse（请改为停用）。
func (a *DeleteBusinessLink) Handle(ctx context.Context, sub identity.Subject, id string) error {
	if sub.Type != member.Type {
		return channel.ErrNotFound.New()
	}
	return database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := findOwnedBusinessLink(ctx, tx, sub.Tenant, sub.ID, id, true); err != nil {
			return err
		}
		hasIdentity, err := tx.NewSelect().Table("contact_identities").
			Where("tenant_id = ? AND channel_id = ?", sub.Tenant, id).Exists(ctx)
		if err != nil {
			return errors.Wrap(err, "check business link contact identities")
		}
		if hasIdentity {
			return ErrBusinessLinkInUse.New()
		}
		hasConv, err := tx.NewSelect().Table("conversations").
			Where("tenant_id = ? AND (channel_id = ? OR reply_channel_id = ?)", sub.Tenant, id, id).Exists(ctx)
		if err != nil {
			return errors.Wrap(err, "check business link conversations")
		}
		if hasConv {
			return ErrBusinessLinkInUse.New()
		}
		_, err = tx.NewDelete().Model((*channel.Channel)(nil)).Where("id = ? AND tenant_id = ?", id, sub.Tenant).Exec(ctx)
		return errors.Wrap(err, "delete business link")
	})
}

// HTTP 处理 DELETE /t/{slug}/api/my/business-links/{id}。
func (a *DeleteBusinessLink) HTTP(ctx context.Context, in *BusinessLinkIDInput) (*struct{}, error) {
	sub, _ := identity.FromContext(ctx)
	return nil, a.Handle(ctx, sub, in.ID)
}

// RegisterBusinessLinkRoutes 在工作区 API（/t/{slug}/api）中注册成员个人网页商务沟通链接管理接口。
func RegisterBusinessLinkRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, publicURL string, checkQuota ...func(ctx context.Context, sub identity.Subject) error) {
	use := require(auth.BusinessChannelsUse)
	route := func(id, method, path, summary string) huma.Operation {
		op := huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary}
		use(&op)
		return op
	}
	createOp := route("business-link-create", http.MethodPost, "/my/business-links", "新建我的商务沟通链接")
	createOp.DefaultStatus = http.StatusCreated
	deleteOp := route("business-link-delete", http.MethodDelete, "/my/business-links/{id}", "删除我的商务沟通链接")
	deleteOp.DefaultStatus = http.StatusNoContent

	rest.Register(api, route("business-link-list", http.MethodGet, "/my/business-links", "我的商务沟通链接列表"), NewListBusinessLinks(db, publicURL).HTTP)
	rest.Register(api, createOp, NewCreateBusinessLink(db, publicURL, checkQuota...).HTTP)
	rest.Register(api, route("business-link-update", http.MethodPut, "/my/business-links/{id}", "修改我的商务沟通链接"), NewUpdateBusinessLink(db, publicURL).HTTP)
	rest.Register(api, deleteOp, NewDeleteBusinessLink(db).HTTP)
}
