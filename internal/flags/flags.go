// Package flags 管理平台的功能开关目录与管理后台接口，基于 tenon/feature 实现全员开关、按比例放量与按工作区覆盖。
package flags

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
	"github.com/go-tenon/tenon/feature"
	"github.com/go-tenon/tenon/identity"
)

// ErrNotFound 表示请求的开关不在代码声明的目录中。
var ErrNotFound = errorx.Define(errorx.NotFound, "flags.not_found")

// ErrTenantRequired 表示作用域为 tenant 时缺少有效的 tenant_id。
var ErrTenantRequired = errorx.Define(errorx.InvalidArgument, "flags.tenant_required")

// 平台声明的功能开关目录：所有灰度开关集中在这里声明，发布后开关名保持固定。
var (
	// AITranslation 控制工作区会话的 AI 自动语言识别与互译能力，支持按工作区灰度或紧急关闭。
	AITranslation = feature.Flag{
		Name:        "conversation.ai_translation",
		Description: "会话 AI 自动语言识别与实时翻译",
		ByTenant:    true,
		Default:     func(context.Context, identity.Subject) bool { return true },
	}
	// AutoAutomation 控制内置自动化规则引擎，支持按工作区灰度或紧急关闭。
	AutoAutomation = feature.Flag{
		Name:        "conversation.auto_automation",
		Description: "会话内置自动化规则引擎（回复时间、留资、关闭评价）",
		ByTenant:    true,
		Default:     func(context.Context, identity.Subject) bool { return true },
	}
	// WidgetVisitorUploads 控制网页聊天插件与商务链接的访客文件上传开关，默认开启，支持按工作区或全员紧急关闭。
	WidgetVisitorUploads = feature.Flag{
		Name:        "widget.visitor_uploads",
		Description: "网页聊天插件访客附件上传",
		ByTenant:    true,
		Default:     func(context.Context, identity.Subject) bool { return true },
	}
	// AIWorkspace 控制工作区端尚在建设中的 AI Agent 模块入口与占位视图，默认关闭。
	AIWorkspace = feature.Flag{
		Name:        "ai.workspace",
		Description: "工作区 AI 中心与助手预览入口",
		ByTenant:    true,
	}
	// BusinessLinks 控制成员个人网页商务沟通链接功能，支持按工作区灰度或关闭。
	BusinessLinks = feature.Flag{
		Name:        "business.links",
		Description: "个人网页商务沟通链接",
		ByTenant:    true,
		Default:     func(context.Context, identity.Subject) bool { return true },
	}
	// BusinessTelegramAccount 控制成员 Telegram 个人号商务同步功能，支持按工作区灰度或关闭。
	BusinessTelegramAccount = feature.Flag{
		Name:        "business.telegram_account",
		Description: "成员 Telegram 个人号商务同步",
		ByTenant:    true,
		Default:     func(context.Context, identity.Subject) bool { return true },
	}
)

// Catalog 是应用注册的全部功能开关。
var Catalog = feature.Catalog{
	AITranslation,
	AutoAutomation,
	WidgetVisitorUploads,
	AIWorkspace,
	BusinessLinks,
	BusinessTelegramAccount,
}

// NewStore 创建绑定到 db 的功能开关存储。
func NewStore(db bun.IDB) *feature.Store {
	return feature.NewStore(db, Catalog)
}

// ScopeKind 是后台接口中的作用域类型。
type ScopeKind string

const (
	ScopeEveryone ScopeKind = "everyone" // 全员
	ScopeTenant   ScopeKind = "tenant"   // 指定工作区
)

// ScopeKinds 是后台支持的开关作用域类型。
var ScopeKinds = enum.New("flags.scope", enum.Item(ScopeEveryone), enum.Item(ScopeTenant))

// Schema 让 OpenAPI 中的 ScopeKind 带枚举约束和译文。
func (ScopeKind) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, ScopeKinds) }

// Mode 是开关设置模式：开启、关闭或按比例放量。
type Mode string

const (
	ModeOn      Mode = "on"      // 开启
	ModeOff     Mode = "off"     // 关闭
	ModePercent Mode = "percent" // 按比例放量（仅全员）
)

// Modes 是全部开关设置模式。
var Modes = enum.New("flags.mode", enum.Item(ModeOn), enum.Item(ModeOff), enum.Item(ModePercent))

// Schema 让 OpenAPI 中的 Mode 带枚举约束和译文。
func (Mode) Schema(r huma.Registry) *huma.Schema { return contract.EnumSchema(r, Modes) }

// RuleDTO 是一条生效中的开关设置。
type RuleDTO struct {
	Scope      ScopeKind `json:"scope"`
	TenantID   string    `json:"tenant_id,omitempty"`
	TenantName string    `json:"tenant_name,omitempty"`
	TenantSlug string    `json:"tenant_slug,omitempty"`
	Mode       Mode      `json:"mode"`
	Percent    int       `json:"percent,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// FlagDTO 是后台列表中的一个功能开关及其全部生效设置。
type FlagDTO struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	ByTenant    bool      `json:"by_tenant"`
	DefaultOn   bool      `json:"default_on"`
	Everyone    *RuleDTO  `json:"everyone,omitempty"`
	Overrides   []RuleDTO `json:"overrides" nullable:"false"`
}

// ListOutput 是全部功能开关列表的响应。
type ListOutput struct {
	Body struct {
		Items []FlagDTO `json:"items" nullable:"false"`
	}
}

// SetInput 是设置开关状态的请求。
type SetInput struct {
	Name string `path:"name"`
	Body struct {
		Scope    ScopeKind `json:"scope"`
		TenantID string    `json:"tenant_id,omitempty"`
		Mode     Mode      `json:"mode"`
		Percent  int       `json:"percent,omitempty" minimum:"0" maximum:"100"`
	}
}

// ClearInput 是清除某条开关设置的请求。
type ClearInput struct {
	Name     string    `path:"name"`
	Scope    ScopeKind `query:"scope"`
	TenantID string    `query:"tenant_id"`
}

// FlagOutput 是单个开关的响应。
type FlagOutput struct {
	Body FlagDTO
}

// findFlag 按名称从目录中查找开关，不存在返回 ErrNotFound。
func findFlag(store *feature.Store, name string) (feature.Flag, error) {
	for _, f := range store.Catalog() {
		if f.Name == name {
			return f, nil
		}
	}
	return feature.Flag{}, ErrNotFound.New()
}

// toScope 将接口参数转换为 feature.Scope。
func toScope(kind ScopeKind, tenantID string) (feature.Scope, error) {
	switch kind {
	case ScopeEveryone:
		return feature.Everyone, nil
	case ScopeTenant:
		if tenantID == "" {
			return feature.Scope{}, ErrTenantRequired.New(errorx.Field("tenant_id"))
		}
		return feature.Tenant(tenantID), nil
	default:
		return feature.Scope{}, feature.ErrInvalid.New(errorx.Field("scope"))
	}
}

// List 列出目录中的全部功能开关及其设置。
type List struct {
	db    *bun.DB
	store *feature.Store
}

// NewList 返回列出功能开关的动作。
func NewList(db *bun.DB, store *feature.Store) *List { return &List{db: db, store: store} }

// Handle 读取全部开关及其设置，并补全覆盖规则里的工作区名称和 slug。
func (a *List) Handle(ctx context.Context) ([]FlagDTO, error) {
	catalog := a.store.Catalog()
	allSettings := make([][]feature.Setting, len(catalog))
	var tenantIDs []string
	for i, f := range catalog {
		s, err := a.store.Settings(ctx, f)
		if err != nil {
			return nil, err
		}
		allSettings[i] = s
		for _, item := range s {
			if tid := item.Scope.Tenant(); tid != "" {
				tenantIDs = append(tenantIDs, tid)
			}
		}
	}
	tenants, err := loadTenantInfo(ctx, a.db, tenantIDs)
	if err != nil {
		return nil, err
	}
	items := make([]FlagDTO, len(catalog))
	for i, f := range catalog {
		items[i] = buildFlagDTO(ctx, f, allSettings[i], tenants)
	}
	return items, nil
}

// HTTP 处理 GET /features。
func (a *List) HTTP(ctx context.Context, _ *struct{}) (*ListOutput, error) {
	items, err := a.Handle(ctx)
	if err != nil {
		return nil, err
	}
	out := &ListOutput{}
	out.Body.Items = items
	return out, nil
}

// Set 设置指定开关在指定范围内的状态。
type Set struct {
	db    *bun.DB
	store *feature.Store
}

// NewSet 返回设置功能开关的动作。
func NewSet(db *bun.DB, store *feature.Store) *Set { return &Set{db: db, store: store} }

// Handle 在事务中写入开关设置并记录操作日志，返回该开关最新的状态视图。
func (a *Set) Handle(ctx context.Context, sub identity.Subject, in SetInput) (FlagDTO, error) {
	f, err := findFlag(a.store, in.Name)
	if err != nil {
		return FlagDTO{}, err
	}
	scope, err := toScope(in.Body.Scope, in.Body.TenantID)
	if err != nil {
		return FlagDTO{}, err
	}
	var state feature.State
	switch in.Body.Mode {
	case ModeOn:
		state = feature.On
	case ModeOff:
		state = feature.Off
	case ModePercent:
		state = feature.Percent(in.Body.Percent)
	default:
		return FlagDTO{}, feature.ErrInvalid.New(errorx.Field("mode"))
	}
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := a.store.WithDB(tx).Set(ctx, f, scope, state); err != nil {
			return err
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "feature.changed", TargetType: "feature", TargetID: f.Name,
			Properties: map[string]any{
				"name": f.Name, "scope": string(in.Body.Scope),
				"tenant_id": in.Body.TenantID, "mode": string(in.Body.Mode), "percent": in.Body.Percent,
			},
		})
	})
	if err != nil {
		return FlagDTO{}, err
	}
	return loadOneFlag(ctx, a.db, a.store, f)
}

// HTTP 处理 PUT /features/{name}。
func (a *Set) HTTP(ctx context.Context, in *SetInput) (*FlagOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, *in)
	if err != nil {
		return nil, err
	}
	return &FlagOutput{Body: dto}, nil
}

// Clear 清除指定开关在指定范围内的设置。
type Clear struct {
	db    *bun.DB
	store *feature.Store
}

// NewClear 返回清除功能开关设置的动作。
func NewClear(db *bun.DB, store *feature.Store) *Clear { return &Clear{db: db, store: store} }

// Handle 在事务中删除开关设置并记录操作日志，返回该开关最新的状态视图。
func (a *Clear) Handle(ctx context.Context, sub identity.Subject, in ClearInput) (FlagDTO, error) {
	f, err := findFlag(a.store, in.Name)
	if err != nil {
		return FlagDTO{}, err
	}
	scope, err := toScope(in.Scope, in.TenantID)
	if err != nil {
		return FlagDTO{}, err
	}
	err = database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if err := a.store.WithDB(tx).Clear(ctx, f, scope); err != nil {
			return err
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "feature.cleared", TargetType: "feature", TargetID: f.Name,
			Properties: map[string]any{"name": f.Name, "scope": string(in.Scope), "tenant_id": in.TenantID},
		})
	})
	if err != nil {
		return FlagDTO{}, err
	}
	return loadOneFlag(ctx, a.db, a.store, f)
}

// HTTP 处理 DELETE /features/{name}。
func (a *Clear) HTTP(ctx context.Context, in *ClearInput) (*FlagOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, *in)
	if err != nil {
		return nil, err
	}
	return &FlagOutput{Body: dto}, nil
}

// tenantInfo 是覆盖列表中展示的工作区基础信息。
type tenantInfo struct {
	Name string
	Slug string
}

// loadTenantInfo 批量查询指定工作区的名称与 slug。
func loadTenantInfo(ctx context.Context, db bun.IDB, ids []string) (map[string]tenantInfo, error) {
	out := map[string]tenantInfo{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID   string `bun:"id"`
		Name string `bun:"name"`
		Slug string `bun:"slug"`
	}
	if err := db.NewSelect().Table("tenants").Column("id", "name", "slug").Where("id IN (?)", bun.List(ids)).Scan(ctx, &rows); err != nil {
		return nil, errors.Wrap(err, "load tenant info for feature flags")
	}
	for _, r := range rows {
		out[r.ID] = tenantInfo{Name: r.Name, Slug: r.Slug}
	}
	return out, nil
}

// loadOneFlag 读取单个开关的最新状态视图。
func loadOneFlag(ctx context.Context, db bun.IDB, store *feature.Store, f feature.Flag) (FlagDTO, error) {
	s, err := store.Settings(ctx, f)
	if err != nil {
		return FlagDTO{}, err
	}
	var tenantIDs []string
	for _, item := range s {
		if tid := item.Scope.Tenant(); tid != "" {
			tenantIDs = append(tenantIDs, tid)
		}
	}
	tenants, err := loadTenantInfo(ctx, db, tenantIDs)
	if err != nil {
		return FlagDTO{}, err
	}
	return buildFlagDTO(ctx, f, s, tenants), nil
}

// buildFlagDTO 组装单个功能开关的响应结构。
func buildFlagDTO(ctx context.Context, f feature.Flag, settings []feature.Setting, tenants map[string]tenantInfo) FlagDTO {
	defaultOn := f.Default != nil && f.Default(ctx, identity.Subject{})
	dto := FlagDTO{
		Name:        f.Name,
		Description: f.Description,
		ByTenant:    f.ByTenant,
		DefaultOn:   defaultOn,
		Overrides:   []RuleDTO{},
	}
	for _, s := range settings {
		tid := s.Scope.Tenant()
		if tid == "" && s.Scope.Subject().IsZero() {
			r := RuleDTO{Scope: ScopeEveryone, Mode: Mode(s.State.Mode), Percent: s.State.Percent, UpdatedAt: s.UpdatedAt}
			dto.Everyone = &r
			continue
		}
		if tid != "" {
			info := tenants[tid]
			dto.Overrides = append(dto.Overrides, RuleDTO{
				Scope: ScopeTenant, TenantID: tid, TenantName: info.Name, TenantSlug: info.Slug,
				Mode: Mode(s.State.Mode), UpdatedAt: s.UpdatedAt,
			})
		}
	}
	return dto
}
