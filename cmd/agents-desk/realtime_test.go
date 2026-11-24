package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/broadcast/broadcasttest"
	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/realtime/realtimetest"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/push"
	"agents-desk/internal/tenant"
)

// TestRealtime 检查实时推送：会话的变化只推送给修改前后能看到它的成员（可见范围、团队收件箱、负责人、被 @ 的人），
// 标记已读只推送给自己，别的工作区的成员收不到；成员经 /t/{slug}/realtime 只能订阅自己的个人频道，并从中收到推送，
// 不是成员的工作区连不上。
func TestRealtime(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	hub := broadcast.New(broadcasttest.NewBackend(), nil)
	t.Cleanup(func() { _ = hub.Close() })
	a := &App{
		cfg: config.Default[Config](), db: env.DB, hub: hub, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		llmSettings: llm.NewGroup(env.DB, env.Crypt), files: env.Files, localDisk: env.Disk,
		channels: channel.DefaultRegistry(), presence: realtimetest.NewPresence(),
	}
	rt, err := a.newRealtime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	h := a.routes(nil)
	h.With(tenant.Middleware(env.DB)).Handle("/t/{slug}/realtime", rt.Handler())
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	// 两个工作区；alpha 中有两位坐席（各在一个团队收件箱中）和一位只看指派给自己的会话的专员
	tenants, owners := map[string]string{}, map[string]string{}
	for _, slug := range []string{"alpha", "beta"} {
		tn, err := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
			Name: slug, Slug: slug, OwnerEmail: "owner@" + slug + ".com", OwnerName: slug, OwnerPassword: "password",
		})
		if err != nil {
			t.Fatal(err)
		}
		tenants[slug], owners[slug] = tn.ID, tn.OwnerID
	}
	login := func(email string) *http.Cookie {
		t.Helper()
		r := call(h, "POST", "/api/auth/login", `{"email":"`+email+`","password":"password"}`, nil)
		if r.status != http.StatusNoContent {
			t.Fatalf("登录 %s：%d %s", email, r.status, r.body)
		}
		return r.cookie
	}
	idOf := func(r response) string {
		t.Helper()
		var v struct{ ID string }
		if err := json.Unmarshal([]byte(r.body), &v); err != nil || v.ID == "" {
			t.Fatalf("%d %s", r.status, r.body)
		}
		return v.ID
	}
	owner := login("owner@alpha.com")
	agentRole := roleID(t, h, "alpha", "坐席", owner)
	soloRole := idOf(call(h, "POST", "/t/alpha/api/roles", `{"name":"专员","conversation_scope":"assigned"}`, owner))
	ids := map[string]string{"owner": owners["alpha"], "beta": owners["beta"]}
	for _, m := range []struct{ key, role string }{{"agent1", agentRole}, {"agent2", agentRole}, {"solo", soloRole}} {
		ids[m.key] = idOf(call(h, "POST", "/t/alpha/api/members", `{"email":"`+m.key+`@alpha.com","name":"`+m.key+`","password":"password","role_id":"`+m.role+`"}`, owner))
	}
	teamA := idOf(call(h, "POST", "/t/alpha/api/teams", `{"name":"A线"}`, owner))
	teamB := idOf(call(h, "POST", "/t/alpha/api/teams", `{"name":"B线"}`, owner))
	call(h, "PUT", "/t/alpha/api/teams/"+teamA+"/members/"+ids["agent1"], "", owner)
	call(h, "PUT", "/t/alpha/api/teams/"+teamB+"/members/"+ids["agent2"], "", owner)

	// 每个成员的个人频道各订阅一个；beta 的主管理员订阅他在 beta 中的频道
	subs, topics := map[string]*broadcast.Subscription{}, map[string]string{}
	for key, id := range ids {
		tn := tenants["alpha"]
		if key == "beta" {
			tn = tenants["beta"]
		}
		topics[key] = push.Channel(tn, id)
		subs[key] = hub.Subscribe(topics[key])
		t.Cleanup(subs[key].Close)
	}
	broadcasttest.Ready(t, hub)
	// received 返回上次检查之后每个成员收到的事件：先往每个频道发一条标记，再读到标记为止
	received := func() map[string][]push.Event {
		t.Helper()
		got := map[string][]push.Event{}
		for key, sub := range subs {
			if err := hub.PublishJSON(ctx, topics[key], push.Event{Type: "marker"}); err != nil {
				t.Fatal(err)
			}
			for {
				var ev push.Event
				if err := json.Unmarshal(broadcasttest.Receive(t, sub).Data, &ev); err != nil {
					t.Fatal(err)
				}
				if ev.Type == "marker" {
					break
				}
				got[key] = append(got[key], ev)
			}
		}
		return got
	}
	expect := func(step string, typ, id string, keys ...string) {
		t.Helper()
		got := received()
		var who []string
		for key, evs := range got {
			for _, ev := range evs {
				if ev.Type != typ || ev.ID != id {
					t.Errorf("%s：%s 收到 %+v", step, key, ev)
				}
			}
			who = append(who, key)
		}
		slices.Sort(who)
		slices.Sort(keys)
		if !slices.Equal(who, keys) {
			t.Errorf("%s：收到推送的是 %v，应当是 %v", step, who, keys)
		}
	}

	// agent1 经实时连接订阅：只能订阅自己的频道
	agent1 := login("agent1@alpha.com")
	ws := dialRealtime(t, srv.URL+"/t/alpha/realtime", agent1)
	if code := ws.subscribe(push.Channel(tenants["alpha"], ids["agent1"])); code != 0 {
		t.Fatalf("订阅自己的频道：%d", code)
	}
	for _, ch := range []string{push.Channel(tenants["alpha"], ids["agent2"]), push.Channel(tenants["beta"], ids["beta"])} {
		if code := ws.subscribe(ch); code != 103 {
			t.Errorf("订阅 %s：%d，应当是 103 没有权限", ch, code)
		}
	}

	// 1. A 线收到客户的消息：主管理员（全部会话）和 A 线的 agent1
	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tenants["alpha"]}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: "网站", TeamID: &teamA})
	if err != nil {
		t.Fatal(err)
	}
	res, err := conversation.NewReceive(env.DB, a.channels, a.downstream()).Handle(ctx, system, conversation.ReceiveParams{ChannelID: ch.ID, From: "rahul", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	id := res.ConversationID
	expect("A 线的新消息", push.Conversation, id, "owner", "agent1")
	if ev := ws.publication(); ev.Type != push.Conversation || ev.ID != id || ev.Seq != 1 {
		t.Errorf("agent1 的实时连接收到 %+v", ev)
	}

	// 2. 指派给专员：再加上新的负责人
	if r := call(h, "PUT", "/t/alpha/api/conversations/"+id+"/assignment", `{"assignee_id":"`+ids["solo"]+`"}`, owner); r.status != http.StatusOK {
		t.Fatalf("指派：%d %s", r.status, r.body)
	}
	expect("指派给专员", push.Conversation, id, "owner", "agent1", "solo")

	// 3. 转到 B 线、没有负责人：原来的 A 线和负责人也收到，他们的列表中不再有它
	if r := call(h, "PUT", "/t/alpha/api/conversations/"+id+"/assignment", `{"team_id":"`+teamB+`","assignee_id":""}`, owner); r.status != http.StatusOK {
		t.Fatalf("转到 B 线：%d %s", r.status, r.body)
	}
	expect("转到 B 线", push.Conversation, id, "owner", "agent1", "agent2", "solo")

	// 4. 内部备注 @ agent1：B 线、主管理员和被 @ 的 agent1
	if r := call(h, "POST", "/t/alpha/api/conversations/"+id+"/notes", `{"text":"看一下","mentions":["`+ids["agent1"]+`"]}`, owner); r.status != http.StatusCreated {
		t.Fatalf("备注：%d %s", r.status, r.body)
	}
	expect("@ agent1", push.Conversation, id, "owner", "agent1", "agent2")

	// 5. agent1 标记已读：只推送给自己
	if r := call(h, "POST", "/t/alpha/api/conversations/"+id+"/read", `{"seq":100}`, agent1); r.status != http.StatusNoContent {
		t.Fatalf("标记已读：%d %s", r.status, r.body)
	}
	expect("agent1 标记已读", push.Read, id, "agent1")

	// 不是成员的工作区连不上
	if code := dialRealtime(t, srv.URL+"/t/beta/realtime", agent1).rejected(); code != 3500 {
		t.Errorf("连接不是成员的工作区：关闭码 %d，应当是 3500", code)
	}
}

// realtimeClient 是测试用的最小 Centrifuge JSON 协议客户端：连接、订阅、读取推送。
type realtimeClient struct {
	t    *testing.T
	conn *websocket.Conn
	id   int
	pubs []push.Event // 等待回复时收到的推送
}

// realtimeFrame 是服务器发来的一帧：命令的回复（id、error）或者推送（push.pub）。
type realtimeFrame struct {
	ID    int `json:"id"`
	Error *struct {
		Code int `json:"code"`
	} `json:"error"`
	Push *struct {
		Pub *struct {
			Data json.RawMessage `json:"data"`
		} `json:"pub"`
	} `json:"push"`
}

// dialRealtime 像浏览器一样带着会话 cookie 建立同源的实时连接，发送 connect 命令，不等它的回复。
func dialRealtime(t *testing.T, url string, cookie *http.Cookie) *realtimeClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// 浏览器的连接带着同源的 Origin，会话 cookie 只在同源的连接中使用
	header := http.Header{"Cookie": {(&http.Cookie{Name: cookie.Name, Value: cookie.Value}).String()}, "Origin": {url[:strings.Index(url, "/t/")]}}
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(url, "http"), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	c := &realtimeClient{t: t, conn: conn}
	c.send(map[string]any{"connect": map[string]any{}})
	return c
}

// send 发送一条命令，返回它的 id。
func (c *realtimeClient) send(cmd map[string]any) int {
	c.t.Helper()
	c.id++
	cmd["id"] = c.id
	data, _ := json.Marshal(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		c.t.Fatal(err)
	}
	return c.id
}

// next 读取下一帧；一条消息中可能有多帧，以换行分隔。
func (c *realtimeClient) next() ([]realtimeFrame, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	var frames []realtimeFrame
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var f realtimeFrame
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			return nil, err
		}
		frames = append(frames, f)
	}
	return frames, nil
}

// wait 等待命令 id 的回复，返回错误码（0 表示成功）；期间收到的推送留给 publication。
func (c *realtimeClient) wait(id int) int {
	c.t.Helper()
	for {
		frames, err := c.next()
		if err != nil {
			c.t.Fatalf("等待回复 %d：%v", id, err)
		}
		for _, f := range frames {
			switch {
			case f.Push != nil && f.Push.Pub != nil:
				var ev push.Event
				_ = json.Unmarshal(f.Push.Pub.Data, &ev)
				c.pubs = append(c.pubs, ev)
			case f.ID == id && f.Error != nil:
				return f.Error.Code
			case f.ID == id:
				return 0
			}
		}
	}
}

// subscribe 订阅频道 channel，返回错误码（0 表示成功，103 表示没有权限）。第一次订阅前先等 connect 的回复。
func (c *realtimeClient) subscribe(channel string) int {
	c.t.Helper()
	if c.id == 1 {
		if code := c.wait(1); code != 0 {
			c.t.Fatalf("connect：%d", code)
		}
	}
	return c.wait(c.send(map[string]any{"subscribe": map[string]any{"channel": channel}}))
}

// publication 返回下一条推送。
func (c *realtimeClient) publication() push.Event {
	c.t.Helper()
	for len(c.pubs) == 0 {
		frames, err := c.next()
		if err != nil {
			c.t.Fatalf("等待推送：%v", err)
		}
		for _, f := range frames {
			if f.Push != nil && f.Push.Pub != nil {
				var ev push.Event
				_ = json.Unmarshal(f.Push.Pub.Data, &ev)
				c.pubs = append(c.pubs, ev)
			}
		}
	}
	ev := c.pubs[0]
	c.pubs = c.pubs[1:]
	return ev
}

// rejected 返回服务器关闭连接时的关闭码。
func (c *realtimeClient) rejected() websocket.StatusCode {
	c.t.Helper()
	for {
		if _, err := c.next(); err != nil {
			return websocket.CloseStatus(err)
		}
	}
}
