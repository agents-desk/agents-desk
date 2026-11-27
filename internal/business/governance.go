package business

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/account"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
)

// OwnerSummary 是管理员治理视图中商务渠道归属成员的概要信息。
type OwnerSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Title     string `json:"title,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

// ChannelItemDTO 是管理员视角下的一条个人商务渠道治理记录（不含密钥或会话密文）。
type ChannelItemDTO struct {
	ID                string                 `json:"id"`
	Kind              channel.Kind           `json:"kind"`
	Name              string                 `json:"name"`
	Enabled           bool                   `json:"enabled"`
	Owner             OwnerSummary           `json:"owner"`
	PublicCode        string                 `json:"public_code,omitempty" doc:"网页商务链接的公开短码"`
	ChatURL           string                 `json:"chat_url,omitempty" doc:"网页商务链接访问地址"`
	Status            string                 `json:"status,omitempty" doc:"Telegram 个人号连接状态（connected/disconnected/auth_expired）"`
	LastError         string                 `json:"last_error,omitempty" doc:"Telegram 个人号最近连接错误"`
	IdentityLabel     string                 `json:"identity_label,omitempty" doc:"Telegram 个人号对外身份摘要（昵称/@username）"`
	SyncedPeerCount   int                    `json:"synced_peer_count" doc:"Telegram 个人号已同步白名单会话数"`
	ConversationCount int                    `json:"conversation_count" doc:"该商务渠道累计产生的外部商务会话数"`
	Business          channel.BusinessPolicy `json:"business" doc:"渠道级通用商务策略（默认协作成员、自动标签、翻译覆写）"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

// ListChannelsInput 是管理员查询全工作区商务渠道列表的筛选参数。
type ListChannelsInput struct {
	Kind          string `query:"kind" enum:",business_link,telegram_account" doc:"按渠道类型筛选"`
	OwnerMemberID string `query:"owner_member_id" doc:"按归属成员 ID 筛选"`
	Enabled       string `query:"enabled" enum:",true,false" doc:"按启用状态筛选"`
	Q             string `query:"q" maxLength:"100" doc:"按渠道名称或成员姓名模糊搜索"`
}

// ListChannelsOutput 是全工作区商务渠道治理列表的响应。
type ListChannelsOutput struct {
	Body []ChannelItemDTO
}

// SetEnabledInput 是管理员强制启用或停用指定商务渠道的请求。
type SetEnabledInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Enabled bool `json:"enabled"`
	}
}

// ChannelItemOutput 是单个商务渠道治理视图的响应。
type ChannelItemOutput struct {
	Body ChannelItemDTO
}

// AddCollaboratorsBody 是管理员应急为商务渠道批量添加协作成员的请求体。
type AddCollaboratorsBody struct {
	MemberIDs       []string `json:"member_ids" minItems:"1" maxItems:"20" doc:"要加入协作的工作区成员 ID 列表"`
	ApplyToExisting bool     `json:"apply_to_existing" doc:"是否同时将成员加入该渠道已有的全部外部商务会话"`
	AddToDefault    bool     `json:"add_to_default" doc:"是否同时写入渠道默认协作成员，后续新商务会话自动拉入"`
}

// AddCollaboratorsInput 是管理员应急添加协作成员的 HTTP 请求。
type AddCollaboratorsInput struct {
	ID   string `path:"id" format:"uuid"`
	Body AddCollaboratorsBody
}

// AddCollaboratorsResult 是应急添加协作成员的结果摘要。
type AddCollaboratorsResult struct {
	UpdatedChats           int      `json:"updated_chats" doc:"实际同步加入的现有外部商务会话数"`
	DefaultCollaboratorIDs []string `json:"default_collaborator_ids" nullable:"false" doc:"更新后的渠道默认协作成员 ID 列表"`
}

// AddCollaboratorsOutput 是应急添加协作成员的 HTTP 响应。
type AddCollaboratorsOutput struct {
	Body AddCollaboratorsResult
}

// Governance 聚合管理员视角的全工作区商务渠道列表、强制启停与应急协作成员接管动作。
type Governance struct {
	db          *bun.DB
	roles       *authz.Store
	files       *filesystem.Service
	down        conversation.Downstream
	publicURL   string
	onTGChanged func()
}

// NewGovernance 返回工作区商务渠道管理员治理动作组。
func NewGovernance(db *bun.DB, roles *authz.Store, files *filesystem.Service, down conversation.Downstream, publicURL string, onTGChanged func()) *Governance {
	return &Governance{db: db, roles: roles, files: files, down: down, publicURL: publicURL, onTGChanged: onTGChanged}
}

// ListChannels 返回 sub 所在工作区中所有成员的个人商务渠道列表。
func (g *Governance) ListChannels(ctx context.Context, sub identity.Subject, in ListChannelsInput) ([]ChannelItemDTO, error) {
	var rows []channel.Channel
	q := g.db.NewSelect().Model(&rows).
		Where("tenant_id = ? AND kind IN (?) AND owner_member_id IS NOT NULL", sub.Tenant, bun.In([]channel.Kind{channel.BusinessLink, channel.TelegramAccount}))
	if in.Kind != "" {
		q = q.Where("kind = ?", in.Kind)
	}
	if ownerID := strings.TrimSpace(in.OwnerMemberID); ownerID != "" {
		if _, err := uuid.Parse(ownerID); err != nil {
			return []ChannelItemDTO{}, nil
		}
		q = q.Where("owner_member_id = ?", ownerID)
	}
	switch in.Enabled {
	case "true":
		q = q.Where("enabled = TRUE")
	case "false":
		q = q.Where("enabled = FALSE")
	}
	if err := q.OrderExpr("created_at DESC, id DESC").Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "list tenant business channels")
	}
	items, err := g.buildItems(ctx, sub.Tenant, rows)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(in.Q))
	if needle == "" {
		return items, nil
	}
	filtered := make([]ChannelItemDTO, 0, len(items))
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Name), needle) ||
			strings.Contains(strings.ToLower(it.Owner.Name), needle) ||
			strings.Contains(strings.ToLower(it.IdentityLabel), needle) ||
			strings.Contains(strings.ToLower(it.PublicCode), needle) {
			filtered = append(filtered, it)
		}
	}
	return filtered, nil
}

// SetChannelEnabled 由管理员启用或停用工作区中的任意个人商务渠道，并记录审计日志。
func (g *Governance) SetChannelEnabled(ctx context.Context, sub identity.Subject, id string, enabled bool) (*ChannelItemDTO, error) {
	var updated *channel.Channel
	err := database.RunInTx(ctx, g.db, nil, func(ctx context.Context, tx database.Tx) error {
		ch, err := findPersonalChannel(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		if ch.Kind == channel.TelegramAccount && !enabled {
			if ch.Settings == nil {
				ch.Settings = map[string]any{}
			}
			ch.Settings["status"] = "disconnected"
		}
		ch.Enabled = enabled
		if _, err := tx.NewUpdate().Model(ch).Column("enabled", "settings", "updated_at").WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update business channel enabled")
		}
		if err := activity.Record(ctx, tx, sub, activity.Activity{
			Event: "business_channel.enabled_changed", TargetType: "channel", TargetID: ch.ID,
			Properties: map[string]any{"kind": string(ch.Kind), "enabled": enabled},
		}); err != nil {
			return err
		}
		updated = ch
		return nil
	})
	if err != nil {
		return nil, err
	}
	if updated.Kind == channel.TelegramAccount && g.onTGChanged != nil {
		g.onTGChanged()
	}
	items, err := g.buildItems(ctx, sub.Tenant, []channel.Channel{*updated})
	if err != nil {
		return nil, err
	}
	return &items[0], nil
}

// AddCollaborators 由管理员应急将工作区成员加入某商务渠道的现有会话及/或默认协作名单。
func (g *Governance) AddCollaborators(ctx context.Context, sub identity.Subject, id string, b AddCollaboratorsBody) (*AddCollaboratorsResult, error) {
	seen := map[string]bool{}
	memberIDs := make([]string, 0, len(b.MemberIDs))
	for _, raw := range b.MemberIDs {
		mid := strings.TrimSpace(raw)
		if mid == "" || seen[mid] {
			continue
		}
		if _, err := uuid.Parse(mid); err != nil {
			return nil, ErrInvalidMember.New(errorx.Field("member_ids"))
		}
		seen[mid] = true
		memberIDs = append(memberIDs, mid)
	}
	if len(memberIDs) == 0 {
		return nil, ErrInvalidMember.New(errorx.Field("member_ids"))
	}
	// 校验每个成员均属于当前工作区且未停用
	for _, mid := range memberIDs {
		m, err := member.Find(ctx, g.db, mid)
		if err != nil || m.DisabledAt != nil {
			return nil, ErrInvalidMember.New(errorx.Field("member_ids"))
		}
		if g.roles != nil {
			rs, err := g.roles.RolesOf(ctx, m.Ref(), sub.Tenant)
			if err != nil {
				return nil, errors.Wrap(err, "check collaborator tenant roles")
			}
			if len(rs) == 0 {
				return nil, ErrInvalidMember.New(errorx.Field("member_ids"))
			}
		}
	}

	var res AddCollaboratorsResult
	err := database.RunInTx(ctx, g.db, nil, func(ctx context.Context, tx database.Tx) error {
		ch, err := findPersonalChannel(ctx, tx, sub.Tenant, id, true)
		if err != nil {
			return err
		}
		bp := channel.BusinessPolicyOf(ch.Settings)
		if b.AddToDefault {
			merged := slices.Clone(bp.DefaultCollaboratorIDs)
			for _, mid := range memberIDs {
				if !slices.Contains(merged, mid) {
					merged = append(merged, mid)
				}
			}
			bp.DefaultCollaboratorIDs = merged
			ch.Settings = channel.SetBusinessPolicy(ch.Settings, bp)
			if _, err := tx.NewUpdate().Model(ch).Column("settings", "updated_at").WherePK().Exec(ctx); err != nil {
				return errors.Wrap(err, "update channel default collaborators")
			}
		}
		if bp.DefaultCollaboratorIDs == nil {
			res.DefaultCollaboratorIDs = []string{}
		} else {
			res.DefaultCollaboratorIDs = bp.DefaultCollaboratorIDs
		}

		if b.ApplyToExisting {
			var chatIDs []string
			if err := tx.NewSelect().Table("chats").Column("conversation_id").
				Where("tenant_id = ? AND scope = 'external' AND channel_id = ?", sub.Tenant, ch.ID).
				Scan(ctx, &chatIDs); err != nil {
				return errors.Wrap(err, "load channel external chats")
			}
			now := time.Now().UTC()
			touched := make([]string, 0, len(chatIDs))
			for _, cid := range chatIDs {
				addedAny := false
				for _, mid := range memberIDs {
					r, err := tx.NewRaw(`INSERT INTO conversation_participants (conversation_id, participant_type, participant_id, created_at)
						VALUES (?, 'member', ?, ?) ON CONFLICT DO NOTHING`, cid, mid, now).Exec(ctx)
					if err != nil {
						return errors.Wrap(err, "add emergency collaborator to chat")
					}
					if n, _ := r.RowsAffected(); n > 0 {
						addedAny = true
					}
				}
				if addedAny {
					touched = append(touched, cid)
				}
			}
			res.UpdatedChats = len(touched)
			if len(touched) > 0 {
				if err := conversation.NotifyUpdated(ctx, tx, g.down, sub.Tenant, touched); err != nil {
					return err
				}
			}
		}
		return activity.Record(ctx, tx, sub, activity.Activity{
			Event: "business_channel.collaborators_added", TargetType: "channel", TargetID: ch.ID,
			Properties: map[string]any{
				"member_ids":        memberIDs,
				"apply_to_existing": b.ApplyToExisting,
				"add_to_default":    b.AddToDefault,
				"updated_chats":     res.UpdatedChats,
			},
		})
	})
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// buildItems 批量拼装商务渠道归属人资料、对话数与连接器摘要信息。
func (g *Governance) buildItems(ctx context.Context, tenantID string, rows []channel.Channel) ([]ChannelItemDTO, error) {
	if len(rows) == 0 {
		return []ChannelItemDTO{}, nil
	}
	ownerIDs := make([]string, 0, len(rows))
	channelIDs := make([]string, 0, len(rows))
	seenOwner := map[string]bool{}
	for _, ch := range rows {
		channelIDs = append(channelIDs, ch.ID)
		if ch.OwnerMemberID != nil && *ch.OwnerMemberID != "" && !seenOwner[*ch.OwnerMemberID] {
			seenOwner[*ch.OwnerMemberID] = true
			ownerIDs = append(ownerIDs, *ch.OwnerMemberID)
		}
	}

	owners := map[string]OwnerSummary{}
	if len(ownerIDs) > 0 {
		var mRows []member.Member
		if err := g.db.NewSelect().Model(&mRows).Where("id IN (?)", bun.In(ownerIDs)).Scan(ctx); err != nil {
			return nil, errors.Wrap(err, "load channel owners")
		}
		for i := range mRows {
			m := &mRows[i]
			avatarURL := ""
			if g.files != nil {
				u, err := account.AvatarURL(ctx, g.files, m.AvatarFileID)
				if err == nil {
					avatarURL = u
				}
			}
			owners[m.ID] = OwnerSummary{ID: m.ID, Name: m.Name, Title: m.Title, AvatarURL: avatarURL}
		}
	}

	var convCounts []struct {
		ChannelID string `bun:"channel_id"`
		Count     int    `bun:"count"`
	}
	if err := g.db.NewSelect().Table("chats").
		Column("channel_id").ColumnExpr("count(*) AS count").
		Where("tenant_id = ? AND scope = 'external' AND channel_id IN (?)", tenantID, bun.In(channelIDs)).
		Group("channel_id").Scan(ctx, &convCounts); err != nil {
		return nil, errors.Wrap(err, "count channel external chats")
	}
	countByCh := make(map[string]int, len(convCounts))
	for _, r := range convCounts {
		countByCh[r.ChannelID] = r.Count
	}

	baseURL := strings.TrimRight(strings.TrimSpace(g.publicURL), "/")
	out := make([]ChannelItemDTO, 0, len(rows))
	for i := range rows {
		ch := &rows[i]
		ownerID := ""
		if ch.OwnerMemberID != nil {
			ownerID = *ch.OwnerMemberID
		}
		owner, ok := owners[ownerID]
		if !ok {
			owner = OwnerSummary{ID: ownerID}
		}
		item := ChannelItemDTO{
			ID:                ch.ID,
			Kind:              ch.Kind,
			Name:              ch.Name,
			Enabled:           ch.Enabled,
			Owner:             owner,
			ConversationCount: countByCh[ch.ID],
			Business:          channel.BusinessPolicyOf(ch.Settings),
			CreatedAt:         ch.CreatedAt,
			UpdatedAt:         ch.UpdatedAt,
		}
		switch ch.Kind {
		case channel.BusinessLink:
			if ch.PublicCode != nil && *ch.PublicCode != "" {
				item.PublicCode = *ch.PublicCode
			}
			item.ChatURL = baseURL + "/widget/?channel=" + ch.ID
		case channel.TelegramAccount:
			var tgMeta struct {
				Status      string `json:"status"`
				LastError   string `json:"last_error"`
				Username    string `json:"username"`
				DisplayName string `json:"display_name"`
				SyncedPeers []any  `json:"synced_peers"`
			}
			if raw, err := json.Marshal(ch.Settings); err == nil {
				_ = json.Unmarshal(raw, &tgMeta)
			}
			item.Status = tgMeta.Status
			if item.Status == "" {
				item.Status = "disconnected"
			}
			item.LastError = tgMeta.LastError
			item.SyncedPeerCount = len(tgMeta.SyncedPeers)
			parts := make([]string, 0, 2)
			if tgMeta.DisplayName != "" {
				parts = append(parts, tgMeta.DisplayName)
			}
			if tgMeta.Username != "" {
				parts = append(parts, "@"+tgMeta.Username)
			}
			item.IdentityLabel = strings.Join(parts, " · ")
		}
		out = append(out, item)
	}
	return out, nil
}

// findPersonalChannel 读取工作区 tenant 中指定的个人商务渠道（business_link 或 telegram_account）。
func findPersonalChannel(ctx context.Context, db bun.IDB, tenant, id string, lock bool) (*channel.Channel, error) {
	ch := new(channel.Channel)
	q := db.NewSelect().Model(ch).
		Where("id = ? AND tenant_id = ? AND owner_member_id IS NOT NULL AND kind IN (?)",
			id, tenant, bun.In([]channel.Kind{channel.BusinessLink, channel.TelegramAccount}))
	if lock {
		q = q.For("UPDATE")
	}
	err := q.Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, channel.ErrNotFound.New()
	}
	return ch, errors.Wrap(err, "load personal business channel")
}

// ListHTTP 处理 GET /t/{slug}/api/business-channels。
func (g *Governance) ListHTTP(ctx context.Context, in *ListChannelsInput) (*ListChannelsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	items, err := g.ListChannels(ctx, sub, *in)
	if err != nil {
		return nil, err
	}
	return &ListChannelsOutput{Body: items}, nil
}

// SetEnabledHTTP 处理 PUT /t/{slug}/api/business-channels/{id}/enabled。
func (g *Governance) SetEnabledHTTP(ctx context.Context, in *SetEnabledInput) (*ChannelItemOutput, error) {
	sub, _ := identity.FromContext(ctx)
	item, err := g.SetChannelEnabled(ctx, sub, in.ID, in.Body.Enabled)
	if err != nil {
		return nil, err
	}
	return &ChannelItemOutput{Body: *item}, nil
}

// AddCollaboratorsHTTP 处理 POST /t/{slug}/api/business-channels/{id}/collaborators。
func (g *Governance) AddCollaboratorsHTTP(ctx context.Context, in *AddCollaboratorsInput) (*AddCollaboratorsOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := g.AddCollaborators(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &AddCollaboratorsOutput{Body: *res}, nil
}
