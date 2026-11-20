package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-tenon/tenon/apitoken"
	"github.com/go-tenon/tenon/config"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/tenant"
)

// TestInbox 在完整的路由上检查收件箱：三种可见范围下的列表和数量、团队收件箱、筛选、两种排序和游标、
// 看不到的会话一律 404、@ 同事（@我、被 @ 后能看到）、未读和标记已读，以及工作区之间隔离。
func TestInbox(t *testing.T) {
	t.Parallel()
	env := apptest.New(t)
	ctx := context.Background()
	tokens := apitoken.Config{Lifetime: time.Hour, IdleTimeout: time.Hour}
	a := &App{
		cfg: config.Default[Config](), db: env.DB, roles: env.Roles,
		adminLogins: env.AdminLogins, adminTokens: apitoken.NewStore(env.DB, tokens),
		memberLogins: env.MemberLogins, memberTokens: apitoken.NewStore(env.DB, tokens),
		llmSettings: llm.NewGroup(env.DB), files: env.Files, localDisk: env.Disk,
		channels: channel.DefaultRegistry(),
	}
	h := a.routes(nil)
	tenants := map[string]string{}
	for _, slug := range []string{"alpha", "beta"} {
		tn, err := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{}).Handle(ctx, apptest.Admin, tenant.CreateParams{
			Name: slug, Slug: slug, OwnerEmail: "owner@" + slug + ".com", OwnerName: slug + " 主管理员", OwnerPassword: "password",
		})
		if err != nil {
			t.Fatal(err)
		}
		tenants[slug] = tn.ID
	}
	login := func(email string) *http.Cookie {
		t.Helper()
		r := call(h, "POST", "/api/auth/login", `{"email":"`+email+`","password":"password"}`, nil)
		if r.status != http.StatusNoContent {
			t.Fatalf("登录 %s：%d %s", email, r.status, r.body)
		}
		return r.cookie
	}
	idOf := func(body string) string {
		var v struct{ ID string }
		_ = json.Unmarshal([]byte(body), &v)
		return v.ID
	}
	owner, betaOwner := login("owner@alpha.com"), login("owner@beta.com")

	// 成员：两位坐席（所在团队收件箱的会话），一位专员（只有指派给自己的）
	agentRole := roleID(t, h, "alpha", "坐席", owner)
	soloRole := idOf(call(h, "POST", "/t/alpha/api/roles", `{"name":"专员","conversation_scope":"assigned"}`, owner).body)
	members := map[string]string{}
	for _, m := range []struct{ key, role string }{{"agent1", agentRole}, {"agent2", agentRole}, {"solo", soloRole}} {
		members[m.key] = idOf(call(h, "POST", "/t/alpha/api/members", `{"email":"`+m.key+`@alpha.com","name":"`+m.key+`","password":"password","role_id":"`+m.role+`"}`, owner).body)
	}
	agent1, solo := login("agent1@alpha.com"), login("solo@alpha.com")
	teamA := idOf(call(h, "POST", "/t/alpha/api/teams", `{"name":"A线"}`, owner).body)
	teamB := idOf(call(h, "POST", "/t/alpha/api/teams", `{"name":"B线"}`, owner).body)
	call(h, "PUT", "/t/alpha/api/teams/"+teamA+"/members/"+members["agent1"], "", owner)
	call(h, "PUT", "/t/alpha/api/teams/"+teamB+"/members/"+members["agent2"], "", owner)

	// 会话：A 线两个，B 线一个，没有团队收件箱的一个，按这个顺序开始等待
	system := identity.Subject{Type: identity.System, ID: "test", Tenant: tenants["alpha"]}
	receive := conversation.NewReceive(env.DB, a.channels, a.downstream())
	start := func(teamID *string, from string) string {
		t.Helper()
		ch, err := channel.NewCreate(env.DB).Handle(ctx, system, channel.CreateParams{Kind: channel.Test, Name: from, TeamID: teamID})
		if err != nil {
			t.Fatal(err)
		}
		res, err := receive.Handle(ctx, system, conversation.ReceiveParams{ChannelID: ch.ID, From: from, Text: "hello " + from})
		if err != nil {
			t.Fatal(err)
		}
		return res.ConversationID
	}
	a1, a2, b1, n1 := start(&teamA, "a1"), start(&teamA, "a2"), start(&teamB, "b1"), start(nil, "n1")
	// a2 指派给专员：指派的动作在 D1b 中加入，这里直接改表
	if _, err := env.DB.NewRaw("UPDATE conversations SET assignee_type = 'member', assignee_id = ? WHERE id = ?", members["solo"], a2).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// 坐席回复 a1：a1 不再等待回复
	if r := call(h, "POST", "/t/alpha/api/conversations/"+a1+"/replies", `{"text":"您好"}`, agent1); r.status != http.StatusCreated {
		t.Fatalf("回复：%d %s", r.status, r.body)
	}

	type summary struct{ Count, Waiting int }
	type inbox struct {
		Mine, Mentions, Unassigned, All summary
		Teams                           []struct {
			ID string
			summary
		}
	}
	inboxOf := func(cookie *http.Cookie) inbox {
		t.Helper()
		r := call(h, "GET", "/t/alpha/api/inbox", "", cookie)
		var v inbox
		if err := json.Unmarshal([]byte(r.body), &v); err != nil || r.status != http.StatusOK {
			t.Fatalf("收件箱：%d %s", r.status, r.body)
		}
		return v
	}
	type conv struct {
		ID          string
		Unread      bool
		MentionedAt *time.Time `json:"mentioned_at"`
	}
	list := func(cookie *http.Cookie, query string) ([]conv, string) {
		t.Helper()
		r := call(h, "GET", "/t/alpha/api/conversations?"+query, "", cookie)
		var p struct {
			Items      []conv
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal([]byte(r.body), &p); err != nil || r.status != http.StatusOK {
			t.Fatalf("会话列表 %s：%d %s", query, r.status, r.body)
		}
		return p.Items, p.NextCursor
	}
	ids := func(items []conv) []string {
		out := make([]string, len(items))
		for i, c := range items {
			out[i] = c.ID
		}
		return out
	}

	// 主管理员看到全部：未指派 1 个，A 线 2 个（1 个等待回复），B 线 1 个
	got := inboxOf(owner)
	if got.All.Count != 4 || got.All.Waiting != 3 || got.Unassigned.Count != 1 || got.Mine.Count != 0 || len(got.Teams) != 2 ||
		got.Teams[0].ID != teamA || got.Teams[0].Count != 2 || got.Teams[0].Waiting != 1 || got.Teams[1].Count != 1 {
		t.Fatalf("主管理员的收件箱：%+v", got)
	}
	// 坐席只看到所在的 A 线；专员只看到指派给自己的
	if got := inboxOf(agent1); got.All.Count != 2 || got.Unassigned.Count != 0 || len(got.Teams) != 1 || got.Teams[0].ID != teamA {
		t.Errorf("坐席的收件箱：%+v", got)
	}
	if got := inboxOf(solo); got.All.Count != 1 || got.Mine.Count != 1 || len(got.Teams) != 0 {
		t.Errorf("专员的收件箱：%+v", got)
	}

	// 按等待时长：等待回复的按开始等待的时间，已回复的排在后面；游标翻页连续、不重复
	if items, _ := list(owner, "inbox=all"); !slices.Equal(ids(items), []string{a2, b1, n1, a1}) {
		t.Errorf("按等待时长：%v", ids(items))
	}
	first, cursor := list(owner, "inbox=all&per_page=3")
	rest, last := list(owner, "inbox=all&per_page=3&cursor="+url.QueryEscape(cursor))
	if cursor == "" || last != "" || !slices.Equal(append(ids(first), ids(rest)...), []string{a2, b1, n1, a1}) {
		t.Errorf("翻页：%v %v", ids(first), ids(rest))
	}
	if items, _ := list(owner, "inbox=all&sort=recent"); items[0].ID != a1 || items[1].ID != n1 {
		t.Errorf("按最近活动：%v", ids(items))
	}
	// 列表和筛选
	for query, want := range map[string][]string{
		"inbox=unassigned":                         {n1},
		"inbox=all&team_id=" + teamA:               {a2, a1},
		"inbox=mine":                               {},
		"inbox=all&assignee_id=" + members["solo"]: {a2},
		"inbox=all&status=closed":                  {},
	} {
		if items, _ := list(owner, query); !slices.Equal(ids(items), want) {
			t.Errorf("%s：%v", query, ids(items))
		}
	}
	if items, _ := list(solo, "inbox=mine"); !slices.Equal(ids(items), []string{a2}) {
		t.Errorf("专员的“我的”：%v", ids(items))
	}

	// 看不到的会话：读取、时间线、回复、备注、标记已读都是 404
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/conversations/" + b1, ""},
		{"GET", "/conversations/" + b1 + "/items", ""},
		{"POST", "/conversations/" + b1 + "/replies", `{"text":"x"}`},
		{"POST", "/conversations/" + b1 + "/notes", `{"text":"x"}`},
		{"POST", "/conversations/" + b1 + "/read", `{"seq":1}`},
	} {
		if r := call(h, c.method, "/t/alpha/api"+c.path, c.body, agent1); r.code != "conversation.not_found" {
			t.Errorf("坐席 %s %s：%d %s", c.method, c.path, r.status, r.body)
		}
	}

	// @ 同事：主管理员在 B 线的会话中 @ 坐席，坐席的“@我”中出现它，并且能看到它
	if r := call(h, "POST", "/t/alpha/api/conversations/"+b1+"/notes", `{"text":"@agent1 看一下","mentions":["`+members["agent1"]+`"]}`, owner); r.status != http.StatusCreated || !strings.Contains(r.body, `"mentions":["`+members["agent1"]+`"]`) {
		t.Fatalf("@ 同事：%d %s", r.status, r.body)
	}
	if got := inboxOf(agent1); got.Mentions.Count != 1 || got.All.Count != 3 {
		t.Errorf("被 @ 之后：%+v", got)
	}
	mentioned, _ := list(agent1, "inbox=mentions")
	if len(mentioned) != 1 || mentioned[0].ID != b1 || mentioned[0].MentionedAt == nil || !mentioned[0].Unread {
		t.Fatalf("@我：%+v", mentioned)
	}
	if r := call(h, "GET", "/t/alpha/api/conversations/"+b1, "", agent1); r.status != http.StatusOK {
		t.Errorf("被 @ 后读取：%d", r.status)
	}
	// @ 的人必须是这个工作区的成员
	betaMember := idOf(call(h, "POST", "/t/beta/api/members", `{"email":"x@beta.com","name":"x","password":"password","role_id":"`+roleID(t, h, "beta", "坐席", betaOwner)+`"}`, betaOwner).body)
	if r := call(h, "POST", "/t/alpha/api/conversations/"+b1+"/notes", `{"text":"x","mentions":["`+betaMember+`"]}`, owner); r.code != "member.not_found" || fieldOf(r.body) != "body.mentions[0]" {
		t.Errorf("@ 别的工作区的人：%d %s", r.status, r.body)
	}

	// 标记已读：读到最新一项后不再未读，“@我”的数量清零，列表中仍保留这个会话；位置超过最新一项时按最新一项记
	var b1Seq struct {
		LastSeq int64 `json:"last_seq"`
	}
	_ = json.Unmarshal([]byte(call(h, "GET", "/t/alpha/api/conversations/"+b1, "", agent1).body), &b1Seq)
	if r := call(h, "POST", "/t/alpha/api/conversations/"+b1+"/read", `{"seq":`+strconv.FormatInt(b1Seq.LastSeq+100, 10)+`}`, agent1); r.status != http.StatusNoContent {
		t.Fatalf("标记已读：%d %s", r.status, r.body)
	}
	if got := inboxOf(agent1); got.Mentions.Count != 0 {
		t.Errorf("已读后的 @我：%+v", got.Mentions)
	}
	if items, _ := list(agent1, "inbox=mentions"); len(items) != 1 || items[0].Unread {
		t.Errorf("已读后的列表：%+v", items)
	}
	var lastRead int64
	if err := env.DB.NewRaw("SELECT last_read_seq FROM conversation_reads WHERE member_id = ? AND conversation_id = ?", members["agent1"], b1).Scan(ctx, &lastRead); err != nil || lastRead != b1Seq.LastSeq {
		t.Errorf("已读的位置：%d %v", lastRead, err)
	}
	// 已读位置只往前推进
	call(h, "POST", "/t/alpha/api/conversations/"+b1+"/read", `{"seq":0}`, agent1)
	if items, _ := list(agent1, "inbox=mentions"); items[0].Unread {
		t.Errorf("已读位置后退了")
	}
	// 自己写的不算未读；对方再发来消息后又是未读
	call(h, "POST", "/t/alpha/api/conversations/"+a1+"/read", `{"seq":1}`, agent1)
	if items, _ := list(agent1, "inbox=all&team_id="+teamA+"&sort=recent"); items[0].ID != a1 || items[0].Unread {
		t.Errorf("自己的回复：%+v", items)
	}

	// 工作区之间隔离：别的工作区中查不到这个会话
	if r := call(h, "GET", "/t/beta/api/conversations/"+a1, "", betaOwner); r.code != "conversation.not_found" {
		t.Errorf("别的工作区：%d %s", r.status, r.body)
	}
	if r := call(h, "GET", "/t/beta/api/conversations?inbox=all", "", betaOwner); r.status != http.StatusOK || strings.Contains(r.body, a1) {
		t.Errorf("别的工作区的列表：%d %s", r.status, r.body)
	}
}
