package conversation

import (
	"context"
	"fmt"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/enum"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/imgproxy"

	"agents-desk/internal/channel"
)

const (
	// MaxAttachments 是一条消息或内部备注最多附带的附件数量。
	MaxAttachments = 10
	// MaxImageSize 是单张图片附件的最大体积。
	MaxImageSize = 10 * filesystem.MiB
	// MaxFileSize 是单个常用文件附件的最大体积。
	MaxFileSize = 20 * filesystem.MiB
	// MaxImageDimension 是图片附件允许的最大边长（像素），避免异常像素炸弹。
	MaxImageDimension = 10000
)

// ImageThumb 是时间线中图片缩略图的尺寸与格式：保持比例缩放到 480×480 以内，输出 WebP。
var ImageThumb = imgproxy.Options{Width: 480, Height: 480, Resize: imgproxy.Fit, Format: "webp"}

// ErrEmptyMessage 是消息或内部备注既没有文字也没有附件。
var ErrEmptyMessage = errorx.Define(errorx.InvalidArgument, "conversation.empty_message")

// AttachmentKind 是附件的展示类型：图片显示缩略图，文件显示文件卡片。
type AttachmentKind string

const (
	AttachmentImage AttachmentKind = "image" // 图片
	AttachmentFile  AttachmentKind = "file"  // 文件
)

// AttachmentKinds 是附件的全部展示类型，显示文字是翻译文件中 conversation.attachment_kind.<类型> 的消息。
var AttachmentKinds = enum.New("conversation.attachment_kind", enum.Item(AttachmentImage), enum.Item(AttachmentFile))

// Schema 让 OpenAPI 中的 AttachmentKind 只接受上面的取值，并带有显示文字。
func (AttachmentKind) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, AttachmentKinds)
}

// AttachmentDTO 是接口中一条消息或内部备注附带的图片或文件。
type AttachmentDTO struct {
	FileID      string         `json:"file_id"`
	Kind        AttachmentKind `json:"kind"`
	Filename    string         `json:"filename"`
	ContentType string         `json:"content_type"`
	Size        int64          `json:"size"`
	Width       int            `json:"width,omitempty" doc:"图片宽度（像素）；非图片为空"`
	Height      int            `json:"height,omitempty" doc:"图片高度（像素）；非图片为空"`
	URL         string         `json:"url" doc:"原文件的公开地址"`
	ThumbURL    string         `json:"thumb_url,omitempty" doc:"图片缩略图地址；未配置 imgproxy 时与 url 相同，非图片为空"`
}

// PreviewAttachment 是会话列表中最后一条消息的附件摘要：前端按界面语言渲染“[图片]”或“[文件]”。
type PreviewAttachment struct {
	Kind  AttachmentKind `json:"kind"`
	Count int            `json:"count"`
}

// Media 是会话读写附件用到的文件服务和图片缩略图服务。测试中不涉及附件时可以为零值。
type Media struct {
	Files  *filesystem.Service
	Images *imgproxy.Proxy
}

// attachmentAccept 是消息和内部备注允许确认的附件白名单：图片（排除 SVG）和常用办公/文本/压缩文件。
var attachmentAccept = filesystem.Accept{
	Types: []string{
		"image/png",
		"image/jpeg",
		"image/webp",
		"image/gif",
		"application/pdf",
		"application/zip",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"application/msword",
		"application/vnd.ms-excel",
		"application/vnd.ms-powerpoint",
		"text/plain",
		"text/csv",
		"text/markdown",
		"application/json",
	},
	MaxSize:      MaxFileSize,
	MaxDimension: MaxImageDimension,
}

// itemFile 是 timeline_item_files 表的记录：时间线项与已确认文件的有序关联。
type itemFile struct {
	bun.BaseModel `bun:"table:timeline_item_files"`

	ItemID   string    `bun:"item_id,pk"`
	FileID   uuid.UUID `bun:"file_id,notnull,type:uuid"`
	Position int       `bun:"position,pk"`
}

// requireMessageContent 检查文字和附件不能同时为空。
func requireMessageContent(text string, attachments []string) error {
	if strings.TrimSpace(text) == "" && len(attachments) == 0 {
		return ErrEmptyMessage.New()
	}
	return nil
}

// confirmAttachments 在事务 tx 中校验并确认 fileIDs：必须是 owner 自己上传、未过期且在白名单中的文件；
// 图片体积不超过 MaxImageSize 且宽高由框架自动解析并校验不超过 MaxImageDimension。任何一项不合格时返回带字段
// attachments[i] 的业务错误，由调用方的事务回滚，所有文件保持 temporary 等待清理。
func confirmAttachments(ctx context.Context, tx bun.IDB, files *filesystem.Service, owner identity.Subject, fileIDs []string) ([]filesystem.File, error) {
	if len(fileIDs) == 0 {
		return nil, nil
	}
	if len(fileIDs) > MaxAttachments {
		return nil, filesystem.ErrNotAccepted.New(errorx.Field("attachments"))
	}
	if files == nil {
		return nil, errors.New("conversation: filesystem service is required for attachments")
	}
	fs := files.WithDB(tx)
	seen := make(map[uuid.UUID]struct{}, len(fileIDs))
	out := make([]filesystem.File, 0, len(fileIDs))
	for i, raw := range fileIDs {
		field := fmt.Sprintf("attachments[%d]", i)
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, filesystem.ErrNotFound.New(errorx.Field(field))
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}

		f, err := fs.Confirm(ctx, owner, id, attachmentAccept)
		if err != nil {
			for _, known := range []*errorx.Template{filesystem.ErrNotFound, filesystem.ErrNotAccepted, filesystem.ErrNotUploaded} {
				if errors.Is(err, known) {
					return nil, known.New(errorx.Field(field))
				}
			}
			return nil, err
		}
		// 已经绑定到其他消息的附件不能再次使用（Confirm 对同主体幂等，跨消息或跨工作区需在此拦截）
		bound, err := tx.NewSelect().Model((*itemFile)(nil)).Where("file_id = ?", id).Exists(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "check attachment binding")
		}
		if bound {
			return nil, filesystem.ErrNotFound.New(errorx.Field(field))
		}
		if attachmentKind(f.ContentType) == AttachmentImage && f.Size > int64(MaxImageSize) {
			return nil, filesystem.ErrNotAccepted.New(errorx.Field(field))
		}
		out = append(out, f)
	}
	return out, nil
}

// insertItemFiles 在事务 tx 中把已确认的附件按顺序绑定到时间线项 itemID。
func insertItemFiles(ctx context.Context, tx bun.IDB, itemID string, confirmed []filesystem.File) error {
	if len(confirmed) == 0 {
		return nil
	}
	rows := make([]itemFile, len(confirmed))
	for i, f := range confirmed {
		rows[i] = itemFile{
			ItemID:   itemID,
			FileID:   f.ID,
			Position: i,
		}
	}
	_, err := tx.NewInsert().Model(&rows).Exec(ctx)
	if database.IsUniqueViolation(err, "timeline_item_files_file") {
		return filesystem.ErrNotFound.New(errorx.Field("attachments"))
	}
	return errors.Wrap(err, "insert timeline item files")
}

// loadAttachments 批量加载 itemIDs 对应的附件列表，按 position 排序，并经 files.GetMany 和 images 生成公开地址与缩略图地址。
func loadAttachments(ctx context.Context, db bun.IDB, media Media, itemIDs []string) (map[string][]AttachmentDTO, error) {
	out := map[string][]AttachmentDTO{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	var links []itemFile
	err := db.NewSelect().Model(&links).Where("item_id IN (?)", bun.In(itemIDs)).
		OrderExpr("item_id, position").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "load timeline item files")
	}
	if len(links) == 0 || media.Files == nil {
		return out, nil
	}
	fileIDs := make([]uuid.UUID, 0, len(links))
	for _, l := range links {
		fileIDs = append(fileIDs, l.FileID)
	}
	filesByID, err := media.Files.WithDB(db).GetMany(ctx, fileIDs)
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		f, ok := filesByID[l.FileID]
		if !ok {
			continue
		}
		kind := attachmentKind(f.ContentType)
		dto := AttachmentDTO{
			FileID:      f.ID.String(),
			Kind:        kind,
			Filename:    f.Filename,
			ContentType: f.ContentType,
			Size:        f.Size,
			Width:       f.Meta.Width,
			Height:      f.Meta.Height,
			URL:         f.URL,
		}
		if kind == AttachmentImage {
			dto.ThumbURL = media.Images.URL(f.URL, ImageThumb)
		}
		out[l.ItemID] = append(out[l.ItemID], dto)
	}
	return out, nil
}

// loadPreviewAttachments 批量查询每个时间线项（会话列表最后一条消息）的附件摘要：首附件类型和附件总数。
func loadPreviewAttachments(ctx context.Context, db bun.IDB, itemIDs []string) (map[string]*PreviewAttachment, error) {
	out := map[string]*PreviewAttachment{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ItemID      string `bun:"item_id"`
		ContentType string `bun:"content_type"`
		Count       int    `bun:"count"`
	}
	err := db.NewRaw(`SELECT tif.item_id,
			(SELECT f.content_type FROM timeline_item_files t2 JOIN files f ON f.id = t2.file_id
				WHERE t2.item_id = tif.item_id ORDER BY t2.position LIMIT 1) AS content_type,
			count(*) AS count
		FROM timeline_item_files tif
		WHERE tif.item_id IN (?)
		GROUP BY tif.item_id`, bun.In(itemIDs)).Scan(ctx, &rows)
	if err != nil {
		return nil, errors.Wrap(err, "load preview attachments")
	}
	for _, r := range rows {
		if r.Count > 0 {
			out[r.ItemID] = &PreviewAttachment{Kind: attachmentKind(r.ContentType), Count: r.Count}
		}
	}
	return out, nil
}

// toChannelAttachments 把会话层的附件 DTO 转为外部渠道连接器使用的结构。
func toChannelAttachments(list []AttachmentDTO) []channel.Attachment {
	if len(list) == 0 {
		return nil
	}
	out := make([]channel.Attachment, len(list))
	for i, a := range list {
		out[i] = channel.Attachment{
			FileID:      a.FileID,
			Kind:        string(a.Kind),
			Filename:    a.Filename,
			ContentType: a.ContentType,
			Size:        a.Size,
			Width:       a.Width,
			Height:      a.Height,
			URL:         a.URL,
			ThumbURL:    a.ThumbURL,
		}
	}
	return out
}

// attachmentKind 根据 MIME 类型判断附件展示为图片还是文件。
func attachmentKind(contentType string) AttachmentKind {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "image/") {
		return AttachmentImage
	}
	return AttachmentFile
}
