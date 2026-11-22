import type {
  AttributeDTO,
  ContactSummary,
  ConversationActions,
  ConversationAssigneeChange,
  ConversationDTO,
  ConversationPriority,
  ConversationTeamChange,
  MacroActions,
  MacroDTO,
  MacroSnooze,
  MemberTenantMember,
  TagDTO,
  TeamDTO,
} from "@agents-desk/client/tenant"
import { snoozeOptions } from "../inbox/menus"

/** TemplateVar 描述一条可插入快捷回复正文的模板变量。 */
export interface TemplateVar {
  key: string
  label: string
}

/** PendingStatus 是回复框中待执行的会话状态变更：关闭，或稍后处理到按成员时区展开后的具体时间点。 */
export type PendingStatus =
  | { kind: "close" }
  | { kind: "snooze"; preset: MacroSnooze; until: Date }

/** PendingActions 是载入回复框草稿、发送前可逐项查看与移除的待执行会话操作。 */
export interface PendingActions {
  team?: ConversationTeamChange
  assignee?: ConversationAssigneeChange
  priority?: ConversationPriority
  addTagIDs: string[]
  setAttributes: Record<string, unknown>
  status?: PendingStatus
}

/** MacroWarning 描述某项附带操作在当前会话或当前权限下被跳过的原因。 */
export type MacroWarning =
  | { kind: "no_assign_perm" }
  | { kind: "team_missing" }
  | { kind: "team_disabled"; name: string }
  | { kind: "member_missing" }
  | { kind: "tag_missing" }
  | { kind: "attr_missing"; key: string }

/** emptyPendingActions 返回空的待执行操作集合。 */
export function emptyPendingActions(): PendingActions {
  return { addTagIDs: [], setAttributes: {} }
}

/** hasPendingActions 报告待执行操作集合是否包含至少一项操作。 */
export function hasPendingActions(a: PendingActions): boolean {
  return Boolean(
    a.team ||
      a.assignee ||
      a.priority ||
      a.addTagIDs.length > 0 ||
      Object.keys(a.setAttributes).length > 0 ||
      a.status,
  )
}

/** countMacroActions 返回快捷回复模板配置的附带操作项数。 */
export function countMacroActions(a: MacroActions | undefined): number {
  if (!a) return 0
  let n = 0
  if (a.team) n++
  if (a.assignee) n++
  if (a.priority) n++
  if (a.add_tag_ids && a.add_tag_ids.length > 0) n += a.add_tag_ids.length
  if (a.set_attributes) n += Object.keys(a.set_attributes).length
  if (a.status) n++
  return n
}

const VAR_PATTERN = /\{\{\s*([a-zA-Z0-9_.-]+)(?:\s*\|\s*([^}]*))?\s*\}\}/g

/** formatAttrValue 把联系人自定义属性值格式化为适合嵌入回复正文的文本。 */
function formatAttrValue(v: unknown): string {
  if (v === null || v === undefined) return ""
  if (Array.isArray(v)) return v.map((x) => String(x)).join(", ")
  if (typeof v === "boolean") return v ? "true" : "false"
  return String(v)
}

/**
 * renderMacroBody 在浏览器本地 0ms 展开快捷回复模板正文：
 * 支持 {{contact.name}}、{{contact.email}}、{{contact.location}}、{{contact.attr.<key>}}、{{member.name}}，
 * 以及兜底值语法 {{key | 默认文字}}。未知变量名保持原样输出。
 */
export function renderMacroBody(
  body: string,
  ctx: { contact?: ContactSummary; memberName?: string },
): string {
  if (!body) return ""
  return body.replace(VAR_PATTERN, (raw, key: string, fallback?: string) => {
    const fb = fallback !== undefined ? fallback.trim() : ""
    let value: string | undefined
    switch (key) {
      case "contact.name":
        value = ctx.contact?.name ?? ""
        break
      case "contact.email":
        value = ctx.contact?.email ?? ""
        break
      case "contact.location":
        value = ctx.contact?.location ?? ""
        break
      case "member.name":
        value = ctx.memberName ?? ""
        break
      default:
        if (key.startsWith("contact.attr.")) {
          const attrKey = key.slice("contact.attr.".length)
          const attrs = ctx.contact?.attributes ?? {}
          value = attrKey in attrs ? formatAttrValue(attrs[attrKey]) : ""
        } else {
          return raw
        }
    }
    return value.trim() ? value : fb
  })
}

/**
 * resolveMacroActions 按当前会话、当前成员权限和工作区元数据把模板附带操作展开为可预览、可移除的 PendingActions，
 * 同时列出被跳过的失效引用或无权操作警告。
 */
export function resolveMacroActions(
  actions: MacroActions | undefined,
  ctx: {
    c: ConversationDTO
    meId?: string
    canAssignOthers: boolean
    teams: TeamDTO[]
    members: MemberTenantMember[]
    tags: TagDTO[]
    convAttrs: AttributeDTO[]
    zone?: string
  },
): { pending: PendingActions; warnings: MacroWarning[] } {
  const pending = emptyPendingActions()
  const warnings: MacroWarning[] = []
  if (!actions) return { pending, warnings }

  const teamMap = new Map(ctx.teams.map((t) => [t.id, t]))
  const memberMap = new Map(ctx.members.map((m) => [m.id, m]))
  const tagMap = new Map(ctx.tags.map((t) => [t.id, t]))
  const attrMap = new Map(ctx.convAttrs.map((a) => [a.key, a]))

  // 指派权限校验与引用检查：领取无负责人的会话或改自己负责的会话不需要 conversations:assign，其余需要
  const isMine = ctx.c.assignee?.type === "member" && ctx.c.assignee.id === ctx.meId
  const isUnassigned = !ctx.c.assignee
  let assignBlocked = false

  if (actions.team) {
    if (!ctx.canAssignOthers && !isMine) {
      assignBlocked = true
    } else if (actions.team.mode === "set") {
      const target = actions.team.id ? teamMap.get(actions.team.id) : undefined
      if (!target) {
        warnings.push({ kind: "team_missing" })
      } else if (target.status === "disabled") {
        warnings.push({ kind: "team_disabled", name: target.name })
      } else {
        pending.team = { mode: "set", id: target.id }
      }
    } else if (actions.team.mode === "clear") {
      pending.team = { mode: "clear" }
    }
  }

  if (actions.assignee) {
    const isClaim = actions.assignee.mode === "me" && isUnassigned && !actions.team
    if (!ctx.canAssignOthers && !isMine && !isClaim) {
      assignBlocked = true
    } else if (actions.assignee.mode === "member") {
      const target = actions.assignee.id ? memberMap.get(actions.assignee.id) : undefined
      if (!target) {
        warnings.push({ kind: "member_missing" })
      } else {
        pending.assignee = { mode: "member", id: target.id }
      }
    } else {
      pending.assignee = { mode: actions.assignee.mode }
    }
  }

  if (assignBlocked) {
    warnings.push({ kind: "no_assign_perm" })
  }

  if (actions.priority) {
    pending.priority = actions.priority
  }

  if (actions.add_tag_ids && actions.add_tag_ids.length > 0) {
    let missing = false
    for (const id of actions.add_tag_ids) {
      if (tagMap.has(id)) {
        if (!pending.addTagIDs.includes(id)) pending.addTagIDs.push(id)
      } else {
        missing = true
      }
    }
    if (missing) warnings.push({ kind: "tag_missing" })
  }

  if (actions.set_attributes) {
    for (const [k, v] of Object.entries(actions.set_attributes)) {
      if (attrMap.has(k)) {
        pending.setAttributes[k] = v
      } else {
        warnings.push({ kind: "attr_missing", key: k })
      }
    }
  }

  if (actions.status) {
    if (actions.status.kind === "close") {
      pending.status = { kind: "close" }
    } else if (actions.status.kind === "snooze" && actions.status.snooze) {
      const preset = actions.status.snooze
      const opt = snoozeOptions(ctx.zone).find((o) => o.key === preset)
      if (opt) {
        pending.status = { kind: "snooze", preset, until: opt.until }
      }
    }
  }

  return { pending, warnings }
}

/** mergePendingActions 把新载入的快捷回复附带操作合并到当前草稿的待执行操作中（标签取并集、属性合并、单值覆盖）。 */
export function mergePendingActions(base: PendingActions, incoming: PendingActions): PendingActions {
  return {
    team: incoming.team ?? base.team,
    assignee: incoming.assignee ?? base.assignee,
    priority: incoming.priority ?? base.priority,
    addTagIDs: [...new Set([...base.addTagIDs, ...incoming.addTagIDs])],
    setAttributes: { ...base.setAttributes, ...incoming.setAttributes },
    status: incoming.status ?? base.status,
  }
}

/** toConversationActions 把草稿中的 PendingActions 转换为后端提交所需的 ConversationActions；无操作时返回 undefined。 */
export function toConversationActions(pending: PendingActions): ConversationActions | undefined {
  if (!hasPendingActions(pending)) return undefined
  const out: ConversationActions = {}
  if (pending.team) out.team = pending.team
  if (pending.assignee) out.assignee = pending.assignee
  if (pending.priority) out.priority = pending.priority
  if (pending.addTagIDs.length > 0) out.add_tag_ids = pending.addTagIDs
  if (Object.keys(pending.setAttributes).length > 0) out.set_attributes = pending.setAttributes
  if (pending.status) {
    out.status =
      pending.status.kind === "close"
        ? { kind: "close" }
        : { kind: "snooze", until: pending.status.until.toISOString() }
  }
  return out
}

/** matchMacros 按输入关键词过滤并排序快捷回复：快捷指令码精确/前缀匹配优先，其次标题和正文匹配。 */
export function matchMacros(all: MacroDTO[], query: string): MacroDTO[] {
  const q = query.trim().toLowerCase()
  if (!q) return all.slice(0, 12)
  const scored: { item: MacroDTO; rank: number }[] = []
  for (const item of all) {
    const sc = item.shortcut.toLowerCase()
    const title = item.title.toLowerCase()
    const body = item.body.toLowerCase()
    let rank = -1
    if (sc === q) rank = 0
    else if (sc && sc.startsWith(q)) rank = 1
    else if (title.startsWith(q)) rank = 2
    else if (sc.includes(q)) rank = 3
    else if (title.includes(q)) rank = 4
    else if (body.includes(q)) rank = 5
    if (rank >= 0) scored.push({ item, rank })
  }
  scored.sort((a, b) => a.rank - b.rank)
  return scored.slice(0, 12).map((x) => x.item)
}
