import { ActionIcon, Avatar, Badge, Box, Button, Center, Divider, Drawer, Group, Loader, SegmentedControl, Text, Tooltip } from "@mantine/core"
import { useElementSize, useMediaQuery } from "@mantine/hooks"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { Link, useNavigate, useParams } from "@tanstack/react-router"
import { IconCheck, IconLayoutSidebarRight, IconRefresh, IconTicket } from "@tabler/icons-react"
import { notifyError, Pane, PaneHeader, useCan, useCollapseNav, useNarrow, useT } from "@go-tenon/ui"
import { type ReactNode, useCallback, useEffect, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { isTypingTarget, NARROW_QUERY } from "../layout"
import { CreateTicketModal } from "../tickets/CreateTicketModal"
import { useConversationChannel } from "./channel"
import { Composer, type ComposerHandle } from "./Composer"
import { useConversation, useMe, useMembers, useTeams, useTimeline } from "./data"
import { Details } from "./Details"
import { contactName } from "./List"
import { AssignMenu, SnoozeMenu } from "./menus"
import type { MatchField } from "./search"
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
  highlight?: string // 当前全文搜索关键词：时间线中高亮命中的词
  focusSeq?: number // 从搜索结果跳转时定位到的目标消息序号
  focusField?: MatchField // 命中来源字段：original 时目标消息自动展示原文
  open: (id: string) => void // 打开另一个会话（详情中的历史会话、下一个待回复）
  next?: () => void // 打开下一个待回复的会话；没有时为空
  back: () => void // 窄屏时返回会话列表
}

/**
 * ConversationView 是会话视图：顶部展示联系人、非默认状态及渠道/团队/负责人上下文，右侧按主次分层排列译文切换、
 * 指派、稍后处理、关闭/重开与导航控件；中间是时间线；底部是回复框。打开时和前台收到新消息时自动标记已读。
 * 详情是第五栏：窗口宽度小于 1600px 时打开详情自动收起二级导航，关闭后恢复；窄屏时详情占满并支持返回列表。
 */
export function ConversationView({ id, tenantId, highlight, focusSeq, focusField, open, next, back }: ConversationProps): ReactNode {
  const t = useT()
  const can = useCan()
  const navigate = useNavigate()
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
  const timeline = useTimeline(id, focusSeq)
  const { viewers, typing, notifyTyping, clearTyping } = useConversationChannel(tenantId, id, me.data?.id)
  const [details, setDetails] = useState(() => (narrow ? false : readSavedDetails()))
  const [original, setOriginal] = useState(false) // 对方的消息都显示原文
  const [createTicketOpen, setCreateTicketOpen] = useState(false)
  const { ref: mainRef, width: mainWidth } = useElementSize()
  const composerRef = useRef<ComposerHandle>(null)
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

  // 会话级快捷键（非输入态生效）：Shift+J 下一个待回复、R 回复、N 内部备注、I 切换详情栏、E 关闭会话
  const status = c?.status
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || isTypingTarget(e.target)) return
      const key = e.key.toLowerCase()
      if (e.shiftKey) {
        if (key === "j" && next) {
          e.preventDefault()
          next()
        }
        return
      }
      if (key === "i") {
        e.preventDefault()
        toggleDetails()
      } else if (key === "r") {
        e.preventDefault()
        composerRef.current?.focus("reply")
      } else if (key === "n") {
        e.preventDefault()
        composerRef.current?.focus("note")
      } else if (key === "e" && status && status !== "closed" && !close.isPending) {
        e.preventDefault()
        close.mutate()
      }
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [close, next, status, toggleDetails])

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
  // 顶栏副标题保留渠道（跨渠道时展示全部参与渠道）、团队和负责人三项高频上下文，语言与所在地等完整资料在右侧详情栏查看
  const channelText =
    c.channels && c.channels.length > 1
      ? c.channels.map((ch) => ch.name).join(" + ")
      : (c.reply_channel?.name ?? c.channel?.name)
  const meta = [channelText, team, assignee ? t("inbox.assigned_to", { who: assignee }) : t("inbox.no_assignee")]
    .filter(Boolean)
    .join(" · ")
  const showTranslateToggle = hasTranslation(timeline.data, me.data?.language, me.data?.id)

  if (narrow && details) return <Details c={c} onClose={closeDetails} onOpen={open} />
  const inlineDetails = details && !narrow && canInlineDetails
  const drawerDetails = details && !narrow && !canInlineDetails
  // 当聊天列实际宽度小于 640px（例如中屏四栏并排）或移动端窄屏时，操作按钮降级为图标 + Tooltip，避免挤压联系人名字
  const compactActions = narrow || (mainWidth > 0 && mainWidth < 640)
  return (
    <Group gap={0} h="100%" wrap="nowrap" align="stretch" style={{ flex: 1, minWidth: 0 }}>
      <Box ref={mainRef} style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column" }}>
        {/* 宽屏锁定 52px 基准顶栏与相邻各栏齐平；窄屏操作按钮换到第二行 */}
        <PaneHeader fixed={!narrow} back={narrow ? back : undefined} style={narrow ? { paddingBlock: 8 } : undefined}>
          <Box style={{ minWidth: 0, flex: 1 }}>
            <Group gap={6} wrap="nowrap">
              {c.contact ? (
                <Link
                  to="/t/$slug/contacts/$id"
                  params={{ slug, id: c.contact.id }}
                  search={{ sort: "recent" }}
                  className="inbox-contact-link"
                >
                  <Text size="md" fw={600} truncate lh={1.25}>
                    {contactName(c, t)}
                  </Text>
                </Link>
              ) : (
                <Text size="md" fw={600} truncate lh={1.25}>
                  {contactName(c, t)}
                </Text>
              )}
              {c.status !== "open" && (
                <Badge size="xs" variant="light" color={c.status === "snoozed" ? "yellow" : "gray"} style={{ flex: "none" }}>
                  {t(`inbox.status_${c.status}` as const)}
                </Badge>
              )}
            </Group>
            <Text size="xs" c="dimmed" truncate lh={1.3}>
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
          <Group
            gap={4}
            wrap="nowrap"
            w={narrow ? "100%" : undefined}
            justify={narrow ? "flex-end" : undefined}
            style={{ flex: "none" }}
            data-tn-density="dense"
          >
            {showTranslateToggle && (
              <>
                <SegmentedControl
                  value={original ? "original" : "translation"}
                  onChange={(v) => setOriginal(v === "original")}
                  data={[
                    { value: "translation", label: t("inbox.translated") },
                    { value: "original", label: t("inbox.original") },
                  ]}
                  aria-label={t("inbox.text_shown")}
                />
                <Divider orientation="vertical" h={16} mx={2} style={{ alignSelf: "center" }} />
              </>
            )}
            <AssignMenu c={c} compact={compactActions} />
            {can("conversations:reply") &&
              (compactActions ? (
                <Tooltip label={t("tickets.create_from_conversation")}>
                  <ActionIcon
                    variant="subtle"
                    color="gray"
                    size="input-sm"
                    onClick={() => setCreateTicketOpen(true)}
                    aria-label={t("tickets.create_from_conversation")}
                  >
                    <IconTicket size={15} />
                  </ActionIcon>
                </Tooltip>
              ) : (
                <Button variant="subtle" color="gray" leftSection={<IconTicket size={14} />} onClick={() => setCreateTicketOpen(true)}>
                  {t("tickets.create_from_conversation")}
                </Button>
              ))}
            {c.status !== "closed" && <SnoozeMenu id={c.id} compact={compactActions} />}
            {c.status === "closed" ? (
              compactActions ? (
                <Tooltip label={t("inbox.reopen")}>
                  <ActionIcon variant="light" size="input-sm" loading={reopen.isPending} onClick={() => reopen.mutate()} aria-label={t("inbox.reopen")}>
                    <IconRefresh size={15} />
                  </ActionIcon>
                </Tooltip>
              ) : (
                <Button variant="light" ml={2} leftSection={<IconRefresh size={14} />} loading={reopen.isPending} onClick={() => reopen.mutate()}>
                  {t("inbox.reopen")}
                </Button>
              )
            ) : compactActions ? (
              <Tooltip label={t("inbox.close")}>
                <ActionIcon variant="light" color="green" size="input-sm" loading={close.isPending} onClick={() => close.mutate()} aria-label={t("inbox.close")}>
                  <IconCheck size={15} />
                </ActionIcon>
              </Tooltip>
            ) : (
              <Button variant="light" color="green" ml={2} leftSection={<IconCheck size={14} />} loading={close.isPending} onClick={() => close.mutate()}>
                {t("inbox.close")}
              </Button>
            )}
            <Divider orientation="vertical" h={16} mx={2} style={{ alignSelf: "center" }} />
            <Tooltip label={t("inbox.details")}>
              <ActionIcon
                variant={details ? "light" : "subtle"}
                color={details ? undefined : "gray"}
                size="input-sm"
                onClick={toggleDetails}
                aria-label={t("inbox.details")}
              >
                <IconLayoutSidebarRight size={16} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </PaneHeader>
        <Timeline
          c={c}
          readSeq={readAtOpen.current ?? 0}
          typing={typing}
          original={original}
          highlight={highlight}
          focusSeq={focusSeq}
          focusField={focusField}
        />
        <Composer
          key={c.id}
          c={c}
          onTyping={notifyTyping}
          onReplied={me.data?.open_next_after_reply && next ? next : undefined}
          handleRef={composerRef}
        />
      </Box>
      {inlineDetails && (
        <Pane border="left" resizable={{ storageKey: "inbox-details", defaultWidth: 320, min: 260, max: 420, side: "left" }}>
          <Details c={c} onClose={closeDetails} onOpen={open} />
        </Pane>
      )}
      {!narrow && !canInlineDetails && (
        <Drawer opened={drawerDetails} onClose={closeDetails} position="right" size={340} padding={0} withCloseButton={false}>
          <Box h="100dvh">
            <Details c={c} onClose={closeDetails} onOpen={open} />
          </Box>
        </Drawer>
      )}
      <CreateTicketModal
        opened={createTicketOpen}
        onClose={() => setCreateTicketOpen(false)}
        sourceConversation={c}
        onCreated={(created) =>
          void navigate({
            to: "/t/$slug/tickets/$id",
            params: { slug, id: created.id },
            search: { box: "all", status: "unresolved" },
          })
        }
      />
    </Group>
  )
}
