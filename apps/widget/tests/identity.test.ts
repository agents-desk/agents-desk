import { beforeEach, describe, expect, test } from "bun:test"
import { ApiError, type Client, type WidgetSessionBody, type WidgetSessionDTO } from "@agents-desk/client/widget"
import { createIdentity } from "../src/identity"

/** 构造模拟 JWT，支持指定 sub 和可选的过期时间戳 exp（秒）。 */
function fakeJWT(sub: string, exp?: number): string {
  const claims: Record<string, unknown> = { sub }
  if (exp !== undefined) claims.exp = exp
  const payload = btoa(JSON.stringify(claims)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
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

  test("login with same user updates token silently without switching or re-establishing", async () => {
    const id = createIdentity()
    id.connect(mockClient())
    const now = Math.floor(Date.now() / 1000)

    await id.start(fakeJWT("u-42", now + 600), undefined, true)
    expect(calls.length).toBe(1)

    let notified = 0
    id.subscribe(() => notified++)

    const fresh = fakeJWT("u-42", now + 3600)
    const switched = await id.login(fresh)
    expect(switched).toBe(false)
    expect(calls.length).toBe(1)
    expect(notified).toBe(0)

    // 续签后 renew 使用刚更新的 token 建立新会话
    await id.renew()
    expect(calls.length).toBe(2)
    expect(calls[1].user_token).toBe(fresh)
  })

  test("login with different user switches session and returns true", async () => {
    const id = createIdentity()
    id.connect(mockClient())
    const now = Math.floor(Date.now() / 1000)

    await id.start(fakeJWT("u-42", now + 600), undefined, true)
    expect(calls.length).toBe(1)

    let notified = 0
    id.subscribe(() => notified++)

    const switched = await id.login(fakeJWT("u-99", now + 600))
    expect(switched).toBe(true)
    expect(calls.length).toBe(2)
    expect(notified).toBe(1)
  })

  test("renew requests fresh token from host when cached user_token is expired", async () => {
    const now = Math.floor(Date.now() / 1000)
    const expired = fakeJWT("u-42", now - 10)
    const fresh = fakeJWT("u-42", now + 3600)
    let requested = 0
    let loginResult: Promise<boolean> | undefined

    const id = createIdentity({
      tokenTimeoutMs: 200,
      requestToken: () => {
        requested++
        queueMicrotask(() => {
          loginResult = id.login(fresh)
        })
      },
    })
    id.connect(mockClient())

    await id.start(fakeJWT("u-42", now + 600), undefined, true)
    expect(calls.length).toBe(1)
    // 模拟已缓存一个过期的 user_token
    await id.login(expired)
    expect(calls.length).toBe(1)

    await id.renew()
    expect(requested).toBe(1)
    expect(calls.length).toBe(2)
    expect(calls[1].user_token).toBe(fresh)
    expect(await loginResult).toBe(false)
  })

  test("renew requests fresh token when server rejects cached token with 401", async () => {
    const now = Math.floor(Date.now() / 1000)
    const rotated = fakeJWT("u-42", now + 600)
    const fresh = fakeJWT("u-42", now + 3600)
    let requested = 0

    const id = createIdentity({
      tokenTimeoutMs: 200,
      requestToken: () => {
        requested++
        queueMicrotask(() => {
          void id.login(fresh)
        })
      },
    })
    const client = {
      widget: {
        session: async (body: WidgetSessionBody): Promise<WidgetSessionDTO> => {
          calls.push(body)
          if (calls.length === 2 && body.user_token === rotated) {
            throw new ApiError(401, { code: "widget.invalid_user_token", message: "invalid user token" })
          }
          seq++
          return {
            token: `tok-${seq}`,
            contact: {
              id: `c-${seq}`,
              kind: "user",
              name: "User",
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
    } as unknown as Client
    id.connect(client)

    await id.start(rotated, undefined, true)
    await id.renew()
    expect(requested).toBe(1)
    expect(calls.length).toBe(3)
    expect(calls[2].user_token).toBe(fresh)
    expect(id.token()).toBe("tok-2")
  })

  test("initial start with non-expired invalid token fails immediately without waiting for host refresh", async () => {
    const now = Math.floor(Date.now() / 1000)
    let requested = 0
    const id = createIdentity({
      tokenTimeoutMs: 200,
      requestToken: () => {
        requested++
      },
    })
    const client = {
      widget: {
        session: async (): Promise<WidgetSessionDTO> => {
          throw new ApiError(401, { code: "widget.invalid_user_token", message: "bad secret" })
        },
      },
    } as unknown as Client
    id.connect(client)

    await expect(id.start(fakeJWT("u-42", now + 600), undefined, true)).rejects.toBeInstanceOf(ApiError)
    expect(requested).toBe(0)
  })

  test("renew fails instead of falling back to anonymous when host does not answer need_token", async () => {
    const now = Math.floor(Date.now() / 1000)
    const expired = fakeJWT("u-42", now - 10)
    let requested = 0
    const id = createIdentity({
      tokenTimeoutMs: 30,
      requestToken: () => {
        requested++
      },
    })
    const client = {
      widget: {
        session: async (body: WidgetSessionBody): Promise<WidgetSessionDTO> => {
          calls.push(body)
          if (calls.length >= 2) {
            throw new ApiError(401, { code: "widget.invalid_user_token", message: "expired" })
          }
          seq++
          return {
            token: `tok-${seq}`,
            contact: {
              id: `c-${seq}`,
              kind: "user",
              name: "User",
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
    } as unknown as Client
    id.connect(client)

    await id.start(fakeJWT("u-42", now + 600), undefined, true)
    await id.login(expired)
    await expect(id.renew()).rejects.toBeInstanceOf(ApiError)
    expect(requested).toBe(1)
    expect(calls[calls.length - 1].user_token).toBe(expired)
  })

  test("logout while awaiting host token refresh cancels wait and switches to visitor", async () => {
    const now = Math.floor(Date.now() / 1000)
    const expired = fakeJWT("u-42", now - 10)
    let id!: ReturnType<typeof createIdentity>
    let logoutDone: Promise<void> | undefined

    id = createIdentity({
      tokenTimeoutMs: 5000,
      requestToken: () => {
        queueMicrotask(() => {
          logoutDone = id.logout(true)
        })
      },
    })
    const client = {
      widget: {
        session: async (body: WidgetSessionBody): Promise<WidgetSessionDTO> => {
          calls.push(body)
          if (body.user_token === expired) {
            throw new ApiError(401, { code: "widget.invalid_user_token", message: "expired" })
          }
          seq++
          const isUser = Boolean(body.user_token)
          return {
            token: `tok-${seq}`,
            visitor_id: isUser ? undefined : `vis-${seq}`,
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
    } as unknown as Client
    id.connect(client)

    await id.start(fakeJWT("u-42", now + 600), undefined, true)
    await id.login(expired)
    await id.renew()
    await logoutDone
    expect(id.contact()?.kind).toBe("lead")
    expect(calls.every((c) => c.user_token !== expired)).toBe(true)
  })
})
