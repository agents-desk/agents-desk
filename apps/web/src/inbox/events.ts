import type { ConversationItemDTO, MemberTenantMember } from "@agents-desk/client/tenant"
import type { useT } from "@go-tenon/ui"
import { languageName } from "../languages"

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

/** whoDid 返回事件的发起者：成员的名字、客户，或者“系统”。 */
export function whoDid(it: ConversationItemDTO, members: Map<string, MemberTenantMember>, t: T): string {
  if (it.actor.type === "member") return members.get(it.actor.id)?.name ?? t("event.someone")
  if (it.actor.type === "contact") return t("event.customer")
  return t("event.system")
}

/** describeEvent 返回系统事件 it 的一句话，语言名按界面语言 ui。不认识的事件显示事件名，以后加的事件不会显示成空白。 */
export function describeEvent(it: ConversationItemDTO, members: Map<string, MemberTenantMember>, names: Names, t: T, ui: string): string {
  const who = whoDid(it, members, t)
  const d = (it.content.data ?? {}) as Record<string, unknown>
  switch (it.content.event) {
    case "conversation.assigned": {
      const assignee = actorName(d.assignee, names, t)
      const team = typeof d.team_id === "string" ? names.team(d.team_id) : undefined
      const teamChanged = d.team_id !== d.from_team_id
      const via = d.reason === "round_robin" ? t("event.via_round_robin") : d.reason === "reply" ? t("event.via_reply") : ""
      if (assignee && teamChanged && team) return t("event.assigned_team_member", { who, team, assignee }) + via
      if (assignee) return t("event.assigned", { who, assignee }) + via
      if (teamChanged && team) return t("event.moved_team", { who, team })
      if (teamChanged) return t("event.removed_team", { who })
      return t("event.unassigned", { who })
    }
    case "conversation.closed":
      return t("event.closed", { who })
    case "conversation.reopened":
      return t("event.reopened", { who })
    case "conversation.snoozed":
      return t("event.snoozed", { who, until: new Date(String(d.until)).toLocaleString() })
    case "conversation.unsnoozed":
      return t("event.unsnoozed", { who })
    case "conversation.priority_changed":
      return t(d.to === "high" ? "event.priority_high" : "event.priority_normal", { who })
    case "conversation.language_set":
      return t("event.language_set", { who, lang: languageName(String(d.to), ui) })
    case "conversation.language_auto":
      return t("event.language_auto", { who })
    case "conversation.rated":
      return d.comment
        ? t("event.rated_comment", { score: Number(d.score), comment: String(d.comment) })
        : t("event.rated", { score: Number(d.score) })
    default:
      return it.content.event ?? ""
  }
}
