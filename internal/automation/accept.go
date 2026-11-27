package automation

import "agents-desk/internal/conversation"

// Accepts 报告时间线项 it 是否交给自动化：对端的消息（首次消息触发）和会改变工单状态、关闭会话的事件。
// commit 用它过滤下游，内部聊天、内部备注和其余事件因此不会产生空任务。
func Accepts(it *conversation.Item) bool {
	switch {
	case it.Kind == conversation.Message:
		return it.ActorType.IsExternal()
	case it.Kind == conversation.Event:
		return it.Content.Event == "conversation.closed" || it.Content.Event == "ticket.status_changed"
	}
	return false
}
