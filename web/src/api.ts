import type {
  Health,
  Progress,
  Report,
  RunRecord,
  RunSummary,
  TracksJSON,
  Workflow,
  WorkflowEntry,
} from './types.ts'

async function okJSON<T>(res: Response): Promise<T> {
  const body = (await res.json()) as T & { error?: string }
  if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`)
  return body
}

export interface Posted<T> {
  body: T
  savedId: string
  runId: string
}

async function postVideo<T>(route: string, file: File, prompts?: string[]): Promise<Posted<T>> {
  const fd = new FormData()
  fd.append('video', file, file.name)
  if (prompts && prompts.length > 0) fd.append('object_prompts', JSON.stringify(prompts))
  const res = await fetch(route, { method: 'POST', body: fd })
  const savedId = res.headers.get('X-Workflow-ID') || ''
  const runId = res.headers.get('X-Run-ID') || ''
  const body = await okJSON<T>(res)
  return { body, savedId, runId }
}

export const api = {
  health(): Promise<Health> {
    return fetch('/api/health').then(okJSON<Health>)
  },
  // Tolerant by design: the progress overlay keeps its honest indeterminate
  // state when the poll misses; the POST result still lands.
  progress(op: 'learn' | 'verify'): Promise<Progress | null> {
    return fetch(`/api/progress?op=${op}`)
      .then((res) => res.json() as Promise<Progress>)
      .catch(() => null)
  },
  learn(file: File, prompts?: string[]): Promise<Posted<Workflow>> {
    return postVideo<Workflow>('/api/learn', file, prompts)
  },
  verify(file: File): Promise<Posted<Report>> {
    return postVideo<Report>('/api/verify', file)
  },
  demoWorkflow(): Promise<Workflow> {
    return fetch('/api/demo/workflow').then(okJSON<Workflow>)
  },
  demoReport(): Promise<Report> {
    return fetch('/api/demo/report').then(okJSON<Report>)
  },
  workflows(): Promise<WorkflowEntry[]> {
    return fetch('/api/workflows')
      .then((res) => res.json() as Promise<unknown>)
      .then((list) => (Array.isArray(list) ? (list as WorkflowEntry[]) : []))
      .catch(() => [])
  },
  selectWorkflow(id: string): Promise<Workflow> {
    return fetch('/api/workflows/select', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id }),
    }).then(okJSON<Workflow>)
  },
  tracks(which: 'learn' | 'verify'): Promise<TracksJSON | null> {
    return fetch(`/api/tracks?video=${which}`)
      .then((res) => res.json() as Promise<TracksJSON>)
      .catch(() => null)
  },
  runs(): Promise<RunSummary[]> {
    return fetch('/api/runs?limit=50')
      .then((res) => res.json() as Promise<unknown>)
      .then((list) => (Array.isArray(list) ? (list as RunSummary[]) : []))
      .catch(() => [])
  },
  run(id: string): Promise<RunRecord> {
    return fetch('/api/runs/' + encodeURIComponent(id)).then(okJSON<RunRecord>)
  },
  runsVerify(): Promise<{ ok: boolean; count: number; bad_id: string } | null> {
    return fetch('/api/runs/verify')
      .then((res) => res.json() as Promise<{ ok: boolean; count: number; bad_id: string }>)
      .catch(() => null)
  },
}
