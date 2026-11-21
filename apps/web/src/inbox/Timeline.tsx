import type { ConversationDTO, ConversationItemDTO } from "@agents-desk/client/tenant"
import { Anchor, Box, Center, Divider, Loader, Paper, Stack, Text, UnstyledButton } from "@mantine/core"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { notifyError, useLanguage, useT, useTimeZone } from "@go-tenon/ui"
import { Fragment, type ReactNode, useEffect, useRef } from "react"
import { kit, tenantApi } from "../api"
import type { TypingNow } from "./channel"
import { useMe, useMembers, useTeams, useTimeline } from "./data"
import { describeEvent, whoDid } from "./events"
import { clock, dayLabel, sameDay } from "./format"
import { contactName } from "./List"

/** deliveryColor 是送达状态的颜色：已读是进行中的颜色，失败是红色，其余是灰色。 */
const deliveryColor = { sending: "dimmed", delivered: "dimmed", read: "bright-blue", failed: "red" } as const

/**
 * Timeline 是会话的时间线：按天分隔；对方的消息在左侧，成员和系统发的在右侧并显示送达状态（失败时可以重发）；
 * 系统事件居中、灰色；内部备注占满一行、黄色底。打开时读到的位置 readSeq 之后显示“新消息”分隔线。
 * 新的内容出现时滚到底部；最下面是正在输入的人。
 */
export function Timeline({ c, readSeq, typing }: { c: ConversationDTO; readSeq: number; typing: TypingNow[] }): ReactNode {
  const t = useT()
  const language = useLanguage()
  const zone = useTimeZone()
  const me = useMe()
  const { byId } = useMembers()
  const teams = useTeams()
  const items = useTimeline(c.id)
  const bottom = useRef<HTMLDivElement>(null)
  const list = items.data ?? []
  const names = {
    member: (id: string) => (id === me.data?.id ? t("inbox.you") : (byId.get(id)?.name ?? t("event.someone"))),
    team: (id: string) => teams.find((x) => x.id === id)?.name ?? "",
  }

  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end" })
  }, [list.length, typing.length])

  if (items.isPending) {
    return (
      <Center style={{ flex: 1 }}>
        <Loader size="sm" />
      </Center>
    )
  }
  const firstNew = list.find((it) => it.seq > readSeq && !(it.actor.type === "member" && it.actor.id === me.data?.id))
  return (
    <Box className="inbox-timeline">
      <Stack gap={10} p="md">
        {list.map((it, i) => (
          <Fragment key={it.id}>
            {(i === 0 || !sameDay(list[i - 1].created_at, it.created_at)) && (
              <Divider label={dayLabel(it.created_at, language, t, zone)} labelPosition="center" />
            )}
            {readSeq > 0 && it === firstNew && <Divider color="red.3" label={<Text size="xs" c="red">{t("inbox.new_messages")}</Text>} labelPosition="center" />}
            {it.kind === "event" ? (
              <Text size="xs" c="dimmed" ta="center" className="inbox-event">
                {describeEvent(it, byId, names, t)} · {clock(it.created_at, language, zone)}
              </Text>
            ) : it.kind === "note" ? (
              <Note it={it} author={names.member(it.actor.id)} time={clock(it.created_at, language, zone)} />
            ) : (
              <Message
                it={it}
                c={c}
                mine={it.actor.type !== "contact"}
                author={it.actor.type === "contact" ? contactName(c, t) : it.actor.type === "member" ? names.member(it.actor.id) : whoDid(it, byId, t)}
                time={clock(it.created_at, language, zone)}
              />
            )}
          </Fragment>
        ))}
        {typing.length > 0 && (
          <Text size="xs" c="dimmed" fs="italic">
            {t("inbox.typing", { who: typing.map((x) => (x.contact ? contactName(c, t) : (x.name ?? ""))).join(", ") })}
          </Text>
        )}
        <div ref={bottom} />
      </Stack>
    </Box>
  )
}

/** Message 是一条消息：对方的在左侧，成员和系统发的在右侧，旁边是送达状态，失败时可以重发。 */
function Message({ it, c, mine, author, time }: { it: ConversationItemDTO; c: ConversationDTO; mine: boolean; author: string; time: string }): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const retry = useMutation({
    mutationFn: () => tenantApi.conversation.retry(c.id, it.id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() }),
    onError: (err) => notifyError(err),
  })
  return (
    <Box style={{ alignSelf: mine ? "flex-end" : "flex-start", maxWidth: "72%" }}>
      <Text size="xs" c="dimmed" mb={2} ta={mine ? "right" : "left"} px={4}>
        {author} · {time}
        {mine && it.delivery && (
          <>
            {" · "}
            <Text span size="xs" c={deliveryColor[it.delivery]}>
              {t(`inbox.delivery_${it.delivery}` as const)}
            </Text>
            {it.delivery === "failed" && (
              <>
                {" · "}
                <UnstyledButton fz="xs" c="red" td="underline" onClick={() => retry.mutate()} disabled={retry.isPending}>
                  {t("inbox.retry")}
                </UnstyledButton>
              </>
            )}
          </>
        )}
      </Text>
      <Paper
        px="sm"
        py={8}
        radius="md"
        withBorder={!mine}
        bg={mine ? "var(--mantine-color-bright-blue-light)" : undefined}
        className="inbox-bubble"
      >
        <Linkified text={it.content.text ?? ""} />
      </Paper>
    </Box>
  )
}

/** Note 是内部备注：占满一行、黄色底，@ 到的同事加粗。只有成员可见。 */
function Note({ it, author, time }: { it: ConversationItemDTO; author: string; time: string }): ReactNode {
  const t = useT()
  return (
    <Paper px="sm" py={8} radius="md" bg="var(--mantine-color-yellow-light)" className="inbox-note">
      <Text size="xs" fw={600} c="yellow.9" mb={2}>
        {t("inbox.note_by", { who: author })} · {time}
      </Text>
      <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>
        {(it.content.text ?? "").split(/(@\S+)/).map((part, i) =>
          part.startsWith("@") ? (
            // biome-ignore lint/suspicious/noArrayIndexKey: 拆分出的片段没有别的标识，顺序固定
            <Text key={i} span fw={700} c="yellow.9">
              {part}
            </Text>
          ) : (
            part
          ),
        )}
      </Text>
    </Paper>
  )
}

/** LINK 匹配消息中的网址：http:// 或 https:// 开头，到空白为止，去掉结尾的标点。 */
const LINK = /https?:\/\/[^\s<>\x22]+[^\s<>\x22\x27.,;:!?)\]}。，；：！？）】」』]/g

/** Linkified 显示消息文字，其中的网址是在新标签页中打开的链接。 */
function Linkified({ text }: { text: string }): ReactNode {
  const parts: ReactNode[] = []
  let last = 0
  for (const m of text.matchAll(LINK)) {
    const at = m.index ?? 0
    if (at > last) parts.push(text.slice(last, at))
    parts.push(
      <Anchor key={at} href={m[0]} target="_blank" rel="noopener noreferrer" size="sm">
        {m[0]}
      </Anchor>,
    )
    last = at + m[0].length
  }
  if (last < text.length) parts.push(text.slice(last))
  return (
    <Text size="sm" style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
      {parts}
    </Text>
  )
}
