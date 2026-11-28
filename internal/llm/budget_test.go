package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/apptest"
	"agents-desk/internal/channel"
	"agents-desk/internal/conversation"
	"agents-desk/internal/llm"
	"agents-desk/internal/tenant"
)

// TestModelSlotsConfigBudgetCircuitBreakerAndGenerationAttribution 验证 6 槽位配置/ModelConfig、
// llm_calls.generation_id 归因、平台默认与工作区覆写预算熔断以及跨工作区预算隔离。
func TestModelSlotsConfigBudgetCircuitBreakerAndGenerationAttribution(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	env := apptest.New(t)

	createTenant := tenant.NewCreate(env.DB, env.Roles, env.MemberLogins, tenant.Addresses{})
	alpha, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "BudgetAlpha", Slug: "budget-alpha", OwnerEmail: "owner@ba.test", OwnerName: "OwnerA", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := createTenant.Handle(ctx, apptest.Admin, tenant.CreateParams{
		Name: "BudgetBeta", Slug: "budget-beta", OwnerEmail: "owner@bb.test", OwnerName: "OwnerB", OwnerPassword: "password",
	})
	if err != nil {
		t.Fatal(err)
	}

	var upstreamCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Omirouter-Cost", "0.50")
		w.Header().Set("X-Omirouter-Prompt-Tokens", "300")
		w.Header().Set("X-Omirouter-Completion-Tokens", "200")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": `{"ok":true}`}}},
		})
	}))
	t.Cleanup(srv.Close)

	group := llm.NewGroup(env.DB, env.Crypt)
	if err := llm.NewSaveSettings(env.DB, group).Handle(ctx, apptest.Admin, llm.Settings{
		Gateway: llm.Gateway{BaseURL: srv.URL + "/v1", APIKey: "sk-budget"},
		Models: llm.Models{
			Chat:        "chat-pro",
			FastRouter:  "router-mini",
			Translation: "trans-v1",
			Embedding:   "embed-v1",
			Rerank:      "rerank-v1",
			Transcribe:  "whisper-v1",
		},
		DefaultBudget: llm.Budget{DailyCostUSD: 1.0},
	}); err != nil {
		t.Fatal(err)
	}

	client := llm.NewClient(env.DB, group, srv.Client())
	for _, ct := range llm.CallTypes.Values() {
		ok, err := client.Configured(ctx, ct)
		if err != nil || !ok {
			t.Fatalf("槽位 %s 应已配置: ok=%v err=%v", ct, ok, err)
		}
		cfg, hc, err := client.ModelConfig(ctx, ct)
		if err != nil || cfg.Model == "" || hc == nil {
			t.Fatalf("槽位 %s ModelConfig 异常: %+v err=%v", ct, cfg, err)
		}
	}

	// 准备一条关联的 bot + generation，验证 generation_id 归因落库
	botID := uuid.Must(uuid.NewV7()).String()
	sysAlpha := identity.Subject{Type: identity.System, ID: "test", Tenant: alpha.ID}
	chA, err := channel.NewCreate(env.DB).Handle(ctx, sysAlpha, channel.CreateParams{Kind: channel.Test, Name: "TestA"})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := conversation.NewReceive(env.DB, channel.DefaultRegistry(), conversation.Downstream{}).Handle(ctx, sysAlpha, conversation.ReceiveParams{
		ChannelID: chA.ID, From: "u-gen", Text: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	nowUTC := time.Now().UTC().Truncate(time.Microsecond)
	genID := uuid.Must(uuid.NewV7()).String()
	if _, err := env.DB.NewRaw(`INSERT INTO bots (id, created_at, updated_at, tenant_id, name, enabled, settings)
		VALUES (?, ?, ?, ?, 'Bot', true, '{}')`, botID, nowUTC, nowUTC, alpha.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := env.DB.NewRaw(`INSERT INTO generations (id, created_at, updated_at, tenant_id, conversation_id, actor_id, trigger_item_id, state, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?)`, genID, nowUTC, nowUTC, alpha.ID, rec.ConversationID, botID, rec.Item.ID, nowUTC.Add(time.Hour)).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	var out struct {
		OK bool `json:"ok"`
	}
	// Alpha 发起 2 次调用（每次 0.50 USD，共 1.00 USD）均成功
	if err := client.JSON(llm.WithGeneration(ctx, genID), llm.Chat, llm.Target{
		Tenant: alpha.ID, Type: "conversation", ID: rec.ConversationID, Operation: "agent_reply",
	}, llm.JSONRequest{Messages: []llm.Message{{Role: "user", Content: "1"}}, SchemaName: "ok", Schema: okSchema()}, &out); err != nil {
		t.Fatal(err)
	}
	if err := client.JSON(ctx, llm.FastRouter, llm.Target{
		Tenant: alpha.ID, Type: "conversation", ID: rec.ConversationID, Operation: "route",
	}, llm.JSONRequest{Messages: []llm.Message{{Role: "user", Content: "2"}}, SchemaName: "ok", Schema: okSchema()}, &out); err != nil {
		t.Fatal(err)
	}

	var storedGenID *string
	if err := env.DB.NewRaw(`SELECT generation_id FROM llm_calls WHERE tenant_id = ? AND operation = 'agent_reply'`, alpha.ID).Scan(ctx, &storedGenID); err != nil {
		t.Fatal(err)
	}
	if storedGenID == nil || *storedGenID != genID {
		t.Fatalf("llm_calls.generation_id 未正确归因: got=%v want=%s", storedGenID, genID)
	}

	// Alpha 第 3 次调用达到每日 1.0 USD 上限 -> 拦截并不得请求上游
	beforeCalls := upstreamCalls.Load()
	err = client.JSON(ctx, llm.Chat, llm.Target{
		Tenant: alpha.ID, Type: "conversation", ID: rec.ConversationID, Operation: "blocked",
	}, llm.JSONRequest{Messages: []llm.Message{{Role: "user", Content: "3"}}, SchemaName: "ok", Schema: okSchema()}, &out)
	if !errors.Is(err, llm.ErrBudgetExceeded) {
		t.Fatalf("超出每日费用上限应返回 ErrBudgetExceeded, 实际 %v", err)
	}
	if upstreamCalls.Load() != beforeCalls {
		t.Fatalf("熔断拦截后不应发起上游 HTTP 请求")
	}

	// 跨工作区隔离：Beta 不受 Alpha 耗尽预算影响，仍可正常调用
	if err := client.JSON(ctx, llm.Translation, llm.Target{
		Tenant: beta.ID, Type: "test", ID: "beta-1", Operation: "incoming",
	}, llm.JSONRequest{Messages: []llm.Message{{Role: "user", Content: "hi"}}, SchemaName: "ok", Schema: okSchema()}, &out); err != nil {
		t.Fatalf("Beta 工作区不应受 Alpha 预算影响: %v", err)
	}

	// 管理端工作区覆写：将 Alpha 每日费用上限提高到 2.0，token 上限设 1500
	tbGroup := llm.NewTenantBudgetGroup(env.DB)
	saveTB := llm.NewSaveTenantBudget(env.DB, group, tbGroup)
	daily2 := 2.0
	tok1500 := int64(1500)
	dto, err := saveTB.Handle(ctx, apptest.Admin, alpha.ID, llm.TenantBudget{DailyCostUSD: &daily2, DailyTotalTokens: &tok1500})
	if err != nil {
		t.Fatal(err)
	}
	if dto.Effective.DailyCostUSD != 2.0 || dto.Effective.DailyTotalTokens != 1500 {
		t.Fatalf("生效预算覆写结果不对: %+v", dto.Effective)
	}
	// Alpha 提高上限后可继续调用（第 3 次成功，累计 token 1500），第 4 次触达每日 token 上限 1500 被熔断
	if err := client.JSON(ctx, llm.Chat, llm.Target{
		Tenant: alpha.ID, Type: "conversation", ID: rec.ConversationID, Operation: "resumed",
	}, llm.JSONRequest{Messages: []llm.Message{{Role: "user", Content: "4"}}, SchemaName: "ok", Schema: okSchema()}, &out); err != nil {
		t.Fatalf("提高预算上限后应允许调用: %v", err)
	}
	err = client.JSON(ctx, llm.Chat, llm.Target{
		Tenant: alpha.ID, Type: "conversation", ID: rec.ConversationID, Operation: "token_capped",
	}, llm.JSONRequest{Messages: []llm.Message{{Role: "user", Content: "5"}}, SchemaName: "ok", Schema: okSchema()}, &out)
	if !errors.Is(err, llm.ErrBudgetExceeded) {
		t.Fatalf("达到工作区每日 token 上限后应熔断, 实际 %v", err)
	}

	// 不存在的 tenant_id 查询或修改预算应返回 ErrTenantNotFound
	if _, err := llm.NewGetTenantBudget(env.DB, group, tbGroup).Handle(ctx, uuid.Must(uuid.NewV7()).String()); !errors.Is(err, llm.ErrTenantNotFound) {
		t.Fatalf("不存在的工作区应返回 ErrTenantNotFound, 实际 %v", err)
	}
}

func okSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"ok": map[string]any{"type": "boolean"}},
		"required":             []string{"ok"},
		"additionalProperties": false,
	}
}
