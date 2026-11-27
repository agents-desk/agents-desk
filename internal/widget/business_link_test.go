package widget

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/ratelimit"
	"github.com/go-tenon/tenon/ratelimit/pglimit"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/flags"
	"agents-desk/internal/member"
)

func TestBusinessLinkEndToEnd(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env := apptest.New(t)
	f := setup(t, env, "biz")

	// 新建另一位同工作区成员，验证个人商务链接的严格归属隔离
	agentRole, err := env.Roles.RoleByName(ctx, "坐席", f.system.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := member.NewAdd(env.DB, env.Roles, env.MemberLogins, env.Files).Handle(ctx, f.system, member.AddParams{
		Email: "bob@biz.com", Name: "Bob", Password: "password", RoleID: agentRole.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	subBob := identity.Subject{Type: member.Type, ID: bob.ID, Tenant: f.system.Tenant, Permissions: auth.WithBase(nil)}

	list := NewListBusinessLinks(env.DB, "https://desk.example.com")
	create := NewCreateBusinessLink(env.DB, "https://desk.example.com")
	update := NewUpdateBusinessLink(env.DB, "https://desk.example.com")
	del := NewDeleteBusinessLink(env.DB)

	// 1. 成员创建个人网页商务沟通链接，自动生成独立短链与二维码
	initLoc := Localization{
		DefaultLanguage: "zh-CN",
		Translations: map[string]LocalizedCopy{
			"zh-CN": {Greeting: "欢迎洽谈合作"},
		},
	}
	link, err := create.Handle(ctx, f.agent, CreateBusinessLinkBody{
		Name:         "Owner 商务专线",
		Localization: &initLoc,
	})
	if err != nil {
		t.Fatalf("创建个人商务沟通链接失败：%v", err)
	}
	if !link.Enabled || link.PublicCode == "" || !strings.HasSuffix(link.ChatURL, "/c/"+link.PublicCode) || !strings.HasPrefix(link.QRDataURL, "data:image/png;base64,") {
		t.Fatalf("商务沟通链接返回异常：%+v", link)
	}
	// 短链重定向 /c/{code} -> /widget/?channel={id}，并透传合法的 lang 查询参数
	shortRouter := chi.NewRouter()
	shortRouter.Get("/c/{code}", ShortLinkHandler(env.DB))
	rec := httptest.NewRecorder()
	shortRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/c/"+link.PublicCode, nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/widget/?channel="+link.ID {
		t.Fatalf("短链重定向异常：%d %s", rec.Code, rec.Header().Get("Location"))
	}
	recLang := httptest.NewRecorder()
	shortRouter.ServeHTTP(recLang, httptest.NewRequest(http.MethodGet, "/c/"+link.PublicCode+"?lang=hi_latn", nil))
	if recLang.Code != http.StatusFound || recLang.Header().Get("Location") != "/widget/?channel="+link.ID+"&lang=hi-Latn" {
		t.Fatalf("短链透传语言异常：%d %s", recLang.Code, recLang.Header().Get("Location"))
	}
	// 重置短码后旧短码失效、新短码可用
	rotated, err := NewRotateBusinessLinkCode(env.DB, "https://desk.example.com").Handle(ctx, f.agent, link.ID)
	if err != nil || rotated.PublicCode == "" || rotated.PublicCode == link.PublicCode {
		t.Fatalf("重置短码失败：%v %+v", err, rotated)
	}
	// 自定义短码、名片、多语言文案、开场留资、快捷话题与口令校验
	cardCfg := BusinessCardConfig{ShowOwner: true}
	fullLoc := Localization{
		DefaultLanguage: "zh-CN",
		Translations: map[string]LocalizedCopy{
			"zh-CN": {
				Greeting:     "欢迎洽谈合作",
				OwnerTitle:   "大客户总监",
				OwnerCompany: "Acme Cloud",
				OwnerBio:     "亚太区渠道合作",
				AwayMessage:  "会议中，留言后尽快回复",
				Starters:     []string{"渠道代理合作", "定制报价咨询"},
			},
			"en": {
				Title:       "Owner Partnership Line",
				Greeting:    "Welcome to partner chat",
				OwnerTitle:  "Key Account Director",
				AwayMessage: "In a meeting, will reply soon",
				Starters:    []string{"Distribution partnership", "Custom pricing"},
			},
		},
	}
	intakeCfg := BusinessIntakeConfig{Enabled: true, Name: IntakeRequired, Company: IntakeRequired, Email: IntakeOptional, Note: IntakeOptional}
	configured, err := update.Handle(ctx, f.agent, link.ID, UpdateBusinessLinkBody{
		Name:         "Owner 商务专线",
		PublicCode:   "owner-bd",
		Localization: &fullLoc,
		Card:         &cardCfg,
		Intake:       &intakeCfg,
		Access:       &BusinessAccessInput{Mode: "passcode", Passcode: "888666"},
		Enabled:      true,
	})
	if err != nil || configured.PublicCode != "owner-bd" || !configured.Access.HasPasscode || len(configured.Localization.Translations["zh-CN"].Starters) != 2 {
		t.Fatalf("更新商务链接工作台配置失败：%v %+v", err, configured)
	}
	pubCfg, err := NewGetConfig(env.DB, env.Files).Handle(f.in(t, link.ID))
	if err != nil || pubCfg.Kind != channel.BusinessLink || pubCfg.Business == nil || pubCfg.Business.Owner == nil ||
		pubCfg.Business.Owner.Title != "大客户总监" || pubCfg.Business.Owner.Company != "Acme Cloud" || !pubCfg.Business.RequiresPasscode {
		t.Fatalf("公开名片配置异常：%v %+v", err, pubCfg)
	}
	// 英文请求：命中英文标题/头衔/话题，未填的 OwnerCompany 字段自动回退主语言 "Acme Cloud"
	enPubCfg, err := NewGetConfig(env.DB, env.Files).Handle(f.in(t, link.ID), "en")
	if err != nil || enPubCfg.Name != "Owner Partnership Line" || enPubCfg.Greeting != "Welcome to partner chat" ||
		enPubCfg.Business == nil || enPubCfg.Business.Owner == nil ||
		enPubCfg.Business.Owner.Title != "Key Account Director" || enPubCfg.Business.Owner.Company != "Acme Cloud" ||
		len(enPubCfg.Business.Starters) != 2 || enPubCfg.Business.Starters[0] != "Distribution partnership" {
		t.Fatalf("英文公开名片配置异常：%v %+v", err, enPubCfg)
	}
	// 恢复为公开模式供后续匿名访客测试，并先验证口令错误被拒绝
	startSess := NewStartSession(env.DB, env.Crypt, f.tokens, conversation.Downstream{})
	if _, err := startSess.Handle(f.in(t, link.ID), SessionBody{Passcode: "wrong"}, apitoken.Device{}); !errors.Is(err, ErrInvalidPasscode) {
		t.Fatalf("口令错误应返回 ErrInvalidPasscode：%v", err)
	}
	if _, err := update.Handle(ctx, f.agent, link.ID, UpdateBusinessLinkBody{
		Name:         "Owner 商务专线",
		Localization: &fullLoc,
		Card:         &cardCfg,
		Intake:       &intakeCfg,
		Access:       &BusinessAccessInput{Mode: "public", ClearPasscode: true},
		Enabled:      true,
	}); err != nil {
		t.Fatalf("切回公开模式失败：%v", err)
	}
	// 验证已过期链接拒绝新访客
	past := time.Now().UTC().Add(-time.Hour)
	if _, err := update.Handle(ctx, f.agent, link.ID, UpdateBusinessLinkBody{
		Name:    "Owner 商务专线",
		Access:  &BusinessAccessInput{Mode: "public", ExpiresAt: &past},
		Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := startSess.Handle(f.in(t, link.ID), SessionBody{}, apitoken.Device{}); !errors.Is(err, ErrBusinessLinkExpired) {
		t.Fatalf("过期链接应返回 ErrBusinessLinkExpired：%v", err)
	}
	if _, err := update.Handle(ctx, f.agent, link.ID, UpdateBusinessLinkBody{
		Name:    "Owner 商务专线",
		Access:  &BusinessAccessInput{Mode: "public", ExpiresAt: nil},
		Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	// 同工作区其他成员看不到、不能修改也不能删除别人的链接
	bobLinks, err := list.Handle(ctx, subBob)
	if err != nil || len(bobLinks) != 0 {
		t.Fatalf("Bob 不应看到 Owner 的个人商务链接：%v %+v", err, bobLinks)
	}
	if _, err := update.Handle(ctx, subBob, link.ID, UpdateBusinessLinkBody{Name: "篡改", Enabled: false}); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("Bob 不应修改 Owner 的个人商务链接：%v", err)
	}
	if err := del.Handle(ctx, subBob, link.ID); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("Bob 不应删除 Owner 的个人商务链接：%v", err)
	}

	// 2. 外部商户打开个人商务沟通链接：建立访客会话、发送消息、读取时间线、成员回复并标记已读
	hub := broadcast.New(broadcasttest.NewBackend())
	t.Cleanup(func() { _ = hub.Close() })
	reg := channel.DefaultRegistry()
	reg[channel.BusinessLink] = NewConnector(hub)
	down := conversation.Downstream{Hub: hub, Roles: env.Roles, ContactPush: Push(hub)}

	linkCtx := f.in(t, link.ID)
	sess, err := NewStartSession(env.DB, env.Crypt, f.tokens, down).Handle(linkCtx, SessionBody{
		Name:  "Elena Partner",
		Email: "elena@partner.io",
	}, apitoken.Device{})
	if err != nil {
		t.Fatalf("外部商户建立商务会话失败：%v", err)
	}
	if sess.Contact.Name != "Elena Partner" || sess.Contact.Email != "elena@partner.io" {
		t.Fatalf("外部商户资料未保存：%+v", sess.Contact)
	}
	subPartner := identity.Subject{Type: contact.Type, ID: sess.Contact.ID, Tenant: f.system.Tenant}
	// 开场留资必填项校验与提交
	intakeAct := NewSubmitIntake(env.DB, down)
	if _, err := intakeAct.Handle(linkCtx, subPartner, ContactIntakeBody{Name: "Elena Partner"}); !errors.Is(err, ErrIntakeRequired) {
		t.Fatalf("缺少必填公司字段应返回 ErrIntakeRequired：%v", err)
	}
	afterIntake, err := intakeAct.Handle(linkCtx, subPartner, ContactIntakeBody{
		Name: "Elena Partner", Company: "Partner Ventures", Email: "elena@partner.io", Note: "Looking for APAC reseller terms",
	})
	if err != nil || afterIntake.Company != "Partner Ventures" {
		t.Fatalf("提交开场留资失败：%v %+v", err, afterIntake)
	}
	if _, err := Resolve(env.DB)(linkCtx, subPartner); err != nil {
		t.Fatalf("商务链接联系人 Resolve 失败：%v", err)
	}
	if ancestors, err := FrameAncestors(ctx, env.DB, link.ID); err != nil || ancestors != "'none'" {
		t.Fatalf("商务沟通链接不允许第三方 iframe 嵌入，应返回 'none'：%v %q", err, ancestors)
	}

	receive := conversation.NewReceive(env.DB, reg, down)
	sent, err := NewSendMessage(env.DB, receive).Handle(linkCtx, subPartner, MessageBody{
		Text:     "Hi, we want to discuss B2B integration.",
		ClientID: "c-1",
	})
	if err != nil || sent.ConversationID == "" {
		t.Fatalf("外部商户发消息失败：%v %+v", err, sent)
	}

	// 成员回复外部商务聊天并走 Deliver 投递
	reply, err := conversation.NewSendChatMessage(env.DB, down).Handle(ctx, f.agent, sent.ConversationID, conversation.SendChatMessageBody{
		Text: "Welcome Elena! Happy to discuss.",
	})
	if err != nil {
		t.Fatalf("成员回复商务聊天失败：%v", err)
	}
	if err := conversation.NewDeliver(env.DB, reg, down).Job(ctx, conversation.Appended{
		TenantID: f.system.Tenant, ConversationID: sent.ConversationID, ItemID: reply.ID, Seq: reply.Seq,
	}); err != nil {
		t.Fatalf("投递商务链接消息失败：%v", err)
	}

	// 外部商户通过 ContactStream 读取完整双向时间线，并标记已读
	stream, err := conversation.NewContactStream(env.DB).Handle(ctx, subPartner, link.ID, "", "", 50)
	if err != nil || len(stream.Items) != 2 || stream.Unread != 1 {
		t.Fatalf("商务链接访客时间线异常：%v %+v", err, stream)
	}
	if err := conversation.NewMarkAllReadByContact(env.DB, down).Handle(ctx, subPartner, link.ID, ""); err != nil {
		t.Fatalf("商务链接访客标记已读失败：%v", err)
	}
	stream2, err := conversation.NewContactStream(env.DB).Handle(ctx, subPartner, link.ID, "", "", 50)
	if err != nil || stream2.Unread != 0 {
		t.Fatalf("标记已读后未读应清零：%v %+v", err, stream2)
	}

	// 已有访客会话在链接过期后同样不能通过 StartSession 绕过过期拦截
	existingSess, err := startSess.Handle(f.in(t, link.ID), SessionBody{VisitorID: sess.VisitorID}, apitoken.Device{})
	if err != nil || existingSess.VisitorID != sess.VisitorID {
		t.Fatalf("未过期前已有访客应可恢复会话：%v %+v", err, existingSess)
	}
	if _, err := update.Handle(ctx, f.agent, link.ID, UpdateBusinessLinkBody{
		Name:    "Owner 商务专线",
		Access:  &BusinessAccessInput{Mode: "public", ExpiresAt: &past},
		Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := startSess.Handle(f.in(t, link.ID), SessionBody{VisitorID: sess.VisitorID}, apitoken.Device{}); !errors.Is(err, ErrBusinessLinkExpired) {
		t.Fatalf("链接过期后已有访客调用 StartSession 应返回 ErrBusinessLinkExpired：%v", err)
	}

	// 跨渠道/非联系人防护：普通 widget 不能提交商务留资，非联系人主体不能提交留资或修改访客语言
	if _, err := intakeAct.Handle(f.in(t, f.widget.ID), subPartner, ContactIntakeBody{Name: "Hacked"}); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("普通网页插件提交商务留资应返回 channel.ErrNotFound：%v", err)
	}
	if _, err := intakeAct.Handle(linkCtx, f.agent, ContactIntakeBody{Name: "Hacked"}); !errors.Is(err, conversation.ErrForbidden) {
		t.Fatalf("非联系人主体提交商务留资应返回 ErrForbidden：%v", err)
	}
	if _, err := NewUpdateLanguage(env.DB).Handle(ctx, f.agent, ContactLanguageBody{Language: "en"}); !errors.Is(err, conversation.ErrForbidden) {
		t.Fatalf("非联系人主体更新访客语言应返回 ErrForbidden：%v", err)
	}

	// 3. 停用后外部不可再访问；已有沟通记录的链接拒绝删除（请停用），未使用的链接可删除
	updated, err := update.Handle(ctx, f.agent, link.ID, UpdateBusinessLinkBody{
		Name:       "Owner 商务专线（暂停）",
		Appearance: link.Appearance,
		Enabled:    false,
	})
	if err != nil || updated.Enabled {
		t.Fatalf("停用个人商务链接失败：%v %+v", err, updated)
	}
	if _, err := findPublic(ctx, env.DB, link.ID); !errors.Is(err, channel.ErrNotFound) {
		t.Fatalf("停用后的商务链接不应公开可访问：%v", err)
	}
	if err := del.Handle(ctx, f.agent, link.ID); !errors.Is(err, ErrBusinessLinkInUse) {
		t.Fatalf("已有记录的商务链接应返回 ErrBusinessLinkInUse：%v", err)
	}
	unused, err := create.Handle(ctx, f.agent, CreateBusinessLinkBody{Name: "临时链接"})
	if err != nil {
		t.Fatal(err)
	}
	if err := del.Handle(ctx, f.agent, unused.ID); err != nil {
		t.Fatalf("删除未使用的个人商务链接失败：%v", err)
	}
	remaining, err := list.Handle(ctx, f.agent)
	if err != nil || len(remaining) != 1 || remaining[0].ID != link.ID {
		t.Fatalf("删除未使用链接后列表异常：%v %+v", err, remaining)
	}
}

func TestEncodeQRDataURLFullScale(t *testing.T) {
	t.Parallel()
	dataURL := encodeQRDataURL("http://localhost:18088/c/owner-bd")
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(dataURL, prefix) {
		t.Fatalf("二维码应返回 PNG data URL：%q", dataURL)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, prefix))
	if err != nil {
		t.Fatalf("base64 解码失败：%v", err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("PNG 解码失败：%v", err)
	}
	b := img.Bounds()
	if b.Dx() < 200 || b.Dy() < 200 || b.Dx() != b.Dy() {
		t.Fatalf("二维码应为高分辨率正方形 PNG，实际 %dx%d", b.Dx(), b.Dy())
	}
	// 验证右下象限存在黑色像素（覆盖整个画布，排除 rsc.io/qr Code.Image 仅在左上角绘制 1x 像素的缺陷）
	darkInBottomRight := 0
	for y := b.Dy() / 2; y < b.Dy(); y++ {
		for x := b.Dx() / 2; x < b.Dx(); x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r < 0x8000 && g < 0x8000 && bl < 0x8000 {
				darkInBottomRight++
			}
		}
	}
	if darkInBottomRight == 0 {
		t.Fatalf("二维码右下象限未包含黑色模块，尺寸=%dx%d", b.Dx(), b.Dy())
	}
}

func TestPasscodeArgon2idLegacyCompatAndRateLimit(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env := apptest.New(t)
	f := setup(t, env, "pass-rl")

	// 1. Argon2id 哈希与旧版 SHA-256 哈希兼容验证
	hashed, err := hashPasscode("secret-888")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hashed, "$argon2id$") {
		t.Fatalf("口令哈希应使用 Argon2id，实际 %q", hashed)
	}
	if !verifyPasscode("ch-any", "secret-888", hashed) || verifyPasscode("ch-any", "wrong", hashed) {
		t.Fatal("Argon2id verifyPasscode 结果不符合预期")
	}
	legacySum := sha256.Sum256([]byte("ch-legacy:old-pass"))
	legacyHash := hex.EncodeToString(legacySum[:])
	if !verifyPasscode("ch-legacy", "old-pass", legacyHash) || verifyPasscode("ch-legacy", "bad", legacyHash) {
		t.Fatal("历史 SHA-256 兼容 verifyPasscode 结果不符合预期")
	}

	// 2. StartSession 对同一渠道 + IP 进行口令暴力试错限流，正确口令通过后重置计数
	link, err := NewCreateBusinessLink(env.DB, "https://desk.example.com").Handle(ctx, f.agent, CreateBusinessLinkBody{
		Name: "限流验证专线",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewUpdateBusinessLink(env.DB, "https://desk.example.com").Handle(ctx, f.agent, link.ID, UpdateBusinessLinkBody{
		Name:    "限流验证专线",
		Access:  &BusinessAccessInput{Mode: "passcode", Passcode: "888666"},
		Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	limiter := pglimit.New(env.DB, "test:widget:passcode", ratelimit.Config{Rate: 2, Interval: time.Minute, Burst: 2})
	start := NewStartSession(env.DB, env.Crypt, f.tokens, conversation.Downstream{}, limiter)
	linkCtx := f.in(t, link.ID)
	ipA := apitoken.Device{IP: "198.51.100.10"}
	for i := range 2 {
		if _, err := start.Handle(linkCtx, SessionBody{Passcode: "wrong"}, ipA); !errors.Is(err, ErrInvalidPasscode) {
			t.Fatalf("第 %d 次错误口令应返回 ErrInvalidPasscode，实际 %v", i+1, err)
		}
	}
	if _, err := start.Handle(linkCtx, SessionBody{Passcode: "wrong"}, ipA); !errors.Is(err, ratelimit.ErrExceeded) {
		t.Fatalf("第 3 次试错应触发 ratelimit.ErrExceeded，实际 %v", err)
	}

	// 另一 IP 先输错 1 次、随后输入正确口令可正常进入并重置限流计数
	ipB := apitoken.Device{IP: "198.51.100.20"}
	if _, err := start.Handle(linkCtx, SessionBody{Passcode: "wrong"}, ipB); !errors.Is(err, ErrInvalidPasscode) {
		t.Fatalf("IP B 首次错误口令应返回 ErrInvalidPasscode，实际 %v", err)
	}
	if _, err := start.Handle(linkCtx, SessionBody{Passcode: "888666"}, ipB); err != nil {
		t.Fatalf("IP B 正确口令应通过，实际 %v", err)
	}
	if _, err := start.Handle(linkCtx, SessionBody{Passcode: "wrong"}, ipB); !errors.Is(err, ErrInvalidPasscode) {
		t.Fatalf("IP B 重置计数后再次输错应允许尝试而不是直接超限，实际 %v", err)
	}
}

func TestWidgetVisitorUploadsFeatureFlag(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env := apptest.New(t)
	f := setup(t, env, "flag-upload")
	store := flags.NewStore(env.DB)

	r := chi.NewRouter()
	down := conversation.Downstream{Media: conversation.Media{Files: env.Files}}
	r.Route("/w/{channel}/api", func(cr chi.Router) {
		cr.Use(Middleware(env.DB))
		api := rest.New(cr, rest.Options{
			Validate: f.tokens.Validate,
			Accounts: identity.Accounts{contact.Type: Resolve(env.DB)},
			Tenant:   TenantFromRequest,
		})
		RegisterVisitorRoutes(api, api.RequireToken(), env.DB, env.Crypt, f.tokens, conversation.NewReceive(env.DB, channel.DefaultRegistry(), down), down, 50*filesystem.MiB, VisitorOptions{
			Features: store,
		})
	})

	sess, _ := f.session(t, SessionBody{})
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	uploadFile := func() *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		part, err := mw.CreateFormFile("file", "dot.png")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(pngBuf.Bytes())
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/w/"+f.widget.ID+"/api/files", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+sess.Token)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// 关闭 widget.visitor_uploads 后，config 返回 visitor_uploads=false 且上传返回 403
	offIn := flags.SetInput{Name: flags.WidgetVisitorUploads.Name}
	offIn.Body.Scope = flags.ScopeTenant
	offIn.Body.TenantID = f.system.Tenant
	offIn.Body.Mode = flags.ModeOff
	if _, err := flags.NewSet(env.DB, store).Handle(ctx, apptest.Admin, offIn); err != nil {
		t.Fatal(err)
	}

	cfgRec := httptest.NewRecorder()
	r.ServeHTTP(cfgRec, httptest.NewRequest(http.MethodGet, "/w/"+f.widget.ID+"/api/config", nil))
	if cfgRec.Code != http.StatusOK {
		t.Fatalf("读取公开配置失败：%d %s", cfgRec.Code, cfgRec.Body.String())
	}
	var cfg ConfigDTO
	if err := json.Unmarshal(cfgRec.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.VisitorUploads {
		t.Fatalf("关闭访客上传开关后 visitor_uploads 应为 false")
	}
	if rec := uploadFile(); rec.Code != http.StatusForbidden {
		t.Fatalf("关闭访客上传开关后 POST /files 应返回 403，实际 %d %s", rec.Code, rec.Body.String())
	}

	// 重新开启后 config 返回 visitor_uploads=true 且上传成功
	onIn := flags.SetInput{Name: flags.WidgetVisitorUploads.Name}
	onIn.Body.Scope = flags.ScopeTenant
	onIn.Body.TenantID = f.system.Tenant
	onIn.Body.Mode = flags.ModeOn
	if _, err := flags.NewSet(env.DB, store).Handle(ctx, apptest.Admin, onIn); err != nil {
		t.Fatal(err)
	}
	cfgRec2 := httptest.NewRecorder()
	r.ServeHTTP(cfgRec2, httptest.NewRequest(http.MethodGet, "/w/"+f.widget.ID+"/api/config", nil))
	if err := json.Unmarshal(cfgRec2.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.VisitorUploads {
		t.Fatalf("开启访客上传开关后 visitor_uploads 应为 true")
	}
	if rec := uploadFile(); rec.Code != http.StatusCreated {
		t.Fatalf("开启访客上传开关后 POST /files 应成功，实际 %d %s", rec.Code, rec.Body.String())
	}
}
