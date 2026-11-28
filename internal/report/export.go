package report

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-tenon/tenon/authz"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"
	"github.com/go-tenon/tenon/period"
	"github.com/go-tenon/tenon/sheet"
	"github.com/uptrace/bun"

	"agents-desk/internal/channel"
)

// ErrUnsupportedReport 是请求的导出报表类型无效。
var ErrUnsupportedReport = errorx.Define(errorx.InvalidArgument, "report.unsupported_type")

// Exporter 生成并输出各业务报表的 CSV/XLSX 同步导出文件。
type Exporter struct {
	db    *bun.DB
	roles *authz.Store
	files *filesystem.Service
}

// NewExporter 返回报表同步导出器。
func NewExporter(db *bun.DB, roles *authz.Store, files *filesystem.Service) *Exporter {
	return &Exporter{db: db, roles: roles, files: files}
}

// ServeHTTP 解析报表查询参数并直接响应 Excel 或 CSV 文件下载流。
func (e *Exporter) ServeHTTP(w http.ResponseWriter, r *http.Request, sub identity.Subject) error {
	q := r.URL.Query()
	reportKind := strings.TrimSpace(q.Get("report"))
	format := sheet.Format(strings.ToLower(strings.TrimSpace(q.Get("format"))))
	if format == "" {
		format = sheet.XLSX
	}
	if format != sheet.XLSX && format != sheet.CSV {
		return sheet.ErrFormat.New()
	}

	p := parsePeriodParams(q)
	common := FilterParams{
		Params:    p,
		Audience:  channel.Audience(q.Get("audience")),
		TeamID:    q.Get("team_id"),
		ChannelID: q.Get("channel_id"),
	}

	var buf bytes.Buffer
	var filename string
	err := sheet.Write(&buf, format, func(rw sheet.RowWriter) error {
		ctx := r.Context()
		switch reportKind {
		case "conversations":
			rep, err := NewConversations(e.db).Handle(ctx, sub, common)
			if err != nil {
				return err
			}
			filename = fmt.Sprintf("conversations-report-%s-%s.%s", rep.Period.From, rep.Period.To, format)
			return writeConversationsSheet(rw, rep)
		case "members":
			rep, err := NewMembers(e.db, e.roles, e.files).Handle(ctx, sub, common)
			if err != nil {
				return err
			}
			filename = fmt.Sprintf("members-report-%s-%s.%s", rep.Period.From, rep.Period.To, format)
			return writeMembersSheet(rw, rep)
		case "satisfaction":
			rep, err := NewSatisfaction(e.db).Handle(ctx, sub, common)
			if err != nil {
				return err
			}
			filename = fmt.Sprintf("satisfaction-report-%s-%s.%s", rep.Period.From, rep.Period.To, format)
			return writeSatisfactionSheet(rw, rep)
		case "response_time":
			rep, err := NewResponseTime(e.db).Handle(ctx, sub, common)
			if err != nil {
				return err
			}
			filename = fmt.Sprintf("response-time-report-%s-%s.%s", rep.Period.From, rep.Period.To, format)
			return writeResponseTimeSheet(rw, rep)
		case "tickets":
			rep, err := NewTickets(e.db, e.roles, e.files).Handle(ctx, sub, TicketFilterParams{
				Params:     p,
				TeamID:     q.Get("team_id"),
				TypeID:     q.Get("type_id"),
				Visibility: q.Get("visibility"),
			})
			if err != nil {
				return err
			}
			filename = fmt.Sprintf("tickets-report-%s-%s.%s", rep.Period.From, rep.Period.To, format)
			return writeTicketsSheet(rw, rep)
		case "business":
			rep, err := NewBusiness(e.db, e.roles, e.files).Handle(ctx, sub, BusinessFilterParams{
				Params:      p,
				ChannelKind: q.Get("channel_kind"),
				OwnerID:     q.Get("owner_id"),
			})
			if err != nil {
				return err
			}
			filename = fmt.Sprintf("business-report-%s-%s.%s", rep.Period.From, rep.Period.To, format)
			return writeBusinessSheet(rw, rep)
		default:
			return ErrUnsupportedReport.New()
		}
	})
	if err != nil {
		return err
	}

	if format == sheet.CSV {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	}
	w.Header().Set("Content-Disposition", sheet.ContentDisposition(filename))
	_, err = w.Write(buf.Bytes())
	return err
}

func parsePeriodParams(q url.Values) period.Params {
	return period.Params{
		From:        q.Get("from"),
		To:          q.Get("to"),
		TimeZone:    q.Get("tz"),
		Granularity: period.Granularity(q.Get("granularity")),
	}
}

func fmtPtrFloat(v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%.1f", *v)
}

func writeConversationsSheet(rw sheet.RowWriter, rep *ConversationsReport) error {
	if err := rw.Write("模块", "日期 / 维度", "新建 / 计数", "重开", "关闭"); err != nil {
		return err
	}
	if err := rw.Write("汇总", fmt.Sprintf("%s ~ %s (%s)", rep.Period.From, rep.Period.To, rep.Period.TimeZone),
		rep.Summary.Created, rep.Summary.Reopened, rep.Summary.Closed); err != nil {
		return err
	}
	for _, pt := range rep.Series {
		if err := rw.Write("趋势", pt.Date, pt.Created, pt.Reopened, pt.Closed); err != nil {
			return err
		}
	}
	for _, ch := range rep.ByChannel {
		if err := rw.Write("按渠道", ch.Name, ch.Created, 0, ch.Closed); err != nil {
			return err
		}
	}
	for _, tm := range rep.ByTeam {
		if err := rw.Write("按团队", tm.Name, tm.Created, 0, tm.Closed); err != nil {
			return err
		}
	}
	for _, tg := range rep.ByTag {
		if err := rw.Write("按标签", tg.Name, tg.Count, 0, 0); err != nil {
			return err
		}
	}
	return nil
}

func writeMembersSheet(rw sheet.RowWriter, rep *MembersReport) error {
	if err := rw.Write("成员编号", "姓名", "状态", "已关闭轮次", "发出回复数", "当前负责打开会话", "首响中位秒数", "解决中位秒数", "评价数", "CSAT(%)", "已解决工单", "当前未解决工单", "商务消息数"); err != nil {
		return err
	}
	for _, m := range rep.Items {
		status := m.OnlineStatus
		if m.Removed {
			status = "removed"
		}
		if err := rw.Write(
			m.MemberID, m.Name, status, m.ClosedCount, m.RepliesSent, m.OpenAssigned,
			fmtPtrFloat(m.FirstReplyMedian), fmtPtrFloat(m.ResolutionMedian),
			m.RatedCount, fmtPtrFloat(m.CSAT), m.TicketsResolved, m.TicketsOpen, m.BusinessMessages,
		); err != nil {
			return err
		}
	}
	return nil
}

func writeSatisfactionSheet(rw sheet.RowWriter, rep *SatisfactionReport) error {
	if err := rw.Write("模块", "日期 / 维度", "评价数", "好评数", "CSAT(%)", "平均分"); err != nil {
		return err
	}
	if err := rw.Write("汇总", fmt.Sprintf("%s ~ %s", rep.Period.From, rep.Period.To),
		rep.Summary.RatedCount, rep.Summary.PositiveCount, fmtPtrFloat(rep.Summary.CSAT), fmtPtrFloat(rep.Summary.AvgScore)); err != nil {
		return err
	}
	for _, pt := range rep.Series {
		if err := rw.Write("趋势", pt.Date, pt.RatedCount, pt.PositiveCount, fmtPtrFloat(pt.CSAT), fmtPtrFloat(pt.AvgScore)); err != nil {
			return err
		}
	}
	for _, tm := range rep.ByTeam {
		if err := rw.Write("按团队", tm.Name, tm.RatedCount, tm.PositiveCount, fmtPtrFloat(tm.CSAT), fmtPtrFloat(tm.AvgScore)); err != nil {
			return err
		}
	}
	for _, ch := range rep.ByChannel {
		if err := rw.Write("按渠道", ch.Name, ch.RatedCount, ch.PositiveCount, fmtPtrFloat(ch.CSAT), fmtPtrFloat(ch.AvgScore)); err != nil {
			return err
		}
	}
	return nil
}

func writeResponseTimeSheet(rw sheet.RowWriter, rep *ResponseTimeReport) error {
	if err := rw.Write("模块", "日期 / 维度", "首响样本", "首响中位秒数", "解决样本", "解决中位秒数"); err != nil {
		return err
	}
	if err := rw.Write("汇总", fmt.Sprintf("%s ~ %s", rep.Period.From, rep.Period.To),
		rep.FirstReply.Samples, fmtPtrFloat(rep.FirstReply.MedianSeconds), rep.Resolution.Samples, fmtPtrFloat(rep.Resolution.MedianSeconds)); err != nil {
		return err
	}
	for _, pt := range rep.Series {
		if err := rw.Write("趋势", pt.Date, pt.FirstReplySamples, fmtPtrFloat(pt.FirstReplyMedian), pt.ResolutionSamples, fmtPtrFloat(pt.ResolutionMedian)); err != nil {
			return err
		}
	}
	for _, tm := range rep.ByTeam {
		if err := rw.Write("按团队", tm.Name, tm.FirstReplySamples, fmtPtrFloat(tm.FirstReplyMedian), tm.ResolutionSamples, fmtPtrFloat(tm.ResolutionMedian)); err != nil {
			return err
		}
	}
	for _, ch := range rep.ByChannel {
		if err := rw.Write("按渠道", ch.Name, ch.FirstReplySamples, fmtPtrFloat(ch.FirstReplyMedian), ch.ResolutionSamples, fmtPtrFloat(ch.ResolutionMedian)); err != nil {
			return err
		}
	}
	return nil
}

func writeTicketsSheet(rw sheet.RowWriter, rep *TicketsReport) error {
	if err := rw.Write("模块", "日期 / 维度", "新建 / 总数", "已解决", "当前未解决", "解决中位秒数"); err != nil {
		return err
	}
	if err := rw.Write("汇总", fmt.Sprintf("%s ~ %s", rep.Period.From, rep.Period.To),
		rep.Summary.Created, rep.Summary.Resolved,
		rep.Summary.SubmittedNow+rep.Summary.InProgressNow+rep.Summary.WaitingNow,
		fmtPtrFloat(rep.Summary.ResolutionMedianSec)); err != nil {
		return err
	}
	for _, pt := range rep.Series {
		if err := rw.Write("趋势", pt.Date, pt.Created, pt.Resolved, "", ""); err != nil {
			return err
		}
	}
	for _, tp := range rep.ByType {
		if err := rw.Write("按工单类型", tp.Name, tp.Created, tp.Resolved, tp.OpenNow, ""); err != nil {
			return err
		}
	}
	for _, as := range rep.ByAssignee {
		if err := rw.Write("按负责人", as.Name, "", as.Resolved, as.OpenNow, fmtPtrFloat(as.ResolutionMedianSec)); err != nil {
			return err
		}
	}
	return nil
}

func writeBusinessSheet(rw sheet.RowWriter, rep *BusinessReport) error {
	if err := rw.Write("模块", "日期 / 维度", "活跃会话", "新增会话", "接收消息", "发出消息"); err != nil {
		return err
	}
	if err := rw.Write("汇总", fmt.Sprintf("%s ~ %s", rep.Period.From, rep.Period.To),
		rep.Summary.ActiveChats, rep.Summary.NewChats, rep.Summary.MessagesInbound, rep.Summary.MessagesOutbound); err != nil {
		return err
	}
	for _, pt := range rep.Series {
		if err := rw.Write("趋势", pt.Date, "", pt.NewChats, pt.Inbound, pt.Outbound); err != nil {
			return err
		}
	}
	for _, o := range rep.ByOwner {
		if err := rw.Write("按归属成员", o.Name, o.ActiveChats, o.NewChats, o.MessagesInbound, o.MessagesOutbound); err != nil {
			return err
		}
	}
	return nil
}
