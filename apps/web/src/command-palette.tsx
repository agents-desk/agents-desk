import { Tooltip, UnstyledButton } from "@mantine/core"
import { useOs } from "@mantine/hooks"
import { Spotlight, type SpotlightActionData, type SpotlightActionGroupData, spotlight } from "@mantine/spotlight"
import { IconAt, IconInbox, IconListDetails, IconSearch, IconUserQuestion, IconUsersGroup } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Icon, type MenuItem, useCan, useNavigation, useT } from "@go-tenon/ui"
import { type ReactNode, useMemo } from "react"
import { kit } from "./api"

/** pages 将菜单树拍平为叶子页面列表，保留上级分组名称和最近图标。 */
function flattenPages(
  items: readonly MenuItem[],
  trail: string[] = [],
  icon?: string,
): { item: MenuItem; trail: string[]; icon?: string }[] {
  return items.flatMap((item) =>
    item.children?.length ? flattenPages(item.children, [...trail, item.title], item.icon ?? icon) : [{ item, trail, icon: item.icon ?? icon }],
  )
}

/** CommandSearchButton 是放在图标栏顶部的全局搜索入口，点击或按 Cmd/Ctrl + K 打开命令面板。 */
export function CommandSearchButton(): ReactNode {
  const t = useT()
  const os = useOs()
  const shortcut = os === "macos" || os === "ios" ? "⌘K" : "Ctrl+K"
  return (
    <Tooltip label={`${t("command.search")}  ${shortcut}`} position="right" openDelay={350}>
      <UnstyledButton className="tn-rail-module" aria-label={t("command.search")} onClick={spotlight.open}>
        <IconSearch size={18} stroke={1.6} />
      </UnstyledButton>
    </Tooltip>
  )
}

/**
 * CommandPalette 是工作台的全局命令面板（Cmd/Ctrl + K）：
 * 支持快速跳转收件箱各视图、团队收件箱，以及当前成员有权限访问的所有页面和设置项。
 */
export function CommandPalette({ slug }: { slug: string }): ReactNode {
  const t = useT()
  const can = useCan()
  const { menu } = useNavigation()
  const navigate = useNavigate()
  const canReadInbox = can("conversations:read")
  const inbox = useQuery({ ...kit.queries.conversation.inbox(), enabled: canReadInbox })

  const actions = useMemo<SpotlightActionGroupData[]>(() => {
    const groups: SpotlightActionGroupData[] = []

    if (canReadInbox) {
      const boxSearch = { status: "open" as const, sort: "waiting" as const }
      const inboxActions: SpotlightActionData[] = [
        {
          id: "inbox:mine",
          label: t("inbox.box_mine"),
          description: t("nav.inbox"),
          leftSection: <IconInbox size={16} stroke={1.6} />,
          onClick: () => void navigate({ to: "/t/$slug/inbox", params: { slug }, search: { ...boxSearch, box: "mine" } }),
        },
        {
          id: "inbox:mentions",
          label: t("inbox.box_mentions"),
          description: t("nav.inbox"),
          leftSection: <IconAt size={16} stroke={1.6} />,
          onClick: () => void navigate({ to: "/t/$slug/inbox", params: { slug }, search: { ...boxSearch, box: "mentions" } }),
        },
        {
          id: "inbox:unassigned",
          label: t("inbox.box_unassigned"),
          description: t("nav.inbox"),
          leftSection: <IconUserQuestion size={16} stroke={1.6} />,
          onClick: () => void navigate({ to: "/t/$slug/inbox", params: { slug }, search: { ...boxSearch, box: "unassigned" } }),
        },
        {
          id: "inbox:all",
          label: t("inbox.box_all"),
          description: t("nav.inbox"),
          leftSection: <IconListDetails size={16} stroke={1.6} />,
          onClick: () => void navigate({ to: "/t/$slug/inbox", params: { slug }, search: { ...boxSearch, box: "all" } }),
        },
        ...(inbox.data?.teams ?? []).map((team) => ({
          id: `inbox:team:${team.id}`,
          label: team.name,
          description: t("inbox.teams"),
          leftSection: <IconUsersGroup size={16} stroke={1.6} />,
          onClick: () =>
            void navigate({ to: "/t/$slug/inbox", params: { slug }, search: { ...boxSearch, box: "all", team: team.id } }),
        })),
      ]
      groups.push({ group: t("command.inbox_group"), actions: inboxActions })
    }

    const pageActions: SpotlightActionData[] = flattenPages(menu).map(({ item, trail, icon }) => ({
      id: `page:${item.id}`,
      label: item.title,
      description: trail.length > 0 ? trail.join(" / ") : undefined,
      leftSection: <Icon name={icon ?? "apps"} size={16} stroke={1.6} />,
      onClick: () => void navigate({ to: item.path }),
    }))
    if (pageActions.length > 0) {
      groups.push({ group: t("command.pages_group"), actions: pageActions })
    }
    return groups
  }, [canReadInbox, inbox.data?.teams, menu, navigate, slug, t])

  return (
    <Spotlight
      actions={actions}
      shortcut="mod + K"
      nothingFound={t("command.empty")}
      highlightQuery
      limit={20}
      scrollable
      maxHeight={420}
      searchProps={{
        leftSection: <IconSearch size={16} stroke={1.6} />,
        placeholder: t("command.placeholder"),
      }}
    />
  )
}
