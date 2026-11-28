import type { ConversationChatDTO, ConversationSummary, ConversationViewSummary } from "@agents-desk/client/tenant"
import { ActionIcon, Badge, Menu, NavLink, Text, Tooltip } from "@mantine/core"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useMatch, useNavigate, useParams } from "@tanstack/react-router"
import {
  IconAlertOctagon,
  IconArrowForwardUp,
  IconAt,
  IconClockCheck,
  IconDots,
  IconFilter,
  IconInbox,
  IconListDetails,
  IconPencil,
  IconPlus,
  IconRobot,
  IconTicket,
  IconTrash,
  IconUserQuestion,
  IconUsers,
  IconUsersGroup,
} from "@tabler/icons-react"
import { confirm, Fill, MenuBadge, notifyError, notifySuccess, RailSection, useCan, useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { kit, tenantApi } from "../api"
import { useChat } from "../chat/data"
import { ChatSections, type ChatNavTarget } from "../chat/Nav"
import { NewChatModal } from "../chat/NewChatModal"
import { useAiWorkspaceEnabled } from "../components/ai-placeholders"
import { CreateTicketModal } from "../tickets/CreateTicketModal"
import type { TicketBox } from "../tickets/search"
import { type Thresholds, useThresholds } from "./data"
import { waitColor, waitLevel } from "./format"
import type { InboxSearch } from "./search"
import { ViewModal } from "./ViewModal"

/** useNow 返回现在的时间，每 30 秒更新一次：等待时长随之变化。 */
export function useNow(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(timer)
  }, [])
  return now
}

/** inChatGroup 报告聊天是否属于二级导航中的一个聊天分组：范围相同，内部聊天再看单聊或群聊，商务沟通再看所属的个人商务渠道。 */
function inChatGroup(chat: ConversationChatDTO, group: ChatNavTarget): boolean {
  if (chat.scope !== group.scope) return false
  if (group.scope === "internal") return !group.filter || chat.mode === group.filter
  return !group.channel || chat.channel?.id === group.channel
}

/** Count 是收件箱列表中一项的数量：等得最久的会话超过时限时，数字用等待时长的颜色。 */
function Count({ s, now, th }: { s?: ConversationSummary; now: number; th?: Thresholds }): ReactNode {
  if (!s) return null
  const color = s.oldest_waiting_since ? waitColor[waitLevel(s.oldest_waiting_since, now, th)] : undefined
  return <MenuBadge value={s.count > 0 ? s.count : undefined} variant="muted" color={color} />
}

/**
 * InboxNav 是收件箱模块的二级导航（nav 插槽），成员在这一个模块中处理所有的沟通：
 * 会话列表（我的、@我、未指派、全部）、团队收件箱、工单分组（我的、未指派、全部）、内部聊天和商务沟通分组、已保存视图。
 * 它在收件箱模块的布局路由中渲染一次，从匹配到的子路由（会话、工单、聊天）读出当前的分组和地址参数来高亮；
 * 在会话分组之间切换时保留打开着的会话；在聊天分组之间切换时，打开着的聊天属于目标分组才保持打开；换到另一类分组时进入它的列表。
 */
export function InboxNav(): ReactNode {
  const t = useT()
  const can = useCan()
  const now = useNow()
  const th = useThresholds()
  const { slug } = useParams({ from: "/$slug" })
  const { id } = useParams({ strict: false }) as { id?: string }
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const inbox = useQuery(kit.queries.conversation.inbox())
  const d = inbox.data

  // 当前打开的是哪一类分组：匹配到的子路由给出它的地址参数，其余两类为空
  const conversations = useMatch({ from: "/$slug/inbox/_conversations", shouldThrow: false })
  const tickets = useMatch({ from: "/$slug/inbox/tickets", shouldThrow: false })
  const chats = useMatch({ from: "/$slug/inbox/chat", shouldThrow: false })
  const search = conversations?.search
  const activeTicketBox = tickets?.search.box
  const openChat = useChat(chats && id ? id : "")
  const activeChat: ChatNavTarget | undefined = chats
    ? { scope: chats.search.scope ?? "internal", filter: chats.search.filter, channel: chats.search.channel }
    : undefined

  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<ConversationViewSummary | undefined>()
  const [createTicketOpen, setCreateTicketOpen] = useState(false)
  const [newChatOpen, setNewChatOpen] = useState(false)

  // navigateInbox 切换会话列表：已经在会话分组中时保留列表浏览参数和打开着的会话，同时清除搜索定位与旧筛选标签
  const navigateInbox = (patch: Partial<InboxSearch>) => {
    if (search) {
      const next = { ...search, q: undefined, scope: undefined, seq: undefined, matchField: undefined, tag: undefined, ...patch }
      void (id
        ? navigate({ to: "/$slug/inbox/$id", params: { slug, id }, search: next })
        : navigate({ to: "/$slug/inbox", params: { slug }, search: next }))
      return
    }
    void navigate({
      to: "/$slug/inbox",
      params: { slug },
      search: {
        box: patch.box ?? "mine",
        team: patch.team,
        view: patch.view,
        filter: patch.filter,
        tag: patch.tag,
        status: patch.status ?? "open",
        sort: patch.sort ?? "waiting",
      },
    })
  }

  // navigateTicket 切换工单列表：已经在工单分组中时保留筛选条件
  const navigateTicket = (box: TicketBox) => {
    void navigate({
      to: "/$slug/inbox/tickets",
      params: { slug },
      search: tickets ? { ...tickets.search, box } : { box, status: "unresolved" },
    })
  }

  // navigateChat 切换聊天分组：打开着的聊天属于目标分组时保持打开，否则回到目标分组的列表
  const navigateChat = (next: ChatNavTarget) => {
    const chatSearch = {
      scope: next.scope === "external" ? ("external" as const) : undefined,
      filter: next.scope === "internal" ? next.filter : undefined,
      channel: next.scope === "external" ? next.channel : undefined,
    }
    void (id && openChat.data && inChatGroup(openChat.data, next)
      ? navigate({ to: "/$slug/inbox/chat/$id", params: { slug, id }, search: chatSearch })
      : navigate({ to: "/$slug/inbox/chat", params: { slug }, search: chatSearch }))
  }

  const remove = useMutation({
    mutationFn: (id: string) => tenantApi.view.delete(id),
    onSuccess: (_res, id) => {
      notifySuccess(t("views.deleted"))
      void queryClient.invalidateQueries({ queryKey: kit.queries.conversation.key() })
      void queryClient.invalidateQueries({ queryKey: kit.queries.view.key() })
      if (search?.view === id) {
        navigateInbox({ view: undefined, filter: undefined, box: "mine", team: undefined })
      }
    },
    onError: (err) => notifyError(err),
  })

  const handleDelete = async (v: ConversationViewSummary) => {
    const ok = await confirm({
      title: t("views.delete_confirm", { name: v.name }),
      message: t(v.scope === "shared" ? "views.delete_message_shared" : "views.delete_message_personal"),
      danger: true,
      confirmLabel: t("views.delete"),
    })
    if (ok) remove.mutate(v.id)
  }

  const openCreateView = () => {
    setEditing(undefined)
    setModalOpen(true)
  }

  const openEditView = (v: ConversationViewSummary) => {
    setEditing(v)
    setModalOpen(true)
  }

  const item = (box: InboxSearch["box"], label: string, icon: ReactNode, s?: ConversationSummary) => (
    <NavLink
      key={box}
      label={label}
      leftSection={icon}
      rightSection={<Count s={s} now={now} th={th} />}
      active={Boolean(search && search.box === box && !search.team && !search.view)}
      onClick={() => navigateInbox({ box, team: undefined, view: undefined, filter: undefined })}
    />
  )

  const ticketItem = (box: TicketBox, label: string, icon: ReactNode, count?: number) => (
    <NavLink
      key={`ticket:${box}`}
      label={label}
      leftSection={icon}
      rightSection={<MenuBadge value={count && count > 0 ? count : undefined} variant="muted" />}
      active={activeTicketBox === box}
      onClick={() => navigateTicket(box)}
    />
  )

  const views = d?.views ?? []
  const aiEnabled = useAiWorkspaceEnabled()

  return (
    <>
      <Fill name="nav">
        {item("mine", t("inbox.box_mine"), <IconInbox size={16} stroke={1.6} />, d?.mine)}
        {item("mentions", t("inbox.box_mentions"), <IconAt size={16} stroke={1.6} />, d?.mentions)}
        {item("unassigned", t("inbox.box_unassigned"), <IconUserQuestion size={16} stroke={1.6} />, d?.unassigned)}
        {item("all", t("inbox.box_all"), <IconListDetails size={16} stroke={1.6} />, d?.all)}
        <NavLink
          key="spam"
          label={t("inbox.box_spam")}
          leftSection={<IconAlertOctagon size={16} stroke={1.6} />}
          rightSection={<MenuBadge value={d?.spam && d.spam > 0 ? d.spam : undefined} variant="muted" />}
          active={Boolean(search && search.box === "spam" && !search.team && !search.view)}
          onClick={() => navigateInbox({ box: "spam", team: undefined, view: undefined, filter: undefined })}
        />

        {aiEnabled && (
          <RailSection
            title={t("inbox.ai_section")}
            collapsible
            storeKey="inbox-ai-reception"
            action={
              <Badge size="xs" variant="light" color="brand" style={{ textTransform: "none", lineHeight: 1.2 }}>
                {t("ai_placeholder.coming_soon")}
              </Badge>
            }
          >
            <NavLink disabled label={t("inbox.ai_handling")} leftSection={<IconRobot size={16} stroke={1.6} />} />
            <NavLink disabled label={t("inbox.ai_pending_approval")} leftSection={<IconClockCheck size={16} stroke={1.6} />} />
            <NavLink disabled label={t("inbox.ai_escalated")} leftSection={<IconArrowForwardUp size={16} stroke={1.6} />} />
          </RailSection>
        )}

        {(d?.teams.length ?? 0) > 0 && (
          <RailSection title={t("inbox.teams")} collapsible storeKey="inbox-teams">
            {d?.teams.map((team) => {
              const active = Boolean(search && search.team === team.id && !search.view)
              return (
                <NavLink
                  key={team.id}
                  label={team.name}
                  leftSection={<IconUsersGroup size={16} stroke={1.6} />}
                  rightSection={<Count s={team} now={now} th={th} />}
                  active={active}
                  onClick={() => navigateInbox({ box: "all", team: team.id, view: undefined, filter: undefined })}
                />
              )
            })}
          </RailSection>
        )}

        <RailSection
          title={t("tickets.nav_section")}
          collapsible
          storeKey="inbox-tickets"
          action={
            can("conversations:reply") ? (
              <Tooltip label={t("tickets.create")} openDelay={300}>
                <ActionIcon
                  size="xs"
                  variant="subtle"
                  color="gray"
                  aria-label={t("tickets.create")}
                  onClick={() => setCreateTicketOpen(true)}
                >
                  <IconPlus size={14} stroke={1.6} />
                </ActionIcon>
              </Tooltip>
            ) : undefined
          }
        >
          {ticketItem("mine", t("tickets.box_mine"), <IconTicket size={16} stroke={1.6} />, d?.tickets.mine)}
          {ticketItem("unassigned", t("tickets.box_unassigned"), <IconUserQuestion size={16} stroke={1.6} />, d?.tickets.unassigned)}
          {ticketItem("all", t("tickets.box_all"), <IconListDetails size={16} stroke={1.6} />, d?.tickets.all)}
        </RailSection>

        <ChatSections active={activeChat} onSelect={navigateChat} onNewChat={() => setNewChatOpen(true)} />

        <RailSection
          title={t("views.section")}
          collapsible
          storeKey="inbox-views"
          action={
            <Tooltip label={t("views.create")} openDelay={300}>
              <ActionIcon size="xs" variant="subtle" color="gray" aria-label={t("views.create")} onClick={openCreateView}>
                <IconPlus size={14} stroke={1.6} />
              </ActionIcon>
            </Tooltip>
          }
        >
          {views.length === 0 ? (
            <Text size="xs" c="dimmed" px={10} py={4}>
              {t("views.empty")}
            </Text>
          ) : (
            views.map((v) => {
              const active = Boolean(search && search.view === v.id)
              return (
                <div key={v.id} className="inbox-view-row" data-editable={v.editable || undefined}>
                  <NavLink
                    label={v.name}
                    leftSection={
                      v.scope === "shared" ? <IconUsers size={16} stroke={1.6} /> : <IconFilter size={16} stroke={1.6} />
                    }
                    rightSection={
                      <span className="inbox-view-count">
                        <Count s={v} now={now} th={th} />
                      </span>
                    }
                    active={active}
                    onClick={() =>
                      navigateInbox({
                        box: "all",
                        team: undefined,
                        view: v.id,
                        filter: undefined,
                        tag: undefined,
                        sort: v.config.sort ?? "waiting",
                      })
                    }
                  />
                  {v.editable && (
                    <div className="inbox-view-actions">
                      <Menu position="bottom-end" withinPortal>
                        <Menu.Target>
                          <ActionIcon
                            size="xs"
                            variant="subtle"
                            color="gray"
                            aria-label={t("views.more_actions")}
                            onClick={(e) => e.stopPropagation()}
                          >
                            <IconDots size={14} />
                          </ActionIcon>
                        </Menu.Target>
                        <Menu.Dropdown>
                          <Menu.Item leftSection={<IconPencil size={14} />} onClick={() => openEditView(v)}>
                            {t("views.edit")}
                          </Menu.Item>
                          <Menu.Item leftSection={<IconTrash size={14} />} color="red" onClick={() => void handleDelete(v)}>
                            {t("views.delete")}
                          </Menu.Item>
                        </Menu.Dropdown>
                      </Menu>
                    </div>
                  )}
                </div>
              )
            })
          )}
        </RailSection>
      </Fill>

      <ViewModal
        opened={modalOpen}
        onClose={() => setModalOpen(false)}
        editing={editing}
        onSaved={(saved) =>
          navigateInbox({
            box: "all",
            team: undefined,
            view: saved.id,
            filter: undefined,
            tag: undefined,
            sort: saved.config.sort ?? "waiting",
          })
        }
      />

      <NewChatModal
        opened={newChatOpen}
        onClose={() => setNewChatOpen(false)}
        onOpened={(chat) => void navigate({ to: "/$slug/inbox/chat/$id", params: { slug, id: chat.id }, search: chats?.search ?? {} })}
      />

      <CreateTicketModal
        opened={createTicketOpen}
        onClose={() => setCreateTicketOpen(false)}
        onCreated={(created) =>
          void navigate({
            to: "/$slug/inbox/tickets/$id",
            params: { slug, id: created.id },
            search: tickets?.search ?? { box: "all", status: "unresolved" },
          })
        }
      />
    </>
  )
}
