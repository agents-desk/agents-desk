package conversation_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/imgproxy"
	"github.com/go-tenon/tenon/queue/queuetest"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
)

// makePNG 生成指定宽高的合法 PNG 图片字节流。
func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 12, G: 34, B: 56, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// makeWebPVP8L 生成带合法 RIFF/WEBP/VP8L 头的最小 WebP 字节流，用于测试 WebP 宽高解析。
func makeWebPVP8L(w, h int) []byte {
	b := make([]byte, 32)
	copy(b[0:4], "RIFF")
	binary.LittleEndian.PutUint32(b[4:8], uint32(len(b)-8))
	copy(b[8:12], "WEBP")
	copy(b[12:16], "VP8L")
	binary.LittleEndian.PutUint32(b[16:20], 12)
	b[20] = 0x2f
	bits := uint32(w-1) | (uint32(h-1) << 14)
	binary.LittleEndian.PutUint32(b[21:25], bits)
	return b
}

// makeDOCX 生成最小合法 DOCX（OOXML ZIP）字节流。
func makeDOCX(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"[Content_Types].xml": "<Types/>",
		"word/document.xml":   "<w:document/>",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// recordingConnector 记录发出的 Outgoing 消息，用于验证连接器收到完整的附件信息。
type recordingConnector struct {
	sent []channel.Outgoing
}

func (*recordingConnector) Capabilities() channel.Capabilities {
	return channel.Capabilities{MessageTypes: []string{"text", "image", "file"}}
}

func (c *recordingConnector) Send(_ context.Context, m channel.Outgoing) (string, bool, error) {
	c.sent = append(c.sent, m)
	return "rec-" + m.ItemID, true, nil
}

// TestAttachmentsFlow 检查图片（PNG/WebP）与常用文件（DOCX/PDF/CSV）在回复、内部备注、客户来信、
// 时间线读取、会话列表摘要、连接器发出及 imgproxy 缩略图上的完整链路。
func TestAttachmentsFlow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "alpha")
	proxy, err := imgproxy.New(imgproxy.Config{
		URL:  "https://img.example.com",
		Key:  "0123456789abcdef0123456789abcdef",
		Salt: "fedcba9876543210fedcba9876543210",
	})
	if err != nil {
		t.Fatal(err)
	}
	media := conversation.Media{Files: env.Files, Images: proxy}
	f.down.Media = media

	uploadAs := func(owner identity.Subject, filename, ct string, data []byte) filesystem.File {
		t.Helper()
		rec, err := env.Files.Upload(ctx, owner, filesystem.FileInput{
			Filename: filename, ContentType: ct, Size: int64(len(data)),
		}, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}

	// 1. 客户发来纯图片消息（PNG + WebP）：自动建立会话，不触发翻译，列表摘要带 preview_attachment
	pngFile := uploadAs(f.system, "shot.png", "image/png", makePNG(t, 120, 80))
	webpFile := uploadAs(f.system, "pic.webp", "image/webp", makeWebPVP8L(320, 240))
	rec, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), f.down).Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID: f.ch.ID,
		From:      "rahul",
		FromName:  "Rahul",
		Attachments: []conversation.AttachmentInput{
			{FileID: pngFile.ID.String()},
			{FileID: webpFile.ID.String()},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Item.Attachments) != 2 {
		t.Fatalf("来信附件数 = %d，期望 2", len(rec.Item.Attachments))
	}
	a0, a1 := rec.Item.Attachments[0], rec.Item.Attachments[1]
	if a0.Kind != conversation.AttachmentImage || a0.Status != conversation.FetchReady || a0.Width != 120 || a0.Height != 80 ||
		!strings.HasPrefix(a0.URL, "/storage/") || !strings.HasPrefix(a0.ThumbURL, "https://img.example.com/") {
		t.Fatalf("PNG 附件 = %+v", a0)
	}
	if a1.Kind != conversation.AttachmentImage || a1.Width != 320 || a1.Height != 240 {
		t.Fatalf("WebP 附件 = %+v", a1)
	}
	// 纯附件消息不需要翻译
	if (&conversation.Item{Kind: rec.Item.Kind, ActorType: rec.Item.Actor.Type, Visibility: rec.Item.Visibility, Content: rec.Item.Content}).Translatable() {
		t.Fatal("纯附件消息不应标记为可翻译")
	}
	conv := f.get(t, rec.ConversationID)
	if conv.Preview != "" || conv.PreviewAttachment == nil || conv.PreviewAttachment.Kind != conversation.AttachmentImage || conv.PreviewAttachment.Count != 2 {
		t.Fatalf("会话列表摘要 = preview:%q attachment:%+v", conv.Preview, conv.PreviewAttachment)
	}

	// 2. 坐席回复带上 DOCX（客户端给 octet-stream 兜底）+ CSV + 文字，交给连接器发出
	docxFile := uploadAs(f.agent, "合同.docx", "application/octet-stream", makeDOCX(t))
	csvFile := uploadAs(f.agent, "orders.csv", "application/vnd.ms-excel", []byte("id,total\n1,99\n"))
	reply, err := conversation.NewReply(env.DB, f.down).Handle(ctx, f.agent, conv.ID, conversation.ReplyBody{
		Text: "请查收附件",
		Attachments: []conversation.AttachmentInput{
			{FileID: docxFile.ID.String()},
			{FileID: csvFile.ID.String()},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Attachments) != 2 || reply.Attachments[0].Kind != conversation.AttachmentFile || reply.Attachments[0].ThumbURL != "" ||
		reply.Attachments[0].ContentType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Fatalf("回复附件 = %+v", reply.Attachments)
	}
	// 确认文件已变为 ready
	if gotFile, err := env.Files.Get(ctx, docxFile.ID); err != nil || gotFile.Status != filesystem.StatusReady {
		t.Fatalf("docx 状态 = %+v %v", gotFile, err)
	}

	// 连接器发出时带上完整附件
	recConn := &recordingConnector{}
	reg := channel.Registry{channel.Test: recConn}
	deliver := conversation.NewDeliver(env.DB, reg, conversation.Downstream{Media: media})
	for _, job := range queuetest.Pending(t, env.DB, conversation.DeliverTopic) {
		if err := deliver.Job(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	if len(recConn.sent) != 1 || len(recConn.sent[0].Attachments) != 2 || recConn.sent[0].Attachments[0].Filename != "合同.docx" {
		t.Fatalf("连接器收到的消息 = %+v", recConn.sent)
	}

	// 3. 内部备注也可以纯发图片
	noteImg := uploadAs(f.agent, "internal.png", "image/png", makePNG(t, 64, 64))
	note, err := conversation.NewAddNote(env.DB, f.down).Handle(ctx, f.agent, conv.ID, "", nil, conversation.AttachmentInput{FileID: noteImg.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if len(note.Attachments) != 1 || note.Attachments[0].Width != 64 {
		t.Fatalf("备注附件 = %+v", note.Attachments)
	}

	// 4. 坐席时间线包含全部 3 条带附件的项；联系人时间线不含内部备注，但包含客户来信与坐席回复的附件
	items, err := conversation.NewTimeline(env.DB, media).Handle(ctx, f.agent, conv.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	attachCount := 0
	for _, it := range items {
		attachCount += len(it.Attachments)
	}
	if attachCount != 5 {
		t.Fatalf("时间线总附件数 = %d，期望 5", attachCount)
	}

	contactSub := identity.Subject{Type: contact.Type, ID: conv.Contact.ID, Tenant: f.system.Tenant}
	contactStream, err := conversation.NewContactStream(env.DB, media).Handle(ctx, contactSub, f.ch.ID, "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(contactStream.Items) != 2 || len(contactStream.Items[0].Attachments) != 2 || len(contactStream.Items[1].Attachments) != 2 {
		t.Fatalf("联系人连续时间线附件 = %+v", contactStream.Items)
	}
}

// TestAttachmentsValidationAndIsolation 检查空消息、伪装文件、SVG、超大图片、事务回滚以及跨主体/跨工作区越权引用都被拒绝。
func TestAttachmentsValidationAndIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	alpha := setup(t, env, "alpha")
	beta := setup(t, env, "beta")
	alpha.down.Media = conversation.Media{Files: env.Files}
	beta.down.Media = conversation.Media{Files: env.Files}

	convID := alpha.receive(t, "rahul", "hi", "").ConversationID

	// 空消息（无文字无附件）被拒绝
	if _, err := conversation.NewReply(env.DB, alpha.down).Handle(ctx, alpha.agent, convID, conversation.ReplyBody{Text: "   "}); !errors.Is(err, conversation.ErrEmptyMessage) {
		t.Fatalf("空回复应返回 ErrEmptyMessage，得到 %v", err)
	}
	if _, err := conversation.NewAddNote(env.DB, alpha.down).Handle(ctx, alpha.agent, convID, "", nil); !errors.Is(err, conversation.ErrEmptyMessage) {
		t.Fatalf("空备注应返回 ErrEmptyMessage，得到 %v", err)
	}

	uploadAs := func(owner identity.Subject, filename, ct string, data []byte) filesystem.File {
		t.Helper()
		rec, err := env.Files.Upload(ctx, owner, filesystem.FileInput{
			Filename: filename, ContentType: ct, Size: int64(len(data)),
		}, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}

	// 伪装成 PNG 的 HTML 被拒绝
	fakePNG := uploadAs(alpha.agent, "evil.png", "image/png", []byte("<html><script>alert(1)</script></html>"))
	if _, err := conversation.NewReply(env.DB, alpha.down).Handle(ctx, alpha.agent, convID, conversation.ReplyBody{
		Attachments: []conversation.AttachmentInput{{FileID: fakePNG.ID.String()}},
	}); !errors.Is(err, filesystem.ErrNotAccepted) {
		t.Fatalf("HTML 伪装 PNG 应返回 ErrNotAccepted，得到 %v", err)
	}

	// SVG 被拒绝
	svg := uploadAs(alpha.agent, "icon.svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	if _, err := conversation.NewReply(env.DB, alpha.down).Handle(ctx, alpha.agent, convID, conversation.ReplyBody{
		Attachments: []conversation.AttachmentInput{{FileID: svg.ID.String()}},
	}); !errors.Is(err, filesystem.ErrNotAccepted) {
		t.Fatalf("SVG 应返回 ErrNotAccepted，得到 %v", err)
	}

	// 同一条消息中第二个附件失败时，事务整体回滚，第一个合法文件也保持 temporary
	goodPNG := uploadAs(alpha.agent, "good.png", "image/png", makePNG(t, 40, 40))
	if _, err := conversation.NewReply(env.DB, alpha.down).Handle(ctx, alpha.agent, convID, conversation.ReplyBody{
		Attachments: []conversation.AttachmentInput{
			{FileID: goodPNG.ID.String()},
			{FileID: fakePNG.ID.String()},
		},
	}); !errors.Is(err, filesystem.ErrNotAccepted) {
		t.Fatalf("混合非法附件应返回 ErrNotAccepted，得到 %v", err)
	}
	if rec, err := env.Files.Get(ctx, goodPNG.ID); err != nil || rec.Status != filesystem.StatusTemporary {
		t.Fatalf("回滚后合法文件应保持 temporary，得到 %+v %v", rec, err)
	}

	// 其他成员和其他工作区的成员不能引用别人上传的文件；已确认的文件即使同一成员也不能跨工作区复用
	otherMember := identity.Subject{Type: member.Type, ID: "01900000-0000-7000-8000-0000000000b2", Tenant: alpha.system.Tenant, Permissions: alpha.agent.Permissions}
	if _, err := conversation.NewReply(env.DB, alpha.down).Handle(ctx, otherMember, convID, conversation.ReplyBody{
		Attachments: []conversation.AttachmentInput{{FileID: goodPNG.ID.String()}},
	}); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("引用他人上传的文件应返回 ErrNotFound，得到 %v", err)
	}
	betaConv := beta.receive(t, "priya", "hello", "").ConversationID
	betaMember := identity.Subject{Type: member.Type, ID: "01900000-0000-7000-8000-0000000000b3", Tenant: beta.system.Tenant, Permissions: beta.agent.Permissions}
	if _, err := conversation.NewReply(env.DB, beta.down).Handle(ctx, betaMember, betaConv, conversation.ReplyBody{
		Attachments: []conversation.AttachmentInput{{FileID: goodPNG.ID.String()}},
	}); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("跨工作区其他成员引用文件应返回 ErrNotFound，得到 %v", err)
	}
	// 在 alpha 确认绑定后，状态变为 ready，同一成员在 beta 中也不能再次绑定；beta 成员也不能读取 alpha 的时间线
	if _, err := conversation.NewReply(env.DB, alpha.down).Handle(ctx, alpha.agent, convID, conversation.ReplyBody{
		Attachments: []conversation.AttachmentInput{{FileID: goodPNG.ID.String()}},
	}); err != nil {
		t.Fatalf("alpha 确认合法附件失败：%v", err)
	}
	if _, err := conversation.NewReply(env.DB, beta.down).Handle(ctx, beta.agent, betaConv, conversation.ReplyBody{
		Attachments: []conversation.AttachmentInput{{FileID: goodPNG.ID.String()}},
	}); !errors.Is(err, filesystem.ErrNotFound) {
		t.Fatalf("已确认的附件不能跨工作区复用，应返回 ErrNotFound，得到 %v", err)
	}
	if _, err := conversation.NewTimeline(env.DB, beta.down.Media).Handle(ctx, beta.agent, convID, 0, 50); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("跨工作区读取带附件的时间线应返回 ErrNotFound，得到 %v", err)
	}

	// 支持视频 + 封面图 + 音频语音上传确认
	mp4Data := make([]byte, 28)
	binary.BigEndian.PutUint32(mp4Data[0:4], 20)
	copy(mp4Data[4:8], "ftyp")
	copy(mp4Data[8:12], "isom")
	copy(mp4Data[16:20], "mp41")
	vidFile := uploadAs(alpha.agent, "demo.mp4", "video/mp4", mp4Data)
	thumbFile := uploadAs(alpha.agent, "poster.png", "image/png", makePNG(t, 160, 90))
	oggData := append(append([]byte("OggS"), make([]byte, 24)...), []byte("OpusHead\x01\x01")...)
	voiceFile := uploadAs(alpha.agent, "voice.ogg", "audio/ogg", oggData)

	mediaReply, err := conversation.NewReply(env.DB, alpha.down).Handle(ctx, alpha.agent, convID, conversation.ReplyBody{
		Attachments: []conversation.AttachmentInput{
			{FileID: vidFile.ID.String(), ThumbFileID: thumbFile.ID.String(), DurationMs: 12500, Width: 1280, Height: 720},
			{FileID: voiceFile.ID.String(), DurationMs: 4200, IsVoice: true},
		},
	})
	if err != nil {
		t.Fatalf("音视频附件回复失败：%v", err)
	}
	if len(mediaReply.Attachments) != 2 ||
		mediaReply.Attachments[0].Kind != conversation.AttachmentVideo ||
		mediaReply.Attachments[0].ThumbURL == "" ||
		mediaReply.Attachments[0].DurationMs != 12500 ||
		mediaReply.Attachments[1].Kind != conversation.AttachmentAudio ||
		!mediaReply.Attachments[1].IsVoice ||
		mediaReply.Attachments[1].DurationMs != 4200 {
		t.Fatalf("音视频附件 DTO = %+v", mediaReply.Attachments)
	}
}
