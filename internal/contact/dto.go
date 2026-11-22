package contact

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
)

// ContactDTO 是接口中的联系人概要信息，用于联系人列表和资料页。
type ContactDTO struct {
	ID                string         `json:"id"`
	Number            int64          `json:"number" doc:"联系人在工作区内的递增编号，从 1 开始"`
	Kind              Kind           `json:"kind"`
	Name              string         `json:"name"`
	Email             string         `json:"email"`
	ExternalUserID    string         `json:"external_user_id,omitempty" doc:"产品后端传来的用户编号；线索为空"`
	Language          string         `json:"language" doc:"客户的语言（BCP 47），按最近几条消息识别或成员手动设置"`
	LanguageManual    bool           `json:"language_manual" doc:"语言是成员手动设置的：收到的消息不再改它"`
	Location          string         `json:"location" doc:"所在地"`
	TimeZone          string         `json:"time_zone" doc:"IANA 时区名，用于显示对方的当地时间"`
	Referrer          string         `json:"referrer" doc:"首次访问的来源页面"`
	Notes             string         `json:"notes" doc:"成员填写的内部备注，只给成员看"`
	Attributes        map[string]any `json:"attributes" doc:"自定义属性"`
	ConversationCount int            `json:"conversation_count" doc:"当前成员可见的该联系人会话数"`
	LastSeenAt        *time.Time     `json:"last_seen_at,omitempty" doc:"最近活跃或联系的时间"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

// IdentityDTO 是联系人在某个渠道中的身份。
type IdentityDTO struct {
	Channel    channel.Summary `json:"channel" doc:"所属渠道"`
	ExternalID string          `json:"external_id" doc:"在该渠道中的外部编号"`
	CreatedAt  time.Time       `json:"created_at"`
}

// ContactDetailDTO 是联系人资料页返回的完整信息，包含渠道身份列表。
type ContactDetailDTO struct {
	ContactDTO
	Identities []IdentityDTO `json:"identities" doc:"联系人在各个渠道中的身份"`
}

// ContactDetailOutput 是返回单个联系人资料的响应。
type ContactDetailOutput struct {
	Body ContactDetailDTO
}

// toDTOs 把一批联系人转换为接口中的形式，并一次查出每个联系人对 sub 可见的会话数。
func toDTOs(ctx context.Context, db bun.IDB, sub identity.Subject, rows []Contact) ([]ContactDTO, error) {
	out := make([]ContactDTO, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	var counts []struct {
		ContactID string `bun:"contact_id"`
		Count     int    `bun:"count"`
	}
	err := visibleConversations(
		db.NewSelect().TableExpr("conversations AS conv").
			Column("conv.contact_id").ColumnExpr("count(*) AS count").
			Where("conv.tenant_id = ? AND conv.contact_id IN (?)", sub.Tenant, bun.In(ids)),
		sub,
	).Group("conv.contact_id").Scan(ctx, &counts)
	if err != nil {
		return nil, errors.Wrap(err, "count contact conversations")
	}
	byID := make(map[string]int, len(counts))
	for _, r := range counts {
		byID[r.ContactID] = r.Count
	}
	for i := range rows {
		c := &rows[i]
		attrs := c.Attributes
		if attrs == nil {
			attrs = map[string]any{}
		}
		out[i] = ContactDTO{
			ID: c.ID, Number: c.Number, Kind: c.Kind, Name: c.Name, Email: c.Email, ExternalUserID: c.ExternalUserID,
			Language: c.Language, LanguageManual: c.LanguageManual,
			Location: c.Location, TimeZone: c.TimeZone, Referrer: c.Referrer, Notes: c.Notes,
			Attributes: attrs, ConversationCount: byID[c.ID],
			LastSeenAt: c.LastSeenAt, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		}
	}
	return out, nil
}

// loadDetail 读取 sub 所在工作区中联系人 c 的完整资料（含渠道身份和 sub 可见的会话数）。
func loadDetail(ctx context.Context, db bun.IDB, sub identity.Subject, c *Contact) (*ContactDetailDTO, error) {
	dtos, err := toDTOs(ctx, db, sub, []Contact{*c})
	if err != nil {
		return nil, err
	}
	var idRows []Identity
	if err := db.NewSelect().Model(&idRows).
		Where("tenant_id = ? AND contact_id = ?", sub.Tenant, c.ID).
		OrderExpr("created_at ASC, channel_id ASC").Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load contact identities")
	}
	channels := map[string]channel.Summary{}
	if len(idRows) > 0 {
		chIDs := make([]string, len(idRows))
		for i, id := range idRows {
			chIDs[i] = id.ChannelID
		}
		var chRows []channel.Channel
		if err := db.NewSelect().Model(&chRows).
			Where("tenant_id = ? AND id IN (?)", sub.Tenant, bun.In(chIDs)).Scan(ctx); err != nil {
			return nil, errors.Wrap(err, "load identity channels")
		}
		for i := range chRows {
			channels[chRows[i].ID] = chRows[i].Summary()
		}
	}
	identities := make([]IdentityDTO, 0, len(idRows))
	for _, id := range idRows {
		ch, ok := channels[id.ChannelID]
		if !ok {
			ch = channel.Summary{ID: id.ChannelID}
		}
		identities = append(identities, IdentityDTO{Channel: ch, ExternalID: id.ExternalID, CreatedAt: id.CreatedAt})
	}
	return &ContactDetailDTO{ContactDTO: dtos[0], Identities: identities}, nil
}
