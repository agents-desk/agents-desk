import type { ConversationPrompt } from "@agents-desk/client/widget"
import { Button, Group, Paper, Stack, Text, TextInput } from "@mantine/core"
import { IconCheck } from "@tabler/icons-react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { describeError } from "@go-tenon/client"
import { useT } from "@go-tenon/ui/core"
import { type FormEvent, type ReactNode, useState } from "react"
import { authed, widgetApi } from "../api"
import { identity } from "../identity"
import { useIdentity } from "../realtime"

type T = ReturnType<typeof useT>

/** TicketPromptTarget 是工单进度或状态变更系统消息对应的工单跳转目标。 */
export interface TicketPromptTarget {
  id: string
  number: number
}

/** ticketPromptTarget 从工单类系统提示中提取工单 ID 与编号，用于渲染直达工单详情的操作入口。 */
export function ticketPromptTarget(prompt: ConversationPrompt | undefined): TicketPromptTarget | null {
  if (!prompt || (prompt.kind !== "ticket_update" && prompt.kind !== "ticket_status_changed")) return null
  const params = prompt.params ?? {}
  const id = typeof params.ticket_id === "string" ? params.ticket_id : ""
  const num = Number(params.number ?? params.ticket_number ?? 0)
  if (!id || !(num > 0)) return null
  return { id, number: num }
}

/** formatNextOpen 把 RFC3339 时刻按客户当前的语言和本地时区格式化成简短的日期与时间。 */
function formatNextOpen(iso: string, language: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return new Intl.DateTimeFormat(language, {
    month: "short",
    day: "numeric",
    weekday: "short",
    hour: "2-digit",
    minute: "2-digit",
  }).format(d)
}

/** promptText 把系统发出的结构化提示按客户当前的语言格式化成一句可读文字；不认识的种类回退到 fallback。 */
export function promptText(prompt: ConversationPrompt | undefined, fallback: string, language: string, t: T): string {
  if (!prompt) return fallback
  const params = prompt.params ?? {}
  switch (prompt.kind) {
    case "reply_time_open": {
      const rt = params.reply_time
      if (rt === "minutes") return t("prompt.reply_time_minutes")
      if (rt === "hour") return t("prompt.reply_time_hour")
      if (rt === "hours") return t("prompt.reply_time_hours")
      if (rt === "day") return t("prompt.reply_time_day")
      return fallback
    }
    case "reply_time_closed": {
      const next = typeof params.next_open_at === "string" ? params.next_open_at : ""
      if (!next) return t("prompt.reply_time_closed_unknown")
      return t("prompt.reply_time_closed", { time: formatNextOpen(next, language) })
    }
    case "collect_contact":
      return t("prompt.collect_contact")
    case "ticket_update": {
      const num = Number(params.number ?? params.ticket_number ?? 0)
      if (num > 0 && fallback) return t("prompt.ticket_update", { number: num, text: fallback })
      return fallback
    }
    case "ticket_status_changed": {
      const num = Number(params.number ?? params.ticket_number ?? 0)
      const title =
        typeof params.title === "string" ? params.title : typeof params.ticket_title === "string" ? params.ticket_title : ""
      const status = typeof params.status === "string" ? params.status : typeof params.to === "string" ? params.to : ""
      const statusLabel =
        status === "submitted"
          ? t("tickets.status_submitted")
          : status === "in_progress"
            ? t("tickets.status_in_progress")
            : status === "waiting_on_customer"
              ? t("tickets.status_waiting_on_customer")
              : status === "resolved"
                ? t("tickets.status_resolved")
                : status
      if (num > 0 && statusLabel) return t("prompt.ticket_status_changed", { number: num, title, status: statusLabel })
      return fallback
    }
    default:
      return fallback
  }
}

/** EMAIL 是前端提交前的轻量邮箱格式校验，最终校验仍在服务端。 */
const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

/**
 * CollectEmailCard 是时间线内请线索留下邮箱的交互卡片：
 * 展示状态直接由联系人当前的 email 推导（时间线本身只追加不修改）——还没有邮箱时显示输入框和提交按钮，
 * 提交成功或之前已经留过邮箱时显示只读的已留邮箱提示。
 */
export function CollectEmailCard({ conversationId, color }: { conversationId?: string; color: string }): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const { contact } = useIdentity()
  const [email, setEmail] = useState("")
  const [invalid, setInvalid] = useState(false)
  const [submitError, setSubmitError] = useState<string | null>(null)

  const submit = useMutation({
    mutationFn: (value: string) => authed(() => widgetApi.contact.email({ email: value, conversation_id: conversationId || undefined })),
    onSuccess: (updated) => {
      setSubmitError(null)
      identity.updateContact(updated)
      void queryClient.invalidateQueries({ queryKey: ["widget", contact?.id] })
    },
    onError: (err) => setSubmitError(describeError(err).message ?? t("chat.failed")),
  })

  if (contact?.email) {
    return (
      <Paper withBorder radius="lg" p="sm" style={{ alignSelf: "flex-start", maxWidth: "88%" }}>
        <Group gap={6} wrap="nowrap">
          <IconCheck size={16} color="var(--mantine-color-green-6)" style={{ flex: "none" }} />
          <Text size="sm" c="green">
            {t("collect_email.saved", { email: contact.email })}
          </Text>
        </Group>
      </Paper>
    )
  }

  // 已登录用户无需通过访客表单留邮箱
  if (contact && contact.kind !== "lead") {
    return null
  }

  const onSubmit = (e: FormEvent) => {
    e.preventDefault()
    const trimmed = email.trim()
    if (!EMAIL.test(trimmed)) {
      setInvalid(true)
      return
    }
    setInvalid(false)
    submit.mutate(trimmed)
  }

  return (
    <Paper withBorder radius="lg" p="md" style={{ alignSelf: "flex-start", maxWidth: "88%", width: "100%" }}>
      <form noValidate onSubmit={onSubmit}>
        <Stack gap="xs">
          <Text size="sm" fw={600}>
            {t("collect_email.title")}
          </Text>
          <TextInput
            type="email"
            value={email}
            onChange={(e) => {
              setEmail(e.currentTarget.value)
              if (invalid) setInvalid(false)
              if (submitError) setSubmitError(null)
            }}
            placeholder={t("collect_email.placeholder")}
            error={invalid ? t("collect_email.invalid") : (submitError ?? undefined)}
            size="sm"
            autoComplete="email"
          />
          <Button type="submit" size="sm" color={color} loading={submit.isPending} fullWidth>
            {t("collect_email.submit")}
          </Button>
        </Stack>
      </form>
    </Paper>
  )
}
