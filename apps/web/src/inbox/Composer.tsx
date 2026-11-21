import type { ConversationDTO, MemberTenantMember } from "@agents-desk/client/tenant"
import { Avatar, Box, Button, Group, Menu, Paper, SegmentedControl, Text, Textarea, UnstyledButton } from "@mantine/core"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { IconChevronDown } from "@tabler/icons-react"
import { notifyError, useT, useTimeZone } from "@go-tenon/ui"
import { type ReactNode, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { useMe, useMembers } from "./data"
import { snoozeOptions } from "./menus"

/** Mode 是回复框的模式：回复发给对方；内部备注只有成员可见，可以 @ 同事。 */
type Mode = "reply" | "note"

/** After 是发送之后对会话做的事：什么也不做、关闭、稍后处理到某个时间。 */
type After = { kind: "none" } | { kind: "close" } | { kind: "snooze"; until: Date }

/**
 * Composer 是会话底部的回复框：回复和内部备注两种模式，内部备注时变成黄色底，输入 @ 选择同事。
 * 回车发送，Shift + 回车换行，用输入法选词时的回车只确认候选词。发送按钮的下拉中有“发送并关闭”“发送并稍后处理”。
 * 输入时调用 onTyping，会话的频道据此告诉别人“正在输入”。
 */
export function Composer({ c, onTyping }: { c: ConversationDTO; onTyping: () => void }): ReactNode {
  const t = useT()
  const zone = useTimeZone()
  const queryClient = useQueryClient()
  const me = useMe()
  const { list: members } = useMembers()
  const [mode, setMode] = useState<Mode>("reply")
  const [text, setText] = useState("")
  const [mentions, setMentions] = useState<MemberTenantMember[]>([])
  const [query, setQuery] = useState<string | null>(null) // 正在输入的 @ 后面的文字；没有在 @ 时为 null
  const [pick, setPick] = useState(0)
  const area = useRef<HTMLTextAreaElement>(null)

  const send = useMutation({
    mutationFn: async (after: After) => {
      const value = text.trim()
      if (mode === "note") {
        // 只算还留在文字中的 @
        const ids = mentions.filter((m) => value.includes(`@${m.name}`)).map((m) => m.id)
        await tenantApi.conversation.note(c.id, { text: value, mentions: ids.length > 0 ? ids : undefined })
      } else {
        await tenantApi.conversation.reply(c.id, { text: value })
      }
      if (after.kind === "close") await tenantApi.conversation.close(c.id)
      if (after.kind === "snooze") await tenantApi.conversation.snooze(c.id, { until: after.until.toISOString() })
    },
    onSuccess: () => {
      setText("")
      setMentions([])
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
    },
    onError: (err) => notifyError(err),
  })
  const submit = (after: After) => {
    if (text.trim() && !send.isPending) send.mutate(after)
  }

  // @ 同事：光标前面是 @ 加上几个字时，列出名字里有这几个字的同事
  const candidates =
    mode === "note" && query !== null
      ? members.filter((m) => m.id !== me.data?.id && m.name.toLowerCase().includes(query.toLowerCase())).slice(0, 6)
      : []
  const onChange = (value: string, caret: number) => {
    setText(value)
    if (value.trim()) onTyping()
    const m = mode === "note" ? /@([^\s@]*)$/.exec(value.slice(0, caret)) : null
    setQuery(m ? m[1] : null)
    setPick(0)
  }
  const choose = (m: MemberTenantMember) => {
    const el = area.current
    const caret = el?.selectionStart ?? text.length
    const before = text.slice(0, caret).replace(/@([^\s@]*)$/, `@${m.name} `)
    const next = before + text.slice(caret)
    setText(next)
    setMentions((all) => (all.some((x) => x.id === m.id) ? all : [...all, m]))
    setQuery(null)
    requestAnimationFrame(() => {
      el?.focus()
      el?.setSelectionRange(before.length, before.length)
    })
  }

  const note = mode === "note"
  return (
    <Box className="inbox-composer" data-note={note || undefined} px="sm" pt={8} pb={10}>
      <Group gap="xs" mb={6}>
        <SegmentedControl
          size="xs"
          value={mode}
          onChange={(v) => {
            setMode(v as Mode)
            setQuery(null)
          }}
          data={[
            { value: "reply", label: t("inbox.mode_reply") },
            { value: "note", label: t("inbox.mode_note") },
          ]}
        />
        {note && (
          <Text size="xs" c="yellow.9">
            {t("inbox.note_hint")}
          </Text>
        )}
      </Group>
      <Box pos="relative">
        {candidates.length > 0 && (
          <Paper withBorder shadow="md" radius="md" p={4} pos="absolute" bottom="100%" left={0} mb={4} w={240} style={{ zIndex: 10 }}>
            {candidates.map((m, i) => (
              <UnstyledButton
                key={m.id}
                w="100%"
                px="xs"
                py={6}
                className="inbox-mention"
                data-active={i === pick || undefined}
                onMouseDown={(e) => {
                  e.preventDefault()
                  choose(m)
                }}
              >
                <Group gap="xs" wrap="nowrap">
                  <Avatar src={m.avatar_url || undefined} size={22} name={m.name} />
                  <Text size="sm" truncate>
                    {m.name}
                  </Text>
                  <Text size="xs" c="dimmed" ml="auto">
                    {m.role_name}
                  </Text>
                </Group>
              </UnstyledButton>
            ))}
          </Paper>
        )}
        <Textarea
          ref={area}
          value={text}
          onChange={(e) => onChange(e.currentTarget.value, e.currentTarget.selectionStart)}
          onKeyDown={(e) => {
            if (e.nativeEvent.isComposing) return
            if (candidates.length > 0 && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
              e.preventDefault()
              setPick((p) => (p + (e.key === "ArrowDown" ? 1 : candidates.length - 1)) % candidates.length)
              return
            }
            if (candidates.length > 0 && (e.key === "Enter" || e.key === "Tab")) {
              e.preventDefault()
              choose(candidates[pick])
              return
            }
            if (e.key === "Escape") setQuery(null)
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault()
              submit({ kind: "none" })
            }
          }}
          placeholder={t(note ? "inbox.note_placeholder" : "inbox.reply_placeholder")}
          autosize
          minRows={2}
          maxRows={8}
          maxLength={10000}
          aria-label={t(note ? "inbox.mode_note" : "inbox.mode_reply")}
        />
      </Box>
      <Group gap="xs" mt={8}>
        <Text size="xs" c="dimmed">
          {t("inbox.enter_hint")}
        </Text>
        <Group gap={0} ml="auto" wrap="nowrap">
          <Button
            size="xs"
            color={note ? "yellow" : undefined}
            disabled={!text.trim()}
            loading={send.isPending}
            onClick={() => submit({ kind: "none" })}
            style={note ? undefined : { borderTopRightRadius: 0, borderBottomRightRadius: 0 }}
          >
            {t(note ? "inbox.add_note" : "inbox.send")}
          </Button>
          {!note && (
            <Menu position="top-end" withinPortal>
              <Menu.Target>
                <Button size="xs" px={6} disabled={!text.trim() || send.isPending} style={{ borderTopLeftRadius: 0, borderBottomLeftRadius: 0, borderLeft: "1px solid var(--mantine-color-white)" }} aria-label={t("inbox.send_options")}>
                  <IconChevronDown size={14} />
                </Button>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Item onClick={() => submit({ kind: "none" })}>{t("inbox.send")}</Menu.Item>
                <Menu.Item onClick={() => submit({ kind: "close" })}>{t("inbox.send_close")}</Menu.Item>
                <Menu.Label>{t("inbox.send_snooze")}</Menu.Label>
                {snoozeOptions(zone).map((o) => (
                  <Menu.Item key={o.key} onClick={() => submit({ kind: "snooze", until: o.until })}>
                    {t(`inbox.snooze_${o.key}` as const)}
                  </Menu.Item>
                ))}
              </Menu.Dropdown>
            </Menu>
          )}
        </Group>
      </Group>
    </Box>
  )
}
