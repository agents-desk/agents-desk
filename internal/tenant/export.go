package tenant

import (
	"context"
	"net/http"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/broadcast"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/httpx/rest"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/login"
	"github.com/go-tenon/tenon/notification"
	"github.com/go-tenon/tenon/notification/notificationapi"
	"github.com/go-tenon/tenon/operation"
	"github.com/go-tenon/tenon/queue"
	"github.com/go-tenon/tenon/sheet"
	"github.com/go-tenon/tenon/sheet/exports"
)

// ExportRequest 是后台导出工作区的队列任务载荷。
type ExportRequest struct {
	ID      uuid.UUID        `json:"id"`
	Subject identity.Subject `json:"subject"`
	Name    string           `json:"name,omitempty"`
	Status  Status           `json:"status,omitempty"`
}

// ExportJob 是后台导出工作区列表的队列主题。
var ExportJob = queue.Topic[ExportRequest]("tenants.export")

// exportColumns 定义导出工作区表格的列顺序与表头。
var exportColumns = sheet.Columns[TenantDTO]{
	{Key: "id", Header: "ID", Value: func(t TenantDTO) any { return t.ID }},
	{Key: "name", Header: "名称", Value: func(t TenantDTO) any { return t.Name }},
	{Key: "slug", Header: "地址", Value: func(t TenantDTO) any { return t.Slug }},
	{Key: "url", Header: "进入地址", Value: func(t TenantDTO) any { return t.URL }},
	{Key: "status", Header: "状态", Value: func(t TenantDTO) any { return string(t.Status) }},
	{Key: "owner_name", Header: "主管理员姓名", Value: func(t TenantDTO) any { return t.OwnerName }},
	{Key: "owner_email", Header: "主管理员邮箱", Value: func(t TenantDTO) any { return t.OwnerEmail }},
	{Key: "created_at", Header: "创建时间", Value: func(t TenantDTO) any { return t.CreatedAt }},
}

// ExportStartInput 是发起后台导出工作区的请求。
type ExportStartInput struct {
	Body struct {
		Format sheet.Format `json:"format,omitempty" enum:"xlsx,csv" doc:"导出格式，默认 xlsx"`
		Name   string       `json:"name,omitempty" maxLength:"60" doc:"按名称或 slug 筛选"`
		Status Status       `json:"status,omitempty" doc:"按状态筛选"`
	}
}

// ExportProgressInput 是查询导出进度事件流的路径参数。
type ExportProgressInput struct {
	ID uuid.UUID `path:"id"`
}

// ExportOutput 是发起后台导出的响应。
type ExportOutput struct {
	Body exports.Progress
}

// QueueExport 在后台队列中将工作区列表导出为表格文件，并实时推送进度与完成通知。
type QueueExport struct {
	db       *bun.DB
	logins   *login.Realm
	addr     Addresses
	hub      *broadcast.Hub
	exporter *exports.Service
}

// NewQueueExport 返回后台导出工作区的动作。
func NewQueueExport(db *bun.DB, logins *login.Realm, addr Addresses, hub *broadcast.Hub, exporter *exports.Service) *QueueExport {
	return &QueueExport{db: db, logins: logins, addr: addr, hub: hub, exporter: exporter}
}

// Handle 在事务中登记导出操作并发布队列任务，返回 pending 状态的导出记录。
func (a *QueueExport) Handle(ctx context.Context, subject identity.Subject, format sheet.Format, name string, status Status) (*exports.Export, error) {
	if format == "" {
		format = sheet.XLSX
	}
	var exp *exports.Export
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if exp, err = a.exporter.Start(ctx, tx, subject, "tenants.export", format); err != nil {
			return err
		}
		return ExportJob.Publish(ctx, tx, ExportRequest{ID: exp.ID, Subject: subject, Name: name, Status: status})
	})
	return exp, err
}

// formatExportProgress 在导出完成且结果存于私有盘时补充平台管理员端鉴权下载地址。
func formatExportProgress(ctx context.Context, exp *exports.Export) exports.Progress {
	out := *exp
	if out.Status == operation.StatusCompleted && out.FileID != uuid.Nil && out.URL == "" {
		out.URL = "/admin/api/tenants/exports/" + out.ID.String() + "/download"
	}
	return out.Progress(ctx)
}

// HTTP 处理 POST /tenants/exports：发起异步导出并返回操作 ID。
func (a *QueueExport) HTTP(ctx context.Context, in *ExportStartInput) (*ExportOutput, error) {
	subject, _ := identity.FromContext(ctx)
	exp, err := a.Handle(ctx, subject, in.Body.Format, in.Body.Name, in.Body.Status)
	if err != nil {
		return nil, err
	}
	return &ExportOutput{Body: formatExportProgress(ctx, exp)}, nil
}

// Progress 处理 GET /tenants/exports/{id}/progress：推送导出进度 SSE 事件直到完成或失败。
func (a *QueueExport) Progress(ctx context.Context, in *ExportProgressInput) (*huma.StreamResponse, error) {
	subject, _ := identity.FromContext(ctx)
	return a.exporter.ProgressSSE(ctx, a.hub, subject, in.ID, exports.ProgressOptions{Format: formatExportProgress})
}

// Job 是队列 worker 的处理函数：统计总数并逐行导出符合筛选条件的工作区，完成后上传文件并发送站内通知。
func (a *QueueExport) Job(ctx context.Context, req ExportRequest) error {
	return a.exporter.Process(ctx, req.ID, func(ctx context.Context, _ *exports.Export, w *exports.Writer) error {
		baseQuery := func() *bun.SelectQuery {
			q := a.db.NewSelect().Model((*Tenant)(nil))
			if req.Name != "" {
				like := "%" + database.EscapeLike(req.Name) + "%"
				q = q.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
					return q.Where(`tenant.name ILIKE ? ESCAPE '\'`, like).WhereOr(`tenant.slug ILIKE ? ESCAPE '\'`, like)
				})
			}
			switch req.Status {
			case Active:
				q = q.Where("tenant.disabled_at IS NULL")
			case Disabled:
				q = q.Where("tenant.disabled_at IS NOT NULL")
			}
			return q
		}
		total, err := baseQuery().Count(ctx)
		if err != nil {
			return errors.Wrap(err, "count tenants for export")
		}
		if err := w.SetTotal(int(total)); err != nil {
			return err
		}
		if err := w.Write(exportColumns.Header()...); err != nil {
			return err
		}
		const exportBatchSize = 500
		var hasCursor bool
		var cursorAt time.Time
		var cursorID string

		for {
			var rows []Tenant
			q := baseQuery().Relation("Owner").OrderExpr("tenant.created_at DESC, tenant.id DESC").Limit(exportBatchSize)
			if hasCursor {
				q = q.Where("(tenant.created_at, tenant.id) < (?, ?)", cursorAt, cursorID)
			}
			if err := q.Scan(ctx, &rows); err != nil {
				return errors.Wrap(err, "load tenants for export")
			}
			if len(rows) == 0 {
				break
			}

			ownerIDs := make([]string, 0, len(rows))
			for i := range rows {
				if rows[i].OwnerID != "" {
					ownerIDs = append(ownerIDs, rows[i].OwnerID)
				}
			}
			emails, err := a.logins.IdentifiersMany(ctx, ownerIDs, login.KindEmail)
			if err != nil {
				return err
			}

			for i := range rows {
				t := &rows[i]
				var ownerName string
				if t.Owner != nil {
					ownerName = t.Owner.Name
				}
				dto := TenantDTO{
					ID: t.ID, Name: t.Name, Slug: t.Slug, URL: a.addr.URL(t), Status: t.Status(),
					OwnerID: t.OwnerID, OwnerName: ownerName, OwnerEmail: emails[t.OwnerID], CreatedAt: t.CreatedAt,
				}
				if err := w.Write(exportColumns.Row(dto)...); err != nil {
					return err
				}
			}

			if len(rows) < exportBatchSize {
				break
			}
			last := rows[len(rows)-1]
			hasCursor = true
			cursorAt, cursorID = last.CreatedAt, last.ID
		}
		return nil
	}, a.saved)
}

// saved 在每次进度持久化事务中被调用：提交后广播进度更新，操作结束时给发起管理员写入站内通知。
func (a *QueueExport) saved(ctx context.Context, tx database.Tx, exp *exports.Export) error {
	if notifySaved := exports.BroadcastSaved(a.hub); notifySaved != nil {
		if err := notifySaved(ctx, tx, exp); err != nil {
			return err
		}
	}
	if !exp.Finished() {
		return nil
	}
	event := "tenants.exported"
	if exp.Status == operation.StatusFailed {
		event = "tenants.export_failed"
	}
	_, err := notificationapi.Send(ctx, tx, a.hub, notification.Notification{
		Event: event,
		Data: map[string]any{
			"operation_id": exp.ID.String(),
			"total":        exp.Total,
			"processed":    exp.Processed,
			"href":         "/tenants",
		},
	}, notification.RecipientOf(exp.Owner()))
	return err
}

// RegisterExportRoutes 在管理员端 API 注册工作区异步导出与进度事件流接口。
func RegisterExportRoutes(api *rest.API, requireRead func(*huma.Operation), db *bun.DB, logins *login.Realm, addr Addresses, exporter *exports.Service, hub *broadcast.Hub) {
	exp := NewQueueExport(db, logins, addr, hub, exporter)
	start := huma.Operation{
		OperationID:   "tenant-export-start",
		Method:        http.MethodPost,
		Path:          "/tenants/exports",
		Summary:       "发起工作区后台导出",
		DefaultStatus: http.StatusAccepted,
		Tags:          []string{"tenant"},
	}
	requireRead(&start)
	progress := huma.Operation{
		OperationID: "tenant-export-progress",
		Method:      http.MethodGet,
		Path:        "/tenants/exports/{id}/progress",
		Summary:     "工作区导出进度",
		Tags:        []string{"tenant"},
	}
	requireRead(&progress)

	rest.Register(api, start, exp.HTTP)
	rest.RegisterSSE(api, progress, map[string]any{"progress": exports.Progress{}}, exp.Progress)
}

// RegisterJobs 在队列 worker 中注册工作区后台导出任务。
func RegisterJobs(w *queue.Worker, db *bun.DB, logins *login.Realm, addr Addresses, hub *broadcast.Hub, exporter *exports.Service) {
	ExportJob.Handle(w, NewQueueExport(db, logins, addr, hub, exporter).Job)
}
