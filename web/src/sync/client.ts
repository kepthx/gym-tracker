import type { ExerciseGuide, Op, SyncResponse, User } from '../types'

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly retryAfter: number

  constructor(status: number, code: string, message: string, retryAfter = 0) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.retryAfter = retryAfter
  }

  /** A server failure rather than a client one: such a request is worth retrying. */
  get retriable(): boolean {
    return this.status >= 500 || this.status === 429
  }
}

/** Network unreachability, kept apart from server failures: at the gym it is expected. */
export class OfflineError extends Error {
  constructor(cause?: unknown) {
    super('нет связи')
    this.name = 'OfflineError'
    this.cause = cause
  }
}

/**
 * The API lives under the same base path the app is served from, so the app works both at
 * the root of a domain and behind a reverse proxy under a prefix. Vite's BASE_URL always
 * ends with a slash.
 */
const API = `${import.meta.env.BASE_URL}api`

let bearer: string | null = null

/**
 * Fallback path to the token for when the cookie is gone but the copy in storage remains.
 *
 * WebKit is known to drop cookies from a home-screen app. The server reissues the cookie on
 * any authenticated request, so as long as one of the two copies survives the login does.
 */
export function setBearerToken(token: string | null): void {
  bearer = token
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body !== undefined) headers.set('Content-Type', 'application/json')
  if (bearer) headers.set('Authorization', `Bearer ${bearer}`)

  let response: Response
  try {
    response = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  } catch (err) {
    throw new OfflineError(err)
  }

  // 304 is an answer, not a failure: the caller asked conditionally and already holds the
  // body. Only the conditional endpoints can ever see it.
  if (response.status === 204 || response.status === 304) return undefined as T

  const text = await response.text()
  let body: unknown = null
  if (text) {
    try {
      body = JSON.parse(text)
    } catch {
      body = null
    }
  }

  if (!response.ok) {
    const err = (body as { error?: { code?: string; message?: string } } | null)?.error
    throw new ApiError(
      response.status,
      err?.code ?? 'unknown',
      err?.message ?? `запрос вернул ${response.status}`,
      Number(response.headers.get('Retry-After') ?? 0),
    )
  }
  return body as T
}

export interface SessionInfo {
  user: User
  expires_at: number
  /**
   * The raw token, present in the login response only. The cookie is HttpOnly, so this is
   * the one chance to keep the second copy that the Bearer fallback runs on.
   */
  token?: string
}

export function login(password: string, username?: string): Promise<SessionInfo> {
  const body: Record<string, string> = { password }
  if (username) body.username = username
  return request<SessionInfo>(`${API}/auth/login`, { method: 'POST', body: JSON.stringify(body) })
}

export function logout(): Promise<void> {
  return request<void>(`${API}/auth/logout`, { method: 'POST' })
}

export function me(): Promise<SessionInfo> {
  return request<SessionInfo>(`${API}/auth/me`)
}

export interface SyncRequest {
  device_id: string
  since: number
  ops: Op[]
  known_programs: string[]
}

export function postSync(body: SyncRequest): Promise<SyncResponse> {
  return request<SyncResponse>(`${API}/sync`, { method: 'POST', body: JSON.stringify(body) })
}

export function getSync(since: number, knownPrograms: string[]): Promise<SyncResponse> {
  const params = new URLSearchParams({ since: String(since) })
  for (const hash of knownPrograms) params.append('known_program', hash)
  return request<SyncResponse>(`${API}/sync?${params}`)
}

export function getProgramHash(): Promise<{ hash: string }> {
  return request<{ hash: string }>(`${API}/program`)
}

export interface GuidesResponse {
  hash: string
  guides: { exercises?: Record<string, ExerciseGuide> }
}

/**
 * The technique reference, as a conditional request. null means 304 — nothing changed, which
 * is the usual answer and costs no traffic at the gym.
 *
 * It goes through request() like every other call rather than reaching for fetch directly,
 * so it carries the same credentials: the bearer fallback exists for the iOS case where the
 * cookie is gone but the copy in storage remains, and a hand-rolled fetch would answer 401
 * on exactly the device class the guides were written for.
 */
export async function getGuides(etag: string): Promise<GuidesResponse | null> {
  const init = etag ? { headers: { 'If-None-Match': etag } } : {}
  return (await request<GuidesResponse | undefined>(`${API}/guides`, init)) ?? null
}

/**
 * A demonstration file.
 *
 * Outside /api/ deliberately: the service worker is forbidden from caching /api/, and these
 * files are immutable and have to be cached, or the guide would need a connection every time
 * it is opened. Names are built by the caller from the exercise id, never taken from data.
 */
export function mediaUrl(name: string): string {
  return `${import.meta.env.BASE_URL}media/${name}`
}

export function exportUrl(): string {
  return `${API}/export`
}
