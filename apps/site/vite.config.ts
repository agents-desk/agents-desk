import { defineConfig } from "vite"
import { tenon } from "@go-tenon/start/vite"

// 官网首页在构建时预渲染成静态 HTML，由 Go 直接返回，兼顾 SEO 首屏与零额外 SSR 进程开销。
export default defineConfig({
  plugins: [tenon({ prerender: ["/"] })],
})
