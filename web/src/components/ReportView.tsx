import { useEffect, useMemo, useState } from 'react'
import { api } from '../api.ts'
import type { Report, RunRecord, RunSummary, SeekRequest, Workflow } from '../types.ts'
import { plural } from '../format.ts'
import { brickColor } from '../bricks.ts'
import { detailOf } from '../findings.ts'
import EvidencePlayer from './EvidencePlayer.tsx'
import FindingCard from './FindingCard.tsx'

interface Props {
  report: Report | null
  workflow: Workflow | null
  demo: boolean
  // The stored run id of the fresh check, or '' when it was not saved
  // (demo data) or the store was unavailable.
  freshRunId: string
}

function runLabel(r: RunSummary): string {
  const when = new Date(r.created_at).toLocaleString()
  const what = r.passed ? 'match' : plural(r.findings_count, 'finding')
  return `${when} · ${r.workflow_name} · ${what}`
}

export default function ReportView({ report, workflow, demo, freshRunId }: Props) {
  const [runs, setRuns] = useState<RunSummary[] | null>(null)
  const [historyId, setHistoryId] = useState('')
  const [historyRun, setHistoryRun] = useState<RunRecord | null>(null)
  const [historyLoading, setHistoryLoading] = useState(false)
  const [historyErr, setHistoryErr] = useState<string | null>(null)
  const [chain, setChain] = useState<{ ok: boolean; count: number; bad_id: string } | null>(null)

  useEffect(() => {
    let alive = true
    api.runs().then((list) => {
      if (alive) setRuns(list)
    })
    api.runsVerify().then((status) => {
      if (alive) setChain(status)
    })
    return () => {
      alive = false
    }
  }, [])

  function handleHistory(id: string): void {
    setHistoryId(id)
    setHistoryRun(null)
    setHistoryErr(null)
    if (!id) return
    setHistoryLoading(true)
    api.run(id).then(
      (rec) => {
        setHistoryRun(rec)
        setHistoryLoading(false)
      },
      (err: unknown) => {
        setHistoryErr('Load failed: ' + (err as Error).message)
        setHistoryLoading(false)
      },
    )
  }

  function downloadEvidence(): void {
    const deliver = (rec: RunRecord) => {
      const blob = new Blob([JSON.stringify(rec, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `processlens-${rec.id}.json`
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
    }
    if (historyRun) {
      deliver(historyRun)
      return
    }
    if (freshRunId) {
      // Fetch the stored bytes so the download matches the record exactly.
      api.run(freshRunId).then(deliver, (err: unknown) => {
        setHistoryErr('Download failed: ' + (err as Error).message)
      })
    }
  }

  // The viewed check: the stored run when one is picked, else the fresh one.
  const effective = historyId ? (historyRun?.report ?? null) : report
  const storedTracks = historyId ? (historyRun?.tracks ?? null) : null

  const sorted = useMemo(() => {
    if (!effective) return []
    console.assert(Array.isArray(effective.findings), 'findings is not an array')
    const order: Record<string, number> = {}
    ;(workflow ? workflow.steps : []).forEach((s, i) => {
      order[s.id] = i
    })
    return effective.findings.slice().sort((a, b) => {
      const ai = order[a.step_id] === undefined ? 1e9 : order[a.step_id]
      const bi = order[b.step_id] === undefined ? 1e9 : order[b.step_id]
      return ai - bi
    })
  }, [effective, workflow])
  const [selState, setSelState] = useState(0)
  // Clamped, never an effect: switching runs keeps the position when it fits.
  const selected = sorted.length ? Math.min(selState, sorted.length - 1) : 0
  const [seek, setSeek] = useState<SeekRequest | null>(null)

  function selectFinding(i: number, seekIt: boolean): void {
    if (!effective || !sorted[i]) return
    setSelState(i)
    const f = sorted[i]
    if (seekIt && f.t_seconds !== undefined && f.t_seconds !== null) {
      const t = Number(f.t_seconds) || 0
      setSeek((prev) => ({ t, n: (prev?.n ?? 0) + 1 }))
    }
  }

  const bad: Record<string, number> = {}
  effective?.findings.forEach((f) => {
    bad[f.step_id] = (bad[f.step_id] || 0) + 1
  })

  const trailBase = historyId
    ? historyLoading
      ? 'Loading…'
      : historyRun
        ? `Run ${historyRun.id} · saved ${new Date(historyRun.created_at).toLocaleString()}`
        : 'Load failed'
    : freshRunId
      ? `Saved as ${freshRunId}`
      : report
        ? 'This check was not saved as a run'
        : 'No checks yet'
  const trailMeta =
    trailBase +
    (historyRun && historyRun.run_hash ? ` · #${historyRun.run_hash.slice(0, 7)}` : '') +
    (chain ? (chain.ok ? ` · chain OK · ${chain.count} runs` : ` · chain BROKEN at ${chain.bad_id}`) : '')
  const downloadable = historyRun !== null || freshRunId !== ''

  return (
    <section data-pane="report">
      <div className="title-row">
        <div>
          <h1>Report</h1>
          <p className="sub" id="report-sub">
            {effective
              ? `\u201C${effective.workflow_name}\u201D · source ${effective.source_video}`
              : 'Nothing checked yet.'}
          </p>
        </div>
        <span className="pill warn" data-demo-badge hidden={!demo}>
          Demo data
        </span>
      </div>
      {(effective || (runs && runs.length > 0)) && (
        <div className="panel" style={{ marginBottom: '12px' }}>
          <header>
            <h2>Evidence trail</h2>
            <span className="meta" id="trail-meta">
              {trailMeta}
            </span>
          </header>
          <div style={{ display: 'flex', gap: '8px', flexWrap: 'wrap', alignItems: 'center' }}>
            <select
              className="input"
              id="trail-select"
              style={{ flex: '1 1 240px' }}
              value={historyId}
              onChange={(e) => handleHistory(e.target.value)}
              aria-label="Evidence run to inspect"
            >
              <option value="">{report ? 'Latest check' : 'Latest check (none yet)'}</option>
              {(runs || []).map((r) => (
                <option key={r.id} value={r.id}>
                  {runLabel(r)}
                </option>
              ))}
            </select>
            <button
              className="btn tertiary"
              id="trail-download"
              disabled={!downloadable}
              onClick={downloadEvidence}
            >
              Download evidence (.json)
            </button>
          </div>
          {historyErr && (
            <div className="errbox" style={{ marginTop: '8px' }}>
              <span>{historyErr}</span>
            </div>
          )}
        </div>
      )}
      <div className="panel">
        <header>
          <h2>Verdict</h2>
          <span className="meta" id="report-meta">
            {effective ? plural(effective.findings.length, 'finding') : '—'}
          </span>
        </header>
        <div id="verdict">
          {!effective ? (
            <div className="empty">Verify a video to fill this view.</div>
          ) : effective.passed ? (
            <div className="verdict">
              <span className="count zero num">0</span>
              <span className="pill pos">Match</span>
              <span className="sentence">The execution matches the learned workflow.</span>
            </div>
          ) : (
            <div className="verdict">
              <span className="count num">{effective.findings.length}</span>
              <span className="pill neg">Deviations</span>
              <span className="sentence">First: {detailOf(sorted[0])}</span>
            </div>
          )}
        </div>
        <div id="rail" style={{ marginTop: '16px' }}>
          {workflow && (
            <div className="rail">
              {workflow.steps.map((s) => {
                const oid = (s.objects || [])[0] || ''
                return (
                  <div key={s.id} className={bad[s.id] ? 'node bad' : 'node'}>
                    <span className="pip" style={{ background: brickColor(oid) }} />
                    <span className="lbl">{s.id}</span>
                    <span className="sub">{bad[s.id] ? plural(bad[s.id], 'finding') : s.title}</span>
                  </div>
                )
              })}
            </div>
          )}
        </div>
      </div>
      <div className="panel" style={{ marginTop: '12px' }}>
        <header>
          <h2>Evidence</h2>
          <span className="meta" id="evidence-meta">
            {effective ? effective.source_video : '—'}
          </span>
        </header>
        <div id="evidence">
          {effective ? (
            <EvidencePlayer
              key={`${effective.source_video}:${historyId || 'live'}`}
              report={effective}
              finding={sorted[selected] ?? null}
              workflow={workflow}
              seek={seek}
              stored={storedTracks}
              mode="verify"
            />
          ) : (
            <div className="empty">Verify a video to enable playback.</div>
          )}
        </div>
      </div>
      <div className="panel" style={{ marginTop: '12px' }}>
        <header>
          <h2>Findings</h2>
          <span className="meta" id="findings-meta">
            {effective ? plural(effective.findings.length, 'finding') : '—'}
          </span>
        </header>
        <div id="findings">
          {!effective ? (
            <div className="empty">No findings yet.</div>
          ) : sorted.length ? (
            sorted.map((f, i) => (
              <FindingCard
                key={f.step_id + ':' + i}
                rep={effective}
                finding={f}
                index={i}
                selected={i === selected}
                workflow={workflow}
                onSelect={selectFinding}
              />
            ))
          ) : (
            <div className="empty">No deviations.</div>
          )}
        </div>
      </div>
    </section>
  )
}
