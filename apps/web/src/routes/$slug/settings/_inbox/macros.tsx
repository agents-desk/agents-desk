import type { MacroActions, MacroDTO } from "@agents-desk/client/tenant"
import { Badge, Code, Group, Text } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { preloadResourceList, ResourcePage, TruncatedText, useT } from "@go-tenon/ui"
import type { ReactNode } from "react"
import { MacroForm } from "../../../../macros/MacroForm"
import { macros } from "../../../../resources"

// 快捷回复设置页：所有成员均可浏览共享快捷回复并管理自己的个人快捷回复；有 macros:manage 权限的成员可管理共享快捷回复。
// 表单使用框架的抽屉模式（form="drawer"）与自定义 renderForm。
export const Route = createFileRoute("/$slug/settings/_inbox/macros")({
  staticData: { title: "nav.macros", icon: "macro", order: 3 },
  validateSearch: macros.listSearch,
  // 预取当前条件下的列表：进入页面时表格直接显示数据
  loaderDeps: ({ search }) => search,
  loader: ({ context, deps }) => preloadResourceList(context.app.queryClient, macros, deps),
  component: MacrosPage,
})

function MacrosPage(): ReactNode {
  const t = useT()

  const renderActions = (actions: MacroActions | undefined): ReactNode => {
    if (!actions) return <Text size="xs" c="dimmed">—</Text>
    const badges: { key: string; label: string; color?: string }[] = []
    if (actions.team || actions.assignee) {
      badges.push({ key: "assign", label: t("macros.summary_assign"), color: "bright-blue" })
    }
    if (actions.priority === "high") {
      badges.push({ key: "priority", label: t("macros.summary_priority_high"), color: "deep-orange" })
    } else if (actions.priority === "normal") {
      badges.push({ key: "priority", label: t("macros.summary_priority_normal"), color: "gray" })
    }
    if (actions.add_tag_ids && actions.add_tag_ids.length > 0) {
      badges.push({ key: "tags", label: t("macros.summary_tag_count", { n: actions.add_tag_ids.length }), color: "brand" })
    }
    const attrCount = actions.set_attributes ? Object.keys(actions.set_attributes).length : 0
    if (attrCount > 0) {
      badges.push({ key: "attrs", label: t("macros.summary_attr_count", { n: attrCount }), color: "violet" })
    }
    if (actions.status?.kind === "close") {
      badges.push({ key: "status", label: t("macros.summary_close"), color: "green" })
    } else if (actions.status?.kind === "snooze") {
      badges.push({ key: "status", label: t("macros.summary_snooze"), color: "yellow" })
    }
    if (badges.length === 0) return <Text size="xs" c="dimmed">—</Text>
    return (
      <Group gap={4} wrap="wrap">
        {badges.map((b) => (
          <Badge key={b.key} size="xs" variant="light" color={b.color}>
            {b.label}
          </Badge>
        ))}
      </Group>
    )
  }

  return (
    <ResourcePage
      model={macros}
      route={Route}
      form="drawer"
      formSize="lg"
      description={t("macros.description")}
      renderForm={(ctx) => <MacroForm ctx={ctx} />}
      render={{
        title: (v, row) => {
          const item = row as unknown as MacroDTO
          return (
            <Group gap="xs" wrap="nowrap">
              <Text size="sm" fw={500} truncate>
                {String(v)}
              </Text>
              {item.shortcut && <Code>/{item.shortcut}</Code>}
            </Group>
          )
        },
        scope: (v) => (
          <Badge size="sm" variant="light" color={v === "shared" ? "brand" : "gray"}>
            {t(v === "shared" ? "inbox.macro_scope_shared" : "inbox.macro_scope_personal")}
          </Badge>
        ),
        body: (v) => {
          const s = typeof v === "string" ? v : ""
          return s ? <TruncatedText value={s} /> : <Text size="xs" c="dimmed">—</Text>
        },
        actions: (v) => renderActions(v as MacroActions | undefined),
      }}
    />
  )
}
