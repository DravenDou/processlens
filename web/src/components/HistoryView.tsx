import { useEffect, useState } from 'react'
import { api } from '../api.ts'
import type { RunSummary, WorkflowEntry } from '../types.ts'
import { plural } from '../format.ts'

interface Props {
  onOpenWorkflow: (id: string) => Promise<void>
  onOpenRun: (id: string) => void
}

function fmtWhen(iso: string): string {
  return new Date(iso).toLocaleString()
}

export default function HistoryView(p: Props) {
  const [workflows, setWorkflows] = useState<WorkflowEntry[] | null>(null)
  const [runs, setRuns] = useState<RunSummary[] | null>(null)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    let alive = true
    api.workflows().then((list) => {
      if (alive) setWorkflows(list)
    })
    api.runs().then((list) => {
      if (alive) setRuns(list)
    })
    return () => {
      alive = false
    }
  }, [])

  function openWorkflow(id: string): void {
    setErr(null)
    p.onOpenWorkflow(id).catch((e: unknown) => {
      setErr('Open failed: ' + (e as Error).message)
    })
  }

  return (
    <section data-pane="history">
      <div className="title-row">
        <div>
          <h1>History</h1>
          <p className="sub">Saved workflows and verification runs, newest first. Videos stay in the app.</p>
        </div>
      </div>
      {err && (
        <div className="errbox" style={{ marginBottom: '12px' }}>
          <span>{err}</span>
        </div>
      )}
      <div className="panel">
        <header>
          <h2>Workflows</h2>
          <span className="meta" id="history-workflows-meta">
            {workflows ? plural(workflows.length, 'workflow') : '—'}
          </span>
        </header>
        <div id="history-workflows">
          {!workflows ? (
            <div className="empty">Loading…</div>
          ) : workflows.length ? (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Video</th>
                    <th className="n">Steps</th>
                    <th>Saved</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {workflows.map((w) => (
                    <tr key={w.id} data-workflow={w.id}>
                      <td>{w.name}</td>
                      <td>
                        <span className="mono">{w.source_video}</span>
                      </td>
                      <td className="n">{w.steps}</td>
                      <td>{w.saved_at ? new Date(w.saved_at * 1000).toLocaleString() : '—'}</td>
                      <td>
                        <button className="btn tertiary" type="button" data-open-workflow={w.id} onClick={() => openWorkflow(w.id)}>
                          Open
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="empty">No saved workflows yet. Learn from a video.</div>
          )}
        </div>
      </div>
      <div className="panel" style={{ marginTop: '12px' }}>
        <header>
          <h2>Verification runs</h2>
          <span className="meta" id="history-runs-meta">
            {runs ? plural(runs.length, 'run', 'runs') : '—'}
          </span>
        </header>
        <div id="history-runs">
          {!runs ? (
            <div className="empty">Loading…</div>
          ) : runs.length ? (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>When</th>
                    <th>Workflow</th>
                    <th>Video</th>
                    <th>Result</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {runs.map((r) => (
                    <tr key={r.id} data-run={r.id}>
                      <td>{fmtWhen(r.created_at)}</td>
                      <td>{r.workflow_name}</td>
                      <td>
                        <span className="mono">{r.source_video}</span>
                      </td>
                      <td>
                        {r.passed ? (
                          <span className="pill pos">Match</span>
                        ) : (
                          <span className="pill neg">{plural(r.findings_count, 'finding')}</span>
                        )}
                      </td>
                      <td>
                        <button className="btn tertiary" type="button" data-open-run={r.id} onClick={() => p.onOpenRun(r.id)}>
                          Open
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="empty">No verification runs yet. Verify an execution.</div>
          )}
        </div>
      </div>
    </section>
  )
}
