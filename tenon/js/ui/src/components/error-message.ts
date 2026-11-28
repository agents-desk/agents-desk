// 接口错误信息格式化工具（不依赖弹窗与通知组件，可供轻量 core 入口与完整组件层共用）。

import { describeError, type ErrorInfo } from "@go-tenon/client"
import { text } from "../i18n/i18n"

/** 没有服务端文案时（断网等）按错误的种类显示的提示。 */
export function errorMessage(info: ErrorInfo): string {
  if (info.message) return info.message
  switch (info.kind) {
    case "network":
      return text("tenon.error.network")
    case "rate_limited":
      return info.retryAfter
        ? text("tenon.error.rate_limited_after", { seconds: info.retryAfter })
        : text("tenon.error.rate_limited")
    case "server":
      return text("tenon.error.server")
    case "forbidden":
      return text("tenon.error.forbidden")
    case "not_found":
      return text("tenon.error.not_found")
    default:
      return text("tenon.error.failed")
  }
}

/** 从任意未知错误值提取简明提示。 */
export function formatErrorMessage(err: unknown): string {
  return errorMessage(describeError(err))
}
