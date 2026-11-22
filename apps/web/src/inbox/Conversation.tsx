import { ActionIcon, Avatar, Badge, Box, Button, Center, Drawer, Group, Loader, SegmentedControl, Text, Tooltip } from "@mantine/core"
import { useElementSize, useMediaQuery } from "@mantine/hooks"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { Link, useParams } from "@tanstack/react-router"
import { IconArrowRight, IconCheck, IconChevronLeft, IconLayoutSidebarRight, IconRefresh } from "@tabler/icons-react"
import { notifyError, useCollapseNav, useLanguage, useNarrow, useT } from "@go-tenon/ui"
import { type ReactNode, useCallback, useEffect, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { languageName } from "../languages"
import { isTypingTarget, NARROW_QUERY, ResizeHandle, useResizableWidth } from "../layout"
import { formatLocalTime } from "../timezone"
import { useConversationChannel } from "./channel"
import { Composer } from "./Composer"
import { useConversation, useMe, useMembers, useTeams, useTimeline } from "./data"
import { Details } from "./Details"
import { contactName } from "./List"
import { AssignMenu, SnoozeMenu } from "./menus"
import { hasTranslation, Timeline } from "./Timeline"

/** DETAILS_STORAGE_KEY 保存宽屏下右侧详情栏的展开偏好，切换会话时保持一致。 */
const DETAILS_STORAGE_KEY = "agents-desk:inbox-details-open"

/** readSavedDetails 读出宽屏下保存的详情栏展开状态，读取失败时默认关闭。 */
function readSavedDetails(): boolean {
  try {
    return localStorage.getItem(DETAILS_STORAGE_KEY) === "1"
  } catch {
    return false
  }
}

/** saveDetails 持久化宽屏下详情栏的展开偏好。 */
function saveDetails(open: boolean): void {
  try {
    localStorage.setItem(DETAILS_STORAGE_KEY, open ? "1" : "0")
  } catch {
    // 浏览器禁用存储时仅在当前会话生效
  }
}

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
  const narrow = useNarrow(NARROW_QUERY)
  // 1600px 以上四栏+详情并排；1360–1599px 打开详情自动收起二级导航；900–1359px 详情改为右侧浮层抽屉避免挤压聊天区。
  const wide = useMediaQuery("(min-width: 1600px)") ?? false
  const canInlineDetails = useMediaQuery("(min-width: 1360px)") ?? false
  const { slug } = useParams({ from: "/t/$slug" })
  const queryClient = useQueryClient()
  const me = useMe()
  const { byId } = useMembers()
  const teams = useTeams()
  const conversation = useConversation(id)
  const timeline = useTimeline(id)
  const { viewers, typing, notifyTyping, clearTyping } = useConversationChannel(tenantId, id, me.data?.id)
  const ui = useLanguage()
  const [details, setDetails] = useState(() => (narrow ? false : readSavedDetails()))
  const [original, setOriginal] = useState(false) // 对方的消息都显示原文
  const detailSize = useResizableWidth("agents-desk:inbox-details-w", 300, 260, 420, "left")
  const { ref: mainRef, width: mainWidth } = useElementSize()
  const composerRef = useRef<HTMLTextAreaElement>(null)
  useCollapseNav(details && canInlineDetails && !wide && !narrow)

  // 从宽屏切到窄屏时自动收起详情，避免详情页占满整屏遮挡聊天记录
  useEffect(() => {
    if (narrow) setDetails(false)
  }, [narrow])

  const closeDetails = useCallback(() => {
    setDetails(false)
    if (!narrow) saveDetails(false)
  }, [narrow])
  const toggleDetails = useCallback(() => {
    setDetails((prev) => {
      const next = !prev
      if (!narrow) saveDetails(next)
      return next
    })
  }, [narrow])

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

  // 会话级单键快捷键（非输入态生效）：R 聚焦回复框、I 切换详情栏、E 关闭会话
  const status = c?.status
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey || isTypingTarget(e.target)) return
      const key = e.key.toLowerCase()
      if (key === "i") {
        e.preventDefault()
        toggleDetails()
      } else if (key === "r") {
        e.preventDefault()
        composerRef.current?.focus()
      } else if (key === "e" && status && status !== "closed" && !close.isPending) {
        e.preventDefault()
        close.mutate()
      }
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [close, status, toggleDetails])

  if (!c) {
    return (
      <Center style={{ flex: 1 }}>
        {conversation.isError ? <Text c="dimmed">{t("inbox.not_found")}</Text> : <Loader size="sm" />}
      </Center>
    )
  }
  const assignee = c.assignee?.type === "member" ? (c.assignee.id === me.data?.id ? t("inbox.you") : byId.get(c.assignee.id)?.name) : undefined
  const teamRow = c.team_id ? teams.find((x) => x.id === c.team_id) : undefined
  const team = teamRow ? (teamRow.status === "disabled" ? t("inbox.team_disabled_suffix", { name: teamRow.name }) : teamRow.name) : undefined
  const localTime = formatLocalTime(c.contact?.time_zone)
  const place = [c.contact?.location, localTime ? t("contacts.local_time", { time: localTime }) : undefined].filter(Boolean).join(" ")
  const meta = [c.channel?.name, c.language ? languageName(c.language, ui) : undefined, place || undefined, team, assignee ? t("inbox.assigned_to", { who: assignee }) : t("inbox.no_assignee")]
    .filter(Boolean)
    .join(" · ")

  if (narrow && details) return <Details c={c} onClose={closeDetails} onOpen={open} />
  const inlineDetails = details && !narrow && canInlineDetails
  const drawerDetails = details && !narrow && !canInlineDetails
  // 当聊天列实际宽度小于 640px（例如中屏四栏并排）或移动端窄屏时，操作按钮降级为图标 + Tooltip，避免挤压联系人名字
  const compactActions = narrow || (mainWidth > 0 && mainWidth < 640)
  return (
    <Group gap={0} h="100%" wrap="nowrap" align="stretch" style={{ flex: 1, minWidth: 0 }}>
      <Box ref={mainRef} style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column" }}>
        {/* 宽屏锁定 52px 基准顶栏与相邻各栏齐平；窄屏操作按钮换到第二行 */}
        <Group gap="sm" px="md" py={narrow ? 8 : 0} wrap={narrow ? "wrap" : "nowrap"} className="inbox-header" data-fixed={!narrow || undefined}>
          {narrow && (
            <ActionIcon variant="subtle" color="gray" onClick={back} aria-label={t("inbox.back")}>
              <IconChevronLeft size={18} />
            </ActionIcon>
          )}
          <Box style={{ minWidth: 0, flex: 1 }}>
            <Group gap={6} wrap="nowrap">
              {c.contact ? (
                <Link
                  to="/t/$slug/contacts/$id"
                  params={{ slug, id: c.contact.id }}
                  search={{ sort: "recent" }}
                  className="inbox-contact-link"
                >
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
          {viewers.length > 0 && !compactActions && (
            <Tooltip label={t("inbox.viewing", { who: viewers.map((v) => v.name).join(", ") })}>
              <Avatar.Group spacing="xs" style={{ flex: "none" }}>
                {viewers.slice(0, 3).map((v) => (
                  <Avatar key={v.id} size={26} name={v.name} color="initials" src={byId.get(v.id)?.avatar_url || undefined} />
                ))}
              </Avatar.Group>
            </Tooltip>
          )}
          <Group gap={6} wrap="nowrap" w={narrow ? "100%" : undefined} justify={narrow ? "flex-end" : undefined} style={{ flex: "none" }}>
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
            <AssignMenu c={c} compact={compactActions} />
            {c.status !== "closed" && <SnoozeMenu id={c.id} compact={compactActions} />}
            {c.status === "closed" ? (
              compactActions ? (
                <Tooltip label={t("inbox.reopen")}>
                  <ActionIcon variant="default" size={30} loading={reopen.isPending} onClick={() => reopen.mutate()} aria-label={t("inbox.reopen")}>
                    <IconRefresh size={15} />
                  </ActionIcon>
                </Tooltip>
              ) : (
                <Button size="xs" variant="default" leftSection={<IconRefresh size={14} />} loading={reopen.isPending} onClick={() => reopen.mutate()}>
                  {t("inbox.reopen")}
                </Button>
              )
            ) : compactActions ? (
              <Tooltip label={t("inbox.close")}>
                <ActionIcon variant="default" size={30} loading={close.isPending} onClick={() => close.mutate()} aria-label={t("inbox.close")}>
                  <IconCheck size={15} />
                </ActionIcon>
              </Tooltip>
            ) : (
              <Button size="xs" variant="default" leftSection={<IconCheck size={14} />} loading={close.isPending} onClick={() => close.mutate()}>
                {t("inbox.close")}
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
              <ActionIcon variant={details ? "light" : "default"} size={30} onClick={toggleDetails} aria-label={t("inbox.details")}>
                <IconLayoutSidebarRight size={16} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </Group>
        <Timeline c={c} readSeq={readAtOpen.current ?? 0} typing={typing} original={original} />
        <Composer key={c.id} c={c} onTyping={notifyTyping} inputRef={composerRef} />
      </Box>
      {inlineDetails && (
        <Box w={detailSize.width} pos="relative" style={{ flex: "none" }} className="inbox-aside">
          <ResizeHandle
            side="left"
            dragging={detailSize.dragging}
            width={detailSize.width}
            min={detailSize.min}
            max={detailSize.max}
            onPointerDown={detailSize.onPointerDown}
            onDoubleClick={detailSize.onDoubleClick}
            onKeyDown={detailSize.onKeyDown}
          />
          <Details c={c} onClose={closeDetails} onOpen={open} />
        </Box>
      )}
      {!narrow && !canInlineDetails && (
        <Drawer opened={drawerDetails} onClose={closeDetails} position="right" size={340} padding={0} withCloseButton={false}>
          <Box h="100dvh">
            <Details c={c} onClose={closeDetails} onOpen={open} />
          </Box>
        </Drawer>
      )}
    </Group>
  )
}
