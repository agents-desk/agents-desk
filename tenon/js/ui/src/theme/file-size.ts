// 文件大小格式化工具（无额外第三方依赖，供 core 轻量入口与完整主题格式层共用）。

/** 把字节数格式化为易读的文件大小，例如 512 B、1.2 KB、3.4 MB。 */
export function formatFileSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B"
  const units = ["B", "KB", "MB", "GB", "TB"]
  let value = bytes
  let i = 0
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024
    i++
  }
  return i === 0 ? `${Math.round(value)} B` : `${Math.round(value * 10) / 10} ${units[i]}`
}
