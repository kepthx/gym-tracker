import type { Op, SyncResponse, User } from '../types'

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

let bearer: string | null = null

/** Fallback path to the token for when the cookie is gone but the copy in storage remains. */
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

  if (response.status === 204) return undefined as T

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
}

export function login(password: string, username?: string): Promise<SessionInfo> {
  const body: Record<string, string> = { password }
  if (username) body.username = username
  return request<SessionInfo>('/api/auth/login', { method: 'POST', body: JSON.stringify(body) })
}

export function logout(): Promise<void> {
  return request<void>('/api/auth/logout', { method: 'POST' })
}

export function me(): Promise<SessionInfo> {
  return request<SessionInfo>('/api/auth/me')
}

export interface SyncRequest {
  device_id: string
  since: number
  ops: Op[]
  known_programs: string[]
}

export function postSync(body: SyncRequest): Promise<SyncResponse> {
  return request<SyncResponse>('/api/sync', { method: 'POST', body: JSON.stringify(body) })
}

export function getSync(since: number, knownPrograms: string[]): Promise<SyncResponse> {
  const params = new URLSearchParams({ since: String(since) })
  for (const hash of knownPrograms) params.append('known_program', hash)
  return request<SyncResponse>(`/api/sync?${params}`)
}

export function exportUrl(): string {
  return '/api/export'
}
