package conversation

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/page"
	"github.com/go-tenon/tenon/search"
	"github.com/go-tenon/tenon/search/gse"

	"agents-desk/internal/auth"
	"agents-desk/internal/lang"
	"agents-desk/internal/member"
)

// ItemSearchDoc 是会话时间线中一条消息或内部备注在全文搜索中的索引文档。
// 每条时间线项以自身 ID 为 key 独立同步，避免长会话重写放大；查询时通过 conversation_id 折叠并结合同库子查询复用会话可见范围。
type ItemSearchDoc struct {
	Tenant         string    `json:"tenant" search:"tenant"`
	ConversationID string    `json:"conversation_id" search:"filter"`
	Seq            int64     `json:"seq"`
	Kind           ItemKind  `json:"kind" search:"filter"`
	ActorType      ActorType `json:"actor_type"`
	ActorID        string    `json:"actor_id"`
	CreatedAt      time.Time `json:"created_at" search:"filter"`
	Translated     string    `json:"translated,omitempty" search:"text"` // 客户消息在工作区主要语言中的译文，或成员翻译后发给对方的译文
	Original       string    `json:"original,omitempty" search:"text"`   // 客户或成员发出的原文
	Note           string    `json:"note,omitempty" search:"text"`       // 内部备注文字
}

// SearchIndex 是会话消息与内部备注的全文搜索索引，中文使用 gse 按词语分词。
var SearchIndex = search.Define[ItemSearchDoc]("conversation_items", search.WithTokenizer(gse.Tokenizer))

// Searchable 报告时间线的一项是否应进入全文搜索索引：消息与内部备注中排除系统自动消息，系统事件不索引。
func (it *Item) Searchable() bool {
	return (it.Kind == Message || it.Kind == Note) && it.ActorType != SystemActor
}

// LoadSearchDocs 读取时间线项 key 当前应进入全文索引的文档；不存在、不可索引或无有效文字时返回空以从索引移除。
func LoadSearchDocs(ctx context.Context, tx bun.IDB, key string) ([]ItemSearchDoc, error) {
	var it Item
	err := tx.NewSelect().Model(&it).Where("id = ?", key).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "load timeline item for search")
	}
	if !it.Searchable() {
		return nil, nil
	}

	doc := ItemSearchDoc{
		Tenant:         it.TenantID,
		ConversationID: it.ConversationID,
		Seq:            it.Seq,
		Kind:           it.Kind,
		ActorType:      it.ActorType,
		ActorID:        it.ActorID,
		CreatedAt:      it.CreatedAt,
	}
	switch {
	case it.Kind == Note:
		doc.Note = strings.TrimSpace(it.Content.Text)
	case it.ActorType == ContactActor:
		doc.Original = strings.TrimSpace(it.Content.Text)
		primary, err := member.PrimaryLanguages(ctx, tx, it.TenantID)
		if err != nil {
			return nil, err
		}
		doc.Translated = primaryTranslationsText(it.Derived, primary)
	default:
		// 成员发出的消息：若经翻译发出，原文和发给对方的译文都纳入索引；否则只索引原文
		orig := strings.TrimSpace(it.Content.Original)
		text := strings.TrimSpace(it.Content.Text)
		if orig != "" {
			doc.Original = orig
			if text != "" && text != orig {
				doc.Translated = text
			}
		} else {
			doc.Original = text
		}
	}
	if doc.Original == "" && doc.Translated == "" && doc.Note == "" {
		return nil, nil
	}
	return []ItemSearchDoc{doc}, nil
}

// primaryTranslationsText 从派生内容 derived 中提取工作区主要语言 primary 对应的译文，去重后按行拼接。
func primaryTranslationsText(derived map[string]any, primary []string) string {
	if len(primary) == 0 || derived == nil {
		return ""
	}
	all, _ := derived["translations"].(map[string]any)
	if len(all) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var parts []string
	for _, target := range primary {
		for k, v := range all {
			if !lang.Same(k, target) {
				continue
			}
			s, _ := v.(string)
			s = strings.TrimSpace(s)
			if s != "" && !seen[s] {
				seen[s] = true
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// ScanSearchKeys 按 ID 顺序列出 after 之后所有可索引的时间线项 ID，供 search reindex 重建索引使用。
func ScanSearchKeys(ctx context.Context, db bun.IDB, after string, limit int) ([]string, error) {
	q := db.NewSelect().Model((*Item)(nil)).ColumnExpr("id::text").
		Where("kind IN (?) AND actor_type <> ?", bun.List([]ItemKind{Message, Note}), SystemActor).
		OrderExpr("id").Limit(int64(limit))
	if after != "" {
		q = q.Where("id > ?::uuid", after)
	}
	var keys []string
	return keys, errors.Wrap(q.Scan(ctx, &keys), "scan searchable timeline items")
}

// SearchInput 是会话全文搜索的请求参数。
type SearchInput struct {
	page.OffsetParams
	Q          string   `query:"q" minLength:"1" maxLength:"200" doc:"搜索关键词"`
	Inbox      Inbox    `query:"inbox" doc:"可选：限定在某个收件箱列表内（mine/mentions/unassigned/all）；留空搜索全部可见会话"`
	TeamID     string   `query:"team_id" format:"uuid" doc:"可选：只搜索这个团队收件箱的会话"`
	Status     Status   `query:"status" doc:"可选：只搜索这个状态的会话；留空包含全部状态（打开、稍后处理、已关闭）"`
	AssigneeID string   `query:"assignee_id" format:"uuid" doc:"可选：只搜索负责人是这位成员的会话"`
	ChannelID  string   `query:"channel_id" format:"uuid" doc:"可选：只搜索来自这个渠道的会话"`
	Priority   Priority `query:"priority" doc:"可选：只搜索这个优先级的会话"`
	TagID      string   `query:"tag_id" format:"uuid" doc:"可选：只搜索带有这个标签的会话"`
}

// SearchMatch 是会话中命中的代表性消息或内部备注及其高亮摘要。
type SearchMatch struct {
	ItemID    string         `json:"item_id" doc:"命中的时间线项 ID"`
	Seq       int64          `json:"seq" doc:"命中的时间线序号，用于跳转定位"`
	Kind      ItemKind       `json:"kind" doc:"命中的是消息还是内部备注"`
	Actor     Actor          `json:"actor" doc:"发起者"`
	Field     string         `json:"field" doc:"展示的摘要来源：translated（译文）、original（原文）或 note（内部备注）"`
	Snippet   search.Snippet `json:"snippet" doc:"包含高亮标记的命中摘要"`
	CreatedAt time.Time      `json:"created_at"`
}

// SearchHitDTO 是会话全文搜索的一条命中结果。
type SearchHitDTO struct {
	Conversation ConversationDTO `json:"conversation"`
	Match        SearchMatch     `json:"match" doc:"该会话内最相关的一条命中消息或内部备注"`
	MatchCount   int             `json:"match_count" doc:"该会话内命中的消息和内部备注总条数"`
	Score        float64         `json:"score"`
}

// SearchOutput 是会话全文搜索的响应（固定分页）。
type SearchOutput struct {
	Body page.Offset[SearchHitDTO]
}

// Search 在工作区中按关键词全文搜索会话消息（原文与工作区主要语言译文）和内部备注。
type Search struct {
	db *bun.DB
}

// NewSearch 返回会话全文搜索的业务动作。
func NewSearch(db *bun.DB) *Search { return &Search{db: db} }

// Handle 按关键词 in.Q 在 sub 所在工作区中搜索 sub 能看到的服务型会话，按会话折叠去重并返回高亮摘要与命中计数。
func (a *Search) Handle(ctx context.Context, sub identity.Subject, in *SearchInput) (*page.Offset[SearchHitDTO], error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	qText := strings.TrimSpace(in.Q)
	if qText == "" || len(gse.Tokenizer.Query(qText)) == 0 {
		empty := page.NewOffset([]SearchHitDTO{}, in.OffsetParams, 0)
		return &empty, nil
	}

	// 同库子查询：直接复用 visible 与收件箱筛选规则，确保权限和状态过滤实时准确
	allowed := a.db.NewSelect().Model((*Conversation)(nil)).ColumnExpr("conversation.id::text").
		Where("conversation.tenant_id = ? AND conversation.kind = ?", sub.Tenant, Service)
	allowed = visible(allowed, sub)
	switch in.Inbox {
	case InboxMine:
		allowed = allowed.Where("conversation.assignee_type = ? AND conversation.assignee_id = ?", MemberActor, sub.ID)
	case InboxMentions:
		allowed = allowed.Where("EXISTS (SELECT 1 FROM conversation_mentions AS m WHERE m.tenant_id = ? AND m.member_id = ? AND m.conversation_id = conversation.id)", sub.Tenant, sub.ID)
	case InboxUnassigned:
		allowed = allowed.Where("conversation.team_id IS NULL AND conversation.assignee_type = ''")
	}
	if in.Status != "" {
		allowed = allowed.Where("conversation.status = ?", in.Status)
	}
	if in.TeamID != "" {
		allowed = allowed.Where("conversation.team_id = ?", in.TeamID)
	}
	if in.AssigneeID != "" {
		allowed = allowed.Where("conversation.assignee_type = ? AND conversation.assignee_id = ?", MemberActor, in.AssigneeID)
	}
	if in.ChannelID != "" {
		allowed = allowed.Where("conversation.channel_id = ?", in.ChannelID)
	}
	if in.Priority != "" {
		allowed = allowed.Where("conversation.priority = ?", in.Priority)
	}
	if in.TagID != "" {
		allowed = allowed.Where("EXISTS (SELECT 1 FROM conversation_tags AS ct WHERE ct.tenant_id = ? AND ct.tag_id = ? AND ct.conversation_id = conversation.id)", sub.Tenant, in.TagID)
	}

	res, err := SearchIndex.Search(ctx, a.db, search.Query{
		Text:     qText,
		Tenant:   sub.Tenant,
		Filter:   search.InQuery("conversation_id", allowed),
		Sort:     []search.Sort{search.Relevance(), search.Desc("created_at")},
		Collapse: "conversation_id",
		Page:     in.OffsetParams,
	})
	if err != nil {
		return nil, errors.Wrap(err, "search conversations")
	}
	if len(res.Items) == 0 {
		empty := page.NewOffset([]SearchHitDTO{}, in.OffsetParams, res.Total)
		return &empty, nil
	}

	convIDs := make([]string, len(res.Items))
	for i, hit := range res.Items {
		convIDs[i] = hit.Doc.ConversationID
	}
	var convs []Conversation
	q := visible(a.db.NewSelect().Model(&convs).Where("conversation.tenant_id = ? AND conversation.id IN (?)", sub.Tenant, bun.In(convIDs)), sub)
	if err := q.Scan(ctx); err != nil {
		return nil, errors.Wrap(err, "load matched conversations")
	}
	dtos, err := toDTOs(ctx, a.db, sub, convs)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]ConversationDTO, len(dtos))
	for _, d := range dtos {
		byID[d.ID] = d
	}

	hits := make([]SearchHitDTO, 0, len(res.Items))
	for _, hit := range res.Items {
		conv, ok := byID[hit.Doc.ConversationID]
		if !ok {
			continue
		}
		field, snippet := pickSearchSnippet(hit)
		count := hit.GroupCount
		if count < 1 {
			count = 1
		}
		hits = append(hits, SearchHitDTO{
			Conversation: conv,
			Match: SearchMatch{
				ItemID:    hit.Key,
				Seq:       hit.Doc.Seq,
				Kind:      hit.Doc.Kind,
				Actor:     Actor{Type: hit.Doc.ActorType, ID: hit.Doc.ActorID},
				Field:     field,
				Snippet:   snippet,
				CreatedAt: hit.Doc.CreatedAt,
			},
			MatchCount: count,
			Score:      hit.Score,
		})
	}
	out := page.NewOffset(hits, in.OffsetParams, res.Total)
	return &out, nil
}

// pickSearchSnippet 按坐席阅读习惯从命中文档的高亮中选择最合适的摘要字段：
// 内部备注取 note；客户消息优先译文（translated）再原文（original）；成员回复优先原文（original）再译文（translated）。
func pickSearchSnippet(hit search.Hit[ItemSearchDoc]) (field string, snippet search.Snippet) {
	var order []string
	switch {
	case hit.Doc.Kind == Note:
		order = []string{"note"}
	case hit.Doc.ActorType == ContactActor:
		order = []string{"translated", "original"}
	default:
		order = []string{"original", "translated"}
	}
	for _, f := range order {
		if s := hit.Highlights[f]; len(s) > 0 {
			return f, s
		}
	}
	// 兜底：若分词截断等极端情况未产出高亮片段，返回原文字段纯文本
	for _, f := range order {
		var text string
		switch f {
		case "note":
			text = hit.Doc.Note
		case "translated":
			text = hit.Doc.Translated
		case "original":
			text = hit.Doc.Original
		}
		if text != "" {
			return f, search.Snippet{{Text: text}}
		}
	}
	return "original", search.Snippet{}
}

// HTTP 处理 GET /t/{slug}/api/conversations/search。
func (a *Search) HTTP(ctx context.Context, in *SearchInput) (*SearchOutput, error) {
	sub, _ := identity.FromContext(ctx)
	res, err := a.Handle(ctx, sub, in)
	if err != nil {
		return nil, err
	}
	return &SearchOutput{Body: *res}, nil
}
