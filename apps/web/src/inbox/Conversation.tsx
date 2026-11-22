import { ActionIcon, Avatar, Badge, Box, Button, Center, Group, Loader, SegmentedControl, Text, Tooltip } from "@mantine/core"
import { useMediaQuery } from "@mantine/hooks"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { Link, useParams } from "@tanstack/react-router"
import { IconArrowRight, IconCheck, IconChevronLeft, IconLayoutSidebarRight, IconRefresh } from "@tabler/icons-react"
import { notifyError, useCollapseNav, useLanguage, useNarrow, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { languageName } from "../languages"
import { formatLocalTime } from "../timezone"
import { useConversationChannel } from "./channel"
import { Composer } from "./Composer"
import { useConversation, useMe, useMembers, useTeams, useTimeline } from "./data"
import { Details } from "./Details"
import { contactName } from "./List"
import { AssignMenu, SnoozeMenu } from "./menus"
import { hasTranslation, Timeline } from "./Timeline"

/** ConversationProps 是会话页的设置。 */
interface ConversationProps {
  id: string
  tenantId: string
  open: (id: string) => void // 打开另一个会话（详情中的历史会话、下一个待回复）
  next?: () => void // 打开下一个待回复的会话；没有时为空
  back: () => void // 窄屏时返回会话列表
}

/**
 * ConversationView 是会话：顶部是联系人、渠道、语言、团队收件箱、负责人，谁也在看，以及指派、稍后处理、关闭或重新打开、
 * 下一个待回复、详情；中间是时间线；底部是回复框。打开时和有新消息时（页面在前台）标记已读。
 * 对方的消息有译文时，顶部的“译文 | 原文”把它们一次切换成原文。
 * 详情是第五栏：窗口宽度小于 1600px 时打开详情先收起二级导航，关掉后恢复。窄屏时左上角返回会话列表，详情占满。
 */
export function ConversationView({ id, tenantId, open, next, back }: ConversationProps): ReactNode {
  const t = useT()
  const narrow = useNarrow()
  const wide = useMediaQuery("(min-width: 1600px)")
  const { slug } = useParams({ from: "/t/$slug" })
  const queryClient = useQueryClient()
  const me = useMe()
  const { byId } = useMembers()
  const teams = useTeams()
  const conversation = useConversation(id)
  const timeline = useTimeline(id)
  const { viewers, typing, notifyTyping, clearTyping } = useConversationChannel(tenantId, id, me.data?.id)
  const ui = useLanguage()
  const [details, setDetails] = useState(false)
  const [original, setOriginal] = useState(false) // 对方的消息都显示原文
  useCollapseNav(details && !wide && !narrow)

  // 打开时读到的位置：“新消息”分隔线画在它之后，读过之后也保持在原处
  const readAtOpen = useRef<number | null>(null)
  const c = conversation.data
  if (c && readAtOpen.current === null) readAtOpen.current = c.read_seq

  // 最新一项是谁发的：他的“正在输入”随之消失
  const last = timeline.data?.at(-1)
  useEffect(() => {
    if (last?.kind === "message" || last?.kind === "note") clearTyping(last.actor.type === "contact" ? "contact" : last.actor.id)
  }, [last, clearTyping])

  // 标记已读：页面在前台、时间线的最后一项比读到的位置新时
  const lastSeq = last?.seq ?? 0
  const marked = useRef(0)
  useEffect(() => {
    if (!c || lastSeq <= Math.max(c.read_seq, marked.current) || document.visibilityState !== "visible") return
    marked.current = lastSeq
    void tenantApi.conversation.read(id, { seq: lastSeq }).then(() =>
      queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() }),
    )
  }, [c, id, lastSeq, queryClient])

  const invalidate = () => queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
  const close = useMutation({ mutationFn: () => tenantApi.conversation.close(id), onSuccess: invalidate, onError: (err) => notifyError(err) })
  const reopen = useMutation({ mutationFn: () => tenantApi.conversation.reopen(id), onSuccess: invalidate, onError: (err) => notifyError(err) })

  if (!c) {
    return (
      <Center style={{ flex: 1 }}>
        {conversation.isError ? <Text c="dimmed">{t("inbox.not_found")}</Text> : <Loader size="sm" />}
      </Center>
    )
  }
  const assignee = c.assignee?.type === "member" ? (c.assignee.id === me.data?.id ? t("inbox.you") : byId.get(c.assignee.id)?.name) : undefined
  const team = c.team_id ? teams.find((x) => x.id === c.team_id)?.name : undefined
  const localTime = formatLocalTime(c.contact?.time_zone)
  const place = [c.contact?.location, localTime ? t("contacts.local_time", { time: localTime }) : undefined].filter(Boolean).join(" ")
  const meta = [c.channel?.name, c.language ? languageName(c.language, ui) : undefined, place || undefined, team, assignee ? t("inbox.assigned_to", { who: assignee }) : t("inbox.no_assignee")]
    .filter(Boolean)
    .join(" · ")

  if (narrow && details) return <Details c={c} onClose={() => setDetails(false)} onOpen={open} />
  return (
    <Group gap={0} h="100%" wrap="nowrap" align="stretch" style={{ flex: 1, minWidth: 0 }}>
      <Box style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column" }}>
        {/* 窄屏时操作按钮换到第二行，联系人的名字留在第一行 */}
        <Group gap="sm" px="md" py={10} wrap={narrow ? "wrap" : "nowrap"} className="inbox-divider">
          {narrow && (
            <ActionIcon variant="subtle" color="gray" onClick={back} aria-label={t("inbox.back")}>
              <IconChevronLeft size={18} />
            </ActionIcon>
          )}
          <Box style={{ minWidth: 0, flex: narrow ? 1 : undefined }}>
            <Group gap={6} wrap="nowrap">
              {c.contact ? (
                <Link to="/t/$slug/contacts/$id" params={{ slug, id: c.contact.id }} style={{ color: "inherit", textDecoration: "none", minWidth: 0 }}>
                  <Text fw={600} truncate>
                    {contactName(c, t)}
                  </Text>
                </Link>
              ) : (
                <Text fw={600} truncate>
                  {contactName(c, t)}
                </Text>
              )}
              {c.contact && (
                <Badge size="xs" variant="light" color="gray" style={{ flex: "none" }}>
                  {kit.label("ContactKind", c.contact.kind)}
                </Badge>
              )}
              <Badge size="xs" variant="light" color={c.status === "open" ? "bright-blue" : c.status === "snoozed" ? "yellow" : "gray"} style={{ flex: "none" }}>
                {t(`inbox.status_${c.status}` as const)}
              </Badge>
            </Group>
            <Text size="xs" c="dimmed" truncate>
              {meta}
            </Text>
          </Box>
          {viewers.length > 0 && !narrow && (
            <Tooltip label={t("inbox.viewing", { who: viewers.map((v) => v.name).join(", ") })}>
              <Avatar.Group ml="auto" spacing="xs">
                {viewers.slice(0, 3).map((v) => (
                  <Avatar key={v.id} size={26} name={v.name} color="initials" src={byId.get(v.id)?.avatar_url || undefined} />
                ))}
              </Avatar.Group>
            </Tooltip>
          )}
          <Group gap={6} wrap="nowrap" ml={viewers.length > 0 && !narrow ? undefined : "auto"} w={narrow ? "100%" : undefined} justify={narrow ? "flex-end" : undefined}>
            {hasTranslation(timeline.data, me.data?.language) && (
              <SegmentedControl
                size="xs"
                value={original ? "original" : "translation"}
                onChange={(v) => setOriginal(v === "original")}
                data={[
                  { value: "translation", label: t("inbox.translated") },
                  { value: "original", label: t("inbox.original") },
                ]}
                aria-label={t("inbox.text_shown")}
              />
            )}
            <AssignMenu c={c} compact={narrow} />
            {c.status !== "closed" && <SnoozeMenu id={c.id} compact={narrow} />}
            {c.status === "closed" ? (
              <Button size="xs" variant="default" leftSection={<IconRefresh size={14} />} loading={reopen.isPending} onClick={() => reopen.mutate()}>
                {t("inbox.reopen")}
              </Button>
            ) : (
              <Button size="xs" variant="default" leftSection={<IconCheck size={14} />} loading={close.isPending} onClick={() => close.mutate()}>
                {narrow ? null : t("inbox.close")}
              </Button>
            )}
            {next && (
              <Tooltip label={t("inbox.next_waiting")}>
                <ActionIcon variant="default" size={30} onClick={next} aria-label={t("inbox.next_waiting")}>
                  <IconArrowRight size={16} />
                </ActionIcon>
              </Tooltip>
            )}
            <Tooltip label={t("inbox.details")}>
              <ActionIcon variant={details ? "light" : "default"} size={30} onClick={() => setDetails((d) => !d)} aria-label={t("inbox.details")}>
                <IconLayoutSidebarRight size={16} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </Group>
        <Timeline c={c} readSeq={readAtOpen.current ?? 0} typing={typing} original={original} />
        <Composer key={c.id} c={c} onTyping={notifyTyping} />
      </Box>
      {details && !narrow && (
        <Box w={300} style={{ flex: "none" }} className="inbox-aside">
          <Details c={c} onClose={() => setDetails(false)} onOpen={open} />
        </Box>
      )}
    </Group>
  )
}
