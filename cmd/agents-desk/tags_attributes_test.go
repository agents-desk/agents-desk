package main

import (
	"context"
	"encoding/json"
	"net/http"
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

// TestTagsAndAttributes 在完整的 HTTP 路由上验证 P1：标签和自定义属性的管理与权限、跨工作区隔离、
// 会话与联系人打标签和改属性（写入时间线系统事件），以及收件箱按标签筛选。
func TestTagsAndAttributes(t *testing.T) {
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

	createTenant := tenant.NewCreate(env.DB, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "alpha", Slug: "alpha", OwnerEmail: "owner@alpha.com", OwnerName: "主管理员", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "beta", Slug: "beta", OwnerEmail: "owner@beta.com", OwnerName: "B主管理员", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
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
	agentRole := roleID(t, h, "alpha", "坐席", owner)
	call(h, "POST", "/t/alpha/api/members", `{"email":"agent@alpha.com","name":"坐席","password":"password","role_id":"`+agentRole+`"}`, owner)
	agent := login("agent@alpha.com")

	// 1. 标签和属性的创建与权限检查：坐席能读、不能管理
	r := call(h, "POST", "/t/alpha/api/tags", `{"name":"VIP","color":"yellow","description":"重要客户"}`, owner)
	if r.status != http.StatusCreated {
		t.Fatalf("新建标签：%d %s", r.status, r.body)
	}
	vipTag := idOf(r.body)
	refundTag := idOf(call(h, "POST", "/t/alpha/api/tags", `{"name":"退款","color":"deep-orange"}`, owner).body)
	betaTag := idOf(call(h, "POST", "/t/beta/api/tags", `{"name":"VIP","color":"red"}`, betaOwner).body)

	if r := call(h, "GET", "/t/alpha/api/tags", "", agent); r.status != http.StatusOK || !strings.Contains(r.body, vipTag) {
		t.Fatalf("坐席读取标签列表：%d %s", r.status, r.body)
	}
	if r := call(h, "POST", "/t/alpha/api/tags", `{"name":"无权限"}`, agent); r.status != http.StatusForbidden {
		t.Fatalf("坐席新建标签应 403：%d %s", r.status, r.body)
	}
	if r := call(h, "GET", "/t/alpha/api/tags/"+betaTag, "", owner); r.code != "tag.not_found" {
		t.Fatalf("跨工作区读取标签应 404：%d %s", r.status, r.body)
	}

	r = call(h, "POST", "/t/alpha/api/attributes", `{"target":"conversation","key":"issue_type","name":"问题类型","type":"select","options":["物流","退款"]}`, owner)
	if r.status != http.StatusCreated {
		t.Fatalf("新建会话属性：%d %s", r.status, r.body)
	}
	issueAttr := idOf(r.body)
	r = call(h, "POST", "/t/alpha/api/attributes", `{"target":"conversation","key":"order_no","name":"订单号","type":"text"}`, owner)
	if r.status != http.StatusCreated {
		t.Fatalf("新建第二条会话属性：%d %s", r.status, r.body)
	}
	orderAttr := idOf(r.body)
	if r := call(h, "PUT", "/t/alpha/api/attributes/order", `{"target":"conversation","ids":["`+orderAttr+`","`+issueAttr+`"]}`, agent); r.status != http.StatusForbidden {
		t.Fatalf("坐席调整属性顺序应 403：%d %s", r.status, r.body)
	}
	if r := call(h, "PUT", "/t/alpha/api/attributes/order", `{"target":"conversation","ids":["`+orderAttr+`","`+issueAttr+`"]}`, owner); r.status != http.StatusOK || strings.Index(r.body, orderAttr) > strings.Index(r.body, issueAttr) {
		t.Fatalf("调整属性顺序：%d %s", r.status, r.body)
	}
	r = call(h, "POST", "/t/alpha/api/attributes", `{"target":"contact","key":"plan","name":"套餐","type":"select","options":["免费版","专业版"]}`, owner)
	if r.status != http.StatusCreated {
		t.Fatalf("新建联系人属性：%d %s", r.status, r.body)
	}
	if r := call(h, "POST", "/t/alpha/api/attributes", `{"target":"conversation","key":"BadKey","name":"非法","type":"text"}`, owner); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("非法 key 格式应 422：%d %s", r.status, r.body)
	}
	if r := call(h, "GET", "/t/alpha/api/attributes?target=conversation", "", agent); r.status != http.StatusOK || !strings.Contains(r.body, "issue_type") || strings.Contains(r.body, "plan") {
		t.Fatalf("坐席按 target 读属性列表：%d %s", r.status, r.body)
	}
	if r := call(h, "POST", "/t/alpha/api/attributes", `{"target":"conversation","key":"x","name":"x","type":"text"}`, agent); r.status != http.StatusForbidden {
		t.Fatalf("坐席新建属性应 403：%d %s", r.status, r.body)
	}

	// 2. 创建两个示例会话
	inAlpha := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
	inBeta := identity.Subject{Type: identity.System, ID: "test", Tenant: beta.ID}
	ch, err := channel.NewCreate(env.DB).Handle(ctx, inAlpha, channel.CreateParams{Kind: channel.Test, Name: "测试渠道"})
	if err != nil {
		t.Fatal(err)
	}
	recv := conversation.NewReceive(env.DB, a.channels, a.downstream())
	c1, err := recv.Handle(ctx, inAlpha, conversation.ReceiveParams{ChannelID: ch.ID, From: "u1", FromName: "张三", Text: "物流太慢"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := recv.Handle(ctx, inAlpha, conversation.ReceiveParams{ChannelID: ch.ID, From: "u2", FromName: "李四", Text: "申请退款"})
	if err != nil {
		t.Fatal(err)
	}
	_ = inBeta

	// 3. 给会话加标签、重复加不写重复事件、移除标签，验证时间线和按标签筛选
	r = call(h, "POST", "/t/alpha/api/conversations/"+c1.ConversationID+"/tags", `{"tag_id":"`+vipTag+`"}`, owner)
	if r.status != http.StatusOK || !strings.Contains(r.body, `"name":"VIP"`) {
		t.Fatalf("给会话加标签：%d %s", r.status, r.body)
	}
	// 不能给会话打别的工作区的标签
	if r := call(h, "POST", "/t/alpha/api/conversations/"+c1.ConversationID+"/tags", `{"tag_id":"`+betaTag+`"}`, owner); r.code != "tag.not_found" {
		t.Fatalf("打别的工作区标签应失败：%d %s", r.status, r.body)
	}
	// 重复添加同一标签不写重复事件
	if r := call(h, "POST", "/t/alpha/api/conversations/"+c1.ConversationID+"/tags", `{"tag_id":"`+vipTag+`"}`, owner); r.status != http.StatusOK {
		t.Fatalf("重复加标签：%d %s", r.status, r.body)
	}
	call(h, "POST", "/t/alpha/api/conversations/"+c2.ConversationID+"/tags", `{"tag_id":"`+refundTag+`"}`, owner)

	// 按标签筛选会话列表
	r = call(h, "GET", "/t/alpha/api/conversations?inbox=all&tag_id="+vipTag, "", owner)
	if r.status != http.StatusOK || !strings.Contains(r.body, c1.ConversationID) || strings.Contains(r.body, c2.ConversationID) {
		t.Fatalf("按标签筛选：%d %s", r.status, r.body)
	}

	// 4. 修改会话属性和联系人标签/属性
	r = call(h, "PATCH", "/t/alpha/api/conversations/"+c1.ConversationID+"/attributes", `{"attributes":{"issue_type":"物流"}}`, owner)
	if r.status != http.StatusOK || !strings.Contains(r.body, `"issue_type":"物流"`) {
		t.Fatalf("修改会话属性：%d %s", r.status, r.body)
	}
	if r := call(h, "PATCH", "/t/alpha/api/conversations/"+c1.ConversationID+"/attributes", `{"attributes":{"unknown":"x"}}`, owner); r.code != "attribute.unknown_key" {
		t.Fatalf("未知属性 key：%d %s", r.status, r.body)
	}
	r = call(h, "POST", "/t/alpha/api/conversations/"+c1.ConversationID+"/contact/tags", `{"tag_id":"`+vipTag+`"}`, owner)
	if r.status != http.StatusOK {
		t.Fatalf("给联系人加标签：%d %s", r.status, r.body)
	}
	r = call(h, "PATCH", "/t/alpha/api/conversations/"+c1.ConversationID+"/contact/attributes", `{"attributes":{"plan":"专业版"}}`, owner)
	if r.status != http.StatusOK || !strings.Contains(r.body, `"plan":"专业版"`) {
		t.Fatalf("修改联系人属性：%d %s", r.status, r.body)
	}

	// 标签使用次数：vipTag 被 1 个会话和 1 个联系人使用
	r = call(h, "GET", "/t/alpha/api/tags/"+vipTag, "", owner)
	if !strings.Contains(r.body, `"conversation_count":1`) || !strings.Contains(r.body, `"contact_count":1`) {
		t.Fatalf("标签使用次数：%s", r.body)
	}

	// 坐席看不到未指派且不在团队里的会话，不能修改它的标签或属性
	if r := call(h, "POST", "/t/alpha/api/conversations/"+c1.ConversationID+"/tags", `{"tag_id":"`+refundTag+`"}`, agent); r.status != http.StatusNotFound {
		t.Fatalf("坐席看不到会话应 404：%d %s", r.status, r.body)
	}
	if r := call(h, "PATCH", "/t/alpha/api/conversations/"+c1.ConversationID+"/contact/attributes", `{"attributes":{"plan":"免费版"}}`, agent); r.status != http.StatusNotFound {
		t.Fatalf("坐席看不到会话不能改联系人属性：%d %s", r.status, r.body)
	}

	// 移除标签并核对时间线系统事件
	if r := call(h, "DELETE", "/t/alpha/api/conversations/"+c1.ConversationID+"/tags/"+vipTag, "", owner); r.status != http.StatusOK {
		t.Fatalf("移除会话标签：%d %s", r.status, r.body)
	}
	if r := call(h, "DELETE", "/t/alpha/api/conversations/"+c1.ConversationID+"/contact/tags/"+vipTag, "", owner); r.status != http.StatusOK {
		t.Fatalf("移除联系人标签：%d %s", r.status, r.body)
	}
	r = call(h, "GET", "/t/alpha/api/conversations/"+c1.ConversationID+"/items", "", owner)
	var items []struct {
		Kind    string `json:"kind"`
		Content struct {
			Event string `json:"event"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(r.body), &items); err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, it := range items {
		if it.Kind == "event" {
			events = append(events, it.Content.Event)
		}
	}
	want := []string{
		"conversation.tag_added",
		"conversation.attributes_changed",
		"contact.tag_added",
		"contact.attributes_changed",
		"conversation.tag_removed",
		"contact.tag_removed",
	}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("时间线事件不符合预期：\ngot:  %v\nwant: %v", events, want)
	}

	// 删除标签后关联自动清理
	if r := call(h, "DELETE", "/t/alpha/api/tags/"+refundTag, "", owner); r.status != http.StatusNoContent {
		t.Fatalf("删除标签：%d %s", r.status, r.body)
	}
	r = call(h, "GET", "/t/alpha/api/conversations/"+c2.ConversationID, "", owner)
	if strings.Contains(r.body, refundTag) {
		t.Fatalf("删除标签后会话仍带有该标签：%s", r.body)
	}
}
