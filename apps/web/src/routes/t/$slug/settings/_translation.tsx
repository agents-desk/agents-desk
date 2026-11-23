import { createFileRoute } from "@tanstack/react-router"

// 二级导航中的“翻译”分组，包含语气、术语表。
export const Route = createFileRoute("/t/$slug/settings/_translation")({
  staticData: { title: "settings.group_translation", order: 4 },
})
