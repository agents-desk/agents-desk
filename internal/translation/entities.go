package translation

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"

	"github.com/cockroachdb/errors"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/member"
	"agents-desk/internal/tickettype"
)

// EnsureTicketMetaForAudience 为工单 ticketID 汇总全部相关受众语言（参与成员 ∪ 工作区主要语言 ∪ 客户可见工单的关联客户语言），
// 并调用 EnsureTicketMeta 批量识别源语言与翻译标题/描述。
func (a *Incoming) EnsureTicketMetaForAudience(ctx context.Context, tenant, ticketID string) error {
	primary, err := member.PrimaryLanguages(ctx, a.db, tenant)
	if err != nil {
		return err
	}
	members, err := conversationMemberLanguages(ctx, a.db, tenant, ticketID, "", "", nil)
	if err != nil {
		return err
	}
	var vis tickettype.Visibility
	err = a.db.NewSelect().Table("tickets").Column("visibility").
		Where("conversation_id = ? AND tenant_id = ?", ticketID, tenant).Scan(ctx, &vis)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load ticket visibility for meta translation")
	}
	var customerLangs []string
	if vis == tickettype.VisibilityAll {
		notifyTargets, err := conversation.ResolveTicketNotifyTargets(ctx, a.db, tenant, ticketID)
		if err != nil {
			return err
		}
		for _, tc := range notifyTargets {
			if tc.Language != "" {
				customerLangs = append(customerLangs, tc.Language)
			}
		}
	}
	targets := mergeUniqueLanguages(primary, members, customerLangs)
	return a.EnsureTicketMeta(ctx, tenant, ticketID, targets)
}

// EnsureTicketMeta 确保工单 ticketID 的标题与描述已识别源语言，并具备 targets 中所有语言的译文，存为 tickets.derived。
// 按工单加锁；未配置翻译模型时静默跳过。
func (a *Incoming) EnsureTicketMeta(ctx context.Context, tenant, ticketID string, targets []string) error {
	if a.locker != nil {
		lk, err := a.locker.Wait(ctx, "translation:ticket_meta:"+ticketID, lockTTL, lockWait)
		if err != nil {
			return errors.Wrap(err, "lock ticket meta for translation")
		}
		defer func() { _ = lk.Release(context.WithoutCancel(ctx)) }()
	}

	var tk conversation.TicketMeta
	err := a.db.NewSelect().Model(&tk).Where("conversation_id = ? AND tenant_id = ?", ticketID, tenant).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load ticket meta")
	}
	if strings.TrimSpace(tk.Title) == "" && strings.TrimSpace(tk.Description) == "" {
		return nil
	}
	source, _ := tk.Derived["language"].(string)
	missing := conversation.TicketMetaMissingLanguages(tk.Title, tk.Description, tk.Derived, targets)
	if source != "" && len(missing) == 0 {
		return nil
	}
	res, err := a.translator.TranslateTicketMeta(ctx, tenant, ticketID, tk.Title, tk.Description, source, missing)
	if errors.Is(err, llm.ErrNotConfigured) || errors.Is(err, llm.ErrBudgetExceeded) {
		slog.WarnContext(ctx, "ticket meta translation skipped", "ticket_id", ticketID, "tenant_id", tenant, "error", err)
		return nil
	}
	if err != nil {
		return err
	}
	if err := conversation.SaveTicketMetaTranslation(ctx, a.db, a.down, tenant, ticketID, res.Language, res.Titles, res.Descriptions); err != nil {
		return err
	}
	slog.InfoContext(ctx, "ticket meta translated", "ticket_id", ticketID, "source", res.Language, "titles", len(res.Titles), "descriptions", len(res.Descriptions))
	return nil
}

// EnsureContactNotes 确保联系人 contactID 的长期备注已识别源语言，并具备 targets 中所有语言的译文，存为 contacts.notes_derived。
func (a *Incoming) EnsureContactNotes(ctx context.Context, tenant, contactID string, targets []string) error {
	if a.locker != nil {
		lk, err := a.locker.Wait(ctx, "translation:contact_notes:"+contactID, lockTTL, lockWait)
		if err != nil {
			return errors.Wrap(err, "lock contact notes for translation")
		}
		defer func() { _ = lk.Release(context.WithoutCancel(ctx)) }()
	}

	var ct contact.Contact
	err := a.db.NewSelect().Model(&ct).Where("id = ? AND tenant_id = ?", contactID, tenant).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "load contact for notes translation")
	}
	if strings.TrimSpace(ct.Notes) == "" {
		return nil
	}
	source, _ := ct.NotesDerived["language"].(string)
	missing := conversation.MissingTranslations(ct.NotesDerived, targets)
	if source != "" && len(missing) == 0 {
		return nil
	}
	detected, translations, err := a.translator.TranslateNoteText(ctx, tenant, "contact", contactID, ct.Notes, source, missing)
	if errors.Is(err, llm.ErrNotConfigured) || errors.Is(err, llm.ErrBudgetExceeded) {
		slog.WarnContext(ctx, "contact notes translation skipped", "contact_id", contactID, "tenant_id", tenant, "error", err)
		return nil
	}
	if err != nil {
		return err
	}
	if err := contact.SaveNotesTranslation(ctx, a.db, tenant, contactID, detected, translations); err != nil {
		return err
	}
	slog.InfoContext(ctx, "contact notes translated", "contact_id", contactID, "source", detected, "translations", len(translations))
	return nil
}

// OnContactNotesUpdated 返回供 contact.Update 在事务内调用的回调：联系人备注更新后，
// 自动按工作区主要成员语言发布补译任务，供跨语言同事查看联系人画像。
func OnContactNotesUpdated(ctx context.Context, tx database.Tx, tenantID, contactID string) error {
	targets, err := member.PrimaryLanguages(ctx, tx, tenantID)
	if err != nil {
		return err
	}
	for _, target := range targets {
		job := FillJob{TenantID: tenantID, ContactID: contactID, Language: target}
		if err := FillTopic.Publish(ctx, tx, job, queue.DedupKey("contact:"+contactID+":"+target)); err != nil {
			return errors.Wrap(err, "publish contact notes translation job")
		}
	}
	return nil
}
