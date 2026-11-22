package widget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/queue/queuetest"

	"agents-desk/internal/apptest"
	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/member"
	"agents-desk/internal/push"
	"agents-desk/internal/tenant"
)

// fixture 是一个测试的工作区、其中的聊天插件和调用者。
type fixture struct {
	env    *apptest.Env
	system identity.Subject // 工作区中的系统主体
	agent  identity.Subject // 工作区中可以看到全部会话的坐席
	widget *WidgetDTO
	tokens *apitoken.Store
}

// setup 创建工作区 slug 和其中的聊天插件。
func setup(t *testing.T, env *apptest.Env, slug string) fixture {
	t.Helper()
	ctx := context.Background()
	tn, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: slug, Slug: slug, OwnerEmail: "owner@" + slug + ".com", OwnerName: "owner", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tn.ID}
	w, err := NewCreate(env.DB, env.Crypt).Handle(ctx, system, Changes{Name: "官网"})
	if err != nil {
		t.Fatal(err)
	}
	agent := identity.Subject{Type: member.Type, ID: tn.OwnerID, Tenant: tn.ID, Permissions: auth.WithBase(auth.RolePermissions(auth.ScopeAll, nil))}
	return fixture{env: env, system: system, agent: agent, widget: w, tokens: apitoken.NewStore(env.DB, apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour})}
}

// in 返回本次请求是聊天插件 id 的 context，与 Middleware 放入的相同；聊天插件停用或不存在时其中是 nil。
func (f fixture) in(t *testing.T, id string) context.Context {
	t.Helper()
	ch, err := findPublic(context.Background(), f.env.DB, id)
	if err != nil && !errors.Is(err, channel.ErrNotFound) {
		t.Fatal(err)
	}
	return context.WithValue(context.Background(), requestKey{}, ch)
}

// session 在聊天插件中建立会话，返回会话和令牌校验、Resolve 之后的联系人主体。
func (f fixture) session(t *testing.T, b SessionBody) (*SessionDTO, identity.Subject) {
	t.Helper()
	ctx := f.in(t, f.widget.ID)
	s, err := NewStartSession(f.env.DB, f.env.Crypt, f.tokens).Handle(ctx, b, apitoken.Device{})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := f.tokens.Validate(ctx, s.Token)
	if err != nil {
		t.Fatal(err)
	}
	if sub, err = Resolve(f.env.DB)(ctx, sub); err != nil {
		t.Fatal(err)
	}
	return s, sub
}

// sign 返回用 secret 签名的用户身份。
func sign(t *testing.T, secret string, claims jwt.Claims, method jwt.SigningMethod) string {
	t.Helper()
	s, err := jwt.NewWithClaims(method, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestSession 检查建立会话：访客编号识别同一位线索，产品后端签名的身份识别用户并更新他的资料，签名不对、过期、
// 没有有效期、换了密钥的身份被拒绝；令牌绑定聊天插件所在的工作区，在别的聊天插件中无效；停用的聊天插件不能建立会话。
func TestSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "alpha")

	// 线索：第一次得到新的访客编号，带上它是同一位线索，编号无效时是新的线索
	first, lead := f.session(t, SessionBody{})
	if first.VisitorID == "" || first.Contact.Kind != contact.Lead || lead.Type != contact.Type || lead.ID != first.Contact.ID || lead.Tenant != f.system.Tenant {
		t.Fatalf("第一次：%+v，主体：%+v", first, lead)
	}
	again, _ := f.session(t, SessionBody{VisitorID: first.VisitorID})
	other, _ := f.session(t, SessionBody{VisitorID: "unknown"})
	if again.Contact.ID != lead.ID || again.VisitorID != first.VisitorID || other.Contact.ID == lead.ID || other.VisitorID == "unknown" {
		t.Fatalf("同一个访客编号：%+v，无效的编号：%+v", again, other)
	}

	// 用户：按签名的身份新建，再来时是同一个用户，名字按最新的身份更新
	inst, err := NewGetInstallation(env.DB, env.Crypt, "https://desk.example.com").Handle(ctx, f.system, f.widget.ID)
	if err != nil {
		t.Fatal(err)
	}
	exp := jwt.NewNumericDate(time.Now().Add(time.Hour))
	token := sign(t, inst.IdentitySecret, userClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: "u-42", ExpiresAt: exp}, Name: "Rahul", Email: "rahul@example.com"}, jwt.SigningMethodHS256)
	u1, user := f.session(t, SessionBody{VisitorID: first.VisitorID, UserToken: token})
	if u1.Contact.Kind != contact.User || u1.Contact.Name != "Rahul" || u1.Contact.Email != "rahul@example.com" || u1.VisitorID != first.VisitorID || user.ID == lead.ID {
		t.Fatalf("用户：%+v", u1)
	}
	renamed := sign(t, inst.IdentitySecret, userClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: "u-42", ExpiresAt: exp}, Name: "Rahul Sharma"}, jwt.SigningMethodHS256)
	if u2, _ := f.session(t, SessionBody{UserToken: renamed}); u2.Contact.ID != user.ID || u2.Contact.Name != "Rahul Sharma" || u2.Contact.Email != "rahul@example.com" {
		t.Fatalf("改名后：%+v", u2)
	}

	// 无效的用户身份
	start := NewStartSession(env.DB, env.Crypt, f.tokens)
	past := jwt.NewNumericDate(time.Now().Add(-time.Minute))
	for name, bad := range map[string]string{
		"签名的密钥不对":  sign(t, "wrong-secret", userClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: "u-42", ExpiresAt: exp}}, jwt.SigningMethodHS256),
		"已过期":      sign(t, inst.IdentitySecret, userClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: "u-42", ExpiresAt: past}}, jwt.SigningMethodHS256),
		"没有有效期":    sign(t, inst.IdentitySecret, userClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: "u-42"}}, jwt.SigningMethodHS256),
		"没有用户编号":   sign(t, inst.IdentitySecret, userClaims{RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: exp}}, jwt.SigningMethodHS256),
		"不是 HS256": sign(t, inst.IdentitySecret, userClaims{RegisteredClaims: jwt.RegisteredClaims{Subject: "u-42", ExpiresAt: exp}}, jwt.SigningMethodHS512),
		"不是 JWT":   "not-a-jwt",
	} {
		if _, err := start.Handle(f.in(t, f.widget.ID), SessionBody{UserToken: bad}, apitoken.Device{}); !errors.Is(err, ErrInvalidUserToken) {
			t.Errorf("%s：%v", name, err)
		}
	}
	// 更换密钥后旧密钥签名的身份无效
	if _, err := NewRotateSecret(env.DB, env.Crypt, "").Handle(ctx, f.system, f.widget.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := start.Handle(f.in(t, f.widget.ID), SessionBody{UserToken: token}, apitoken.Device{}); !errors.Is(err, ErrInvalidUserToken) {
		t.Errorf("更换密钥后：%v", err)
	}

	// 令牌在别的聊天插件中无效：联系人在那里没有身份
	second, err := NewCreate(env.DB, env.Crypt).Handle(ctx, f.system, Changes{Name: "App"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(env.DB)(f.in(t, second.ID), lead); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Errorf("别的聊天插件：%v", err)
	}
	// 停用的聊天插件查不到，不能建立会话
	if _, err := NewUpdate(env.DB).Handle(ctx, f.system, second.ID, UpdateBody{Changes: Changes{Name: "App"}, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := start.Handle(f.in(t, second.ID), SessionBody{}, apitoken.Device{}); !errors.Is(err, channel.ErrNotFound) {
		t.Errorf("停用的聊天插件：%v", err)
	}
}

// TestConversation 检查客户在聊天窗口中的往来：发起会话进入聊天插件的团队收件箱，重发同一条消息只记录一次；
// 坐席的回复由连接器推送到客户的频道并记为已送达；客户只看到对所有人可见的项，读到后回复记为已读、推送给成员；
// 别的客户和别的聊天插件看不到这个会话。
func TestConversation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "alpha")
	hub := broadcast.New(broadcasttest.NewBackend(), nil)
	t.Cleanup(func() { _ = hub.Close() })
	registry := channel.Registry{channel.Widget: NewConnector(hub)}
	down := conversation.Downstream{Topics: []queue.Topic[conversation.Appended]{conversation.DeliverTopic}, Hub: hub, Roles: env.Roles}
	send := NewSendMessage(env.DB, conversation.NewReceive(env.DB, registry, down))

	_, visitor := f.session(t, SessionBody{})
	_, stranger := f.session(t, SessionBody{})
	in := f.in(t, f.widget.ID)

	// 发起会话；同一个 client_id 重发时是同一条
	sent, err := send.Handle(in, visitor, "", MessageBody{Text: "mera order nahi aaya", ClientID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	resent, err := send.Handle(in, visitor, sent.ConversationID, MessageBody{Text: "mera order nahi aaya", ClientID: "c1"})
	if err != nil || resent.Item.ID != sent.Item.ID {
		t.Fatalf("重发：%+v %v", resent, err)
	}
	// 别人用同一个 client_id 是他自己的消息；他不能往别人的会话里发
	if s, err := send.Handle(in, stranger, "", MessageBody{Text: "hello", ClientID: "c1"}); err != nil || s.ConversationID == sent.ConversationID {
		t.Fatalf("别人的同一个 client_id：%+v %v", s, err)
	}
	if _, err := send.Handle(in, stranger, sent.ConversationID, MessageBody{Text: "hi"}); !errors.Is(err, conversation.ErrNotFound) {
		t.Fatalf("往别人的会话里发：%v", err)
	}

	// 坐席写备注、回复；连接器把回复推送到客户的频道
	if _, err := conversation.NewAddNote(env.DB, down).Handle(ctx, f.agent, sent.ConversationID, "查一下物流", nil); err != nil {
		t.Fatal(err)
	}
	pubs := hub.Subscribe(Channel(f.widget.ID, visitor.ID))
	defer pubs.Close()
	reply, err := conversation.NewReply(env.DB, down).Handle(ctx, f.agent, sent.ConversationID, conversation.ReplyBody{Text: "您好，我帮您查一下"})
	if err != nil {
		t.Fatal(err)
	}
	deliver := conversation.NewDeliver(env.DB, registry)
	for _, job := range queuetest.Pending(t, env.DB, conversation.DeliverTopic) {
		if err := deliver.Job(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case msg := <-pubs.C():
		var ev push.Event
		if err := json.Unmarshal(msg.Data, &ev); err != nil || ev.ID != sent.ConversationID || ev.Seq != reply.Seq {
			t.Fatalf("推送：%+v %v", ev, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("没有收到推送")
	}

	// 客户看到的会话和时间线：自己的消息和坐席的回复，没有备注和指派的系统事件
	list, err := conversation.NewListForContact(env.DB).Handle(ctx, visitor, f.widget.ID)
	if err != nil || len(list) != 1 || list[0].ID != sent.ConversationID || list[0].Unread != 1 || list[0].Preview != "您好，我帮您查一下" ||
		list[0].PreviewAuthor.Type != conversation.MemberActor || list[0].PreviewAuthor.Name != "owner" {
		t.Fatalf("客户的会话列表：%+v %v", list, err)
	}
	items, err := conversation.NewTimelineForContact(env.DB).Handle(ctx, visitor, f.widget.ID, sent.ConversationID, 0, 100)
	if err != nil || len(items) != 2 || items[0].Author.Type != conversation.ContactActor ||
		items[1].ID != reply.ID || items[1].Author.Type != conversation.MemberActor || items[1].Author.Name != "owner" {
		t.Fatalf("客户的时间线：%+v %v", items, err)
	}

	// 客户读到回复：记为已读，成员收到推送
	memberPubs := hub.Subscribe(push.Channel(f.system.Tenant, f.agent.ID))
	defer memberPubs.Close()
	if err := conversation.NewMarkReadByContact(env.DB, down).Handle(ctx, visitor, f.widget.ID, sent.ConversationID, reply.Seq); err != nil {
		t.Fatal(err)
	}
	select {
	case <-memberPubs.C():
	case <-time.After(5 * time.Second):
		t.Fatal("成员没有收到已读的推送")
	}
	timeline, err := conversation.NewTimeline(env.DB).Handle(ctx, f.agent, sent.ConversationID, reply.Seq-1, 1)
	if err != nil || len(timeline) != 1 || timeline[0].Delivery == nil || *timeline[0].Delivery != conversation.Read {
		t.Fatalf("坐席看到的送达状态：%+v %v", timeline, err)
	}
	if got, err := conversation.NewGetForContact(env.DB).Handle(ctx, visitor, f.widget.ID, sent.ConversationID); err != nil || got.Unread != 0 {
		t.Fatalf("读过之后：%+v %v", got, err)
	}

	// 别的客户、别的聊天插件、成员都不能以客户的身份读取
	second, err := NewCreate(env.DB, env.Crypt).Handle(ctx, f.system, Changes{Name: "App"})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		sub     identity.Subject
		channel string
		want    error
	}{
		"别的客户":   {stranger, f.widget.ID, conversation.ErrNotFound},
		"别的聊天插件": {visitor, second.ID, conversation.ErrNotFound},
		"成员":     {f.agent, f.widget.ID, conversation.ErrForbidden},
	} {
		if _, err := conversation.NewTimelineForContact(env.DB).Handle(ctx, c.sub, c.channel, sent.ConversationID, 0, 100); !errors.Is(err, c.want) {
			t.Errorf("%s读取时间线：%v", name, err)
		}
		if err := conversation.NewMarkReadByContact(env.DB, down).Handle(ctx, c.sub, c.channel, sent.ConversationID, 100); !errors.Is(err, c.want) {
			t.Errorf("%s标记已读：%v", name, err)
		}
	}
}

// TestManage 检查聊天插件的管理：默认的主题色，团队收件箱必须属于这个工作区，别的工作区读不到，修改不动身份验证密钥。
func TestManage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	alpha, beta := setup(t, env, "alpha"), setup(t, env, "beta")

	if alpha.widget.Color != defaultColor || !alpha.widget.Enabled {
		t.Fatalf("新建的聊天插件：%+v", alpha.widget)
	}
	var betaTeam string
	if err := env.DB.NewRaw("INSERT INTO teams (id, created_at, updated_at, tenant_id, name) VALUES (?, now(), now(), ?, 'beta') RETURNING id",
		newID(), beta.system.Tenant).Scan(ctx, &betaTeam); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCreate(env.DB, env.Crypt).Handle(ctx, alpha.system, Changes{Name: "x", TeamID: &betaTeam}); !errors.Is(err, ErrTeamNotFound) {
		t.Errorf("别的工作区的团队收件箱：%v", err)
	}
	if _, err := NewGet(env.DB).Handle(ctx, beta.system, alpha.widget.ID); !errors.Is(err, channel.ErrNotFound) {
		t.Errorf("别的工作区读取：%v", err)
	}
	if _, err := NewUpdate(env.DB).Handle(ctx, beta.system, alpha.widget.ID, UpdateBody{Changes: Changes{Name: "x"}, Enabled: true}); !errors.Is(err, channel.ErrNotFound) {
		t.Errorf("别的工作区修改：%v", err)
	}
	if _, err := NewGetInstallation(env.DB, env.Crypt, "").Handle(ctx, beta.system, alpha.widget.ID); !errors.Is(err, channel.ErrNotFound) {
		t.Errorf("别的工作区读取嵌入代码：%v", err)
	}

	before, err := NewGetInstallation(env.DB, env.Crypt, "https://desk.example.com/").Handle(ctx, alpha.system, alpha.widget.ID)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewUpdate(env.DB).Handle(ctx, alpha.system, alpha.widget.ID, UpdateBody{Changes: Changes{Name: "官网", Greeting: "你好", Color: "green"}, Enabled: true})
	if err != nil || w.Greeting != "你好" || w.Color != "green" {
		t.Fatalf("修改后：%+v %v", w, err)
	}
	after, err := NewGetInstallation(env.DB, env.Crypt, "https://desk.example.com/").Handle(ctx, alpha.system, alpha.widget.ID)
	if err != nil || after.IdentitySecret != before.IdentitySecret || len(after.IdentitySecret) != 43 {
		t.Fatalf("修改后的密钥：%q %q %v", before.IdentitySecret, after.IdentitySecret, err)
	}
	cfg, err := NewGetConfig().Handle(alpha.in(t, alpha.widget.ID))
	if err != nil || cfg.Greeting != "你好" || cfg.Color != "green" || cfg.Name != "官网" {
		t.Fatalf("公开设置：%+v %v", cfg, err)
	}
}

// TestRating 检查客户评价：打开的会话不能评价；坐席关闭会话时推送给客户，客户看到已关闭、还没有评价；评价后
// 成员看到分数，时间线中有只有成员可见的系统事件，同一次关闭不能再评价；客户再发消息重新打开、坐席再关闭后可以再评价。
// 稍后处理的会话对客户显示为打开。
func TestRating(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "alpha")
	hub := broadcast.New(broadcasttest.NewBackend(), nil)
	t.Cleanup(func() { _ = hub.Close() })
	registry := channel.Registry{channel.Widget: NewConnector(hub)}
	down := conversation.Downstream{Hub: hub, Roles: env.Roles, ContactPush: Push(hub)}
	send := NewSendMessage(env.DB, conversation.NewReceive(env.DB, registry, down))
	rate := conversation.NewRateByContact(env.DB, down)
	get := conversation.NewGetForContact(env.DB)

	_, visitor := f.session(t, SessionBody{})
	in := f.in(t, f.widget.ID)
	pubs := hub.Subscribe(Channel(f.widget.ID, visitor.ID))
	defer pubs.Close()
	broadcasttest.Ready(t, hub)
	expectPush := func(step string) {
		t.Helper()
		var ev push.Event
		if err := json.Unmarshal(broadcasttest.Receive(t, pubs).Data, &ev); err != nil || ev.Type != push.Conversation {
			t.Fatalf("%s：推送 %+v %v", step, ev, err)
		}
	}

	sent, err := send.Handle(in, visitor, "", MessageBody{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	expectPush("客户自己发的消息")
	if _, err := rate.Handle(ctx, visitor, f.widget.ID, sent.ConversationID, 5, ""); !errors.Is(err, conversation.ErrNotClosed) {
		t.Fatalf("评价打开的会话：%v", err)
	}

	// 稍后处理对客户显示为打开，状态变了也推送
	if _, err := conversation.NewSnooze(env.DB, down).Handle(ctx, f.agent, sent.ConversationID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	expectPush("稍后处理")
	if c, err := get.Handle(ctx, visitor, f.widget.ID, sent.ConversationID); err != nil || c.Status != conversation.Open {
		t.Fatalf("稍后处理的会话：%+v %v", c, err)
	}

	// 关闭：推送给客户，客户看到已关闭、可以评价
	if _, err := conversation.NewClose(env.DB, down).Handle(ctx, f.agent, sent.ConversationID); err != nil {
		t.Fatal(err)
	}
	expectPush("关闭")
	if c, err := get.Handle(ctx, visitor, f.widget.ID, sent.ConversationID); err != nil || c.Status != conversation.Closed || c.Rating != nil {
		t.Fatalf("关闭后：%+v %v", c, err)
	}
	rated, err := rate.Handle(ctx, visitor, f.widget.ID, sent.ConversationID, 4, "挺快的")
	if err != nil || rated.Rating == nil || rated.Rating.Score != 4 || rated.Rating.Comment != "挺快的" {
		t.Fatalf("评价：%+v %v", rated, err)
	}
	if _, err := rate.Handle(ctx, visitor, f.widget.ID, sent.ConversationID, 5, ""); !errors.Is(err, conversation.ErrAlreadyRated) {
		t.Fatalf("再评价一次：%v", err)
	}
	member, err := conversation.NewGet(env.DB).Handle(ctx, f.agent, sent.ConversationID)
	if err != nil || member.Rating == nil || member.Rating.Score != 4 {
		t.Fatalf("成员看到的评价：%+v %v", member, err)
	}
	items, err := conversation.NewTimeline(env.DB).Handle(ctx, f.agent, sent.ConversationID, 0, 100)
	if err != nil || items[len(items)-1].Content.Event != "conversation.rated" || items[len(items)-1].Visibility != conversation.MembersOnly {
		t.Fatalf("时间线：%+v %v", items, err)
	}
	if theirs, err := conversation.NewTimelineForContact(env.DB).Handle(ctx, visitor, f.widget.ID, sent.ConversationID, 0, 100); err != nil || len(theirs) != 1 {
		t.Fatalf("客户看到的时间线：%+v %v", theirs, err)
	}

	// 客户再发消息重新打开，坐席再关闭后可以再评价
	if _, err := send.Handle(in, visitor, sent.ConversationID, MessageBody{Text: "one more thing"}); err != nil {
		t.Fatal(err)
	}
	expectPush("重新打开")
	if c, err := get.Handle(ctx, visitor, f.widget.ID, sent.ConversationID); err != nil || c.Status != conversation.Open || c.Rating != nil {
		t.Fatalf("重新打开后：%+v %v", c, err)
	}
	if _, err := conversation.NewClose(env.DB, down).Handle(ctx, f.agent, sent.ConversationID); err != nil {
		t.Fatal(err)
	}
	expectPush("再关闭")
	if again, err := rate.Handle(ctx, visitor, f.widget.ID, sent.ConversationID, 5, ""); err != nil || again.Rating.Score != 5 {
		t.Fatalf("再关闭后评价：%+v %v", again, err)
	}
}

// TestFrameAncestors 检查允许嵌入的网站：整理成小写的 scheme://host，去掉重复的，写得不对时报错；聊天窗口页面的
// frame-ancestors 按它生成，没有设置时是 *，聊天插件不存在或停用时是 'none'。
func TestFrameAncestors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)
	f := setup(t, env, "alpha")

	if got, err := FrameAncestors(ctx, env.DB, f.widget.ID); err != nil || got != "*" {
		t.Fatalf("没有设置时：%q %v", got, err)
	}
	for _, bad := range []string{"shop.example.com", "ftp://shop.example.com", "https://shop.example.com/path", "https://shop.example.com?a=1", "https://"} {
		_, err := NewUpdate(env.DB).Handle(ctx, f.system, f.widget.ID, UpdateBody{Changes: Changes{Name: "官网", AllowedOrigins: []string{bad}}, Enabled: true})
		if !errors.Is(err, ErrInvalidOrigin) {
			t.Errorf("%q：%v", bad, err)
		}
	}
	w, err := NewUpdate(env.DB).Handle(ctx, f.system, f.widget.ID, UpdateBody{
		Changes: Changes{Name: "官网", AllowedOrigins: []string{"https://Shop.Example.com/", " https://shop.example.com", "http://localhost:3000"}}, Enabled: true,
	})
	if err != nil || len(w.AllowedOrigins) != 2 || w.AllowedOrigins[0] != "https://shop.example.com" || w.AllowedOrigins[1] != "http://localhost:3000" {
		t.Fatalf("整理后：%+v %v", w, err)
	}
	if got, err := FrameAncestors(ctx, env.DB, f.widget.ID); err != nil || got != "https://shop.example.com http://localhost:3000" {
		t.Fatalf("设置后：%q %v", got, err)
	}
	for name, id := range map[string]string{"不是 UUID": "x", "不存在": newID()} {
		if got, err := FrameAncestors(ctx, env.DB, id); err != nil || got != "'none'" {
			t.Errorf("%s：%q %v", name, got, err)
		}
	}
	if _, err := NewUpdate(env.DB).Handle(ctx, f.system, f.widget.ID, UpdateBody{Changes: Changes{Name: "官网"}, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if got, err := FrameAncestors(ctx, env.DB, f.widget.ID); err != nil || got != "'none'" {
		t.Fatalf("停用后：%q %v", got, err)
	}
}

// TestUserTokenCommand 检查 dev user-token 签名的用户身份能建立用户的会话。
func TestUserTokenCommand(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	f := setup(t, env, "alpha")
	cmd := UserTokenCommand(env.DB, env.Crypt)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--tenant", "alpha", "--sub", "u-7", "--name", "Priya"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, _ := f.session(t, SessionBody{UserToken: strings.TrimSpace(out.String())})
	if s.Contact.Kind != contact.User || s.Contact.Name != "Priya" {
		t.Fatalf("用户的会话：%+v", s)
	}
}
