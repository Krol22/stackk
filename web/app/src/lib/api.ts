// The stackk-web API: a thin layer over the stackk CLI's JSON.

export type Task = {
  id: string
  name: string
  stack: string
  stack_name: string
  view: 'stack' | 'running' | 'deferred' | 'exited' | 'paused' | 'hidden'
  phase: 'idle' | 'working' | 'waiting' | 'unknown' | 'shell'
  waiting_kind?: string
  agent: string
  cwd: string
  last_msg: string
  last_tool?: string
  last_tool_at?: number
  note?: string
  error?: string
  phase_since: number
  queued_at: number
  created_at: number
  run_state: 'starting' | 'live' | 'exited'
}

export type Screen = { task: Task; lines: string[] }

async function call<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, body === undefined ? {} : {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  const data = await res.json().catch(() => ({ error: res.statusText }))
  if (!res.ok) throw new Error(data.error || res.statusText)
  return data as T
}

export const api = {
  tasks: () => call<Task[]>('/api/tasks'),
  screen: (id: string, lines = 300) => call<Screen>(`/api/tasks/${encodeURIComponent(id)}/screen?lines=${lines}`),
  send: (id: string, text: string, enter = true) => call(`/api/tasks/${encodeURIComponent(id)}/send`, { text, enter }),
  keys: (id: string, keys: string[]) => call(`/api/tasks/${encodeURIComponent(id)}/key`, { keys }),
  action: (id: string, action: 'pause' | 'resume') => call(`/api/tasks/${encodeURIComponent(id)}/${action}`, {}),
}

export function ago(ts: number, now = Date.now()): string {
  if (!ts) return ''
  const s = Math.max(0, now / 1000 - ts)
  if (s < 60) return `${Math.floor(s)}s`
  if (s < 3600) return `${Math.floor(s / 60)}m`
  if (s < 48 * 3600) return `${Math.floor(s / 3600)}h`
  return `${Math.floor(s / 86400)}d`
}

export function mark(t: Task): string {
  if (t.run_state === 'exited') return '○'
  if (t.phase === 'shell') return '$'
  if (t.phase === 'waiting') return '?'
  if (t.phase === 'unknown') return '⁇'
  if (t.phase === 'idle') return '✓'
  return '◐'
}

// poll runs fn now and every ms while the page is visible; it returns a stop function.
export function poll(fn: () => Promise<void> | void, ms: number): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined
  let stopped = false
  const tick = async () => {
    if (stopped) return
    if (!document.hidden) await Promise.resolve(fn()).catch(() => {})
    timer = setTimeout(tick, ms)
  }
  const wake = () => { if (!document.hidden) { clearTimeout(timer); tick() } }
  document.addEventListener('visibilitychange', wake)
  tick()
  return () => { stopped = true; clearTimeout(timer); document.removeEventListener('visibilitychange', wake) }
}
