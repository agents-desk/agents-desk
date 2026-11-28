package conversation

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/contract"
	"github.com/go-tenon/tenon/crypt"
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
	// MaxThumbSize 是单张视频或文档封面缩略图的最大体积。
	MaxThumbSize = 2 * filesystem.MiB
	// MaxAudioSize 是单个音频或语音附件的最大体积。
	MaxAudioSize = 15 * filesystem.MiB
	// MaxFileSize 是单个常用文件附件的最大体积。
	MaxFileSize = 20 * filesystem.MiB
	// MaxVideoSize 是单个视频附件的最大体积。
	MaxVideoSize = 50 * filesystem.MiB
	// MaxImageDimension 是图片附件允许的最大边长（像素），避免异常像素炸弹。
	MaxImageDimension = 10000
	// MaxThumbDimension 是封面缩略图允许的最大边长（像素）。
	MaxThumbDimension = 2048
)

// ImageThumb 是时间线中图片缩略图的尺寸与格式：保持比例缩放到 480×480 以内，输出 WebP。
var ImageThumb = imgproxy.Options{Width: 480, Height: 480, Resize: imgproxy.Fit, Format: "webp"}

var (
	// ErrEmptyMessage 是消息或内部备注既没有文字也没有附件。
	ErrEmptyMessage = errorx.Define(errorx.InvalidArgument, "conversation.empty_message")
	// ErrAttachmentNotFound 是附件不存在，或者不属于这个工作区和会话。
	ErrAttachmentNotFound = errorx.Define(errorx.NotFound, "conversation.attachment_not_found")
)

// AttachmentKind 是附件的展示类型：图片显示缩略图网格，视频显示封面与播放器，音频显示语音/音频条，文件显示下载卡片。
type AttachmentKind string

const (
	AttachmentImage AttachmentKind = "image" // 图片
	AttachmentVideo AttachmentKind = "video" // 视频
	AttachmentAudio AttachmentKind = "audio" // 音频或语音
	AttachmentFile  AttachmentKind = "file"  // 文件
)

// AttachmentKinds 是附件的全部展示类型，显示文字是翻译文件中 conversation.attachment_kind.<类型> 的消息。
var AttachmentKinds = enum.New("conversation.attachment_kind",
	enum.Item(AttachmentImage),
	enum.Item(AttachmentVideo),
	enum.Item(AttachmentAudio),
	enum.Item(AttachmentFile),
)

// Schema 让 OpenAPI 中的 AttachmentKind 只接受上面的取值，并带有显示文字。
func (AttachmentKind) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, AttachmentKinds)
}

// FetchStatus 是附件主文件的物化状态。
type FetchStatus string

const (
	FetchReady       FetchStatus = "ready"       // 主文件已落盘就绪，可直接预览、播放或下载
	FetchRemote      FetchStatus = "remote"      // 位于外部渠道尚未拉取（如视频或大文件），点击后按需下载
	FetchPending     FetchStatus = "pending"     // 外部媒体已入队，等待后台自动拉取
	FetchFetching    FetchStatus = "fetching"    // 正在从外部渠道拉取
	FetchUnavailable FetchStatus = "unavailable" // 无法从外部渠道拉取（如超出渠道下载限制或源文件失效）
)

// FetchStatuses 是全部附件物化状态，显示文字是翻译文件中 conversation.fetch_status.<状态> 的消息。
var FetchStatuses = enum.New("conversation.fetch_status",
	enum.Item(FetchReady),
	enum.Item(FetchRemote),
	enum.Item(FetchPending),
	enum.Item(FetchFetching),
	enum.Item(FetchUnavailable),
)

// Schema 让 OpenAPI 中的 FetchStatus 只接受上面的取值，并带有显示文字。
func (FetchStatus) Schema(r huma.Registry) *huma.Schema {
	return contract.EnumSchema(r, FetchStatuses)
}

// AttachmentInput 是客户端发消息或内部备注时提交的一个已上传媒体附件。
type AttachmentInput struct {
	FileID      string `json:"file_id" format:"uuid" doc:"通过 /files 上传的主文件 ID"`
	ThumbFileID string `json:"thumb_file_id,omitempty" format:"uuid" doc:"可选：通过 /files 上传的封面缩略图 ID（例如视频首帧）"`
	DurationMs  int    `json:"duration_ms,omitempty" minimum:"0" maximum:"86400000" doc:"可选：音频或视频时长（毫秒）"`
	Width       int    `json:"width,omitempty" minimum:"0" maximum:"10000" doc:"可选：视频宽度（像素）"`
	Height      int    `json:"height,omitempty" minimum:"0" maximum:"10000" doc:"可选：视频高度（像素）"`
	IsVoice     bool   `json:"is_voice,omitempty" doc:"是否为语音消息（按语音条样式展示）"`
}

// IncomingAttachment 是外部渠道连接器收到的媒体元数据，入库时不阻塞 Webhook，由队列自动拉取或点击按需拉取。
type IncomingAttachment struct {
	Kind           AttachmentKind // 媒体展示类型：image、video、audio、file
	Filename       string         // 原始或推断的文件名
	ContentType    string         // 渠道声明的 MIME 类型
	Size           int64          // 渠道报告的字节数（未知时为 0）
	Width          int            // 图片或视频宽度（像素）
	Height         int            // 图片或视频高度（像素）
	DurationMs     int            // 音频或视频时长（毫秒）
	IsVoice        bool           // 是否为语音条消息
	SourceRef      string         // 外部渠道主文件引用（例如 Telegram file_id）
	ThumbSourceRef string         // 外部渠道封面缩略图引用（例如 Telegram thumbnail.file_id）
	AutoFetch      bool           // true 表示后台队列自动拉取主文件；false 表示保持 remote 等待点击按需拉取
	Unavailable    string         // 非空表示已知无法拉取的原因码（如 channel.media_too_large）
}

// AttachmentDTO 是接口中一条消息或内部备注附带的媒体附件。
type AttachmentDTO struct {
	ID          string         `json:"id" doc:"附件唯一标识"`
	FileID      string         `json:"file_id,omitempty" doc:"主文件 ID；远端尚未拉取时为空"`
	Kind        AttachmentKind `json:"kind"`
	Status      FetchStatus    `json:"status" doc:"物化状态：ready 可直接播放/下载，remote 点击按需拉取，pending/fetching 拉取中，unavailable 不可用"`
	Error       string         `json:"error,omitempty" doc:"不可用或拉取失败的原因码"`
	Filename    string         `json:"filename"`
	ContentType string         `json:"content_type"`
	Size        int64          `json:"size"`
	Width       int            `json:"width,omitempty" doc:"图片或视频宽度（像素）"`
	Height      int            `json:"height,omitempty" doc:"图片或视频高度（像素）"`
	DurationMs  int            `json:"duration_ms,omitempty" doc:"音频或视频时长（毫秒）"`
	IsVoice     bool           `json:"is_voice,omitempty" doc:"是否为语音条消息"`
	URL         string         `json:"url,omitempty" doc:"原文件的公开地址；尚未拉取时为空"`
	ThumbURL    string         `json:"thumb_url,omitempty" doc:"图片缩略图或视频封面地址"`
}

// AttachmentOutput 是返回单个附件的响应（用于按需拉取完成后返回最新状态与公开地址）。
type AttachmentOutput struct {
	Body AttachmentDTO
}

// PreviewAttachment 是会话列表中最后一条消息的附件摘要：前端按界面语言渲染“[图片]”、“[视频]”、“[语音]”或“[文件]”。
type PreviewAttachment struct {
	Kind  AttachmentKind `json:"kind"`
	Count int            `json:"count"`
}

// Media 是会话读写附件用到的文件服务、图片缩略图服务与签名器。测试中不涉及附件时可以为零值。
type Media struct {
	Files  *filesystem.Service
	Images *imgproxy.Proxy
	Crypt  *crypt.Crypt
}

// AttachmentAccept 返回消息与内部备注允许上传及确认的媒体白名单副本。
func AttachmentAccept() *filesystem.Accept {
	cp := attachmentAccept
	cp.Types = slices.Clone(attachmentAccept.Types)
	return &cp
}

// attachmentAccept 是消息和内部备注允许确认的附件白名单：图片（排除 SVG）、主流视频与音频/语音以及常用办公/文本/压缩文件。
var attachmentAccept = filesystem.Accept{
	Types: []string{
		"image/png",
		"image/jpeg",
		"image/webp",
		"image/gif",
		"video/mp4",
		"video/webm",
		"video/quicktime",
		"audio/ogg",
		"audio/mpeg",
		"audio/mp4",
		"audio/webm",
		"audio/wav",
		"audio/aac",
		"audio/flac",
		"audio/amr",
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
	MaxSize:      MaxVideoSize,
	MaxDimension: MaxImageDimension,
}

// thumbAccept 是视频或文档封面缩略图允许确认的白名单：仅常见静态/动图图片，限制体积和像素尺寸。
var thumbAccept = filesystem.Accept{
	Types: []string{
		"image/png",
		"image/jpeg",
		"image/webp",
		"image/gif",
	},
	MaxSize:      MaxThumbSize,
	MaxDimension: MaxThumbDimension,
}

// Attachment 是 timeline_attachments 表的领域记录：独立保存媒体元数据、主/封面存储指针与外部渠道懒拉取引用。
type Attachment struct {
	bun.BaseModel `bun:"table:timeline_attachments,alias:ta"`

	ID string `bun:"id,pk"`
	database.Timestamps
	TenantID        string         `bun:"tenant_id,notnull"`
	ItemID          string         `bun:"item_id,notnull"`
	Position        int            `bun:"position,notnull"`
	Kind            AttachmentKind `bun:"kind,notnull"`
	Filename        string         `bun:"filename,notnull"`
	ContentType     string         `bun:"content_type,notnull"`
	Size            int64          `bun:"size,notnull"`
	Width           int            `bun:"width,notnull"`
	Height          int            `bun:"height,notnull"`
	DurationMs      int            `bun:"duration_ms,notnull"`
	IsVoice         bool           `bun:"is_voice,notnull"`
	FileID          *uuid.UUID     `bun:"file_id,type:uuid"`
	ThumbFileID     *uuid.UUID     `bun:"thumb_file_id,type:uuid"`
	SourceChannelID *string        `bun:"source_channel_id"`
	SourceRef       string         `bun:"source_ref,notnull"`
	ThumbSourceRef  string         `bun:"thumb_source_ref,notnull"`
	FetchStatus     FetchStatus    `bun:"fetch_status,notnull"`
	FetchError      string         `bun:"fetch_error,notnull"`
}

// requireMessageContent 检查文字和附件不能同时为空。
func requireMessageContent(text string, attachmentCount int) error {
	if strings.TrimSpace(text) == "" && attachmentCount == 0 {
		return ErrEmptyMessage.New()
	}
	return nil
}

// maxSizeForKind 返回指定媒体类型允许的最大文件体积。
func maxSizeForKind(kind AttachmentKind) filesystem.ByteSize {
	switch kind {
	case AttachmentImage:
		return MaxImageSize
	case AttachmentVideo:
		return MaxVideoSize
	case AttachmentAudio:
		return MaxAudioSize
	default:
		return MaxFileSize
	}
}

// confirmAttachmentInputs 在事务 tx 中校验并确认客户端提交的媒体附件输入列表：
// 主文件和可选封面都必须是 owner 自己上传、未过期、未绑定到其他消息且满足白名单与各类型体积上限。
// 任何一项不合格时返回带字段 attachments[i] 的业务错误，由调用方事务回滚，所有文件保持 temporary 等待清理。
func confirmAttachmentInputs(ctx context.Context, tx bun.IDB, files *filesystem.Service, owner identity.Subject, inputs []AttachmentInput) ([]*Attachment, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	if len(inputs) > MaxAttachments {
		return nil, filesystem.ErrNotAccepted.New(errorx.Field("attachments"))
	}
	if files == nil {
		return nil, errors.New("conversation: filesystem service is required for attachments")
	}
	fs := files.WithDB(tx)
	seen := make(map[uuid.UUID]struct{}, len(inputs)*2)
	out := make([]*Attachment, 0, len(inputs))

	checkUnbound := func(id uuid.UUID, field string) error {
		bound, err := tx.NewSelect().Model((*Attachment)(nil)).
			Where("file_id = ? OR thumb_file_id = ?", id, id).Exists(ctx)
		if err != nil {
			return errors.Wrap(err, "check attachment binding")
		}
		if bound {
			return filesystem.ErrNotFound.New(errorx.Field(field))
		}
		return nil
	}

	for i, in := range inputs {
		field := fmt.Sprintf("attachments[%d]", i)
		id, err := uuid.Parse(in.FileID)
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
		if err := checkUnbound(id, field); err != nil {
			return nil, err
		}
		kind := attachmentKind(f.ContentType)
		if f.Size > int64(maxSizeForKind(kind)) {
			return nil, filesystem.ErrNotAccepted.New(errorx.Field(field))
		}

		// 可选封面缩略图：同样校验归属、未绑定和图片白名单
		var thumbID *uuid.UUID
		if in.ThumbFileID != "" {
			tid, err := uuid.Parse(in.ThumbFileID)
			if err != nil {
				return nil, filesystem.ErrNotFound.New(errorx.Field(field + ".thumb_file_id"))
			}
			if _, dup := seen[tid]; dup {
				return nil, filesystem.ErrNotAccepted.New(errorx.Field(field + ".thumb_file_id"))
			}
			seen[tid] = struct{}{}
			tf, err := fs.Confirm(ctx, owner, tid, thumbAccept)
			if err != nil {
				for _, known := range []*errorx.Template{filesystem.ErrNotFound, filesystem.ErrNotAccepted, filesystem.ErrNotUploaded} {
					if errors.Is(err, known) {
						return nil, known.New(errorx.Field(field + ".thumb_file_id"))
					}
				}
				return nil, err
			}
			if err := checkUnbound(tf.ID, field+".thumb_file_id"); err != nil {
				return nil, err
			}
			thumbID = &tf.ID
		}

		w, h := f.Meta.Width, f.Meta.Height
		if kind == AttachmentVideo && w == 0 && h == 0 {
			w, h = in.Width, in.Height
		}
		dur := 0
		if kind == AttachmentVideo || kind == AttachmentAudio {
			dur = in.DurationMs
		}
		fileUUID := f.ID
		out = append(out, &Attachment{
			ID:          database.NewID(),
			Kind:        kind,
			Filename:    f.Filename,
			ContentType: f.ContentType,
			Size:        f.Size,
			Width:       w,
			Height:      h,
			DurationMs:  dur,
			IsVoice:     kind == AttachmentAudio && in.IsVoice,
			FileID:      &fileUUID,
			ThumbFileID: thumbID,
			FetchStatus: FetchReady,
		})
	}
	return out, nil
}

// buildIncomingAttachments 将外部渠道收到的媒体元数据转为待落库的 Attachment 记录，返回记录列表和是否需要异步拉取队列。
func buildIncomingAttachments(channelID string, incoming []IncomingAttachment) ([]*Attachment, bool) {
	if len(incoming) == 0 {
		return nil, false
	}
	if len(incoming) > MaxAttachments {
		incoming = incoming[:MaxAttachments]
	}
	out := make([]*Attachment, 0, len(incoming))
	needsIngest := false
	for _, in := range incoming {
		kind := in.Kind
		if kind == "" {
			kind = attachmentKind(in.ContentType)
		}
		status := FetchRemote
		fetchErr := ""
		switch {
		case in.Unavailable != "":
			status = FetchUnavailable
			fetchErr = in.Unavailable
		case in.SourceRef == "":
			status = FetchUnavailable
			fetchErr = "channel.media_unavailable"
		case in.AutoFetch:
			status = FetchPending
			needsIngest = true
		}
		if in.ThumbSourceRef != "" {
			needsIngest = true
		}
		chID := channelID
		out = append(out, &Attachment{
			ID:              database.NewID(),
			Kind:            kind,
			Filename:        in.Filename,
			ContentType:     in.ContentType,
			Size:            max(in.Size, 0),
			Width:           max(in.Width, 0),
			Height:          max(in.Height, 0),
			DurationMs:      max(in.DurationMs, 0),
			IsVoice:         kind == AttachmentAudio && in.IsVoice,
			SourceChannelID: &chID,
			SourceRef:       in.SourceRef,
			ThumbSourceRef:  in.ThumbSourceRef,
			FetchStatus:     status,
			FetchError:      fetchErr,
		})
	}
	return out, needsIngest
}

// insertAttachments 在事务 tx 中把附件列表按顺序绑定到租户 tenantID 的时间线项 itemID。
func insertAttachments(ctx context.Context, tx bun.IDB, tenantID, itemID string, rows []*Attachment) error {
	if len(rows) == 0 {
		return nil
	}
	at := database.Now()
	for i, a := range rows {
		if a.ID == "" {
			a.ID = database.NewID()
		}
		a.CreatedAt = at
		a.UpdatedAt = at
		a.TenantID = tenantID
		a.ItemID = itemID
		a.Position = i
	}
	_, err := tx.NewInsert().Model(&rows).Exec(ctx)
	if database.IsUniqueViolation(err, "timeline_attachments_file") || database.IsUniqueViolation(err, "timeline_attachments_thumb") {
		return filesystem.ErrNotFound.New(errorx.Field("attachments"))
	}
	return errors.Wrap(err, "insert timeline attachments")
}

// LoadItemAttachments 读取租户 tenantID 中时间线项 itemID 已就绪（FetchReady）的全部附件行，按 position 排序。
// 供工单公开进度向关联客户会话广播时复用附件文件引用。
func LoadItemAttachments(ctx context.Context, db bun.IDB, tenantID, itemID string) ([]*Attachment, error) {
	var rows []*Attachment
	err := db.NewSelect().Model(&rows).
		Where("tenant_id = ? AND item_id = ? AND fetch_status = ?", tenantID, itemID, FetchReady).
		OrderExpr("position").
		Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "load item attachments")
	}
	return rows, nil
}

// loadAttachments 批量加载 itemIDs 对应的附件列表，按 position 排序，并经 files.GetMany 和 images 生成公开地址与缩略图地址。
func loadAttachments(ctx context.Context, db bun.IDB, media Media, itemIDs []string) (map[string][]AttachmentDTO, error) {
	out := map[string][]AttachmentDTO{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	var rows []Attachment
	err := db.NewSelect().Model(&rows).Where("item_id IN (?)", bun.In(itemIDs)).
		OrderExpr("item_id, position").Scan(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "load timeline attachments")
	}
	if len(rows) == 0 {
		return out, nil
	}
	dtos, err := buildAttachmentDTOs(ctx, db, media, rows)
	if err != nil {
		return nil, err
	}
	for i, row := range rows {
		out[row.ItemID] = append(out[row.ItemID], dtos[i])
	}
	return out, nil
}

// buildAttachmentDTOs 批量为附件记录填充主文件与封面缩略图的公开地址和最终元数据。
func buildAttachmentDTOs(ctx context.Context, db bun.IDB, media Media, rows []Attachment) ([]AttachmentDTO, error) {
	fileIDs := make([]uuid.UUID, 0, len(rows)*2)
	for _, r := range rows {
		if r.FileID != nil {
			fileIDs = append(fileIDs, *r.FileID)
		}
		if r.ThumbFileID != nil {
			fileIDs = append(fileIDs, *r.ThumbFileID)
		}
	}
	filesByID := map[uuid.UUID]filesystem.File{}
	if len(fileIDs) > 0 && media.Files != nil {
		var err error
		filesByID, err = media.Files.WithDB(db).GetMany(ctx, fileIDs)
		if err != nil {
			return nil, err
		}
	}
	out := make([]AttachmentDTO, len(rows))
	for i, r := range rows {
		dto := AttachmentDTO{
			ID:          r.ID,
			Kind:        r.Kind,
			Status:      r.FetchStatus,
			Error:       r.FetchError,
			Filename:    r.Filename,
			ContentType: r.ContentType,
			Size:        r.Size,
			Width:       r.Width,
			Height:      r.Height,
			DurationMs:  r.DurationMs,
			IsVoice:     r.IsVoice,
		}
		if r.FileID != nil {
			dto.FileID = r.FileID.String()
			if f, ok := filesByID[*r.FileID]; ok {
				if f.URL != "" {
					dto.URL = f.URL
				} else {
					dto.URL = "/api/attachments/" + r.ID + "/file"
				}
				if dto.Filename == "" {
					dto.Filename = f.Filename
				}
				if dto.ContentType == "" {
					dto.ContentType = f.ContentType
				}
				if dto.Size == 0 {
					dto.Size = f.Size
				}
				if dto.Width == 0 {
					dto.Width = f.Meta.Width
				}
				if dto.Height == 0 {
					dto.Height = f.Meta.Height
				}
				if r.Kind == AttachmentImage && r.ThumbFileID == nil {
					if f.URL != "" {
						dto.ThumbURL = media.Images.URL(f.URL, ImageThumb)
					} else {
						dto.ThumbURL = dto.URL
					}
				}
			}
		}
		if r.ThumbFileID != nil {
			if tf, ok := filesByID[*r.ThumbFileID]; ok {
				if tf.URL != "" {
					dto.ThumbURL = media.Images.URL(tf.URL, ImageThumb)
				} else {
					dto.ThumbURL = "/api/attachments/" + r.ID + "/file?variant=thumb"
				}
			}
		}
		out[i] = dto
	}
	return out, nil
}

// contactAttachmentTTL 是发给聊天窗口访客的私有附件签名有效期。
const contactAttachmentTTL = 15 * time.Minute

// SignContactAttachments 把成员端私有附件地址转换为聊天插件渠道下带短期 HMAC 签名的地址。
func SignContactAttachments(atts []AttachmentDTO, channelID string, signer *crypt.Crypt) []AttachmentDTO {
	if len(atts) == 0 || channelID == "" {
		return atts
	}
	rewrite := func(raw string) string {
		if !strings.HasPrefix(raw, "/api/attachments/") {
			return raw
		}
		target := "/w/" + url.PathEscape(channelID) + raw
		if signer != nil {
			return signer.SignURL(target, contactAttachmentTTL)
		}
		return target
	}
	out := make([]AttachmentDTO, len(atts))
	for i, att := range atts {
		att.URL = rewrite(att.URL)
		att.ThumbURL = rewrite(att.ThumbURL)
		out[i] = att
	}
	return out
}

// AttachmentMeta 是附件下载鉴权时需要的租户、所属会话和底层文件指针。
type AttachmentMeta struct {
	ID             string
	TenantID       string
	ConversationID string
	Kind           AttachmentKind
	FileID         *uuid.UUID
	ThumbFileID    *uuid.UUID
}

// LoadAttachment 读取成员端待下载的附件元数据；附件不存在时返回 ErrAttachmentNotFound。
func LoadAttachment(ctx context.Context, db bun.IDB, id string) (*AttachmentMeta, error) {
	var row struct {
		ID             string         `bun:"id"`
		TenantID       string         `bun:"tenant_id"`
		ConversationID string         `bun:"conversation_id"`
		Kind           AttachmentKind `bun:"kind"`
		FileID         *uuid.UUID     `bun:"file_id"`
		ThumbFileID    *uuid.UUID     `bun:"thumb_file_id"`
	}
	err := db.NewSelect().TableExpr("timeline_attachments AS ta").
		Join("JOIN timeline_items AS ti ON ti.id = ta.item_id").
		Column("ta.id", "ta.tenant_id", "ti.conversation_id", "ta.kind", "ta.file_id", "ta.thumb_file_id").
		Where("ta.id = ?", id).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAttachmentNotFound.New()
	}
	if err != nil {
		return nil, errors.Wrap(err, "load attachment meta")
	}
	return &AttachmentMeta{
		ID: row.ID, TenantID: row.TenantID, ConversationID: row.ConversationID,
		Kind: row.Kind, FileID: row.FileID, ThumbFileID: row.ThumbFileID,
	}, nil
}

// LoadAttachmentForContact 读取聊天插件访客端经签名地址下载的附件元数据，限定同工作区且原消息对所有人可见。
func LoadAttachmentForContact(ctx context.Context, db bun.IDB, tenantID, id string) (*AttachmentMeta, error) {
	var row struct {
		ID             string         `bun:"id"`
		TenantID       string         `bun:"tenant_id"`
		ConversationID string         `bun:"conversation_id"`
		Kind           AttachmentKind `bun:"kind"`
		FileID         *uuid.UUID     `bun:"file_id"`
		ThumbFileID    *uuid.UUID     `bun:"thumb_file_id"`
	}
	err := db.NewSelect().TableExpr("timeline_attachments AS ta").
		Join("JOIN timeline_items AS ti ON ti.id = ta.item_id").
		Column("ta.id", "ta.tenant_id", "ti.conversation_id", "ta.kind", "ta.file_id", "ta.thumb_file_id").
		Where("ta.id = ? AND ta.tenant_id = ? AND ti.tenant_id = ? AND ti.visibility = ?", id, tenantID, tenantID, Everyone).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAttachmentNotFound.New()
	}
	if err != nil {
		return nil, errors.Wrap(err, "load contact attachment meta")
	}
	return &AttachmentMeta{
		ID: row.ID, TenantID: row.TenantID, ConversationID: row.ConversationID,
		Kind: row.Kind, FileID: row.FileID, ThumbFileID: row.ThumbFileID,
	}, nil
}

// loadPreviewAttachments 批量查询每个时间线项（会话列表最后一条消息）的附件摘要：首附件类型和附件总数。
func loadPreviewAttachments(ctx context.Context, db bun.IDB, itemIDs []string) (map[string]*PreviewAttachment, error) {
	out := map[string]*PreviewAttachment{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ItemID string         `bun:"item_id"`
		Kind   AttachmentKind `bun:"kind"`
		Count  int            `bun:"count"`
	}
	err := db.NewRaw(`SELECT ta.item_id,
			(SELECT t2.kind FROM timeline_attachments t2
				WHERE t2.item_id = ta.item_id ORDER BY t2.position LIMIT 1) AS kind,
			count(*) AS count
		FROM timeline_attachments ta
		WHERE ta.item_id IN (?)
		GROUP BY ta.item_id`, bun.In(itemIDs)).Scan(ctx, &rows)
	if err != nil {
		return nil, errors.Wrap(err, "load preview attachments")
	}
	for _, r := range rows {
		if r.Count > 0 {
			out[r.ItemID] = &PreviewAttachment{Kind: r.Kind, Count: r.Count}
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
			ID:          a.ID,
			FileID:      a.FileID,
			Kind:        string(a.Kind),
			Filename:    a.Filename,
			ContentType: a.ContentType,
			Size:        a.Size,
			Width:       a.Width,
			Height:      a.Height,
			DurationMs:  a.DurationMs,
			IsVoice:     a.IsVoice,
			URL:         a.URL,
			ThumbURL:    a.ThumbURL,
		}
	}
	return out
}

// attachmentKind 根据 MIME 类型判断附件展示类型（图片、视频、音频或文件）。
func attachmentKind(contentType string) AttachmentKind {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	switch {
	case strings.HasPrefix(ct, "image/"):
		return AttachmentImage
	case strings.HasPrefix(ct, "video/"):
		return AttachmentVideo
	case strings.HasPrefix(ct, "audio/"):
		return AttachmentAudio
	default:
		return AttachmentFile
	}
}
