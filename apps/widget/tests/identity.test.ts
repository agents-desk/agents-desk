import { beforeEach, describe, expect, test } from "bun:test"
import type { Client, WidgetSessionBody, WidgetSessionDTO } from "@agents-desk/client/widget"
import { createIdentity } from "../src/identity"

/** 构造仅包含 sub 载荷的模拟 JWT，用于验证本地用户身份切换逻辑。 */
function fakeJWT(sub: string): string {
  const payload = btoa(JSON.stringify({ sub })).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
  return `hdr.${payload}.sig`
}

/** 构造内存版 localStorage，方便在单测中完全隔离浏览器状态。 */
function createMemoryStorage() {
  const map = new Map<string, string>()
  return {
    getItem: (k: string) => map.get(k) ?? null,
    setItem: (k: string, v: string) => {
      map.set(k, v)
    },
    removeItem: (k: string) => {
      map.delete(k)
    },
    clear: () => map.clear(),
    key: (i: number) => [...map.keys()][i] ?? null,
    get length() {
      return map.size
    },
  }
}

describe("widget identity lazy lead creation", () => {
  let seq = 0
  let calls: WidgetSessionBody[] = []

  const mockClient = () =>
    ({
      widget: {
        session: async (body: WidgetSessionBody): Promise<WidgetSessionDTO> => {
          calls.push(body)
          seq++
          const isUser = Boolean(body.user_token)
          return {
            token: `tok-${seq}`,
            visitor_id: isUser ? undefined : (body.visitor_id ?? `vis-${seq}`),
            contact: {
              id: `c-${seq}`,
              kind: isUser ? "user" : "lead",
              name: isUser ? "User" : "",
              email: "",
              language: "",
              language_manual: false,
              location: "",
              time_zone: "",
              tags: [],
              attributes: {},
            },
          }
        },
      },
    }) as unknown as Client

  beforeEach(() => {
    seq = 0
    calls = []
    Object.defineProperty(globalThis, "localStorage", {
      value: createMemoryStorage(),
      configurable: true,
      writable: true,
    })
  })

  test("embedded collapsed anonymous visitor creates lead only on first open", async () => {
    const id = createIdentity()
    id.connect(mockClient())

    await id.start(undefined, "https://shop.example.com", false)
    expect(calls.length).toBe(0)
    expect(id.contact()).toBeUndefined()

    await Promise.all([id.ensure(), id.ensure()])
    expect(calls.length).toBe(1)
    expect(calls[0].visitor_id).toBeUndefined()
    expect(id.contact()?.kind).toBe("lead")

    await id.ensure()
    expect(calls.length).toBe(1)
  })

  test("returning visitor with stored visitor_id restores session while collapsed", async () => {
    globalThis.localStorage.setItem("agents-desk.widget..visitor", "vis-existing")
    const id = createIdentity()
    id.connect(mockClient())

    await id.start(undefined, undefined, false)
    expect(calls.length).toBe(1)
    expect(calls[0].visitor_id).toBe("vis-existing")
    expect(id.contact()?.kind).toBe("lead")
  })

  test("signed-in user starts session while collapsed and stays idle after collapsed logout", async () => {
    const id = createIdentity()
    id.connect(mockClient())

    await id.start(fakeJWT("u-42"), undefined, false)
    expect(calls.length).toBe(1)
    expect(id.contact()?.kind).toBe("user")

    await id.logout(false)
    expect(calls.length).toBe(1)
    expect(id.contact()).toBeUndefined()

    await id.ensure()
    expect(calls.length).toBe(2)
    expect(id.contact()?.kind).toBe("lead")
  })

  test("standalone mode creates lead immediately on mount", async () => {
    const id = createIdentity()
    id.connect(mockClient())

    await id.start(undefined, undefined, true)
    expect(calls.length).toBe(1)
    expect(id.contact()?.kind).toBe("lead")
  })
})
