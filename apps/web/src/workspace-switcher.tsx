import {
  Avatar,
  Badge,
  Box,
  Divider,
  Group,
  Menu,
  ScrollArea,
  Stack,
  Text,
  TextInput,
  Tooltip,
  UnstyledButton,
} from "@mantine/core"
import {
  IconCheck,
  IconLayoutGrid,
  IconLock,
  IconSearch,
  IconSelector,
  IconSettings,
} from "@tabler/icons-react"
import { useNavigate } from "@tanstack/react-router"
import { useT, useTenant } from "@go-tenon/ui"
import { type ReactNode, useMemo, useState } from "react"
import { useWorkspaces } from "./workspace"

/** WorkspaceSwitcher 的当前工作区信息。 */
export interface WorkspaceSwitcherTenant {
  slug: string
  name: string
  logo_url?: string
}

/**
 * WorkspaceSwitcher 位于左侧图标栏顶部（RailLayout.logo），提供当前工作区标识与一键直切菜单：
 * - 普通会话列出账号所属全部工作区（含 Logo、slug、停用状态），超过 6 个自动出现搜索框；
 * - 点击目标工作区直接调用 useTenant().switchTo，自带未保存修改确认与缓存/连接重建；
 * - 平台管理员代为登录时锁定在当前工作区，仅展示提示与工作区入口。
 */
export function WorkspaceSwitcher({
  tenant,
  impersonation,
}: {
  tenant: WorkspaceSwitcherTenant
  impersonation: boolean
}): ReactNode {
  const t = useT()
  const navigate = useNavigate()
  const { switchTo } = useTenant()
  const { items } = useWorkspaces()
  const [opened, setOpened] = useState(false)
  const [search, setSearch] = useState("")

  const kw = search.trim().toLowerCase()
  const { activeList, disabledList } = useMemo(() => {
    const matches = (name: string, slug: string) =>
      !kw || name.toLowerCase().includes(kw) || slug.toLowerCase().includes(kw)
    const filtered = items.filter((item) => matches(item.name, item.slug))
    return {
      activeList: filtered.filter((item) => item.status === "active"),
      disabledList: filtered.filter((item) => item.status !== "active"),
    }
  }, [items, kw])

  const canSwitch = !impersonation && items.length > 1
  const showSearch = !impersonation && items.length > 6

  const handleSelect = async (slug: string) => {
    setOpened(false)
    if (slug === tenant.slug) return
    await switchTo(slug)
  }

  return (
    <Menu
      opened={opened}
      onChange={(next) => {
        setOpened(next)
        if (!next) setSearch("")
      }}
      width={276}
      position="right-start"
      offset={10}
      shadow="md"
      radius="md"
    >
      <Menu.Target>
        <Tooltip
          label={canSwitch ? `${tenant.name} · ${t("workspace.switch_tooltip")}` : tenant.name}
          position="right"
          openDelay={300}
          disabled={opened}
        >
          <UnstyledButton
            className="desk-ws-trigger"
            data-active={opened || undefined}
            aria-label={t("workspace.switch_label")}
          >
            <Avatar src={tenant.logo_url || null} size={30} radius="md" color="brand" variant="filled">
              {tenant.name.slice(0, 1)}
            </Avatar>
            {canSwitch && (
              <span className="desk-ws-trigger-badge" aria-hidden="true">
                <IconSelector size={10} stroke={2.4} />
              </span>
            )}
          </UnstyledButton>
        </Tooltip>
      </Menu.Target>

      <Menu.Dropdown p={0} className="desk-ws-dropdown">
        <Box px="sm" py="xs" className="desk-ws-header">
          <Group gap="sm" wrap="nowrap">
            <Avatar src={tenant.logo_url || null} size={32} radius="md" color="brand" variant="filled">
              {tenant.name.slice(0, 1)}
            </Avatar>
            <Box style={{ flex: 1, minWidth: 0 }}>
              <Text size="sm" fw={600} truncate>
                {tenant.name}
              </Text>
              <Text size="xs" c="dimmed" truncate>
                /app/{tenant.slug}/
              </Text>
            </Box>
          </Group>
          {impersonation && (
            <Group gap={6} mt={8} px={8} py={4} className="desk-ws-locked-note" wrap="nowrap">
              <IconLock size={13} stroke={1.8} style={{ flex: "none" }} />
              <Text size="xs">{t("workspace.impersonation_locked")}</Text>
            </Group>
          )}
        </Box>

        {!impersonation && items.length > 0 && (
          <>
            <Divider />
            {showSearch && (
              <Box p={6} pb={2}>
                <TextInput
                  size="xs"
                  placeholder={t("workspace.search_placeholder")}
                  leftSection={<IconSearch size={13} stroke={1.8} />}
                  value={search}
                  onChange={(e) => setSearch(e.currentTarget.value)}
                  onClick={(e) => e.stopPropagation()}
                  onKeyDown={(e) => e.stopPropagation()}
                />
              </Box>
            )}
            <ScrollArea.Autosize mah={260} type="hover">
              <Stack gap={2} p={4}>
                {activeList.length === 0 && disabledList.length === 0 ? (
                  <Text size="xs" c="dimmed" ta="center" py="sm">
                    {t("workspace.no_match")}
                  </Text>
                ) : (
                  <>
                    {activeList.map((ws) => {
                      const current = ws.slug === tenant.slug
                      return (
                        <Menu.Item
                          key={ws.slug}
                          className="desk-ws-item"
                          data-current={current || undefined}
                          onClick={() => void handleSelect(ws.slug)}
                          leftSection={
                            <Avatar src={ws.logo_url || null} size={24} radius="sm" color="brand" variant="light">
                              {ws.name.slice(0, 1)}
                            </Avatar>
                          }
                          rightSection={
                            current ? <IconCheck size={15} stroke={2.2} className="desk-ws-check" /> : undefined
                          }
                        >
                          <Box style={{ minWidth: 0 }}>
                            <Text size="sm" fw={current ? 600 : 500} truncate>
                              {ws.name}
                            </Text>
                            <Text size="xs" c="dimmed" truncate>
                              {ws.slug}
                            </Text>
                          </Box>
                        </Menu.Item>
                      )
                    })}
                    {disabledList.map((ws) => (
                      <Menu.Item
                        key={ws.slug}
                        disabled
                        leftSection={
                          <Avatar src={ws.logo_url || null} size={24} radius="sm" color="gray" variant="light">
                            {ws.name.slice(0, 1)}
                          </Avatar>
                        }
                        rightSection={
                          <Badge size="xs" variant="light" color="gray">
                            {t("choose_tenant.disabled")}
                          </Badge>
                        }
                      >
                        <Text size="sm" c="dimmed" truncate>
                          {ws.name}
                        </Text>
                      </Menu.Item>
                    ))}
                  </>
                )}
              </Stack>
            </ScrollArea.Autosize>
          </>
        )}

        <Divider />
        <Box p={4}>
          <Menu.Item
            leftSection={<IconSettings size={15} stroke={1.6} />}
            onClick={() => {
              setOpened(false)
              void navigate({ to: "/$slug/settings", params: { slug: tenant.slug } })
            }}
          >
            {t("workspace.settings")}
          </Menu.Item>
          {!impersonation && (
            <Menu.Item
              leftSection={<IconLayoutGrid size={15} stroke={1.6} />}
              onClick={() => {
                setOpened(false)
                void navigate({ to: "/workspaces" })
              }}
            >
              {t("workspace.all_workspaces")}
            </Menu.Item>
          )}
        </Box>
      </Menu.Dropdown>
    </Menu>
  )
}
