package contact

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/httpx"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/operation"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/sheet"
	"github.com/go-tenon/tenon/sheet/exports"

	"agents-desk/internal/attribute"
)

// ExportRequest 是后台导出联系人的队列任务载荷。
type ExportRequest struct {
	ID      uuid.UUID        `json:"id"`
	Subject identity.Subject `json:"subject"`
	Filter  Filter           `json:"filter"`
	Sort    Sort             `json:"sort,omitempty"`
}

// ExportJob 是后台导出联系人列表的队列主题。
var ExportJob = queue.Topic[ExportRequest]("contacts.export")

// ExportStartInput 是发起后台导出联系人的 HTTP 请求。
type ExportStartInput struct {
	Body struct {
		Format    sheet.Format `json:"format,omitempty" enum:"xlsx,csv" doc:"导出格式，默认 xlsx"`
		ViewID    string       `json:"view_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"基于指定联系人视图的筛选条件导出"`
		Kind      Kind         `json:"kind,omitempty" doc:"按联系人类型筛选：user 或 lead"`
		Q         string       `json:"q,omitempty" maxLength:"100" doc:"关键词筛选"`
		Language  string       `json:"language,omitempty" maxLength:"35" doc:"语言筛选"`
		ChannelID string       `json:"channel_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"渠道筛选"`
		TagID     string       `json:"tag_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"标签筛选"`
		Sort      Sort         `json:"sort,omitempty" doc:"排序方式"`
	}
}

// ExportProgressInput 是查询联系人导出进度事件流的路径参数。
type ExportProgressInput struct {
	ID uuid.UUID `path:"id"`
}

// ExportProgress 是返回给前端的联系人导出进度与下载信息，附带失败原因译文。
type ExportProgress struct {
	*exports.Export
	ErrorMessage string `json:"error_message,omitempty"`
}

// ExportOutput 是发起后台导出的 HTTP 响应。
type ExportOutput struct {
	Body ExportProgress
}

// QueueExport 在后台队列中将符合筛选条件与调用者可见范围的联系人导出为表格文件，并实时推送进度。
type QueueExport struct {
	db       *bun.DB
	hub      *broadcast.Hub
	exporter *exports.Service
}

// NewQueueExport 返回联系人后台导出动作。
func NewQueueExport(db *bun.DB, hub *broadcast.Hub, exporter *exports.Service) *QueueExport {
	return &QueueExport{db: db, hub: hub, exporter: exporter}
}

// exportOperationTopic 返回某个后台导出操作的进度广播主题。
func exportOperationTopic(id uuid.UUID) string { return "operation:" + id.String() }

// Handle 校验可选视图并合并筛选条件，在事务中登记导出操作并发布队列任务。
func (a *QueueExport) Handle(ctx context.Context, sub identity.Subject, format sheet.Format, filter Filter, sort Sort, viewID string) (*exports.Export, error) {
	if format == "" {
		format = sheet.XLSX
	}
	if viewID != "" {
		v, err := NewViews(a.db).Get(ctx, sub, viewID)
		if err != nil {
			return nil, err
		}
		cfg := normalizeViewConfig(v.Config)
		filter = mergeFilter(cfg.Filter, filter)
		if sort == "" {
			sort = cfg.Sort
		}
	}
	if sort == "" {
		sort = ByRecent
	}
	var exp *exports.Export
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if exp, err = a.exporter.Start(ctx, tx, sub, "contacts.export", format); err != nil {
			return err
		}
		return ExportJob.Publish(ctx, tx, ExportRequest{ID: exp.ID, Subject: sub, Filter: filter, Sort: sort})
	})
	return exp, err
}

// formatContactExportProgress 在导出完成且结果存于私有盘时补充工作区成员鉴权下载地址。
func formatContactExportProgress(ctx context.Context, exp *exports.Export) ExportProgress {
	out := *exp
	if out.Status == operation.StatusCompleted && out.FileID != uuid.Nil && out.URL == "" {
		if slug := chi.URLParamFromCtx(ctx, "slug"); slug != "" {
			out.URL = "/t/" + url.PathEscape(slug) + "/api/contacts/exports/" + out.ID.String() + "/download"
		}
	}
	return ExportProgress{Export: &out, ErrorMessage: out.ErrorMessage(ctx)}
}

// HTTP 处理 POST /t/{slug}/api/contacts/exports。
func (a *QueueExport) HTTP(ctx context.Context, in *ExportStartInput) (*ExportOutput, error) {
	sub, _ := identity.FromContext(ctx)
	f := Filter{
		Kind:      in.Body.Kind,
		Q:         in.Body.Q,
		Language:  in.Body.Language,
		ChannelID: in.Body.ChannelID,
		TagID:     in.Body.TagID,
	}
	exp, err := a.Handle(ctx, sub, in.Body.Format, f, in.Body.Sort, in.Body.ViewID)
	if err != nil {
		return nil, err
	}
	return &ExportOutput{Body: formatContactExportProgress(ctx, exp)}, nil
}

// Progress 处理 GET /t/{slug}/api/contacts/exports/{id}/progress：推送导出进度 SSE 事件直到完成或失败。
func (a *QueueExport) Progress(ctx context.Context, in *ExportProgressInput) (*huma.StreamResponse, error) {
	sub, _ := identity.FromContext(ctx)
	if _, err := a.exporter.Get(ctx, sub, in.ID); err != nil {
		return nil, err
	}
	return rest.SSE(func(s *httpx.Stream) error {
		var events *broadcast.Subscription
		if a.hub != nil {
			events = a.hub.Subscribe(exportOperationTopic(in.ID))
			defer events.Close()
		}
		for {
			exp, err := a.exporter.Get(s.Context(), sub, in.ID)
			if err != nil {
				return err
			}
			p := formatContactExportProgress(s.Context(), exp)
			if err := s.SendJSON(httpx.Event{Name: "progress"}, p); err != nil || exp.Finished() {
				return nil
			}
			if events == nil {
				return nil
			}
			select {
			case <-s.Done():
				return nil
			case _, ok := <-events.C():
				if !ok {
					return nil
				}
			}
		}
	}), nil
}

// Job 是队列 worker 的处理函数：严格按发起人所在工作区和可见范围导出联系人及标签、自定义属性列。
func (a *QueueExport) Job(ctx context.Context, req ExportRequest) error {
	return a.exporter.Process(ctx, req.ID, func(ctx context.Context, _ *exports.Export, w *exports.Writer) error {
		baseQuery := func() *bun.SelectQuery {
			q := visible(a.db.NewSelect().Model((*Contact)(nil)).Where("contact.tenant_id = ?", req.Subject.Tenant), req.Subject)
			return applyFilter(q, req.Filter)
		}
		total, err := baseQuery().Count(ctx)
		if err != nil {
			return errors.Wrap(err, "count contacts for export")
		}
		if err := w.SetTotal(int(total)); err != nil {
			return err
		}

		// 读取工作区定义的联系人自定义属性作为动态列
		var attrDefs []attribute.Definition
		if err := a.db.NewSelect().Model(&attrDefs).
			Where("tenant_id = ? AND target = ?", req.Subject.Tenant, attribute.TargetContact).
			OrderExpr("position ASC, created_at ASC").Scan(ctx); err != nil {
			return errors.Wrap(err, "load contact attribute definitions for export")
		}

		headers := []any{"编号", "名字", "类型", "邮箱", "用户编号", "语言", "所在地", "时区", "来源页面", "标签", "内部备注", "最近活跃", "创建时间"}
		for _, def := range attrDefs {
			headers = append(headers, def.Name)
		}
		if err := w.Write(headers...); err != nil {
			return err
		}

		var rows []Contact
		q := baseQuery()
		if req.Sort == ByCreated {
			q = q.OrderExpr("contact.created_at DESC, contact.id DESC")
		} else {
			q = q.OrderExpr("COALESCE(contact.last_seen_at, contact.created_at) DESC, contact.id DESC")
		}
		if err := q.Scan(ctx, &rows); err != nil {
			return errors.Wrap(err, "load contacts for export")
		}
		if len(rows) == 0 {
			return nil
		}

		// 批量查出所有待导出联系人的标签名称
		contactIDs := make([]string, len(rows))
		for i := range rows {
			contactIDs[i] = rows[i].ID
		}
		var tagRows []struct {
			ContactID string `bun:"contact_id"`
			TagName   string `bun:"tag_name"`
		}
		if err := a.db.NewSelect().
			TableExpr("contact_tags AS ct").
			Join("JOIN tags AS t ON t.id = ct.tag_id AND t.tenant_id = ct.tenant_id").
			Column("ct.contact_id").
			ColumnExpr("t.name AS tag_name").
			Where("ct.tenant_id = ? AND ct.contact_id IN (?)", req.Subject.Tenant, bun.In(contactIDs)).
			OrderExpr("t.name ASC").
			Scan(ctx, &tagRows); err != nil {
			return errors.Wrap(err, "load contact tags for export")
		}
		tagsByContact := make(map[string][]string)
		for _, tr := range tagRows {
			tagsByContact[tr.ContactID] = append(tagsByContact[tr.ContactID], tr.TagName)
		}

		for i := range rows {
			c := &rows[i]
			var lastSeen any = ""
			if c.LastSeenAt != nil {
				lastSeen = *c.LastSeenAt
			}
			line := []any{
				c.Number,
				c.Name,
				string(c.Kind),
				c.Email,
				c.ExternalUserID,
				c.Language,
				c.Location,
				c.TimeZone,
				c.Referrer,
				strings.Join(tagsByContact[c.ID], ", "),
				c.Notes,
				lastSeen,
				c.CreatedAt,
			}
			for _, def := range attrDefs {
				line = append(line, formatExportAttr(c.Attributes[def.Key]))
			}
			if err := w.Write(line...); err != nil {
				return err
			}
		}
		return nil
	}, a.saved)
}

// formatExportAttr 把自定义属性值转换为适合表格导出的标量。
func formatExportAttr(v any) any {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, ", ")
	case []string:
		return strings.Join(x, ", ")
	default:
		return x
	}
}

// saved 在每次导出进度持久化事务提交后广播事件，触发 SSE 客户端刷新。
func (a *QueueExport) saved(_ context.Context, tx database.Tx, exp *exports.Export) error {
	if a.hub == nil {
		return nil
	}
	topic := exportOperationTopic(exp.ID)
	tx.AfterCommit(func(ctx context.Context) error {
		return a.hub.Publish(ctx, topic, nil)
	})
	return nil
}

// RegisterJobs 在队列 worker 中注册联系人后台导出任务。
func RegisterJobs(w *queue.Worker, db *bun.DB, hub *broadcast.Hub, exporter *exports.Service) {
	ExportJob.Handle(w, NewQueueExport(db, hub, exporter).Job)
}
