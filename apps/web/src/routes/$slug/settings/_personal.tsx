import { createFileRoute } from "@tanstack/react-router"

// 设置中“个人”分组：个人设置（基本资料、修改密码、偏好设置、通知）与我的商务渠道。
export const Route = createFileRoute("/$slug/settings/_personal")({
  staticData: { title: "settings.group_personal", order: 0 },
})
