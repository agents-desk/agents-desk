package conversation

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
)

// ErrMessageNotCorrectable 是这条消息不能更正（例如对方发来的消息、内部备注、系统事件、已撤回的消息，或试图用内部备注作为更正）。
var ErrMessageNotCorrectable = errorx.Define(errorx.Conflict, "conversation.message_not_correctable")

// correctionRow 是从 message_corrections 读出的更正关系记录。
type correctionRow struct {
	OriginalItemID   string    `bun:"original_item_id"`
	CorrectionItemID string    `bun:"correction_item_id"`
	CreatedAt        time.Time `bun:"created_at"`
}

// validateCorrectionTarget 检查会话 c 中的时间线项 targetID 是否允许被调用者 sub 更正，并返回原时间线项。
// 找不到或不属于同一会话返回 ErrNotFound；内部聊天不是作者本人返回 ErrMessageNotAuthor；其余不可更正的情况返回 ErrMessageNotCorrectable。
func validateCorrectionTarget(ctx context.Context, tx bun.IDB, sub identity.Subject, c *Conversation, chatScope ChatScope, targetID string) (*Item, error) {
	var target Item
	if err := tx.NewSelect().Model(&target).
		Where("id = ? AND tenant_id = ? AND conversation_id = ?", targetID, c.TenantID, c.ID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound.New()
		}
		return nil, errors.Wrap(err, "load item for correction")
	}
	if target.Kind != Message || target.Visibility != Everyone {
		return nil, ErrMessageNotCorrectable.New()
	}
	if target.ActorType != MemberActor && target.ActorType != BotActor {
		return nil, ErrMessageNotCorrectable.New()
	}
	if c.Kind == Chat && chatScope == ChatInternal && sub.Type != identity.System {
		if target.ActorType != MemberActor || target.ActorID != sub.ID {
			return nil, ErrMessageNotAuthor.New()
		}
	}
	recalled, err := tx.NewSelect().Table("message_recalls").Where("item_id = ?", target.ID).Exists(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "check message recall before correction")
	}
	if recalled {
		return nil, ErrMessageNotCorrectable.New()
	}
	return &target, nil
}

// recordCorrection 在事务 tx 中把新发出的消息 correctionItemID 记为原消息 originalItemID 的更正。
// 同一条原消息再次更正时，覆盖为最近一次更正。
func recordCorrection(ctx context.Context, tx bun.IDB, c *Conversation, originalItemID, correctionItemID string, at time.Time) error {
	_, err := tx.NewRaw(`INSERT INTO message_corrections (original_item_id, correction_item_id, tenant_id, conversation_id, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (original_item_id) DO UPDATE
		SET correction_item_id = EXCLUDED.correction_item_id, created_at = EXCLUDED.created_at`,
		originalItemID, correctionItemID, c.TenantID, c.ID, at).Exec(ctx)
	return errors.Wrap(err, "insert message correction")
}

// loadCorrections 批量读取时间线项集合 ids 涉及的更正关系：
// correctedBy 以被更正的原消息 ID 为键，correctsItem 以更正消息 ID 为键映射到原消息 ID。
func loadCorrections(ctx context.Context, db bun.IDB, ids []string) (map[string]correctionRow, map[string]string, error) {
	correctedBy := map[string]correctionRow{}
	correctsItem := map[string]string{}
	if len(ids) == 0 {
		return correctedBy, correctsItem, nil
	}
	var rows []correctionRow
	if err := db.NewRaw(`SELECT original_item_id, correction_item_id, created_at
		FROM message_corrections
		WHERE original_item_id IN (?) OR correction_item_id IN (?)`, bun.In(ids), bun.In(ids)).
		Scan(ctx, &rows); err != nil {
		return nil, nil, errors.Wrap(err, "load message corrections")
	}
	for _, r := range rows {
		correctedBy[r.OriginalItemID] = r
		correctsItem[r.CorrectionItemID] = r.OriginalItemID
	}
	return correctedBy, correctsItem, nil
}
