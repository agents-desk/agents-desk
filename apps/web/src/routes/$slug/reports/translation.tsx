import { createFileRoute } from "@tanstack/react-router"
import { translationReportSearch } from "../../../reports/search"
import { TranslationPage } from "../../../reports/TranslationPage"

// AI 与翻译报表：模型调用量、成功率、token、费用、按业务场景与语言分布，以及 AI Agent 效能占位。
export const Route = createFileRoute("/$slug/reports/translation")({
  staticData: { title: "nav.reports_ai", icon: "ai", order: 7, permission: "reports:read" },
  validateSearch: translationReportSearch({ defaultPreset: "7d" }),
  component: () => <TranslationPage route={Route} />,
})
