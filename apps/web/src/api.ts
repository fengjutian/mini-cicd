export type ApiError = { error: { code: string; message: string } }

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`/api/v1${path}`, {
    credentials: 'same-origin',
    ...init,
    headers: { ...(init?.body ? { 'Content-Type': 'application/json' } : {}), ...init?.headers },
  })
  if (response.status === 204) return undefined as T
  const body = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error((body as ApiError).error?.message || `请求失败 (${response.status})`)
  return body as T
}

export function json(method: string, value?: unknown): RequestInit {
  return { method, body: value === undefined ? undefined : JSON.stringify(value) }
}
