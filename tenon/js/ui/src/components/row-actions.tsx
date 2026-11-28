// 列表/表格最右侧行操作组：独立编辑按钮，其余操作收入“更多”菜单，危险操作置于分隔线后以红色显示。

import { ActionIcon, Group, Menu, Tooltip } from "@mantine/core"
import { IconDots, IconPencil } from "@tabler/icons-react"
import type { ReactNode } from "react"
import { useText } from "../i18n/i18n"

/** “更多”菜单中的一项操作。 */
export interface RowMenuAction {
  label: string
  icon?: ReactNode
  /** 危险操作（删除、停用等）排在分隔线后并以红色显示。 */
  danger?: boolean
  disabled?: boolean
  /** 当前操作是否处于提交中状态。 */
  loading?: boolean
  onClick: () => void
}

/** RowActions 的选项。 */
export interface RowActionsProps {
  /** 点击编辑按钮；省略时不显示铅笔图标。 */
  onEdit?: () => void
  /** 编辑按钮提示文字，默认读取 tenon.common.edit。 */
  editLabel?: string
  /** 收进“更多”菜单的操作项。 */
  actions?: readonly RowMenuAction[]
  /** 操作按钮是否处于加载中（如直接点击行内删除或菜单操作）。 */
  loading?: boolean
  /** 放在编辑按钮之前的附加按钮或链接。 */
  children?: ReactNode
}

/** 行操作组：编辑图标与“更多”操作菜单。 */
export function RowActions({ onEdit, editLabel, actions = [], loading, children }: RowActionsProps): ReactNode {
  const t = useText()
  const normal = actions.filter((a) => !a.danger)
  const danger = actions.filter((a) => a.danger)
  const label = editLabel ?? t("tenon.common.edit")
  const menuLoading = Boolean(loading || actions.some((a) => a.loading))
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
            <ActionIcon aria-label={t("tenon.common.more")} loading={menuLoading}>
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
