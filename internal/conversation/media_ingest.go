package conversation

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue"

	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
)

// MediaIngestJob 是异步拉取外部渠道附件（封面缩略图与自动物化主文件）的队列消息。
type MediaIngestJob struct {
	TenantID       string `json:"tenant_id"`
	ConversationID string `json:"conversation_id"`
	ItemID         string `json:"item_id"`
}

// MediaIngestTopic 是异步拉取外部渠道媒体的队列主题，在 Receive 写入远端附件的同一事务中发布。
var MediaIngestTopic = queue.Topic[MediaIngestJob]("conversation.media_ingest")

// MediaIngestor 处理 MediaIngestTopic 任务：异步拉取外部渠道的封面图以及标记为 pending 的主文件（如图片、语音、小文件），
// 不下载标记为 remote 的大视频/大文件，完成后推送会话更新通知前端。
type MediaIngestor struct {
	db       *bun.DB
	registry channel.Registry
	down     Downstream
}

// NewMediaIngestor 返回异步拉取外部渠道媒体的任务处理器。
func NewMediaIngestor(db *bun.DB, registry channel.Registry, down Downstream) *MediaIngestor {
	return &MediaIngestor{db: db, registry: registry, down: down}
}

// Job 拉取 job.ItemID 下所有需要自动物化的封面缩略图和主文件：
//   - 只要 thumb_source_ref 非空且 thumb_file_id 为空，总是先拉取几 KB 封面图落盘；
//   - 只有 fetch_status == pending 的主文件才自动下载；remote 保持原样等待坐席点击按需拉取；
//   - 永久错误（文件超限、不在白名单、源文件失效）记为 unavailable，不触发队列死循环重试；
//   - 临时网络错误返回错误交给队列按退避重试。
func (m *MediaIngestor) Job(ctx context.Context, job MediaIngestJob) error {
	if m.down.Media.Files == nil {
		return nil
	}
	var rows []Attachment
	if err := m.db.NewSelect().Model(&rows).
		Where("tenant_id = ? AND item_id = ?", job.TenantID, job.ItemID).
		OrderExpr("position").Scan(ctx); err != nil {
		return errors.Wrap(err, "load attachments for ingest")
	}
	if len(rows) == 0 {
		return nil
	}

	changed := false
	var retryErr error
	for i := range rows {
		att := &rows[i]
		if att.SourceChannelID == nil || *att.SourceChannelID == "" {
			continue
		}
		ch, fetcher, err := resolveMediaFetcher(ctx, m.db, m.registry, att.TenantID, *att.SourceChannelID)
		if err != nil {
			if isPermanentMediaError(err) {
				if att.FetchStatus == FetchPending {
					if markErr := markAttachmentUnavailable(ctx, m.db, att.ID, mediaErrorCode(err)); markErr != nil {
						return markErr
					}
					changed = true
				}
				continue
			}
			retryErr = errors.Join(retryErr, err)
			continue
		}

		// 1. 拉取缺失的封面缩略图（视频、动图、大文件都先下几 KB 封面供前端展示；MIME 与文件名交由上游与 Confirm 识别）
		if att.ThumbSourceRef != "" && att.ThumbFileID == nil {
			tf, err := materializeStream(ctx, m.db, m.down.Media.Files, fetcher, ch, att.TenantID, att.ThumbSourceRef, "", "", thumbAccept, int64(MaxThumbSize))
			if err != nil {
				if isPermanentMediaError(err) {
					slog.WarnContext(ctx, "ingest attachment thumbnail permanently failed", "attachment_id", att.ID, "error", err)
					if _, uerr := m.db.NewUpdate().Model((*Attachment)(nil)).
						Set("thumb_source_ref = ''").
						Where("id = ?", att.ID).Exec(ctx); uerr != nil {
						return errors.Wrap(uerr, "clear failed thumb source")
					}
				} else {
					retryErr = errors.Join(retryErr, err)
				}
			} else {
				tid := tf.ID
				if _, uerr := m.db.NewUpdate().Model((*Attachment)(nil)).
					Set("thumb_file_id = ?", tid).
					Where("id = ? AND thumb_file_id IS NULL", att.ID).Exec(ctx); uerr != nil {
					return errors.Wrap(uerr, "save attachment thumb")
				}
				changed = true
			}
		}

		// 2. 自动物化标记为 pending 的主文件（图片、语音、小文件）；remote（视频/大文件）保持未下载
		if att.FetchStatus != FetchPending || att.SourceRef == "" || att.FileID != nil {
			continue
		}
		f, err := materializeStream(ctx, m.db, m.down.Media.Files, fetcher, ch, att.TenantID, att.SourceRef, att.Filename, att.ContentType, attachmentAccept, int64(maxSizeForKind(att.Kind)))
		if err != nil {
			if isPermanentMediaError(err) {
				slog.WarnContext(ctx, "ingest attachment permanently failed", "attachment_id", att.ID, "kind", att.Kind, "error", err)
				if markErr := markAttachmentUnavailable(ctx, m.db, att.ID, mediaErrorCode(err)); markErr != nil {
					return markErr
				}
				changed = true
			} else {
				retryErr = errors.Join(retryErr, err)
			}
			continue
		}
		if err := completeAttachmentFile(ctx, m.db, att, f); err != nil {
			return err
		}
		changed = true
	}

	if changed {
		if err := database.RunInTx(ctx, m.db, nil, func(ctx context.Context, tx database.Tx) error {
			return NotifyUpdated(ctx, tx, m.down, job.TenantID, []string{job.ConversationID})
		}); err != nil {
			return err
		}
	}
	return retryErr
}

// FetchAttachmentInput 是按需拉取远端附件的 HTTP 请求。
type FetchAttachmentInput struct {
	ID           string `path:"id" format:"uuid"`
	AttachmentID string `path:"attachment_id" format:"uuid"`
}

// FetchAttachment 是成员点击远端未下载附件（如 Telegram 视频或大文件）时按需从外部渠道拉取并落盘的业务动作。
type FetchAttachment struct {
	db       *bun.DB
	registry channel.Registry
	down     Downstream
}

// NewFetchAttachment 返回按需拉取远端附件的动作。
func NewFetchAttachment(db *bun.DB, registry channel.Registry, down Downstream) *FetchAttachment {
	return &FetchAttachment{db: db, registry: registry, down: down}
}

// Handle 校验成员 sub 有权查看会话 convID，然后按需从外部渠道流式拉取附件 attachmentID 并落盘为正式文件。
// 附件已经落盘就绪时幂等返回；其他请求正在拉取时直接返回当前 fetching 状态；拉取完成后实时推送给同一会话的其他成员。
func (a *FetchAttachment) Handle(ctx context.Context, sub identity.Subject, convID, attachmentID string) (*AttachmentDTO, error) {
	if err := authorize(sub, auth.ConversationsRead); err != nil {
		return nil, err
	}
	if a.down.Media.Files == nil {
		return nil, errors.New("conversation: filesystem service is required to fetch attachment")
	}

	var att Attachment
	claimed := false
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := findVisible(ctx, tx, sub, convID, false); err != nil {
			return err
		}
		err := tx.NewSelect().Model(&att).
			Join("JOIN timeline_items AS ti ON ti.id = ta.item_id").
			Where("ta.id = ? AND ta.tenant_id = ? AND ti.conversation_id = ?", attachmentID, sub.Tenant, convID).
			For("UPDATE OF ta").Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAttachmentNotFound.New()
		}
		if err != nil {
			return errors.Wrap(err, "load attachment for fetch")
		}
		if att.FetchStatus == FetchReady && att.FileID != nil {
			return nil
		}
		if att.FetchStatus == FetchFetching {
			return nil
		}
		if att.SourceChannelID == nil || *att.SourceChannelID == "" || att.SourceRef == "" {
			return channel.ErrMediaUnavailable.New()
		}
		// 仅允许 remote、pending、或曾因临时错误标记的记录重新发起拉取；已知超出渠道硬上限的直接返回错误
		if att.FetchStatus == FetchUnavailable && att.FetchError == channel.ErrMediaTooLarge.Code() {
			return channel.ErrMediaTooLarge.New()
		}
		if _, err := tx.NewUpdate().Model((*Attachment)(nil)).
			Set("fetch_status = ?", FetchFetching).Set("fetch_error = ''").
			Where("id = ?", att.ID).Exec(ctx); err != nil {
			return errors.Wrap(err, "claim attachment fetch")
		}
		att.FetchStatus = FetchFetching
		att.FetchError = ""
		claimed = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !claimed {
		dtos, err := buildAttachmentDTOs(ctx, a.db, a.down.Media, []Attachment{att})
		if err != nil {
			return nil, err
		}
		return &dtos[0], nil
	}

	ch, fetcher, err := resolveMediaFetcher(ctx, a.db, a.registry, att.TenantID, *att.SourceChannelID)
	if err != nil {
		revertFetchStatus(ctx, a.db, att.ID, err)
		return nil, err
	}

	f, err := materializeStream(ctx, a.db, a.down.Media.Files, fetcher, ch, att.TenantID, att.SourceRef, att.Filename, att.ContentType, attachmentAccept, int64(maxSizeForKind(att.Kind)))
	if err != nil {
		revertFetchStatus(ctx, a.db, att.ID, err)
		return nil, err
	}
	if err := completeAttachmentFile(ctx, a.db, &att, f); err != nil {
		revertFetchStatus(ctx, a.db, att.ID, err)
		return nil, err
	}

	if err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		return NotifyUpdated(ctx, tx, a.down, sub.Tenant, []string{convID})
	}); err != nil {
		slog.WarnContext(ctx, "notify after attachment fetch failed", "conversation_id", convID, "attachment_id", att.ID, "error", err)
	}

	dtos, err := buildAttachmentDTOs(ctx, a.db, a.down.Media, []Attachment{att})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// HTTP 处理 POST /t/{slug}/api/conversations/{id}/attachments/{attachment_id}/fetch。
func (a *FetchAttachment) HTTP(ctx context.Context, in *FetchAttachmentInput) (*AttachmentOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID, in.AttachmentID)
	if err != nil {
		return nil, err
	}
	return &AttachmentOutput{Body: *dto}, nil
}

// EvictCachedMedia 回收早于 cutoff 且带有外部来源引用的视频缓存文件：删除本地/S3 存储对象，
// 保留封面缩略图与 source_ref 并将状态回退为 remote，后续需要查看时仍可按需重新拉取。
func EvictCachedMedia(ctx context.Context, db *bun.DB, files *filesystem.Service, cutoff time.Time, limit int) (int, error) {
	if files == nil || limit <= 0 {
		return 0, nil
	}
	var rows []Attachment
	err := db.NewSelect().Model(&rows).
		Where("source_ref <> '' AND kind = ? AND file_id IS NOT NULL AND updated_at < ?", AttachmentVideo, cutoff).
		OrderExpr("updated_at ASC").Limit(int64(limit)).Scan(ctx)
	if err != nil {
		return 0, errors.Wrap(err, "select cached video attachments for eviction")
	}
	evicted := 0
	for _, r := range rows {
		if r.FileID == nil {
			continue
		}
		fid := *r.FileID
		if err := database.RunInTx(ctx, db, nil, func(ctx context.Context, tx database.Tx) error {
			res, err := tx.NewUpdate().Model((*Attachment)(nil)).
				Set("file_id = NULL").Set("fetch_status = ?", FetchRemote).
				Where("id = ? AND file_id = ?", r.ID, fid).Exec(ctx)
			if err != nil {
				return errors.Wrap(err, "evict attachment file")
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil
			}
			stillReferenced, err := tx.NewSelect().Model((*Attachment)(nil)).
				Where("file_id = ? OR thumb_file_id = ?", fid, fid).Exists(ctx)
			if err != nil {
				return errors.Wrap(err, "check shared attachment file reference")
			}
			if stillReferenced {
				return nil
			}
			return files.WithDB(tx).Delete(ctx, fid)
		}); err != nil {
			return evicted, err
		}
		evicted++
	}
	return evicted, nil
}

// resolveMediaFetcher 加载渠道并确认其连接器实现了 channel.MediaFetcher。
func resolveMediaFetcher(ctx context.Context, db bun.IDB, reg channel.Registry, tenantID, channelID string) (*channel.Channel, channel.MediaFetcher, error) {
	ch, err := channel.Find(ctx, db, tenantID, channelID)
	if err != nil {
		return nil, nil, err
	}
	conn, err := reg.Connector(ch.Kind)
	if err != nil {
		return nil, nil, channel.ErrMediaUnavailable.New()
	}
	fetcher, ok := conn.(channel.MediaFetcher)
	if !ok {
		return nil, nil, channel.ErrMediaUnavailable.New()
	}
	return ch, fetcher, nil
}

// materializeStream 从渠道连接器流式拉取媒体、流式上传到 filesystem 并在事务外完成 Confirm，返回已确认的正式文件。
func materializeStream(
	ctx context.Context,
	db *bun.DB,
	files *filesystem.Service,
	fetcher channel.MediaFetcher,
	ch *channel.Channel,
	tenantID, ref, nameHint, mimeHint string,
	accept filesystem.Accept,
	maxBytes int64,
) (filesystem.File, error) {
	fetched, err := fetcher.FetchMedia(ctx, ch, ref, maxBytes)
	if err != nil {
		return filesystem.File{}, err
	}
	defer func() { _ = fetched.Body.Close() }()

	contentType := firstNonEmptyStr(mimeHint, fetched.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	filename := ensureFilenameExt(firstNonEmptyStr(nameHint, fetched.Filename), contentType)
	owner := identity.Subject{Type: identity.System, ID: string(ch.Kind) + ":" + ch.ID, Tenant: tenantID}

	size := fetched.Size
	if size <= 0 {
		size = filesystem.UnknownSize
	}
	uploaded, err := files.Upload(ctx, owner, filesystem.FileInput{
		Disk:        files.PrivateDisk(),
		Filename:    filename,
		ContentType: contentType,
		Size:        size,
	}, fetched.Body)
	if err != nil {
		return filesystem.File{}, err
	}
	if maxBytes > 0 && uploaded.Size > maxBytes {
		_ = files.Delete(context.WithoutCancel(ctx), uploaded.ID)
		return filesystem.File{}, filesystem.ErrNotAccepted.New()
	}
	confirmed, err := files.WithDB(db).Confirm(ctx, owner, uploaded.ID, accept)
	if err != nil {
		_ = files.Delete(context.WithoutCancel(ctx), uploaded.ID)
		return filesystem.File{}, err
	}
	return confirmed, nil
}

// completeAttachmentFile 将已确认的主文件回写到附件记录并同步更新规范 MIME、体积、宽高与状态。
func completeAttachmentFile(ctx context.Context, db *bun.DB, att *Attachment, f filesystem.File) error {
	fid := f.ID
	kind := attachmentKind(f.ContentType)
	filename := firstNonEmptyStr(att.Filename, f.Filename)
	width, height := att.Width, att.Height
	if f.Meta.Width > 0 && f.Meta.Height > 0 {
		width, height = f.Meta.Width, f.Meta.Height
	}
	at := now()
	_, err := db.NewUpdate().Model((*Attachment)(nil)).
		Set("file_id = ?", fid).
		Set("kind = ?", kind).
		Set("filename = ?", filename).
		Set("content_type = ?", f.ContentType).
		Set("size = ?", f.Size).
		Set("width = ?", width).
		Set("height = ?", height).
		Set("fetch_status = ?", FetchReady).
		Set("fetch_error = ''").
		Where("id = ?", att.ID).Exec(ctx)
	if err != nil {
		return errors.Wrap(err, "complete attachment file")
	}
	att.FileID = &fid
	att.Kind = kind
	att.Filename = filename
	att.ContentType = f.ContentType
	att.Size = f.Size
	att.Width = width
	att.Height = height
	att.FetchStatus = FetchReady
	att.FetchError = ""
	att.UpdatedAt = at
	return nil
}

// revertFetchStatus 在按需拉取失败后恢复附件状态：永久错误记为 unavailable，临时错误回退为 remote 以便重试。
func revertFetchStatus(ctx context.Context, db *bun.DB, id string, cause error) {
	status := FetchRemote
	if isPermanentMediaError(cause) {
		status = FetchUnavailable
	}
	code := mediaErrorCode(cause)
	if _, err := db.NewUpdate().Model((*Attachment)(nil)).
		Set("fetch_status = ?", status).Set("fetch_error = ?", code).
		Where("id = ? AND fetch_status = ?", id, FetchFetching).Exec(context.WithoutCancel(ctx)); err != nil {
		slog.WarnContext(ctx, "revert attachment fetch status failed", "attachment_id", id, "error", err)
	}
}

// markAttachmentUnavailable 将附件标记为不可拉取并记录原因码。
func markAttachmentUnavailable(ctx context.Context, db *bun.DB, id, code string) error {
	_, err := db.NewUpdate().Model((*Attachment)(nil)).
		Set("fetch_status = ?", FetchUnavailable).Set("fetch_error = ?", code).
		Where("id = ?", id).Exec(ctx)
	return errors.Wrap(err, "mark attachment unavailable")
}

// isPermanentMediaError 判断拉取媒体时的错误是否属于无需重试的永久性错误。
func isPermanentMediaError(err error) bool {
	return errors.Is(err, channel.ErrMediaTooLarge) ||
		errors.Is(err, channel.ErrMediaUnavailable) ||
		errors.Is(err, channel.ErrNotFound) ||
		errors.Is(err, filesystem.ErrNotAccepted) ||
		errors.Is(err, filesystem.ErrTooLarge)
}

// mediaErrorCode 提取写入 fetch_error 字段的稳定错误码。
func mediaErrorCode(err error) string {
	switch {
	case errors.Is(err, channel.ErrMediaTooLarge) || errors.Is(err, filesystem.ErrTooLarge):
		return channel.ErrMediaTooLarge.Code()
	case errors.Is(err, filesystem.ErrNotAccepted):
		return filesystem.ErrNotAccepted.Code()
	case errors.Is(err, channel.ErrMediaUnavailable) || errors.Is(err, channel.ErrNotFound):
		return channel.ErrMediaUnavailable.Code()
	default:
		return "fetch_failed"
	}
}

// ensureFilenameExt 确保文件名带有与 contentType 匹配的扩展名，方便 filesystem 按扩展名和内容双重核验。
func ensureFilenameExt(name, contentType string) string {
	name = strings.TrimSpace(name)
	if name != "" && strings.Contains(name, ".") {
		return name
	}
	ext := extForMIME(contentType)
	if name == "" {
		return "media" + ext
	}
	return name + ext
}

// extForMIME 返回常见媒体 MIME 类型对应的规范扩展名。
func extForMIME(ct string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0])) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	case "audio/ogg", "application/ogg", "audio/opus":
		return ".ogg"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/mp4", "audio/x-m4a", "audio/m4a":
		return ".m4a"
	case "audio/webm":
		return ".webm"
	case "audio/wav", "audio/wave", "audio/x-wav":
		return ".wav"
	case "audio/aac", "audio/x-aac":
		return ".aac"
	case "audio/flac", "audio/x-flac":
		return ".flac"
	case "application/pdf":
		return ".pdf"
	case "application/zip":
		return ".zip"
	default:
		return ""
	}
}

func firstNonEmptyStr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}
