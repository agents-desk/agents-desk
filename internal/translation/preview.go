package translation

import (
	"context"
	"slices"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/auth"
	"agents-desk/internal/conversation"
	"agents-desk/internal/lang"
	"agents-desk/internal/llm"
	"agents-desk/internal/member"
)

// ErrForbidden 是调用者没有回复会话的权限。
var ErrForbidden = errorx.Define(errorx.PermissionDenied, "translation.forbidden")

// PreviewMode 是输入框的物理通道模式：outbound（对外投递给客户/外部对方）或 internal（内部同事协作）。
type PreviewMode string

const (
	PreviewOutbound PreviewMode = "outbound"
	PreviewInternal PreviewMode = "internal"
)

// PreviewBody 是翻译预览的请求体。
type PreviewBody struct {
	Text                string      `json:"text" minLength:"1" maxLength:"10000" doc:"成员写的回复或消息"`
	Language            string      `json:"language,omitempty" maxLength:"35" doc:"目标语言（BCP 47）；不填时按通道模式与真实接收者自动推断"`
	Mode                PreviewMode `json:"mode,omitempty" enum:"outbound,internal" doc:"通道模式：outbound（对外回复/公开进度）或 internal（内部聊天/内部备注/内部讨论）；不填按会话类型推断"`
	Mentions            []string    `json:"mentions,omitempty" maxItems:"20" uniqueItems:"true" doc:"内部协作模式下 @ 提及的成员 ID，优先作为目标语言推导来源"`
	Tone                Tone        `json:"tone,omitempty" doc:"可选：覆盖本次翻译的语气（friendly、neutral 或 formal）；不填时使用工作区默认语气"`
	SkipBackTranslation bool        `json:"skip_back_translation,omitempty" doc:"为 true 时仅返回译文、跳过盲回译"`
}

// PreviewInput 是翻译预览的 HTTP 请求。
type PreviewInput struct {
	ID   string `path:"id" format:"uuid"`
	Body PreviewBody
}

// PreviewDTO 是回复发出前的译文和回译。
type PreviewDTO struct {
	Needed          bool   `json:"needed" doc:"是否需要翻译：目标语言未知、与成员语言相同或文本本身已是目标语言时为 false"`
	From            string `json:"from" doc:"成员的沟通语言"`
	Language        string `json:"language" doc:"目标语言；不需要翻译时可能为空"`
	Translation     string `json:"translation" doc:"目标语言的译文；不需要翻译时等于原文"`
	BackTranslation string `json:"back_translation" doc:"译文译回成员语言的结果，成员据此确认；不需要翻译时为空"`
}

// PreviewOutput 是翻译预览的响应。
type PreviewOutput struct {
	Body PreviewDTO
}

// Preview 是成员回复前的翻译预览：把文字翻译成接收者语言，并按成员语言给出盲回译。
type Preview struct {
	db         *bun.DB
	translator *Translator
	incoming   *Incoming
}

// NewPreview 返回翻译预览的动作；incoming 用来在还不知道外部对方的语言时先识别对方的消息。
func NewPreview(db *bun.DB, translator *Translator, incoming *Incoming) *Preview {
	return &Preview{db: db, translator: translator, incoming: incoming}
}

// AutoTranslateFunc 返回供 conversation.Deliver 在后台异步投递前使用的自动翻译函数：
// 按成员语言与会话/客户语言判断是否需要翻译（目标未知时先识别对方最近消息），跳过盲回译直接生成发给对方的译文；
// 若文本本身已经是目标语言，返回 needed=false 按原文发出；平台未配置翻译模型时降级为按原文发出，不阻断投递。
func AutoTranslateFunc(db *bun.DB, translator *Translator, incoming *Incoming) conversation.AutoTranslator {
	preview := NewPreview(db, translator, incoming)
	return func(ctx context.Context, req conversation.AutoTranslateJob) (string, string, bool, error) {
		var from string
		if req.MemberID != "" {
			m, err := member.Find(ctx, db, req.MemberID)
			if err != nil {
				return "", "", false, err
			}
			from = string(m.Language)
		}
		to := req.Language
		if to == "" {
			var convLang string
			err := db.NewRaw(`SELECT COALESCE(NULLIF(ct.language, ''), c.language) FROM conversations c
				LEFT JOIN contacts ct ON ct.id = c.contact_id AND ct.tenant_id = c.tenant_id
				WHERE c.id = ? AND c.tenant_id = ?`, req.ConversationID, req.TenantID).Scan(ctx, &convLang)
			if err != nil {
				return "", "", false, errors.Wrap(err, "load conversation language for auto translate")
			}
			to = convLang
			if to == "" {
				sys := identity.Subject{Type: identity.System, ID: "deliver", Tenant: req.TenantID}
				detected, err := preview.detect(ctx, sys, req.ConversationID, from)
				if errors.Is(err, llm.ErrNotConfigured) {
					return req.Text, "", false, nil
				}
				if err != nil {
					return "", "", false, err
				}
				to = detected
			}
		}
		if to == "" || (from != "" && lang.Same(from, to)) {
			return req.Text, to, false, nil
		}
		translated, needed, err := translator.ReplyText(ctx, req.TenantID, req.ConversationID, req.Text, from, to, Tone(req.Tone))
		if errors.Is(err, llm.ErrNotConfigured) {
			return req.Text, "", false, nil
		}
		if err != nil {
			return "", "", false, err
		}
		if !needed {
			return req.Text, to, false, nil
		}
		return translated, to, true, nil
	}
}

// Handle 为成员 sub 在他所在工作区的会话 id（支持服务型会话、内部/商务聊天与工单）中写的文字 b.Text 给出目标语言译文和可选盲回译。
// 目标语言是 b.Language，不填时严格按通道模式与真实接收者自动推断：
//   - outbound：服务型会话与外部商务聊天取对方语言（未知时先识别最近消息），工单取关联受影响客户的主要异语；
//   - internal：优先取 @ 提及同事的主要异语，否则取当前会话/工单的直接协作方（发起人、负责人、参与者）的主要异语，不扫描无关全员。
//
// 目标语言未知、与成员语言相同或文本本身已是目标语言时不返回译文（Needed 为 false）。
func (a *Preview) Handle(ctx context.Context, sub identity.Subject, id string, b PreviewBody) (*PreviewDTO, error) {
	if sub.Type != member.Type || !sub.Can(string(auth.ConversationsRead)) {
		return nil, ErrForbidden.New()
	}
	c, err := conversation.NewGet(a.db).Handle(ctx, sub, id)
	if err != nil {
		return nil, err
	}
	if c.Kind != conversation.Chat && !sub.Can(string(auth.ConversationsReply)) {
		return nil, ErrForbidden.New()
	}
	m, err := member.Find(ctx, a.db, sub.ID)
	if err != nil {
		return nil, err
	}
	from, to := string(m.Language), strings.TrimSpace(b.Language)
	if to == "" {
		mode := b.Mode
		if mode == "" {
			if c.Kind == conversation.Chat && c.Channel == nil {
				mode = PreviewInternal
			} else {
				mode = PreviewOutbound
			}
		}
		if mode == PreviewInternal {
			if to, err = a.detectInternalLanguage(ctx, sub.Tenant, c, sub.ID, from, b.Mentions); err != nil {
				return nil, err
			}
		} else {
			switch c.Kind {
			case conversation.Ticket:
				if to, err = a.detectTicketCustomerLanguage(ctx, sub.Tenant, c.ID, from); err != nil {
					return nil, err
				}
			default:
				to = c.Language
				if to == "" {
					if to, err = a.detect(ctx, sub, c.ID, from); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	if to == "" || (from != "" && lang.Same(from, to)) {
		return &PreviewDTO{From: from, Language: to, Translation: b.Text}, nil
	}
	if b.SkipBackTranslation {
		translation, needed, err := a.translator.ReplyText(ctx, sub.Tenant, id, b.Text, from, to, b.Tone)
		if err != nil {
			return nil, err
		}
		if !needed {
			return &PreviewDTO{Needed: false, From: from, Language: to, Translation: b.Text}, nil
		}
		return &PreviewDTO{Needed: true, From: from, Language: to, Translation: translation}, nil
	}
	translation, back, needed, err := a.translator.Reply(ctx, sub.Tenant, id, b.Text, from, to, b.Tone)
	if err != nil {
		return nil, err
	}
	if !needed {
		return &PreviewDTO{Needed: false, From: from, Language: to, Translation: b.Text}, nil
	}
	return &PreviewDTO{Needed: true, From: from, Language: to, Translation: translation, BackTranslation: back}, nil
}

// detectInternalLanguage 为内部协作通道推导默认目标语言：
//  1. 优先取 mentions 中 @ 提及同事（排除自己）最常见的异语；
//  2. 否则按会话类型取当前会话直接协作方（发起人、负责人、参与者；服务型会话无参与者时回退所属团队成员）最常见的异语；
//  3. 没有异语协作方时返回空字符串，不扫描全工作区无关成员。
func (a *Preview) detectInternalLanguage(ctx context.Context, tenant string, c *conversation.ConversationDTO, viewerID, from string, mentions []string) (string, error) {
	if len(mentions) > 0 {
		ids := make([]string, 0, len(mentions))
		for _, id := range mentions {
			if id != "" && id != viewerID {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			return a.mostCommonMemberLanguage(ctx, ids, from)
		}
	}

	var peerIDs []string
	seen := map[string]bool{viewerID: true}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			peerIDs = append(peerIDs, id)
		}
	}

	if c.Assignee != nil && c.Assignee.Type == conversation.MemberActor {
		add(c.Assignee.ID)
	}
	if c.Kind == conversation.Ticket {
		var createdBy *string
		if err := a.db.NewSelect().Table("tickets").Column("created_by").
			Where("conversation_id = ? AND tenant_id = ?", c.ID, tenant).Scan(ctx, &createdBy); err != nil {
			return "", errors.Wrap(err, "load ticket creator for language")
		}
		if createdBy != nil {
			add(*createdBy)
		}
	}

	var partIDs []string
	if err := a.db.NewSelect().Table("conversation_participants").Column("participant_id").
		Where("conversation_id = ? AND participant_type = ?", c.ID, conversation.MemberActor).
		OrderExpr("created_at ASC, participant_id ASC").Scan(ctx, &partIDs); err != nil {
		return "", errors.Wrap(err, "load participant ids")
	}
	for _, id := range partIDs {
		add(id)
	}
	if l, err := a.mostCommonMemberLanguage(ctx, peerIDs, from); err != nil || l != "" {
		return l, err
	}

	// 服务型会话或工单尚未有异语参与者但归属团队收件箱时，检查该团队的直接成员
	if c.TeamID != nil && *c.TeamID != "" {
		var teamMemberIDs []string
		if err := a.db.NewSelect().Table("team_members").Column("member_id").
			Where("team_id = ?", *c.TeamID).Scan(ctx, &teamMemberIDs); err != nil {
			return "", errors.Wrap(err, "load team member ids")
		}
		filtered := make([]string, 0, len(teamMemberIDs))
		for _, id := range teamMemberIDs {
			if id != viewerID {
				filtered = append(filtered, id)
			}
		}
		return a.mostCommonMemberLanguage(ctx, filtered, from)
	}
	return "", nil
}

// mostCommonMemberLanguage 从一组未停用成员 ID 中选出最常见且与 from 不同的沟通语言；没有异语成员时返回空字符串。
func (a *Preview) mostCommonMemberLanguage(ctx context.Context, memberIDs []string, from string) (string, error) {
	if len(memberIDs) == 0 {
		return "", nil
	}
	var rows []struct {
		ID       string `bun:"id"`
		Language string `bun:"language"`
	}
	if err := a.db.NewSelect().Table("members").Column("id", "language").
		Where("id IN (?) AND disabled_at IS NULL", bun.In(memberIDs)).Scan(ctx, &rows); err != nil {
		return "", errors.Wrap(err, "load member languages")
	}
	byID := make(map[string]string, len(rows))
	for _, r := range rows {
		byID[r.ID] = r.Language
	}
	best, counts := "", map[string]int{}
	for _, id := range memberIDs {
		l := strings.TrimSpace(byID[id])
		if l == "" || (from != "" && lang.Same(from, l)) {
			continue
		}
		counts[l]++
		if best == "" || counts[l] > counts[best] {
			best = l
		}
	}
	return best, nil
}

// detectTicketCustomerLanguage 为工单公开进度推断默认目标语言：仅从关联受影响客户中选出最常见且与 from 不同的语言；
// 没有异语客户时返回空字符串，不回退到内部成员语言。
func (a *Preview) detectTicketCustomerLanguage(ctx context.Context, tenant, ticketID, from string) (string, error) {
	targets, err := conversation.ResolveTicketNotifyTargets(ctx, a.db, tenant, ticketID)
	if err != nil {
		return "", err
	}
	best, counts := "", map[string]int{}
	for _, tc := range targets {
		if tc.Language == "" || (from != "" && lang.Same(from, tc.Language)) {
			continue
		}
		counts[tc.Language]++
		if best == "" || counts[tc.Language] > counts[best] {
			best = tc.Language
		}
	}
	return best, nil
}

// detect 识别 sub 能看到的会话 id 中对方最近还没识别过语言的消息，顺带翻译成工作区主要语言和当前成员的语言
// （避免先只识别、后台任务再调一次模型补译），一旦确定出客户的语言就立刻返回，其余消息交由后台任务处理。
func (a *Preview) detect(ctx context.Context, sub identity.Subject, id, memberLang string) (string, error) {
	var ids []string
	err := a.db.NewSelect().Table("timeline_items").Column("id").
		Where("tenant_id = ? AND conversation_id = ? AND kind = ? AND actor_type = ? AND visibility = ?", sub.Tenant, id, conversation.Message, conversation.ContactActor, conversation.Everyone).
		Where("content->>'text' <> '' AND COALESCE(derived->>'language', '') = ''").
		OrderExpr("seq DESC").Limit(3).Scan(ctx, &ids)
	if err != nil {
		return "", errors.Wrap(err, "load undetected messages")
	}
	if len(ids) == 0 {
		return "", nil
	}
	targets, err := member.PrimaryLanguages(ctx, a.db, sub.Tenant)
	if err != nil {
		return "", err
	}
	if memberLang != "" && !slices.Contains(targets, memberLang) {
		targets = append(targets, memberLang)
		slices.Sort(targets)
	}
	for _, itemID := range ids {
		if err := a.incoming.Ensure(ctx, sub.Tenant, itemID, targets); err != nil {
			return "", err
		}
		c, err := conversation.NewGet(a.db).Handle(ctx, sub, id)
		if err != nil {
			return "", err
		}
		if c.Language != "" {
			return c.Language, nil
		}
	}
	return "", nil
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/translations。
func (a *Preview) HTTP(ctx context.Context, in *PreviewInput) (*PreviewOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	return &PreviewOutput{Body: *dto}, nil
}
