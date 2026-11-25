import type { ReactNode } from "react"

// brandLogo 渲染平台或工作区 Logo 图片；地址为空时返回 undefined，交由布局回退到默认首字母标志。
export function brandLogo(url: string | undefined, alt: string | undefined, size = 26): ReactNode {
  if (!url) return undefined
  return (
    <img
      src={url}
      alt={alt ?? ""}
      style={{
        width: size,
        height: size,
        borderRadius: "var(--mantine-radius-md)",
        objectFit: "contain",
        flex: "none",
      }}
    />
  )
}
