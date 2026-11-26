import { ActionIcon, Group, Menu, Tooltip } from "@mantine/core"
import { IconDots, IconPencil } from "@tabler/icons-react"
import { useT } from "@go-tenon/ui"
import type { ReactNode } from "react"

/** RowAction 是“更多”菜单中的一项。 */
export interface RowAction {
  label: string
  icon?: ReactNode
  /** 危险的操作（删除、解绑）显示为红色，排在分隔线之后。 */
  danger?: boolean
  disabled?: boolean
  onClick: () => void
}

/**
 * RowActions 是设置页表格最右侧的行操作，与框架 ResourcePage 一致：
 * 一个编辑按钮（铅笔），其余操作收进“更多”菜单，危险操作排在最后、红色。没有编辑时只显示“更多”。
 */
export function RowActions({ onEdit, editLabel, actions = [], children }: {
  onEdit?: () => void
  /** 编辑按钮的提示，默认“编辑”。 */
  editLabel?: string
  actions?: RowAction[]
  /** 编辑按钮之前的其他内容，例如“配置”链接按钮。 */
  children?: ReactNode
}): ReactNode {
  const t = useT()
  const normal = actions.filter((a) => !a.danger)
  const danger = actions.filter((a) => a.danger)
  const label = editLabel ?? t("common.edit")
  return (
    <Group gap={2} justify="flex-end" wrap="nowrap">
      {children}
      {onEdit && (
        <Tooltip label={label} openDelay={300}>
          <ActionIcon aria-label={label} onClick={onEdit}>
            <IconPencil size={16} stroke={1.6} />
          </ActionIcon>
        </Tooltip>
      )}
      {actions.length > 0 && (
        <Menu position="bottom-end" width={180} withinPortal>
          <Menu.Target>
            <ActionIcon aria-label={t("common.more")}>
              <IconDots size={16} stroke={1.6} />
            </ActionIcon>
          </Menu.Target>
          <Menu.Dropdown>
            {normal.map((a) => (
              <Menu.Item key={a.label} leftSection={a.icon} disabled={a.disabled} onClick={a.onClick}>
                {a.label}
              </Menu.Item>
            ))}
            {normal.length > 0 && danger.length > 0 && <Menu.Divider />}
            {danger.map((a) => (
              <Menu.Item key={a.label} leftSection={a.icon} color="red" disabled={a.disabled} onClick={a.onClick}>
                {a.label}
              </Menu.Item>
            ))}
          </Menu.Dropdown>
        </Menu>
      )}
    </Group>
  )
}
