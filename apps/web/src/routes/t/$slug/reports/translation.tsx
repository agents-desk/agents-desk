import { createFileRoute } from "@tanstack/react-router"
import { periodSearch } from "@go-tenon/ui"
import { TranslationPage } from "../../../../reports/TranslationPage"

// 翻译报表：翻译模型调用量、成功率、Token、费用与语言分布。
export const Route = createFileRoute("/t/$slug/reports/translation")({
  staticData: { title: "nav.reports_translation", icon: "language", order: 5, permission: "reports:read" },
  validateSearch: periodSearch({ defaultPreset: "7d" }),
  component: () => <TranslationPage route={Route} />,
})
