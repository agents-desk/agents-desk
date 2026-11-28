import type { AgentGenerationDTO, ChannelExternalMention, ConversationItemDTO } from "@agents-desk/client/tenant"
import { ActionIcon, Alert, Avatar, Badge, Box, Button, Checkbox, Code, Divider, Group, Modal, Paper, Stack, Text, TextInput, Tooltip } from "@mantine/core"
import { useElementSize } from "@mantine/hooks"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { Link, useNavigate, useParams } from "@tanstack/react-router"
import {
  IconAlertOctagon,
  IconArrowRight,
  IconCheck,
  IconInfoCircle,
  IconLayoutSidebarRight,
  IconPlayerStop,
  IconRefresh,
  IconRobot,
  IconTicket,
  IconX,
} from "@tabler/icons-react"
import { AsidePane, notifyError, notifySuccess, PaneHeader, useAsidePaneState, useCan, useNarrow, useT } from "@go-tenon/ui"
import { type ReactNode, useCallback, useEffect, useRef, useState } from "react"
import { kit, tenantApi } from "../api"
import { isTypingTarget, NARROW_QUERY } from "../layout"
import { CreateTicketModal } from "../tickets/CreateTicketModal"
import { useConversationChannel } from "./channel"
import { Composer, type ComposerHandle } from "./Composer"
import {
  handlingModeColor,
  useContactHistory,
  useConversation,
  useConversationGenerations,
  useGroupHistory,
  useMe,
  useMembers,
  useTeams,
  useTimeline,
} from "./data"
import { Details } from "./Details"
import { contactName, ConversationPeerAvatar } from "./List"
import { AssignMenu, SnoozeMenu } from "./menus"
import { useSyncRead } from "./MessagePrimitives"
import type { MatchField } from "./search"
import { Timeline } from "./Timeline"
import { ConversationSkeleton, InspectorSkeleton } from "../components/detail-skeleton"
import { EmptyState } from "../components/empty-state"

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
 * ConversationView 是会话视图：顶部展示联系人、非默认状态及渠道/团队/负责人上下文，右侧按主次分层排列
 * 指派、稍后处理、关闭/重开与详情栏开关；中间是时间线；底部是回复框。打开时和前台收到新消息时自动标记已读。
 * 右侧详情栏由框架 AsidePane 统一响应式适配（宽屏内联可调宽、中屏浮层抽屉、窄屏全屏覆盖）。
 */
export function ConversationView({ id, tenantId, highlight, focusSeq, focusField, open, next, back }: ConversationProps): ReactNode {
  const t = useT()
  const can = useCan()
  const navigate = useNavigate()
  const narrow = useNarrow(NARROW_QUERY)
  const { slug } = useParams({ from: "/$slug" })
  const queryClient = useQueryClient()
  const me = useMe()
  const { byId } = useMembers()
  const teams = useTeams()
  const conversation = useConversation(id)
  const timeline = useTimeline(id, focusSeq)
  const { viewers, typing, notifyTyping, clearTyping } = useConversationChannel(tenantId, id, me.data?.id)
  const details = useAsidePaneState("inbox-details", { compactQuery: NARROW_QUERY })
  const [createTicketOpen, setCreateTicketOpen] = useState(false)
  const [spamModalOpen, setSpamModalOpen] = useState(false)
  const [spamBlockContact, setSpamBlockContact] = useState(false)
  const [spamReason, setSpamReason] = useState("")
  const [replyToItem, setReplyToItem] = useState<ConversationItemDTO | null>(null)
  const [correctingItem, setCorrectingItem] = useState<ConversationItemDTO | null>(null)
  const [adoptedDraft, setAdoptedDraft] = useState<{ generationId: string; text: string } | null>(null)
  const [extMentions, setExtMentions] = useState<ChannelExternalMention[]>([])
  const { ref: mainRef, width: mainWidth } = useElementSize()
  const composerRef = useRef<ComposerHandle>(null)

  // 切换会话时清空引用回复、更正目标、已采纳草稿与群 @ 列表
  useEffect(() => {
    setReplyToItem(null)
    setCorrectingItem(null)
    setAdoptedDraft(null)
    setExtMentions([])
  }, [id])

  const handleReplyTo = useCallback((item: ConversationItemDTO) => {
    setCorrectingItem(null)
    setReplyToItem(item)
    if (item.actor.type === "external_user" && item.actor.external_id) {
      const target: ChannelExternalMention = {
        external_id: item.actor.external_id,
        external_user_id: item.actor.id || undefined,
        name: item.actor.name || item.actor.external_id,
      }
      setExtMentions((prev) => (prev.some((m) => m.external_id === target.external_id) ? prev : [...prev, target]))
    }
    composerRef.current?.focus("reply")
  }, [])

  const handleCorrect = useCallback((item: ConversationItemDTO) => {
    setReplyToItem(null)
    setCorrectingItem(item)
    composerRef.current?.focus("reply")
  }, [])

  // 打开时读到的位置：“新消息”分隔线画在它之后，读过之后也保持在原处
  const readAtOpen = useRef<number | null>(null)
  const c = conversation.data
  if (c && readAtOpen.current === null) readAtOpen.current = c.read_seq
  const pollAI = Boolean(
    c && (c.handling_mode === "ai_auto" || c.handling_mode === "ai_copilot" || c.handling_mode === "waiting_approval"),
  )
  const generationsQuery = useConversationGenerations(id, pollAI)
  const invalidateAI = () => {
    void queryClient.invalidateQueries({ queryKey: kit.queries.generation.key() })
    void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
  }
  const cancelGeneration = useMutation({
    mutationFn: (genId: string) => tenantApi.generation.cancel(genId),
    onSuccess: invalidateAI,
    onError: (err) => notifyError(err),
  })
  const approveTool = useMutation({
    mutationFn: ({ genId, callId }: { genId: string; callId: string }) =>
      tenantApi.generation.approvalApprove(genId, callId, {}),
    onSuccess: invalidateAI,
    onError: (err) => notifyError(err),
  })
  const rejectTool = useMutation({
    mutationFn: ({ genId, callId }: { genId: string; callId: string }) =>
      tenantApi.generation.approvalReject(genId, callId, {}),
    onSuccess: invalidateAI,
    onError: (err) => notifyError(err),
  })

  // 最新一项是谁发的：他的“正在输入”随之消失
  const last = timeline.data?.at(-1)
  useEffect(() => {
    if (last?.kind === "message" || last?.kind === "note") {
      clearTyping(last.actor.type === "contact" || last.actor.type === "external_user" ? "contact" : last.actor.id)
    }
  }, [last, clearTyping])

  // 标记已读：页面在前台、时间线的最后一项比读到的位置新时推进，从后台切回前台时补标
  useSyncRead({
    ready: Boolean(c),
    readSeq: c?.read_seq ?? 0,
    lastSeq: last?.seq ?? 0,
    onMark: (seq) => {
      void tenantApi.conversation.read(id, { seq }).then(() =>
        queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() }),
      )
    },
  })

  const invalidate = () => queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
  const close = useMutation({ mutationFn: () => tenantApi.conversation.close(id), onSuccess: invalidate, onError: (err) => notifyError(err) })
  const reopen = useMutation({
    mutationFn: () => tenantApi.conversation.reopen(id),
    onSuccess: invalidate,
    onError: (err) => {
      notifyError(err)
      void invalidate()
    },
  })
  const markSpam = useMutation({
    mutationFn: () =>
      tenantApi.conversation.markSpam(id, {
        block_contact: spamBlockContact,
        reason: spamBlockContact ? spamReason.trim() || undefined : undefined,
      }),
    onSuccess: () => {
      setSpamModalOpen(false)
      void invalidate()
      void queryClient.invalidateQueries({ queryKey: kit.queries.contact.key() })
      notifySuccess(t("inbox.marked_spam"))
    },
    onError: (err) => notifyError(err),
  })
  const unmarkSpam = useMutation({
    mutationFn: () => tenantApi.conversation.unmarkSpam(id),
    onSuccess: () => {
      void invalidate()
      notifySuccess(t("inbox.unmarked_spam"))
    },
    onError: (err) => notifyError(err),
  })
  const contactHistory = useContactHistory(c?.status === "closed" ? c.contact?.id : undefined)
  const groupHistory = useGroupHistory(c?.status === "closed" ? c.external_group?.id : undefined)
  const siblingList = c?.external_group ? groupHistory.data?.items : contactHistory.data?.items
  const activeSibling = c?.status === "closed" ? siblingList?.find((x) => x.id !== c.id && x.status !== "closed") : undefined

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
        details.toggle()
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
  }, [close, next, status, details.toggle])

  // 还没有数据（直接打开链接或网络较慢）时显示与页面同样布局的骨架，详情栏保持原来的开合，数据到达后只替换内容
  if (!c) {
    if (conversation.isError) return <EmptyState fill title={t("inbox.not_found")} />
    return (
      <Group gap={0} h="100%" wrap="nowrap" align="stretch" style={{ flex: 1, minWidth: 0 }}>
        <ConversationSkeleton back={narrow ? back : undefined} />
        <AsidePane
          opened={details.opened}
          onClose={details.close}
          compactQuery={NARROW_QUERY}
          resizable={{ storageKey: "inbox-details", defaultWidth: 320, min: 260, max: 420 }}
        >
          <InspectorSkeleton onClose={details.close} />
        </AsidePane>
      </Group>
    )
  }
  const assignee =
    c.assignee?.type === "bot"
      ? `${t("inbox.bot_badge")} · ${c.assignee.name}`
      : c.assignee?.type === "member"
        ? c.assignee.id === me.data?.id
          ? t("inbox.you")
          : byId.get(c.assignee.id)?.name
        : undefined
  const generations: AgentGenerationDTO[] = generationsQuery.data ?? []
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

  // 当聊天列实际宽度小于 640px（例如中屏四栏并排）或移动端窄屏时，操作按钮改为图标 + Tooltip，给联系人名字留出宽度
  const compactActions = narrow || (mainWidth > 0 && mainWidth < 640)
  return (
    <Group gap={0} h="100%" wrap="nowrap" align="stretch" style={{ flex: 1, minWidth: 0 }}>
      <Box ref={mainRef} style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column" }}>
        {/* 宽屏锁定 52px 基准顶栏与相邻各栏齐平；窄屏操作按钮换到第二行 */}
        <PaneHeader fixed={!narrow} back={narrow ? back : undefined} style={narrow ? { paddingBlock: 8 } : undefined}>
          <Group gap="sm" wrap="nowrap" style={{ flex: 1, minWidth: 0 }}>
            <ConversationPeerAvatar c={c} size={32} />
            <Box style={{ minWidth: 0, flex: 1 }}>
              <Group gap={6} wrap="nowrap">
                {c.contact ? (
                  <Link
                    to="/$slug/contacts/$id"
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
                {c.external_group?.status === "pending_setup" && (
                  <Badge size="xs" variant="light" color="yellow" style={{ flex: "none" }}>
                    {t("extgroups.filter_pending")}
                  </Badge>
                )}
                {c.external_group?.status === "removed" && (
                  <Badge size="xs" variant="light" color="red" style={{ flex: "none" }}>
                    {t("extgroups.filter_removed")}
                  </Badge>
                )}
                {c.audience === "internal" && (
                  <Badge size="xs" variant="light" color="green" style={{ flex: "none" }}>
                    {t("inbox.audience_internal_badge")}
                  </Badge>
                )}
                {c.audience === "partner" && (
                  <Badge size="xs" variant="light" color="brand" style={{ flex: "none" }}>
                    {t("inbox.audience_partner_badge")}
                  </Badge>
                )}
                {c.handling_mode && c.handling_mode !== "human" && (
                  <Badge size="xs" variant="light" color={handlingModeColor(c.handling_mode)} style={{ flex: "none" }}>
                    {t(`inbox.handling_mode_${c.handling_mode}` as const)}
                  </Badge>
                )}
                {c.status !== "open" && (
                  <Badge size="xs" variant="light" color={c.status === "snoozed" ? "yellow" : "gray"} style={{ flex: "none" }}>
                    {t(`inbox.status_${c.status}` as const)}
                  </Badge>
                )}
                {c.spam_at && (
                  <Badge size="xs" variant="light" color="red" style={{ flex: "none" }}>
                    {t("inbox.spam_badge")}
                  </Badge>
                )}
                {c.contact?.blocked_at && (
                  <Badge size="xs" variant="light" color="red" style={{ flex: "none" }}>
                    {t("contacts.blocked_badge")}
                  </Badge>
                )}
              </Group>
              <Text size="xs" c="dimmed" truncate lh={1.3}>
                {meta}
              </Text>
            </Box>
          </Group>
          {viewers.length > 0 && (
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
                    <IconTicket size={16} />
                  </ActionIcon>
                </Tooltip>
              ) : (
                <Button variant="subtle" color="gray" leftSection={<IconTicket size={14} />} onClick={() => setCreateTicketOpen(true)}>
                  {t("tickets.create_from_conversation")}
                </Button>
              ))}
            {c.status !== "closed" && <SnoozeMenu id={c.id} compact={compactActions} />}
            {c.status === "closed" ? (
              activeSibling ? (
                compactActions ? (
                  <Tooltip label={t("inbox.go_to_active_conversation")}>
                    <ActionIcon
                      variant="light"
                      size="input-sm"
                      onClick={() => open(activeSibling.id)}
                      aria-label={t("inbox.go_to_active_conversation")}
                    >
                      <IconArrowRight size={16} />
                    </ActionIcon>
                  </Tooltip>
                ) : (
                  <Button variant="light" ml={2} rightSection={<IconArrowRight size={14} />} onClick={() => open(activeSibling.id)}>
                    {t("inbox.go_to_active_conversation")}
                  </Button>
                )
              ) : compactActions ? (
                <Tooltip label={t("inbox.reopen")}>
                  <ActionIcon variant="light" size="input-sm" loading={reopen.isPending} onClick={() => reopen.mutate()} aria-label={t("inbox.reopen")}>
                    <IconRefresh size={16} />
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
                  <IconCheck size={16} />
                </ActionIcon>
              </Tooltip>
            ) : (
              <Button variant="light" color="green" ml={2} leftSection={<IconCheck size={14} />} loading={close.isPending} onClick={() => close.mutate()}>
                {t("inbox.close")}
              </Button>
            )}
            {can("conversations:reply") && !c.spam_at && (
              <Tooltip label={t("inbox.mark_spam")}>
                <ActionIcon
                  variant="subtle"
                  color="gray"
                  size="input-sm"
                  onClick={() => {
                    setSpamBlockContact(false)
                    setSpamReason("")
                    setSpamModalOpen(true)
                  }}
                  aria-label={t("inbox.mark_spam")}
                >
                  <IconAlertOctagon size={16} />
                </ActionIcon>
              </Tooltip>
            )}
            <Divider orientation="vertical" h={16} mx={2} style={{ alignSelf: "center" }} />
            <Tooltip label={t("inbox.details")}>
              <ActionIcon
                variant={details.opened ? "light" : "subtle"}
                color={details.opened ? undefined : "gray"}
                size="input-sm"
                onClick={details.toggle}
                aria-label={t("inbox.details")}
              >
                <IconLayoutSidebarRight size={16} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </PaneHeader>
        {c.spam_at && (
          <Alert variant="light" color="deep-orange" radius={0} py={8} px="md" icon={<IconAlertOctagon size={16} />}>
            <Group justify="space-between" gap="sm" wrap="wrap">
              <Text size="xs">{t("inbox.spam_banner")}</Text>
              {can("conversations:reply") && (
                <Button size="compact-xs" variant="light" color="deep-orange" loading={unmarkSpam.isPending} onClick={() => unmarkSpam.mutate()}>
                  {t("inbox.unmark_spam")}
                </Button>
              )}
            </Group>
          </Alert>
        )}
        {c.contact?.blocked_at && (
          <Alert variant="light" color="red" radius={0} py={8} px="md" icon={<IconInfoCircle size={16} />}>
            <Text size="xs">
              {c.contact.blocked_reason
                ? t("contacts.blocked_banner_reason", { reason: c.contact.blocked_reason })
                : t("contacts.blocked_banner")}
            </Text>
          </Alert>
        )}
        {activeSibling && (
          <Alert variant="light" color="bright-blue" radius={0} py={8} px="md" icon={<IconInfoCircle size={16} />}>
            <Text size="xs">{t("inbox.active_conversation_banner")}</Text>
          </Alert>
        )}
        <Timeline
          c={c}
          readSeq={readAtOpen.current ?? 0}
          typing={typing}
          highlight={highlight}
          focusSeq={focusSeq}
          focusField={focusField}
          onReplyTo={c.external_group ? handleReplyTo : undefined}
          onCorrect={can("conversations:reply") ? handleCorrect : undefined}
        />
        {generations.length > 0 && (
          <Stack gap={6} px="md" py={8}>
            {generations.map((g) => {
              const pendingCalls = g.pending_approvals.filter((a) => a.status === "pending")
              if (g.state === "waiting_approval" && pendingCalls.length > 0) {
                return (
                  <Alert key={g.id} variant="light" color="deep-orange" radius="md" py={8} px="sm" icon={<IconRobot size={16} />}>
                    <Stack gap={6}>
                      <Text size="xs" fw={600}>
                        {t("inbox.ai_approval_title")}
                      </Text>
                      {pendingCalls.map((call) => (
                        <Group key={call.call_id} justify="space-between" gap="xs" wrap="wrap">
                          <Group gap={6} style={{ minWidth: 0, flex: 1 }}>
                            <Code>{call.name}</Code>
                            {call.arguments && (
                              <Text size="xs" c="dimmed" truncate>
                                {call.arguments}
                              </Text>
                            )}
                          </Group>
                          {can("conversations:reply") && (
                            <Group gap={6}>
                              <Button
                                size="compact-xs"
                                color="green"
                                leftSection={<IconCheck size={12} />}
                                loading={approveTool.isPending}
                                onClick={() => approveTool.mutate({ genId: g.id, callId: call.call_id })}
                              >
                                {t("inbox.ai_approve")}
                              </Button>
                              <Button
                                size="compact-xs"
                                variant="light"
                                color="red"
                                leftSection={<IconX size={12} />}
                                loading={rejectTool.isPending}
                                onClick={() => rejectTool.mutate({ genId: g.id, callId: call.call_id })}
                              >
                                {t("inbox.ai_reject")}
                              </Button>
                            </Group>
                          )}
                        </Group>
                      ))}
                    </Stack>
                  </Alert>
                )
              }
              if (g.state === "pending" || g.state === "streaming") {
                return (
                  <Alert key={g.id} variant="light" color="bright-blue" radius="md" py={8} px="sm" icon={<IconRobot size={16} />}>
                    <Group justify="space-between" gap="xs" wrap="nowrap">
                      <Stack gap={2} style={{ minWidth: 0, flex: 1 }}>
                        <Text size="xs" fw={600}>
                          {t("inbox.ai_generating")}
                        </Text>
                        {g.text && (
                          <Text size="xs" lineClamp={3}>
                            {g.text}
                          </Text>
                        )}
                      </Stack>
                      <Button
                        size="compact-xs"
                        variant="light"
                        color="gray"
                        leftSection={<IconPlayerStop size={12} />}
                        loading={cancelGeneration.isPending}
                        onClick={() => cancelGeneration.mutate(g.id)}
                      >
                        {t("inbox.ai_stop")}
                      </Button>
                    </Group>
                  </Alert>
                )
              }
              if (g.mode === "draft" && g.state === "completed" && g.text) {
                const titles = g.citations.map((ct) => ct.title).filter(Boolean).join(", ")
                return (
                  <Paper key={g.id} withBorder radius="md" px="sm" py={8} bg="var(--mantine-color-bright-blue-light)">
                    <Stack gap={4}>
                      <Group justify="space-between" gap="xs">
                        <Group gap={6}>
                          <IconRobot size={14} style={{ color: "var(--mantine-color-bright-blue-6)" }} />
                          <Text size="xs" fw={600} c="bright-blue">
                            {t("inbox.ai_draft_title")}
                          </Text>
                        </Group>
                        {can("conversations:reply") && (
                          <Button
                            size="compact-xs"
                            variant="light"
                            color="bright-blue"
                            onClick={() => {
                              setAdoptedDraft({ generationId: g.id, text: g.text })
                              composerRef.current?.focus("reply")
                            }}
                          >
                            {t("inbox.ai_draft_adopt")}
                          </Button>
                        )}
                      </Group>
                      <Text size="sm" style={{ whiteSpace: "pre-wrap" }}>
                        {g.text}
                      </Text>
                      {titles && (
                        <Text size="xs" c="dimmed">
                          {t("inbox.ai_citations", { titles })}
                        </Text>
                      )}
                    </Stack>
                  </Paper>
                )
              }
              return null
            })}
          </Stack>
        )}
        <Composer
          key={c.id}
          c={c}
          replyTo={replyToItem}
          onClearReplyTo={() => setReplyToItem(null)}
          correcting={correctingItem}
          onClearCorrecting={() => setCorrectingItem(null)}
          adoptedDraft={adoptedDraft}
          onClearAdoptedDraft={() => setAdoptedDraft(null)}
          extMentions={extMentions}
          onChangeExtMentions={setExtMentions}
          onTyping={notifyTyping}
          colleaguesTyping={typing.filter((x) => !x.contact)}
          onReplied={me.data?.open_next_after_reply && next ? next : undefined}
          handleRef={composerRef}
        />
      </Box>
      <Modal opened={spamModalOpen} onClose={() => setSpamModalOpen(false)} title={t("inbox.mark_spam_confirm")} centered>
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            {t("inbox.mark_spam_desc")}
          </Text>
          {c.contact && can("contacts:update") && !c.contact.blocked_at && (
            <Stack gap="xs">
              <Checkbox
                label={t("inbox.mark_spam_also_block")}
                checked={spamBlockContact}
                onChange={(e) => setSpamBlockContact(e.currentTarget.checked)}
              />
              {spamBlockContact && (
                <TextInput
                  label={t("contacts.block_reason_label")}
                  placeholder={t("contacts.block_reason_placeholder")}
                  maxLength={500}
                  value={spamReason}
                  onChange={(e) => setSpamReason(e.currentTarget.value)}
                />
              )}
            </Stack>
          )}
          <Group justify="flex-end" gap="xs">
            <Button variant="default" onClick={() => setSpamModalOpen(false)}>
              {t("inbox.cancel")}
            </Button>
            <Button color="red" loading={markSpam.isPending} onClick={() => markSpam.mutate()}>
              {t("inbox.mark_spam")}
            </Button>
          </Group>
        </Stack>
      </Modal>
      <AsidePane
        opened={details.opened}
        onClose={details.close}
        compactQuery={NARROW_QUERY}
        resizable={{ storageKey: "inbox-details", defaultWidth: 320, min: 260, max: 420 }}
      >
        <Details c={c} onClose={details.close} onOpen={open} />
      </AsidePane>
      <CreateTicketModal
        opened={createTicketOpen}
        onClose={() => setCreateTicketOpen(false)}
        sourceConversation={c}
        onCreated={(created) =>
          void navigate({
            to: "/$slug/inbox/tickets/$id",
            params: { slug, id: created.id },
            search: { box: "all", status: "unresolved" },
          })
        }
      />
    </Group>
  )
}
