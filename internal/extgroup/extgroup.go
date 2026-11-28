// Package extgroup 管理机器人所在第三方群聊的轻量路由配置（external_groups）与群内发言人资料（external_users）。
// 外部群不搬运第三方群聊的日常消息，仅用于群内 @ 机器人与命令的服务请求归并、默认团队收件箱路由、组织与 AI 用途治理。
package extgroup

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
	"agents-desk/internal/lang"
	"agents-desk/internal/organization"
	"agents-desk/internal/team"
)

var (
	// ErrNotFound 是外部群不存在，或者属于别的工作区。
	ErrNotFound = errorx.Define(errorx.NotFound, "extgroup.not_found")
	// ErrRemoved 是机器人已被移出该外部群，无法继续向群发送消息。
	ErrRemoved = errorx.Define(errorx.Conflict, "extgroup.removed")
	// ErrInvalidLanguage 是设置的群语言不是合法的 BCP 47 标签。
	ErrInvalidLanguage = errorx.Define(errorx.InvalidArgument, "extgroup.invalid_language")
)

// Status 是外部群的生命周期状态。
type Status string

const (
	StatusPendingSetup Status = "pending_setup" // 待设置：新群默认态，人工可正常接待，AI 与业务工具保持关闭
	StatusActive       Status = "active"        // 已启用：管理员已完成归属团队、组织或 AI 用途配置
	StatusRemoved      Status = "removed"       // 已移出：机器人离开该群，保留历史会话，禁止继续投递
)

// Statuses 是全部外部群状态枚举，显示文字在 extgroup.status.<状态>。
var Statuses = enum.New("extgroup.status", enum.Item(StatusPendingSetup), enum.Item(StatusActive), enum.Item(StatusRemoved))

// Schema 让 OpenAPI 中的 Status 只接受目录取值。
func (Status) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Statuses) }

// Group 是 external_groups 表的数据库模型。
type Group struct {
	bun.BaseModel `bun:"table:external_groups"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID       string         `bun:"tenant_id,notnull"`
	ChannelID      string         `bun:"channel_id,notnull"`
	ExternalID     string         `bun:"external_id,notnull"`
	Name           string         `bun:"name,notnull"`
	Status         Status         `bun:"status,notnull"`
	TeamID         *string        `bun:"team_id"`
	OrganizationID *string        `bun:"organization_id"`
	AIProfileID    *string        `bun:"ai_profile_id"`
	Language       string         `bun:"language,notnull"`
	LanguageManual bool           `bun:"language_manual,notnull"`
	Metadata       map[string]any `bun:"metadata,type:jsonb,notnull"`
	LastActiveAt   *time.Time     `bun:"last_active_at"`
}

// Summary 是嵌入会话与工单 DTO 的外部群概要信息。
type Summary struct {
	ID             string  `json:"id"`
	ChannelID      string  `json:"channel_id"`
	ExternalID     string  `json:"external_id"`
	Name           string  `json:"name"`
	Status         Status  `json:"status"`
	TeamID         *string `json:"team_id,omitempty"`
	OrganizationID *string `json:"organization_id,omitempty"`
	Language       string  `json:"language"`
	LanguageManual bool    `json:"language_manual"`
}

// Summary 返回外部群 g 的轻量概要。
func (g *Group) Summary() Summary {
	return Summary{
		ID:             g.ID,
		ChannelID:      g.ChannelID,
		ExternalID:     g.ExternalID,
		Name:           g.Name,
		Status:         g.Status,
		TeamID:         g.TeamID,
		OrganizationID: g.OrganizationID,
		Language:       g.Language,
		LanguageManual: g.LanguageManual,
	}
}

// GroupDTO 是设置页管理外部群的完整视图。
type GroupDTO struct {
	ID             string                `json:"id"`
	Channel        channel.Summary       `json:"channel"`
	ExternalID     string                `json:"external_id"`
	Name           string                `json:"name"`
	Status         Status                `json:"status"`
	TeamID         *string               `json:"team_id,omitempty"`
	OrganizationID *string               `json:"organization_id,omitempty"`
	Organization   *organization.Summary `json:"organization,omitempty" doc:"所属客户公司/组织"`
	Language       string                `json:"language"`
	LanguageManual bool                  `json:"language_manual"`
	LastActiveAt   *time.Time            `json:"last_active_at,omitempty"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
}

// User 是 external_users 表的数据库模型，记录群内发言人的显示名称与平台账号，不写入客户联系人表。
type User struct {
	bun.BaseModel `bun:"table:external_users"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID   string         `bun:"tenant_id,notnull"`
	ChannelID  string         `bun:"channel_id,notnull"`
	ExternalID string         `bun:"external_id,notnull"`
	Name       string         `bun:"name,notnull"`
	AvatarURL  string         `bun:"avatar_url,notnull"`
	Metadata   map[string]any `bun:"metadata,type:jsonb,notnull"`
}

// Find 读取工作区 tenant 中的外部群 id，不存在或属于别的工作区时返回 ErrNotFound。
func Find(ctx context.Context, db bun.IDB, tenant, id string) (*Group, error) {
	g := new(Group)
	err := db.NewSelect().Model(g).Where("id = ? AND tenant_id = ?", id, tenant).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound.New()
	}
	return g, errors.Wrap(err, "load external group")
}

// EnsureGroup 在事务 tx 中按 (tenant, channelID, externalID) 查找或新建外部群记录，并同步群名与最近活跃时间。
// 若历史记录曾处于 removed 状态（机器人被重新拉回群），恢复为 pending_setup 等待重新确认配置。
func EnsureGroup(ctx context.Context, tx bun.IDB, tenant, channelID, externalID, name string, at time.Time) (*Group, bool, error) {
	extID := strings.TrimSpace(externalID)
	title := strings.TrimSpace(name)
	g := new(Group)
	err := tx.NewSelect().Model(g).
		Where("tenant_id = ? AND channel_id = ? AND external_id = ?", tenant, channelID, extID).
		For("UPDATE").Scan(ctx)
	switch {
	case err == nil:
		var cols []string
		if title != "" && g.Name != title {
			g.Name, cols = title, append(cols, "name")
		}
		if g.Status == StatusRemoved {
			g.Status, cols = StatusPendingSetup, append(cols, "status")
		}
		if g.LastActiveAt == nil || at.After(*g.LastActiveAt) {
			t := at
			g.LastActiveAt, cols = &t, append(cols, "last_active_at")
		}
		if len(cols) > 0 {
			g.UpdatedAt = at
			if _, err := tx.NewUpdate().Model(g).Column(append(cols, "updated_at")...).WherePK().Exec(ctx); err != nil {
				return nil, false, errors.Wrap(err, "update external group")
			}
		}
		return g, false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return nil, false, errors.Wrap(err, "load external group by external id")
	}

	t := at
	g = &Group{
		ID:           newID(),
		TenantID:     tenant,
		ChannelID:    channelID,
		ExternalID:   extID,
		Name:         title,
		Status:       StatusPendingSetup,
		Metadata:     map[string]any{},
		LastActiveAt: &t,
	}
	g.CreatedAt, g.UpdatedAt = at, at
	if _, err := tx.NewInsert().Model(g).On("CONFLICT (tenant_id, channel_id, external_id) DO NOTHING").Exec(ctx); err != nil {
		return nil, false, errors.Wrap(err, "insert external group")
	}
	if err := tx.NewSelect().Model(g).
		Where("tenant_id = ? AND channel_id = ? AND external_id = ?", tenant, channelID, extID).
		For("UPDATE").Scan(ctx); err != nil {
		return nil, false, errors.Wrap(err, "reload external group")
	}
	return g, true, nil
}

// EnsureUser 在事务 tx 中按 (tenant, channelID, externalID) 查找或新建轻量群发言人资料，按需刷新姓名与头像。
func EnsureUser(ctx context.Context, tx bun.IDB, tenant, channelID, externalID, name, avatarURL string) (*User, error) {
	extID := strings.TrimSpace(externalID)
	display := strings.TrimSpace(name)
	avatar := strings.TrimSpace(avatarURL)
	at := now()

	u := new(User)
	err := tx.NewSelect().Model(u).
		Where("tenant_id = ? AND channel_id = ? AND external_id = ?", tenant, channelID, extID).
		Scan(ctx)
	switch {
	case err == nil:
		var cols []string
		if display != "" && u.Name != display {
			u.Name, cols = display, append(cols, "name")
		}
		if avatar != "" && u.AvatarURL != avatar {
			u.AvatarURL, cols = avatar, append(cols, "avatar_url")
		}
		if len(cols) > 0 {
			u.UpdatedAt = at
			if _, err := tx.NewUpdate().Model(u).Column(append(cols, "updated_at")...).WherePK().Exec(ctx); err != nil {
				return nil, errors.Wrap(err, "update external user")
			}
		}
		return u, nil
	case !errors.Is(err, sql.ErrNoRows):
		return nil, errors.Wrap(err, "load external user")
	}

	u = &User{
		ID:         newID(),
		TenantID:   tenant,
		ChannelID:  channelID,
		ExternalID: extID,
		Name:       display,
		AvatarURL:  avatar,
		Metadata:   map[string]any{},
	}
	u.CreatedAt, u.UpdatedAt = at, at
	if _, err := tx.NewInsert().Model(u).On("CONFLICT (tenant_id, channel_id, external_id) DO NOTHING").Exec(ctx); err != nil {
		return nil, errors.Wrap(err, "insert external user")
	}
	if err := tx.NewSelect().Model(u).
		Where("tenant_id = ? AND channel_id = ? AND external_id = ?", tenant, channelID, extID).
		Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "reload external user")
	}
	return u, nil
}

// SyncJoined 由连接器在收到“机器人被拉入群”事件时调用：创建或恢复外部群为 pending_setup 并同步群名。
func SyncJoined(ctx context.Context, db *bun.DB, tenant, channelID, externalID, name string) (*Group, error) {
	var out *Group
	err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
		g, _, err := EnsureGroup(ctx, tx, tenant, channelID, externalID, name, now())
		out = g
		return err
	})
	return out, err
}

// SyncRemoved 由连接器在收到“机器人被移出群”事件时调用：将对应外部群标记为 removed。
func SyncRemoved(ctx context.Context, db *bun.DB, tenant, channelID, externalID string) error {
	_, err := db.NewUpdate().Model((*Group)(nil)).
		Set("status = ?", StatusRemoved).
		Where("tenant_id = ? AND channel_id = ? AND external_id = ?", tenant, channelID, strings.TrimSpace(externalID)).
		Exec(ctx)
	return errors.Wrap(err, "mark external group removed")
}

// ListInput 是外部群列表的查询条件。
type ListInput struct {
	ChannelID      string           `query:"channel_id" format:"uuid" doc:"可选：按渠道筛选"`
	OrganizationID string           `query:"organization_id" format:"uuid" doc:"可选：按所属客户组织筛选"`
	Audience       channel.Audience `query:"audience" doc:"可选：按所属渠道服务对象筛选（customer、partner 或 internal）"`
	Status         Status           `query:"status" doc:"可选：按状态筛选（pending_setup、active、removed）"`
}

// ListOutput 是外部群列表的响应。
type ListOutput struct {
	Body []GroupDTO
}

// List 列出当前工作区的外部群配置。
type List struct {
	db *bun.DB
}

// NewList 返回外部群列表动作。
func NewList(db *bun.DB) *List { return &List{db: db} }

// Handle 返回 sub 所在工作区符合筛选条件的外部群列表（待设置的排在最前，便于管理员及时配置）。
func (a *List) Handle(ctx context.Context, sub identity.Subject, in *ListInput) ([]GroupDTO, error) {
	var rows []Group
	q := a.db.NewSelect().Model(&rows).Where("\"group\".tenant_id = ?", sub.Tenant)
	if in != nil && in.ChannelID != "" {
		q = q.Where("\"group\".channel_id = ?", in.ChannelID)
	}
	if in != nil && in.OrganizationID != "" {
		q = q.Where("\"group\".organization_id = ?", in.OrganizationID)
	}
	if in != nil && in.Audience != "" {
		q = q.Where("EXISTS (SELECT 1 FROM channels ch WHERE ch.id = \"group\".channel_id AND ch.tenant_id = \"group\".tenant_id AND ch.audience = ?)", in.Audience)
	}
	if in != nil && in.Status != "" {
		q = q.Where("\"group\".status = ?", in.Status)
	}
	q = q.OrderExpr("(\"group\".status = 'pending_setup') DESC, COALESCE(\"group\".last_active_at, \"group\".updated_at) DESC, \"group\".id DESC")
	if err := q.Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "list external groups")
	}
	return toDTOs(ctx, a.db, sub.Tenant, rows)
}

// HTTP 处理 GET /t/{slug}/api/external-groups。
func (a *List) HTTP(ctx context.Context, in *ListInput) (*ListOutput, error) {
	sub, _ := identity.FromContext(ctx)
	items, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Body: items}, nil
}

// UpdateBody 是管理员配置外部群的请求体。
type UpdateBody struct {
	Name           *string `json:"name,omitempty" maxLength:"120" doc:"群名称覆写"`
	Status         *Status `json:"status,omitempty" doc:"外部群状态：pending_setup 或 active"`
	TeamID         *string `json:"team_id,omitempty" doc:"默认团队收件箱 ID；传空字符串表示回退到渠道默认团队"`
	OrganizationID *string `json:"organization_id,omitempty" doc:"所属组织 ID；传空字符串表示内部群"`
	Language       *string `json:"language,omitempty" maxLength:"35" doc:"群沟通语言（BCP 47）；传空字符串恢复自动识别"`
}

// UpdateInput 是更新外部群配置的 HTTP 请求。
type UpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body UpdateBody
}

// GroupOutput 是返回单个外部群的响应。
type GroupOutput struct {
	Body GroupDTO
}

// Update 更新外部群的路由与治理配置（完成待设置新群的默认团队、归属与激活）。
type Update struct {
	db *bun.DB
}

// NewUpdate 返回更新外部群配置的动作。
func NewUpdate(db *bun.DB) *Update { return &Update{db: db} }

// Handle 更新工作区中外部群 id 的配置，并记录操作日志。
func (a *Update) Handle(ctx context.Context, sub identity.Subject, id string, b UpdateBody) (*GroupDTO, error) {
	var canonicalLang string
	if b.Language != nil {
		l := strings.TrimSpace(*b.Language)
		if l != "" {
			c, ok := lang.Canonical(l)
			if !ok {
				return nil, ErrInvalidLanguage.New(errorx.Field("language"))
			}
			canonicalLang = c
		}
	}

	var updated *Group
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		g := new(Group)
		err := tx.NewSelect().Model(g).Where("id = ? AND tenant_id = ?", id, sub.Tenant).For("UPDATE").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound.New()
		}
		if err != nil {
			return errors.Wrap(err, "load external group for update")
		}
		var cols []string
		props := map[string]any{}
		if b.Name != nil {
			name := strings.TrimSpace(*b.Name)
			if name != g.Name {
				g.Name, cols, props["name"] = name, append(cols, "name"), name
			}
		}
		if b.Status != nil && (*b.Status == StatusActive || *b.Status == StatusPendingSetup) {
			if g.Status != *b.Status {
				g.Status, cols, props["status"] = *b.Status, append(cols, "status"), string(*b.Status)
			}
		}
		if b.TeamID != nil {
			tid := strings.TrimSpace(*b.TeamID)
			if tid == "" {
				if g.TeamID != nil {
					g.TeamID, cols, props["team_id"] = nil, append(cols, "team_id"), ""
				}
			} else {
				var row struct {
					DisabledAt *time.Time `bun:"disabled_at"`
				}
				err := tx.NewSelect().Table("teams").Column("disabled_at").
					Where("id = ? AND tenant_id = ?", tid, sub.Tenant).Scan(ctx, &row)
				if errors.Is(err, sql.ErrNoRows) {
					return team.ErrNotFound.New(errorx.Field("team_id"))
				}
				if err != nil {
					return errors.Wrap(err, "check external group team")
				}
				if row.DisabledAt != nil {
					return team.ErrDisabled.New(errorx.Field("team_id"))
				}
				if g.TeamID == nil || *g.TeamID != tid {
					idCopy := tid
					g.TeamID, cols, props["team_id"] = &idCopy, append(cols, "team_id"), tid
				}
			}
		}
		if b.OrganizationID != nil {
			oid := strings.TrimSpace(*b.OrganizationID)
			if oid == "" {
				if g.OrganizationID != nil {
					g.OrganizationID, cols, props["organization_id"] = nil, append(cols, "organization_id"), ""
				}
			} else if g.OrganizationID == nil || *g.OrganizationID != oid {
				if _, err := organization.Find(ctx, tx, sub.Tenant, oid); err != nil {
					if errors.Is(err, organization.ErrNotFound) {
						return organization.ErrNotFound.New(errorx.Field("organization_id"))
					}
					return err
				}
				idCopy := oid
				g.OrganizationID, cols, props["organization_id"] = &idCopy, append(cols, "organization_id"), oid
			}
		}
		if b.Language != nil {
			if canonicalLang == "" {
				if g.LanguageManual {
					g.LanguageManual, cols, props["language_manual"] = false, append(cols, "language_manual"), false
				}
			} else if g.Language != canonicalLang || !g.LanguageManual {
				g.Language, g.LanguageManual = canonicalLang, true
				cols = append(cols, "language", "language_manual")
				props["language"], props["language_manual"] = canonicalLang, true
			}
		}
		if len(cols) == 0 {
			updated = g
			return nil
		}
		g.UpdatedAt = now()
		if _, err := tx.NewUpdate().Model(g).Column(append(cols, "updated_at")...).WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update external group")
		}
		if err := activity.Record(ctx, tx, sub, activity.Activity{
			Event: "external_group.updated", TargetType: "external_group", TargetID: g.ID, Properties: props,
		}); err != nil {
			return err
		}
		updated = g
		return nil
	})
	if err != nil {
		return nil, err
	}
	dtos, err := toDTOs(ctx, a.db, sub.Tenant, []Group{*updated})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// HTTP 处理 PUT /t/{slug}/api/external-groups/{id}。
func (a *Update) HTTP(ctx context.Context, in *UpdateInput) (*GroupOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &GroupOutput{Body: *dto}, nil
}

func toDTOs(ctx context.Context, db bun.IDB, tenant string, rows []Group) ([]GroupDTO, error) {
	out := make([]GroupDTO, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	seenCh := map[string]bool{}
	seenOrg := map[string]bool{}
	chIDs := make([]string, 0, len(rows))
	orgIDs := make([]string, 0, len(rows))
	for _, r := range rows {
		if !seenCh[r.ChannelID] {
			seenCh[r.ChannelID] = true
			chIDs = append(chIDs, r.ChannelID)
		}
		if r.OrganizationID != nil && *r.OrganizationID != "" && !seenOrg[*r.OrganizationID] {
			seenOrg[*r.OrganizationID] = true
			orgIDs = append(orgIDs, *r.OrganizationID)
		}
	}
	var chRows []channel.Channel
	if err := db.NewSelect().Model(&chRows).Where("tenant_id = ? AND id IN (?)", tenant, bun.In(chIDs)).Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load external group channels")
	}
	chByID := make(map[string]channel.Summary, len(chRows))
	for i := range chRows {
		chByID[chRows[i].ID] = chRows[i].Summary()
	}
	orgs, err := organization.Summaries(ctx, db, tenant, orgIDs)
	if err != nil {
		return nil, err
	}
	for i, r := range rows {
		ch, ok := chByID[r.ChannelID]
		if !ok {
			ch = channel.Summary{ID: r.ChannelID}
		}
		var orgSummary *organization.Summary
		if r.OrganizationID != nil && *r.OrganizationID != "" {
			if s, ok := orgs[*r.OrganizationID]; ok {
				orgSummary = &s
			}
		}
		out[i] = GroupDTO{
			ID:             r.ID,
			Channel:        ch,
			ExternalID:     r.ExternalID,
			Name:           r.Name,
			Status:         r.Status,
			TeamID:         r.TeamID,
			OrganizationID: r.OrganizationID,
			Organization:   orgSummary,
			Language:       r.Language,
			LanguageManual: r.LanguageManual,
			LastActiveAt:   r.LastActiveAt,
			CreatedAt:      r.CreatedAt,
			UpdatedAt:      r.UpdatedAt,
		}
	}
	return out, nil
}

func newID() string  { return uuid.Must(uuid.NewV7()).String() }
func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
