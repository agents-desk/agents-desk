import { afterEach, describe, expect, test } from "bun:test"

type MessageListener = (e: MessageEvent) => void

/** 构造模拟的浏览器窗口环境，用于独立验证 host.ts 在不同嵌入参数下的握手与外链行为。 */
function installMockBrowser(search: string, inIframe: boolean) {
  const listeners = new Set<MessageListener>()
  const posted: Array<{ message: unknown; targetOrigin: string }> = []
  const opened: string[] = []
  const parentWin = {
    postMessage: (message: unknown, targetOrigin: string) => {
      posted.push({ message, targetOrigin })
    },
  }
  const dataset: Record<string, string> = {}
  const win: Record<string, unknown> = {
    addEventListener: (type: string, fn: MessageListener) => {
      if (type === "message") listeners.add(fn)
    },
    removeEventListener: (type: string, fn: MessageListener) => {
      if (type === "message") listeners.delete(fn)
    },
    open: (url: string) => {
      opened.push(url)
    },
  }
  win.parent = inIframe ? parentWin : win

  Object.defineProperty(globalThis, "location", { value: { search }, configurable: true, writable: true })
  Object.defineProperty(globalThis, "window", { value: win, configurable: true, writable: true })
  Object.defineProperty(globalThis, "document", {
    value: { documentElement: { dataset }, referrer: "" },
    configurable: true,
    writable: true,
  })

  return {
    posted,
    opened,
    dataset,
    dispatchFromParent: (data: unknown) => {
      for (const fn of [...listeners]) {
        fn({ source: parentWin, data } as unknown as MessageEvent)
      }
    },
  }
}

describe("widget host inline embed and bridge messages", () => {
  afterEach(() => {
    delete (globalThis as Record<string, unknown>).window
    delete (globalThis as Record<string, unknown>).document
    delete (globalThis as Record<string, unknown>).location
  })

  test("inline embed uses opaque page, respects hide_close, delegates open_url, and emits need_token", async () => {
    const env = installMockBrowser("?channel=ch-1&embed=1&inline=1", true)
    const host = await import(`../src/host.ts?case=inline-open-url`)

    expect(host.embedded).toBe(true)
    expect(host.inline).toBe(true)
    expect(env.dataset.embedded).toBeUndefined()
    expect(host.hasCloseButton()).toBe(true)
    expect(host.delegatesOpenLink()).toBe(false)

    const initPromise = host.waitInit()
    expect(env.posted).toEqual([{ message: { type: "agents-desk:ready" }, targetOrigin: "*" }])

    env.dispatchFromParent({
      type: "agents-desk:init",
      user_token: "tok-jwt",
      mobile: false,
      referrer: "https://app.example.com/support",
      open_url: true,
    })
    const init = await initPromise
    expect(init).toEqual({
      userToken: "tok-jwt",
      mobile: true,
      referrer: "https://app.example.com/support",
    })
    expect(host.delegatesOpenLink()).toBe(true)

    host.openLink("https://docs.example.com")
    expect(env.opened).toEqual([])
    expect(env.posted[1]).toEqual({
      message: { type: "agents-desk:open_url", url: "https://docs.example.com" },
      targetOrigin: "*",
    })

    host.tell("need_token")
    expect(env.posted[2]).toEqual({
      message: { type: "agents-desk:need_token" },
      targetOrigin: "*",
    })
  })

  test("inline embed with hide_close=1 hides close button and defaults to window.open when open_url is unset", async () => {
    const env = installMockBrowser("?channel=ch-1&embed=1&inline=1&hide_close=1", true)
    const host = await import(`../src/host.ts?case=inline-hide-close`)

    expect(host.inline).toBe(true)
    expect(host.hasCloseButton()).toBe(false)

    const initPromise = host.waitInit()
    env.dispatchFromParent({ type: "agents-desk:init", user_token: "" })
    await initPromise

    expect(host.delegatesOpenLink()).toBe(false)
    host.openLink("https://example.com/help")
    expect(env.opened).toEqual(["https://example.com/help"])
  })

  test("floating embed sets data-embedded for transparent background", async () => {
    const env = installMockBrowser("?channel=ch-1&embed=1", true)
    const host = await import(`../src/host.ts?case=floating`)

    expect(host.embedded).toBe(true)
    expect(host.inline).toBe(false)
    expect(env.dataset.embedded).toBe("")
    expect(host.hasCloseButton()).toBe(true)
  })
})
