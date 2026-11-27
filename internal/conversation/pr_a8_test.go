package conversation_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/tenant"
)

// TestAMRVoiceUploadAndMaterialize 验证 audio/amr 语音格式可经文件系统 Confirm 与客户来信入库物化为 .amr 附件。
func TestAMRVoiceUploadAndMaterialize(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "amr-alpha")

	amrBytes := append([]byte("#!AMR\n"), bytes.Repeat([]byte{0x3c, 0x01, 0x02}, 16)...)
	fetcher := &mockMediaFetcher{
		media: map[string][]byte{"amr-1": amrBytes},
		mime:  map[string]string{"amr-1": "audio/amr"},
		name:  map[string]string{"amr-1": "voice-msg"},
	}
	reg := channel.Registry{channel.Test: fetcher}
	down := conversation.Downstream{
		Roles: env.Roles,
		Media: conversation.Media{Files: env.Files},
		Sinks: f.down.Sinks,
	}

	rec, err := conversation.NewReceive(env.DB, reg, down).Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID:  f.ch.ID,
		From:       "cust-amr",
		FromName:   "AMR Visitor",
		ExternalID: "ext-amr-1",
		IncomingAttachments: []conversation.IncomingAttachment{
			{Kind: "audio", Filename: "voice-msg", ContentType: "audio/amr", Size: int64(len(amrBytes)), SourceRef: "amr-1", AutoFetch: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ingestor := conversation.NewMediaIngestor(env.DB, reg, down)
	if err := ingestor.Job(ctx, conversation.MediaIngestJob{
		TenantID:       f.system.Tenant,
		ConversationID: rec.ConversationID,
		ItemID:         rec.Item.ID,
	}); err != nil {
		t.Fatalf("AMR 媒体拉取失败：%v", err)
	}

	var att conversation.Attachment
	if err := env.DB.NewSelect().Model(&att).Where("item_id = ?", rec.Item.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if att.FetchStatus != conversation.FetchReady || att.FileID == nil || att.ContentType != "audio/amr" {
		t.Fatalf("AMR 附件状态异常：%+v", att)
	}
	storedFile, err := env.Files.Get(ctx, *att.FileID)
	if err != nil {
		t.Fatal(err)
	}
	if storedFile.Filename != "voice-msg.amr" {
		t.Fatalf("AMR 文件扩展名应补齐为 .amr，实际 %q", storedFile.Filename)
	}
}

// TestDeliverJobReclaimsFailedDelivery 验证队列死信重试再次调用 Deliver.Job 时可从 failed 恢复投递并清除 translation_failed。
func TestDeliverJobReclaimsFailedDelivery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)

	alpha, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "DeadRetry", Slug: "dead-retry", OwnerEmail: "owner@dead-retry.test", OwnerName: "Owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	sys := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
	subOwner := identity.Subject{Type: member.Type, ID: alpha.OwnerID, Tenant: alpha.ID, Permissions: auth.WithBase(auth.Owner.Permissions)}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, sys, channel.CreateParams{Kind: channel.Test, Name: "测试渠道"})
	if err != nil {
		t.Fatal(err)
	}
	conn := &captureConnector{}
	reg := channel.Registry{channel.Test: conn}
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })

	failDown := conversation.Downstream{
		Hub:   hub,
		Roles: env.Roles,
		AutoTranslate: func(_ context.Context, _ conversation.AutoTranslateJob) (string, string, bool, error) {
			return "", "", false, errors.New("llm unavailable")
		},
	}
	rec, err := conversation.NewReceive(env.DB, reg, failDown).Handle(ctx, sys, conversation.ReceiveParams{
		ChannelID: ch.ID, From: "cust-dead", FromName: "Visitor", Text: "Help please",
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := conversation.NewReply(env.DB, failDown).Handle(ctx, subOwner, rec.ConversationID, conversation.ReplyBody{
		Text:          "请稍等，马上为您处理",
		AutoTranslate: &conversation.AutoTranslateRequest{Language: "en"},
	})
	if err != nil {
		t.Fatal(err)
	}
	job := conversation.Appended{TenantID: alpha.ID, ConversationID: rec.ConversationID, ItemID: reply.ID, Seq: reply.Seq}
	failDeliver := conversation.NewDeliver(env.DB, reg, failDown)
	if err := failDeliver.Job(ctx, job); !errors.Is(err, conversation.ErrAutoTranslateFailed) {
		t.Fatalf("期望 ErrAutoTranslateFailed，实际 %v", err)
	}
	if err := failDeliver.OnDead(ctx, job, conversation.ErrAutoTranslateFailed); err != nil {
		t.Fatal(err)
	}

	okDown := failDown
	okDown.AutoTranslate = func(_ context.Context, _ conversation.AutoTranslateJob) (string, string, bool, error) {
		return "One moment, handling it now.", "en", true, nil
	}
	if err := conversation.NewDeliver(env.DB, reg, okDown).Job(ctx, job); err != nil {
		t.Fatalf("从 failed 状态重跑 Deliver.Job 应成功，实际 %v", err)
	}
	if len(conn.sent) != 1 || conn.sent[0].Text != "One moment, handling it now." {
		t.Fatalf("重跑 Deliver.Job 应完成翻译并投递译文：%v", conn.sent)
	}
	var d conversation.Delivery
	if err := env.DB.NewSelect().Model(&d).Where("item_id = ?", reply.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if d.Status != conversation.Delivered || d.Error != "" {
		t.Fatalf("重跑后 delivery 状态应为 delivered 且清空 error：%+v", d)
	}
	var item conversation.Item
	if err := env.DB.NewSelect().Model(&item).Where("id = ?", reply.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if item.Derived["translation_failed"] != nil {
		t.Fatalf("重跑成功后应清除 translation_failed：%+v", item.Derived)
	}
}

// TestMediaIngestorOnDeadAndRecoverStaleFetches 验证媒体任务死信收敛、陈旧 fetching 抢占及定时恢复行为。
func TestMediaIngestorOnDeadAndRecoverStaleFetches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "media-recover")

	videoPayload := makeMP4Box()
	fetcher := &mockMediaFetcher{
		media: map[string][]byte{"vid-1": videoPayload, "vid-2": videoPayload},
		mime:  map[string]string{"vid-1": "video/mp4", "vid-2": "video/mp4"},
		name:  map[string]string{"vid-1": "clip1.mp4", "vid-2": "clip2.mp4"},
	}
	reg := channel.Registry{channel.Test: fetcher}
	down := conversation.Downstream{
		Roles: env.Roles,
		Media: conversation.Media{Files: env.Files},
		Sinks: f.down.Sinks,
	}

	rec, err := conversation.NewReceive(env.DB, reg, down).Handle(ctx, f.system, conversation.ReceiveParams{
		ChannelID:  f.ch.ID,
		From:       "cust-media",
		FromName:   "Media Visitor",
		ExternalID: "ext-media-1",
		IncomingAttachments: []conversation.IncomingAttachment{
			{Kind: "image", Filename: "pic.png", ContentType: "image/png", Size: 64, SourceRef: "missing-img", AutoFetch: true},
			{Kind: "video", Filename: "clip1.mp4", ContentType: "video/mp4", Size: int64(len(videoPayload)), SourceRef: "vid-1", AutoFetch: false},
			{Kind: "video", Filename: "clip2.mp4", ContentType: "video/mp4", Size: int64(len(videoPayload)), SourceRef: "vid-2", AutoFetch: false},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var atts []conversation.Attachment
	if err := env.DB.NewSelect().Model(&atts).Where("item_id = ?", rec.Item.ID).OrderExpr("position ASC").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(atts) != 3 {
		t.Fatalf("期望 3 条附件，实际 %d", len(atts))
	}

	// 1. OnDead 仅将 pending 图片收敛为 unavailable，保留 remote 视频
	ingestor := conversation.NewMediaIngestor(env.DB, reg, down)
	if err := ingestor.OnDead(ctx, conversation.MediaIngestJob{
		TenantID:       f.system.Tenant,
		ConversationID: rec.ConversationID,
		ItemID:         rec.Item.ID,
	}, errors.New("upstream unreachable")); err != nil {
		t.Fatal(err)
	}
	if err := env.DB.NewSelect().Model(&atts).Where("item_id = ?", rec.Item.ID).OrderExpr("position ASC").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if atts[0].FetchStatus != conversation.FetchUnavailable || atts[1].FetchStatus != conversation.FetchRemote || atts[2].FetchStatus != conversation.FetchRemote {
		t.Fatalf("OnDead 后状态异常：%+v", atts)
	}

	// 2. 陈旧 fetching（>5m）可被定时回收器恢复为 remote，活跃 fetching 不受影响
	staleAt := time.Now().UTC().Add(-6 * time.Minute)
	freshAt := time.Now().UTC().Add(-1 * time.Minute)
	if _, err := env.DB.NewRaw(`UPDATE timeline_attachments SET fetch_status = ?, updated_at = ? WHERE id = ?`,
		conversation.FetchFetching, staleAt, atts[1].ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw(`UPDATE timeline_attachments SET fetch_status = ?, updated_at = ? WHERE id = ?`,
		conversation.FetchFetching, freshAt, atts[2].ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	recovered, err := conversation.RecoverStaleMediaFetches(ctx, env.DB, 5*time.Minute, 100)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("应仅恢复 1 条超时 fetching 记录，实际 %d", recovered)
	}

	if err := env.DB.NewSelect().Model(&atts).Where("item_id = ?", rec.Item.ID).OrderExpr("position ASC").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if atts[1].FetchStatus != conversation.FetchRemote || atts[2].FetchStatus != conversation.FetchFetching {
		t.Fatalf("RecoverStaleMediaFetches 后状态异常：att1=%s att2=%s", atts[1].FetchStatus, atts[2].FetchStatus)
	}

	// 3. 超时停留在 fetching 的记录也可在点击 FetchAttachment 时直接抢占重拉
	if _, err := env.DB.NewRaw(`UPDATE timeline_attachments SET fetch_status = ?, updated_at = ? WHERE id = ?`,
		conversation.FetchFetching, staleAt, atts[2].ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	gotDTO, err := conversation.NewFetchAttachment(env.DB, reg, down).Handle(ctx, f.agent, rec.ConversationID, atts[2].ID)
	if err != nil {
		t.Fatalf("抢占超时 fetching 记录重拉失败：%v", err)
	}
	if gotDTO.Status != conversation.FetchReady || gotDTO.URL == "" {
		t.Fatalf("重拉后应为 ready：%+v", gotDTO)
	}
}

type mockMediaFetcher struct {
	media map[string][]byte
	mime  map[string]string
	name  map[string]string
}

func (*mockMediaFetcher) Capabilities() channel.Capabilities {
	return channel.Capabilities{MessageTypes: []string{"text", "image", "audio", "video", "file"}}
}

func (*mockMediaFetcher) Send(_ context.Context, _ channel.Outgoing) (string, bool, error) {
	return "", false, nil
}

func (m *mockMediaFetcher) FetchMedia(_ context.Context, _ *channel.Channel, ref string, _ int64) (*channel.FetchedMedia, error) {
	data, ok := m.media[ref]
	if !ok {
		return nil, channel.ErrMediaUnavailable.New()
	}
	return &channel.FetchedMedia{
		Body:        io.NopCloser(bytes.NewReader(data)),
		Filename:    m.name[ref],
		ContentType: m.mime[ref],
		Size:        int64(len(data)),
	}, nil
}

func makeMP4Box() []byte {
	var buf bytes.Buffer
	// ftyp box
	buf.Write([]byte{0x00, 0x00, 0x00, 0x18})
	buf.WriteString("ftypmp42")
	buf.Write([]byte{0x00, 0x00, 0x00, 0x00})
	buf.WriteString("mp42isom")
	// mdat box
	buf.Write([]byte{0x00, 0x00, 0x00, 0x10})
	buf.WriteString("mdat01234567")
	return buf.Bytes()
}
