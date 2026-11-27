package translation

import "agents-desk/internal/conversation"

// Accepts 报告时间线项 it 是否交给翻译任务：可译的消息和内部备注，以及会让工单标题、描述需要译文的工单事件。
// commit 用它过滤下游，系统事件和不需要翻译的项因此不会产生空任务。
func Accepts(it *conversation.Item) bool {
	if it.Kind == conversation.Event {
		switch it.Content.Event {
		case "ticket.created", "ticket.updated", "ticket.contact_linked":
			return true
		}
		return false
	}
	return it.Translatable()
}
