package conversation

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

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

	"agents-desk/internal/auth"
	"agents-desk/internal/channel"
)

// ExportQuery 是后台导出会话列表时复用的筛选条件。
type ExportQuery struct {
	Inbox      Inbox            `json:"inbox,omitempty" doc:"收件箱分组：mine、mentions、unassigned、all、spam"`
	ViewID     string           `json:"view_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"按已保存视图筛选"`
	Filter     string           `json:"filter,omitempty" maxLength:"4096" doc:"JSON 编码的高级筛选条件"`
	TeamID     string           `json:"team_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"按团队收件箱筛选"`
	Status     Status           `json:"status,omitempty" doc:"按会话状态筛选"`
	AssigneeID string           `json:"assignee_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"按负责人筛选"`
	ChannelID  string           `json:"channel_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"按渠道筛选"`
	Audience   channel.Audience `json:"audience,omitempty" doc:"按服务对象筛选"`
	Priority   Priority         `json:"priority,omitempty" doc:"按优先级筛选"`
	TagID      string           `json:"tag_id,omitempty" pattern:"^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?$" doc:"按标签筛选"`
}

// ExportRequest 是后台导出会话的队列任务载荷。
type ExportRequest struct {
	ID      uuid.UUID        `json:"id"`
	Subject identity.Subject `json:"subject"`
	Query   ExportQuery      `json:"query"`
}

// ExportJob 是后台导出会话列表的队列主题。
var ExportJob = queue.Topic[ExportRequest]("conversations.export")

// ExportStartInput 是发起后台导出会话的 HTTP 请求。
type ExportStartInput struct {
	Body struct {
		Format sheet.Format `json:"format,omitempty" enum:"xlsx,csv" doc:"导出格式，默认 xlsx"`
		ExportQuery
	}
}

// ExportProgressInput 是查询会话导出进度事件流的路径参数。
type ExportProgressInput struct {
	ID uuid.UUID `path:"id"`
}

// ExportProgress 是返回给前端的会话导出进度与下载信息，附带失败原因译文。
type ExportProgress struct {
	*exports.Export
	ErrorMessage string `json:"error_message,omitempty"`
}

// ExportOutput 是发起后台导出的 HTTP 响应。
type ExportOutput struct {
	Body ExportProgress
}

// QueueExport 在后台队列中按发起人可见范围与筛选条件异步导出服务型会话表格并推送进度。
type QueueExport struct {
	db       *bun.DB
	hub      *broadcast.Hub
	exporter *exports.Service
}

// NewQueueExport 返回会话后台导出动作。
func NewQueueExport(db *bun.DB, hub *broadcast.Hub, exporter *exports.Service) *QueueExport {
	return &QueueExport{db: db, hub: hub, exporter: exporter}
}

func exportOperationTopic(id uuid.UUID) string { return "operation:" + id.String() }

// Handle 校验调用者的权限与筛选条件，在事务中登记导出操作并发布队列任务。
func (a *QueueExport) Handle(ctx context.Context, sub identity.Subject, format sheet.Format, query ExportQuery) (*exports.Export, error) {
	if err := authorize(sub, auth.ReportsRead); err != nil {
		return nil, err
	}
	if format == "" {
		format = sheet.XLSX
	}
	listIn := query.toListInput()
	if _, _, _, err := buildServiceListQuery(ctx, a.db, sub, listIn); err != nil {
		return nil, err
	}
	var exp *exports.Export
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		var err error
		if exp, err = a.exporter.Start(ctx, tx, sub, "conversations.export", format); err != nil {
			return err
		}
		return ExportJob.Publish(ctx, tx, ExportRequest{ID: exp.ID, Subject: sub, Query: query})
	})
	return exp, err
}

func (q ExportQuery) toListInput() *ListInput {
	inbox := q.Inbox
	if inbox == "" {
		inbox = InboxAll
	}
	return &ListInput{
		Inbox:      inbox,
		ViewID:     q.ViewID,
		Filter:     q.Filter,
		TeamID:     q.TeamID,
		Status:     q.Status,
		AssigneeID: q.AssigneeID,
		ChannelID:  q.ChannelID,
		Audience:   q.Audience,
		Priority:   q.Priority,
		TagID:      q.TagID,
	}
}

func formatConversationExportProgress(ctx context.Context, exp *exports.Export) ExportProgress {
	out := *exp
	if out.Status == operation.StatusCompleted && out.FileID != uuid.Nil && out.URL == "" {
		if slug := chi.URLParamFromCtx(ctx, "slug"); slug != "" {
			out.URL = "/t/" + url.PathEscape(slug) + "/api/conversations/exports/" + out.ID.String() + "/download"
		}
	}
	return ExportProgress{Export: &out, ErrorMessage: out.ErrorMessage(ctx)}
}

// HTTP 处理 POST /t/{slug}/api/conversations/exports。
func (a *QueueExport) HTTP(ctx context.Context, in *ExportStartInput) (*ExportOutput, error) {
	sub, _ := identity.FromContext(ctx)
	exp, err := a.Handle(ctx, sub, in.Body.Format, in.Body.ExportQuery)
	if err != nil {
		return nil, err
	}
	return &ExportOutput{Body: formatConversationExportProgress(ctx, exp)}, nil
}

// Progress 处理 GET /t/{slug}/api/conversations/exports/{id}/progress。
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
			p := formatConversationExportProgress(s.Context(), exp)
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

// Job 按发起人所在工作区和可见范围游标分批导出服务型会话表格。
func (a *QueueExport) Job(ctx context.Context, req ExportRequest) error {
	return a.exporter.Process(ctx, req.ID, func(ctx context.Context, _ *exports.Export, w *exports.Writer) error {
		listIn := req.Query.toListInput()
		countQuery, activeFilter, _, err := buildServiceListQuery(ctx, a.db, req.Subject, listIn)
		if err != nil {
			return err
		}
		if activeFilter == nil && listIn.Inbox == InboxMentions {
			countQuery = countQuery.Join(
				"JOIN (SELECT conversation_id FROM conversation_mentions WHERE tenant_id = ? AND member_id = ? GROUP BY conversation_id) AS m ON m.conversation_id = conversation.id",
				req.Subject.Tenant, req.Subject.ID,
			)
		}
		total, err := countQuery.Count(ctx)
		if err != nil {
			return errors.Wrap(err, "count conversations for export")
		}
		if err := w.SetTotal(int(total)); err != nil {
			return err
		}

		headers := []any{
			"会话编号", "状态", "优先级", "服务对象", "来源渠道", "团队收件箱", "负责人",
			"联系人", "联系人邮箱", "语言", "标签", "首次回复时间", "关闭时间", "满意度评分", "创建时间", "最近活动",
		}
		if err := w.Write(headers...); err != nil {
			return err
		}

		const exportBatchSize = 500
		var hasCursor bool
		var cursorAt time.Time
		var cursorID string

		for {
			q, af, _, err := buildServiceListQuery(ctx, a.db, req.Subject, listIn)
			if err != nil {
				return err
			}
			if af == nil && listIn.Inbox == InboxMentions {
				q = q.Join(
					"JOIN (SELECT conversation_id FROM conversation_mentions WHERE tenant_id = ? AND member_id = ? GROUP BY conversation_id) AS m ON m.conversation_id = conversation.id",
					req.Subject.Tenant, req.Subject.ID,
				)
			}
			if hasCursor {
				q = q.Where("(conversation.created_at, conversation.id) < (?, ?)", cursorAt, cursorID)
			}
			var rows []Conversation
			if err := q.OrderExpr("conversation.created_at DESC, conversation.id DESC").
				Limit(exportBatchSize).Scan(ctx, &rows); err != nil {
				return errors.Wrap(err, "load conversations for export")
			}
			if len(rows) == 0 {
				break
			}

			teamNames, err := loadExportTeamNames(ctx, a.db, req.Subject.Tenant, rows)
			if err != nil {
				return err
			}
			dtos, err := toDTOs(ctx, a.db, req.Subject, rows)
			if err != nil {
				return err
			}
			for i := range dtos {
				d := &dtos[i]
				tagNames := make([]string, 0, len(d.Tags))
				for _, tg := range d.Tags {
					tagNames = append(tagNames, tg.Name)
				}
				var chName, teamName, assigneeName, contactName, contactEmail string
				if d.Channel != nil {
					chName = d.Channel.Name
				}
				if d.TeamID != nil {
					teamName = teamNames[*d.TeamID]
				}
				if d.Assignee != nil {
					assigneeName = d.Assignee.Name
				}
				if d.Contact != nil {
					contactName = d.Contact.Name
					contactEmail = d.Contact.Email
				}
				var firstReply, closedAt any = "", ""
				if d.FirstReplyAt != nil {
					firstReply = *d.FirstReplyAt
				}
				if d.ClosedAt != nil {
					closedAt = *d.ClosedAt
				}
				var ratingScore any = ""
				if d.Rating != nil {
					ratingScore = d.Rating.Score
				}
				if err := w.Write(
					d.ID, string(d.Status), string(d.Priority), string(d.Audience),
					chName, teamName, assigneeName, contactName, contactEmail,
					d.Language, strings.Join(tagNames, ", "),
					firstReply, closedAt, ratingScore, d.CreatedAt, d.LastMessageAt,
				); err != nil {
					return err
				}
			}

			if len(rows) < exportBatchSize {
				break
			}
			last := rows[len(rows)-1]
			hasCursor = true
			cursorAt = last.CreatedAt
			cursorID = last.ID
		}
		return nil
	}, a.saved)
}

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

// RegisterExportRoutes 在工作区 API（/t/{slug}/api）中注册会话异步导出与进度查询接口（要求 reports:read 权限）。
func RegisterExportRoutes(api *rest.API, require func(...auth.Permission) func(*huma.Operation), db *bun.DB, hub *broadcast.Hub, exporter *exports.Service) {
	readReports := require(auth.ReportsRead)
	exp := NewQueueExport(db, hub, exporter)

	startOp := huma.Operation{OperationID: "conversation-export-start", Method: http.MethodPost, Path: "/conversations/exports", Summary: "发起会话后台导出", DefaultStatus: http.StatusAccepted}
	readReports(&startOp)
	rest.Register(api, startOp, exp.HTTP)

	progressOp := huma.Operation{OperationID: "conversation-export-progress", Method: http.MethodGet, Path: "/conversations/exports/{id}/progress", Summary: "会话导出进度"}
	readReports(&progressOp)
	rest.RegisterSSE(api, progressOp, map[string]any{"progress": ExportProgress{}}, exp.Progress)
}

// loadExportTeamNames 批量读取当前批会话涉及的团队收件箱名称。
func loadExportTeamNames(ctx context.Context, db bun.IDB, tenantID string, rows []Conversation) (map[string]string, error) {
	seen := make(map[string]bool)
	var teamIDs []string
	for i := range rows {
		if rows[i].TeamID != nil && *rows[i].TeamID != "" && !seen[*rows[i].TeamID] {
			seen[*rows[i].TeamID] = true
			teamIDs = append(teamIDs, *rows[i].TeamID)
		}
	}
	names := make(map[string]string, len(teamIDs))
	if len(teamIDs) == 0 {
		return names, nil
	}
	var list []struct {
		ID   string `bun:"id"`
		Name string `bun:"name"`
	}
	if err := db.NewSelect().Table("teams").Column("id", "name").
		Where("tenant_id = ? AND id IN (?)", tenantID, bun.In(teamIDs)).Scan(ctx, &list); err != nil {
		return nil, errors.Wrap(err, "load team names for conversation export")
	}
	for _, r := range list {
		names[r.ID] = r.Name
	}
	return names, nil
}

// RegisterExportJobs 在队列 worker 上注册会话后台导出任务。
func RegisterExportJobs(w *queue.Worker, db *bun.DB, hub *broadcast.Hub, exporter *exports.Service) {
	ExportJob.Handle(w, NewQueueExport(db, hub, exporter).Job)
}
