import type { ConversationItemDTO, MemberTenantMember } from "@agents-desk/client/tenant"
import type { useT } from "@go-tenon/ui"
import { languageName } from "../languages"
import { fullDateTime } from "./format"

// 系统事件在时间线中显示成一句话，例如“张三 把会话指派给 李四”。事件的名称和数据由服务端的会话动作写入。

/** Names 用来把事件中的 ID 换成名字。 */
export interface Names {
  member: (id: string) => string
  team: (id: string) => string
}

type T = ReturnType<typeof useT>

/** actorName 返回事件数据中的参与者 {type, id} 的名字；为空时返回 undefined。 */
function actorName(v: unknown, names: Names, t: T): string | undefined {
  const a = v as { type?: string; id?: string } | null | undefined
  if (!a?.type) return undefined
  if (a.type === "member" && a.id) return names.member(a.id)
  return t("event.system")
}

/** whoDid 返回事件的发起者：成员的名字、外部发言人、客户或“系统”。 */
export function whoDid(it: ConversationItemDTO, members: Map<string, MemberTenantMember>, t: T): string {
  if (it.actor.type === "member") return members.get(it.actor.id)?.name ?? t("event.someone")
  if (it.actor.type === "external_user") return it.actor.name || it.actor.external_id || t("event.customer")
  if (it.actor.type === "contact") return t("event.customer")
  return t("event.system")
}

/** formatAttrValue 把时间线属性变更中的值格式化成可读文字。 */
function formatAttrValue(v: unknown, t: T): string {
  if (typeof v === "boolean") return t(v ? "event.attr_yes" : "event.attr_no")
  if (Array.isArray(v)) return v.map((x) => String(x)).join(", ")
  return String(v)
}

/** formatAttrChanges 把一次属性修改中的各项变更拼成可读摘要。 */
function formatAttrChanges(raw: unknown, t: T): string {
  if (!Array.isArray(raw)) return ""
  return raw
    .map((item) => {
      const ch = item as { key?: string; name?: string; from?: unknown; to?: unknown }
      const name = ch.name || ch.key || ""
      if (ch.to === null || ch.to === undefined) return t("event.attr_cleared", { name })
      if (ch.from === null || ch.from === undefined) return t("event.attr_set", { name, to: formatAttrValue(ch.to, t) })
      return t("event.attr_changed", { name, from: formatAttrValue(ch.from, t), to: formatAttrValue(ch.to, t) })
    })
    .filter(Boolean)
    .join(", ")
}

/** describeEvent 返回系统事件 it 的一句话，语言名按界面语言 ui。不认识的事件显示事件名，以后加的事件不会显示成空白。 */
export function describeEvent(
  it: ConversationItemDTO,
  members: Map<string, MemberTenantMember>,
  names: Names,
  t: T,
  ui: string,
  timeZone?: string,
): string {
  const who = whoDid(it, members, t)
  const d = (it.content.data ?? {}) as Record<string, unknown>
  switch (it.content.event) {
    case "conversation.assigned": {
      const assignee = actorName(d.assignee, names, t)
      const team = typeof d.team_id === "string" ? names.team(d.team_id) : undefined
      const teamChanged = d.team_id !== d.from_team_id
      const via =
        d.reason === "round_robin"
          ? t("event.via_round_robin")
          : d.reason === "reply"
            ? t("event.via_reply")
            : d.reason === "default"
              ? t("event.via_default")
              : d.reason === "sla_timeout"
                ? t("event.via_sla_timeout")
                : d.reason === "handover"
                  ? t("event.via_handover")
                  : ""
      if (assignee && teamChanged && team) return t("event.assigned_team_member", { who, team, assignee }) + via
      if (assignee) return t("event.assigned", { who, assignee }) + via
      if (teamChanged && team) return t("event.moved_team", { who, team })
      if (teamChanged) return t("event.removed_team", { who })
      return t("event.unassigned", { who })
    }
    case "conversation.closed":
      return d.reason === "auto_close" ? t("event.closed_auto") : t("event.closed", { who })
    case "conversation.marked_spam":
      return t(d.block_contact ? "event.marked_spam_blocked" : "event.marked_spam", { who })
    case "conversation.unmarked_spam":
      return t("event.unmarked_spam", { who })
    case "conversation.reopened":
      return t("event.reopened", { who })
    case "conversation.snoozed":
      return t("event.snoozed", { who, until: fullDateTime(String(d.until), ui, timeZone) })
    case "conversation.unsnoozed":
      return t("event.unsnoozed", { who })
    case "conversation.priority_changed":
      return t(d.to === "high" ? "event.priority_high" : "event.priority_normal", { who })
    case "conversation.language_set":
      return t("event.language_set", { who, lang: languageName(String(d.to), ui) })
    case "conversation.language_auto":
      return t("event.language_auto", { who })
    case "conversation.tag_added":
      return t("event.conv_tag_added", { who, tag: String(d.name ?? "") })
    case "conversation.tag_removed":
      return t("event.conv_tag_removed", { who, tag: String(d.name ?? "") })
    case "conversation.attributes_changed":
      return t("event.conv_attrs_changed", { who, changes: formatAttrChanges(d.changes, t) })
    case "contact.tag_added":
      return t("event.contact_tag_added", { who, tag: String(d.name ?? "") })
    case "contact.tag_removed":
      return t("event.contact_tag_removed", { who, tag: String(d.name ?? "") })
    case "contact.attributes_changed":
      return t("event.contact_attrs_changed", { who, changes: formatAttrChanges(d.changes, t) })
    case "conversation.rated":
      return d.comment
        ? t("event.rated_comment", { score: Number(d.score), comment: String(d.comment) })
        : t("event.rated", { score: Number(d.score) })
    case "conversation.rating_requested":
      return t("event.rating_requested")
    case "contact.email_provided":
      return t("event.contact_email_provided", { email: String(d.email ?? "") })
    case "conversation.merged": {
      const ch = String(d.source_channel_name ?? "").trim()
      const count = Number(d.moved_items ?? 0)
      return ch ? t("event.merged", { who, channel: ch, count }) : t("event.merged_unknown", { who, count })
    }
    case "ticket.created": {
      const typeName = String(d.type_name ?? "")
      if (!typeName) {
        return t("event.ticket_created_generic", { who, number: Number(d.number ?? 0) })
      }
      return t("event.ticket_created", {
        who,
        number: Number(d.number ?? 0),
        type: typeName,
      })
    }
    case "ticket.updated":
      return t("event.ticket_updated", { who })
    case "ticket.status_changed": {
      if (d.reason === "customer_replied") {
        return t("event.ticket_resumed_by_customer")
      }
      const statusText = (s: unknown) => {
        switch (String(s ?? "")) {
          case "submitted":
            return t("tickets.status_submitted")
          case "in_progress":
            return t("tickets.status_in_progress")
          case "waiting_on_customer":
            return t("tickets.status_waiting_on_customer")
          case "resolved":
            return t("tickets.status_resolved")
          default:
            return String(s ?? "")
        }
      }
      return t("event.ticket_status_changed", { who, from: statusText(d.from), to: statusText(d.to) })
    }
    case "ticket.contact_linked": {
      const contactName = String(d.contact_name ?? "").trim()
      return contactName ? t("event.ticket_contact_linked_name", { who, name: contactName }) : t("event.ticket_contact_linked", { who })
    }
    case "ticket.contact_unlinked": {
      const contactName = String(d.contact_name ?? "").trim()
      return contactName ? t("event.ticket_contact_unlinked_name", { who, name: contactName }) : t("event.ticket_contact_unlinked", { who })
    }
    case "conversation.ticket_created":
      return t("event.conv_ticket_created", { who, number: Number(d.number ?? 0), title: String(d.title ?? "") })
    case "conversation.ticket_linked":
      return t("event.conv_ticket_linked", { who, number: Number(d.number ?? 0), title: String(d.title ?? "") })
    case "conversation.ticket_unlinked":
      return t("event.conv_ticket_unlinked", { who, number: Number(d.number ?? 0) })
    case "chat.created":
      return t("event.chat_created", { who, name: String(d.name ?? "") })
    case "chat.renamed":
      return t("event.chat_renamed", { who, name: String(d.to ?? "") })
    case "chat.member_added": {
      const ids = Array.isArray(d.member_ids) ? d.member_ids.map(String) : []
      const addedNames = ids.map((id) => names.member(id)).filter(Boolean).join(", ")
      return t("event.chat_member_added", { who, names: addedNames || t("event.someone") })
    }
    case "chat.member_removed": {
      const target = typeof d.participant_id === "string" ? names.member(d.participant_id) : t("event.someone")
      return t("event.chat_member_removed", { who, name: target })
    }
    case "chat.member_left":
      return t("event.chat_member_left", { who })
    default:
      return it.content.event ?? ""
  }
}
