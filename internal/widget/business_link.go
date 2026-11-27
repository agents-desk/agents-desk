package widget

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
	"rsc.io/qr"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/lang"
	"agents-desk/internal/member"
)

// BusinessAccessDTO 是返回给成员的网页商务链接访问控制视图（只暴露是否已设置口令，不回传明文或哈希）。
type BusinessAccessDTO struct {
	Mode        string     `json:"mode" enum:"public,passcode" doc:"访问模式：public（公开）或 passcode（口令保护）"`
	HasPasscode bool       `json:"has_passcode" doc:"是否已设置访问口令"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty" doc:"可选：链接有效期，过期后不再接受新访客进入"`
}

// BusinessAccessInput 是修改网页商务链接访问控制的请求结构。
type BusinessAccessInput struct {
	Mode          string     `json:"mode,omitempty" enum:"public,passcode" doc:"访问模式：public 或 passcode"`
	Passcode      string     `json:"passcode,omitempty" maxLength:"64" doc:"可选：设置或更换访问口令；留空保持原有口令不变"`
	ClearPasscode bool       `json:"clear_passcode,omitempty" doc:"为 true 时清除已有访问口令"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty" doc:"可选：链接有效期，不传或传 null 表示永久有效"`
}

// BusinessLinkDTO 是成员个人网页商务沟通链接的完整接口视图。
type BusinessLinkDTO struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name" doc:"商务沟通链接内部管理名称；对外未设置多语言标题时也作为默认标题"`
	PublicCode   string                 `json:"public_code" doc:"网页商务链接的公开短码，用于 /c/{code} 访问"`
	Appearance   Appearance             `json:"appearance" doc:"聊天窗口视觉外观配置（主题色）"`
	Localization Localization           `json:"localization" doc:"链接语言策略与多语言自定义文案（标题、问候语、名片、离开提示与快捷话题）"`
	Card         BusinessCardConfig     `json:"card" doc:"对外商务名片展示开关"`
	Intake       BusinessIntakeConfig   `json:"intake" doc:"开场留资表单配置"`
	Access       BusinessAccessDTO      `json:"access" doc:"访问控制（口令保护与有效期）"`
	Business     channel.BusinessPolicy `json:"business" doc:"通用商务策略（默认协作成员、自动标签与翻译覆写）"`
	ChatURL      string                 `json:"chat_url" doc:"发给外部合作方直接打开的短链地址（/c/{code}）"`
	WidgetURL    string                 `json:"widget_url" doc:"底层聊天窗口直连地址（/widget/?channel={id}）"`
	QRDataURL    string                 `json:"qr_data_url,omitempty" doc:"短链对应的 PNG 二维码（data:image/png;base64,...）"`
	Enabled      bool                   `json:"enabled" doc:"是否启用；停用后对方无法继续打开或发消息"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
}

// BusinessLinkOutput 是返回单个商务沟通链接的响应。
type BusinessLinkOutput struct {
	Body BusinessLinkDTO
}

// BusinessLinkListOutput 是返回当前成员全部商务沟通链接的响应。
type BusinessLinkListOutput struct {
	Body []BusinessLinkDTO
}

// CreateBusinessLinkBody 是新建个人商务沟通链接的请求体（保持极简，建完即可进入工作台精调）。
type CreateBusinessLinkBody struct {
	Name         string                  `json:"name" minLength:"1" maxLength:"60" doc:"链接名称，例如：Alex 商务合作专线"`
	Appearance   Appearance              `json:"appearance,omitempty" doc:"聊天窗口视觉外观配置"`
	Localization *Localization           `json:"localization,omitempty" doc:"可选：语言策略与多语言文案"`
	Business     *channel.BusinessPolicy `json:"business,omitempty" doc:"可选：通用商务策略"`
}

// CreateBusinessLinkInput 是新建个人商务沟通链接的 HTTP 请求。
type CreateBusinessLinkInput struct {
	Body CreateBusinessLinkBody
}

// UpdateBusinessLinkBody 是修改个人商务沟通链接的请求体。
type UpdateBusinessLinkBody struct {
	Name         string                  `json:"name" minLength:"1" maxLength:"60"`
	PublicCode   string                  `json:"public_code,omitempty" maxLength:"40" doc:"可选：自定义短码（3-40 位小写字母、数字或连字符）"`
	Appearance   Appearance              `json:"appearance,omitempty"`
	Localization *Localization           `json:"localization,omitempty"`
	Card         *BusinessCardConfig     `json:"card,omitempty"`
	Intake       *BusinessIntakeConfig   `json:"intake,omitempty"`
	Access       *BusinessAccessInput    `json:"access,omitempty"`
	Business     *channel.BusinessPolicy `json:"business,omitempty"`
	Enabled      bool                    `json:"enabled"`
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

// BusinessLinkHooks 为个人商务链接动作提供工作区策略与配额钩子（避免包间循环依赖）。
type BusinessLinkHooks struct {
	CheckCreate   func(ctx context.Context, sub identity.Subject) error
	DefaultIntake func(ctx context.Context, tenantID string) bool
}

// defaultBusinessCard 返回新建商务链接的默认名片展示配置。
func defaultBusinessCard() BusinessCardConfig {
	return BusinessCardConfig{ShowOwner: true}
}

// defaultBusinessIntake 返回商务链接默认开场留资配置。
func defaultBusinessIntake(enabled bool) BusinessIntakeConfig {
	return BusinessIntakeConfig{
		Enabled: enabled,
		Name:    IntakeRequired,
		Company: IntakeOptional,
		Email:   IntakeOptional,
		Note:    IntakeOff,
	}
}

// normalizeIntake 规范化开场留资配置，填充缺省模式。
func normalizeIntake(in BusinessIntakeConfig) BusinessIntakeConfig {
	fix := func(m, fallback IntakeFieldMode) IntakeFieldMode {
		switch m {
		case IntakeOff, IntakeOptional, IntakeRequired:
			return m
		default:
			return fallback
		}
	}
	return BusinessIntakeConfig{
		Enabled: in.Enabled,
		Name:    fix(in.Name, IntakeRequired),
		Company: fix(in.Company, IntakeOptional),
		Email:   fix(in.Email, IntakeOptional),
		Note:    fix(in.Note, IntakeOff),
	}
}

// normalizeCard 规范化名片展示开关。
func normalizeCard(in BusinessCardConfig) BusinessCardConfig {
	return BusinessCardConfig{ShowOwner: in.ShowOwner}
}

// normalizeStarters 去重、截断空白并限制快捷开场话题最多 5 条。
func normalizeStarters(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if len([]rune(s)) > 120 {
			s = string([]rune(s)[:120])
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if len(out) >= 5 {
			break
		}
	}
	return out
}

// hashPasscode 使用 channelID 加盐计算访问口令的 SHA-256 十六进制摘要。
func hashPasscode(channelID, passcode string) string {
	sum := sha256.Sum256([]byte(channelID + ":" + strings.TrimSpace(passcode)))
	return hex.EncodeToString(sum[:])
}

// verifyPasscode 以常量时间比对访客提交的口令与保存的摘要。
func verifyPasscode(channelID, passcode, expectedHash string) bool {
	if expectedHash == "" {
		return true
	}
	got := hashPasscode(channelID, passcode)
	return subtle.ConstantTimeCompare([]byte(got), []byte(expectedHash)) == 1
}

// businessLinkShortURL 返回短链地址 /c/{code}；无短码时回退到 /widget/?channel={id}。
func businessLinkShortURL(publicURL, channelID, publicCode string) string {
	base := strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if publicCode != "" {
		return base + "/c/" + publicCode
	}
	return base + "/widget/?channel=" + channelID
}

// businessLinkWidgetURL 返回底层聊天窗口直连地址。
func businessLinkWidgetURL(publicURL, channelID string) string {
	base := strings.TrimRight(strings.TrimSpace(publicURL), "/")
	return base + "/widget/?channel=" + channelID
}

// encodeQRDataURL 将目标链接编码为按比例缩放、带白边的清晰 PNG data URL（用于工作台二维码展示与下载）。
func encodeQRDataURL(targetURL string) string {
	if strings.TrimSpace(targetURL) == "" {
		return ""
	}
	code, err := qr.Encode(targetURL, qr.M)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())
}

// resolvedCard 读取 Settings 中的名片配置，兼容旧数据未设置 card 字段时默认展示归属成员名片。
func (s Settings) resolvedCard() BusinessCardConfig {
	if s.Card == nil {
		return defaultBusinessCard()
	}
	return normalizeCard(*s.Card)
}

// resolvedIntake 读取 Settings 中的开场留资配置。
func (s Settings) resolvedIntake() BusinessIntakeConfig {
	if s.Intake == nil {
		return defaultBusinessIntake(false)
	}
	return normalizeIntake(*s.Intake)
}

// resolvedAccess 读取 Settings 中的访问控制配置。
func (s Settings) resolvedAccess() BusinessAccessStored {
	if s.Access == nil {
		return BusinessAccessStored{Mode: "public"}
	}
	mode := s.Access.Mode
	if mode != "passcode" {
		mode = "public"
	}
	return BusinessAccessStored{
		Mode:         mode,
		PasscodeHash: s.Access.PasscodeHash,
		ExpiresAt:    s.Access.ExpiresAt,
	}
}

// toBusinessLinkDTO 将个人商务链接渠道模型转为接口视图；withQR 为 true 时附带 PNG 二维码。
func toBusinessLinkDTO(ch *channel.Channel, publicURL string, withQR bool) (BusinessLinkDTO, error) {
	s, err := settingsOf(ch)
	if err != nil {
		return BusinessLinkDTO{}, err
	}
	code := ""
	if ch.PublicCode != nil {
		code = *ch.PublicCode
	}
	chatURL := businessLinkShortURL(publicURL, ch.ID, code)
	access := s.resolvedAccess()
	dto := BusinessLinkDTO{
		ID:           ch.ID,
		Name:         ch.Name,
		PublicCode:   code,
		Appearance:   s.appearance(),
		Localization: s.localization(),
		Card:         s.resolvedCard(),
		Intake:       s.resolvedIntake(),
		Access: BusinessAccessDTO{
			Mode:        access.Mode,
			HasPasscode: access.PasscodeHash != "",
			ExpiresAt:   access.ExpiresAt,
		},
		Business:  channel.BusinessPolicyOf(ch.Settings),
		ChatURL:   chatURL,
		WidgetURL: businessLinkWidgetURL(publicURL, ch.ID),
		Enabled:   ch.Enabled,
		CreatedAt: ch.CreatedAt,
		UpdatedAt: ch.UpdatedAt,
	}
	if withQR {
		dto.QRDataURL = encodeQRDataURL(chatURL)
	}
	return dto, nil
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

// ShortLinkHandler 处理公开短链 GET /c/{code}，将短码解析为启用中的商务沟通链接并 302 跳转至聊天窗口，透传合法的 lang 查询参数。
func ShortLinkHandler(db bun.IDB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "code")))
		if code == "" {
			http.Redirect(w, r, "/widget/", http.StatusFound)
			return
		}
		var chID string
		err := db.NewSelect().Table("channels").Column("id").
			Where("public_code = ? AND kind = ? AND enabled = TRUE", code, channel.BusinessLink).
			Where("EXISTS (SELECT 1 FROM tenants t WHERE t.id = channels.tenant_id AND t.disabled_at IS NULL)").
			Limit(1).Scan(r.Context(), &chID)
		if err != nil || chID == "" {
			http.Redirect(w, r, "/widget/?channel=unavailable", http.StatusFound)
			return
		}
		target := "/widget/?channel=" + url.QueryEscape(chID)
		if canon, ok := lang.Canonical(r.URL.Query().Get("lang")); ok {
			target += "&lang=" + url.QueryEscape(canon)
		}
		http.Redirect(w, r, target, http.StatusFound)
	}
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
		dto, err := toBusinessLinkDTO(&rows[i], a.publicURL, false)
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

// GetBusinessLink 读取当前成员的单个网页商务沟通链接详情（附带二维码）。
type GetBusinessLink struct {
	db        *bun.DB
	publicURL string
}

// NewGetBusinessLink 返回读取单个商务沟通链接的动作。
func NewGetBusinessLink(db *bun.DB, publicURL string) *GetBusinessLink {
	return &GetBusinessLink{db: db, publicURL: publicURL}
}

// Handle 读取属于 sub 的个人商务沟通链接 id。
func (a *GetBusinessLink) Handle(ctx context.Context, sub identity.Subject, id string) (*BusinessLinkDTO, error) {
	if sub.Type != member.Type {
		return nil, channel.ErrNotFound.New()
	}
	ch, err := findOwnedBusinessLink(ctx, a.db, sub.Tenant, sub.ID, id, false)
	if err != nil {
		return nil, err
	}
	dto, err := toBusinessLinkDTO(ch, a.publicURL, true)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

// HTTP 处理 GET /t/{slug}/api/my/business-links/{id}。
func (a *GetBusinessLink) HTTP(ctx context.Context, in *BusinessLinkIDInput) (*BusinessLinkOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &BusinessLinkOutput{Body: *dto}, nil
}

// CreateBusinessLink 为当前成员新建一条个人网页商务沟通链接。
type CreateBusinessLink struct {
	db        *bun.DB
	publicURL string
	hooks     BusinessLinkHooks
}

// NewCreateBusinessLink 返回新建个人商务沟通链接的动作；可选传入配额检查函数（或通过 WithHooks 配置工作区默认留资）。
func NewCreateBusinessLink(db *bun.DB, publicURL string, checkQuota ...func(ctx context.Context, sub identity.Subject) error) *CreateBusinessLink {
	var h BusinessLinkHooks
	if len(checkQuota) > 0 {
		h.CheckCreate = checkQuota[0]
	}
	return &CreateBusinessLink{db: db, publicURL: publicURL, hooks: h}
}

// NewCreateBusinessLinkWithHooks 返回绑定完整策略钩子的新建商务链接动作。
func NewCreateBusinessLinkWithHooks(db *bun.DB, publicURL string, hooks BusinessLinkHooks) *CreateBusinessLink {
	return &CreateBusinessLink{db: db, publicURL: publicURL, hooks: hooks}
}

// Handle 在 sub 所在工作区为 sub 新建一条启用中的个人网页商务沟通链接。
func (a *CreateBusinessLink) Handle(ctx context.Context, sub identity.Subject, b CreateBusinessLinkBody) (*BusinessLinkDTO, error) {
	if sub.Type != member.Type {
		return nil, channel.ErrNotFound.New()
	}
	if a.hooks.CheckCreate != nil {
		if err := a.hooks.CheckCreate(ctx, sub); err != nil {
			return nil, err
		}
	}
	appearance, err := normalizeAppearance(b.Appearance)
	if err != nil {
		return nil, err
	}
	loc := defaultLocalization(lang.En)
	if b.Localization != nil {
		loc, err = normalizeLocalization(*b.Localization, true)
		if err != nil {
			return nil, err
		}
	}
	requireIntake := false
	if a.hooks.DefaultIntake != nil {
		requireIntake = a.hooks.DefaultIntake(ctx, sub.Tenant)
	}
	card := defaultBusinessCard()
	intake := defaultBusinessIntake(requireIntake)
	access := BusinessAccessStored{Mode: "public"}
	s := Settings{
		Appearance:     appearance,
		Localization:   loc,
		AllowedOrigins: []string{},
		Card:           &card,
		Intake:         &intake,
		Access:         &access,
	}
	if b.Business != nil {
		bp, err := channel.NormalizeBusinessPolicy(*b.Business)
		if err != nil {
			return nil, err
		}
		s.Business = bp
	}
	settings, err := s.toMap()
	if err != nil {
		return nil, err
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
	dto, err := toBusinessLinkDTO(ch, a.publicURL, true)
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
	var nextLoc *Localization
	if b.Localization != nil {
		normalized, err := normalizeLocalization(*b.Localization, true)
		if err != nil {
			return nil, err
		}
		nextLoc = &normalized
	}
	var nextCode *string
	if strings.TrimSpace(b.PublicCode) != "" {
		normalized, err := channel.NormalizePublicCode(b.PublicCode)
		if err != nil {
			return nil, err
		}
		nextCode = &normalized
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
		if nextLoc != nil {
			s.Localization = *nextLoc
		}
		if b.Card != nil {
			card := normalizeCard(*b.Card)
			s.Card = &card
		}
		if b.Intake != nil {
			intake := normalizeIntake(*b.Intake)
			s.Intake = &intake
		}
		if b.Access != nil {
			cur := s.resolvedAccess()
			mode := b.Access.Mode
			if mode == "" {
				mode = cur.Mode
			}
			if mode != "passcode" {
				mode = "public"
			}
			cur.Mode = mode
			cur.ExpiresAt = b.Access.ExpiresAt
			if b.Access.ClearPasscode || mode == "public" {
				cur.PasscodeHash = ""
			}
			if pass := strings.TrimSpace(b.Access.Passcode); pass != "" {
				cur.PasscodeHash = hashPasscode(ch.ID, pass)
				cur.Mode = "passcode"
			}
			if cur.Mode == "passcode" && cur.PasscodeHash == "" {
				return ErrInvalidPasscode.New(errorx.Field("access.passcode"))
			}
			s.Access = &cur
		}
		if b.Business != nil {
			bp, err := channel.NormalizeBusinessPolicy(*b.Business)
			if err != nil {
				return err
			}
			s.Business = bp
		}
		settings, err := s.toMap()
		if err != nil {
			return err
		}
		ch.Name = strings.TrimSpace(b.Name)
		ch.Enabled = b.Enabled
		ch.Settings = settings
		cols := []string{"name", "enabled", "settings", "updated_at"}
		if nextCode != nil {
			ch.PublicCode = nextCode
			cols = append(cols, "public_code")
		}
		if _, err := tx.NewUpdate().Model(ch).Column(cols...).WherePK().Exec(ctx); err != nil {
			if database.IsUniqueViolation(err, "channels_public_code_unique") {
				return channel.ErrPublicCodeTaken.New(errorx.Field("public_code"))
			}
			return errors.Wrap(err, "update business link")
		}
		updated = ch
		return nil
	})
	if err != nil {
		return nil, err
	}
	dto, err := toBusinessLinkDTO(updated, a.publicURL, true)
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

// RotateBusinessLinkCode 一键重置个人网页商务沟通链接的公开短码（旧短链失效，历史会话保留）。
type RotateBusinessLinkCode struct {
	db        *bun.DB
	publicURL string
}

// NewRotateBusinessLinkCode 返回重置短码的动作。
func NewRotateBusinessLinkCode(db *bun.DB, publicURL string) *RotateBusinessLinkCode {
	return &RotateBusinessLinkCode{db: db, publicURL: publicURL}
}

// Handle 为属于 sub 的商务沟通链接 id 重新生成新的公开短码。
func (a *RotateBusinessLinkCode) Handle(ctx context.Context, sub identity.Subject, id string) (*BusinessLinkDTO, error) {
	if sub.Type != member.Type {
		return nil, channel.ErrNotFound.New()
	}
	var updated *channel.Channel
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		ch, err := findOwnedBusinessLink(ctx, tx, sub.Tenant, sub.ID, id, true)
		if err != nil {
			return err
		}
		code := channel.GeneratePublicCode()
		ch.PublicCode = &code
		if _, err := tx.NewUpdate().Model(ch).Column("public_code", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "rotate business link public code")
		}
		updated = ch
		return nil
	})
	if err != nil {
		return nil, err
	}
	dto, err := toBusinessLinkDTO(updated, a.publicURL, true)
	if err != nil {
		return nil, err
	}
	return &dto, nil
}

// HTTP 处理 POST /t/{slug}/api/my/business-links/{id}/rotate-code。
func (a *RotateBusinessLinkCode) HTTP(ctx context.Context, in *BusinessLinkIDInput) (*BusinessLinkOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
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
func RegisterBusinessLinkRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, publicURL string, hooks ...BusinessLinkHooks) {
	var h BusinessLinkHooks
	if len(hooks) > 0 {
		h = hooks[0]
	}
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
	rest.Register(api, route("business-link-get", http.MethodGet, "/my/business-links/{id}", "读取我的商务沟通链接详情"), NewGetBusinessLink(db, publicURL).HTTP)
	rest.Register(api, createOp, NewCreateBusinessLinkWithHooks(db, publicURL, h).HTTP)
	rest.Register(api, route("business-link-update", http.MethodPut, "/my/business-links/{id}", "修改我的商务沟通链接"), NewUpdateBusinessLink(db, publicURL).HTTP)
	rest.Register(api, route("business-link-rotate-code", http.MethodPost, "/my/business-links/{id}/rotate-code", "重置我的商务沟通链接短码"), NewRotateBusinessLinkCode(db, publicURL).HTTP)
	rest.Register(api, deleteOp, NewDeleteBusinessLink(db).HTTP)
}
