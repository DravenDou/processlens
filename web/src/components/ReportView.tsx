import { useMemo, useState } from 'react'
import type { Report, SeekRequest, Workflow } from '../types.ts'
import { plural } from '../format.ts'
import { brickColor } from '../bricks.ts'
import { detailOf } from '../findings.ts'
import EvidencePlayer from './EvidencePlayer.tsx'
import FindingCard from './FindingCard.tsx'

interface Props {
  report: Report | null
  workflow: Workflow | null
  demo: boolean
}

export default function ReportView({ report, workflow, demo }: Props) {
  const sorted = useMemo(() => {
    if (!report) return []
    console.assert(Array.isArray(report.findings), 'findings is not an array')
    const order: Record<string, number> = {}
    ;(workflow ? workflow.steps : []).forEach((s, i) => {
      order[s.id] = i
    })
    return report.findings.slice().sort((a, b) => {
      const ai = order[a.step_id] === undefined ? 1e9 : order[a.step_id]
      const bi = order[b.step_id] === undefined ? 1e9 : order[b.step_id]
      return ai - bi
    })
  }, [report, workflow])
  const [selected, setSelected] = useState(0)
  const [seek, setSeek] = useState<SeekRequest | null>(null)

  function selectFinding(i: number, seekIt: boolean): void {
    if (!report || !sorted[i]) return
    setSelected(i)
    const f = sorted[i]
    if (seekIt && f.t_seconds !== undefined && f.t_seconds !== null) {
      const t = Number(f.t_seconds) || 0
      setSeek((prev) => ({ t, n: (prev?.n ?? 0) + 1 }))
    }
  }

  const bad: Record<string, number> = {}
  report?.findings.forEach((f) => {
    bad[f.step_id] = (bad[f.step_id] || 0) + 1
  })

  return (
    <section data-pane="report">
      <div className="title-row">
        <div>
          <h1>Report</h1>
          <p className="sub" id="report-sub">
            {report ? `\u201C${report.workflow_name}\u201D · source ${report.source_video}` : 'Nothing checked yet.'}
          </p>
        </div>
        <span className="pill warn" data-demo-badge hidden={!demo}>
          Demo data
        </span>
      </div>
      <div className="panel">
        <header>
          <h2>Verdict</h2>
          <span className="meta" id="report-meta">
            {report ? plural(report.findings.length, 'finding') : '—'}
          </span>
        </header>
        <div id="verdict">
          {!report ? (
            <div className="empty">Verify a video to fill this view.</div>
          ) : report.passed ? (
            <div className="verdict">
              <span className="count zero num">0</span>
              <span className="pill pos">Match</span>
              <span className="sentence">The execution matches the learned workflow.</span>
            </div>
          ) : (
            <div className="verdict">
              <span className="count num">{report.findings.length}</span>
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
            {report ? report.source_video : '—'}
          </span>
        </header>
        <div id="evidence">
          {report ? (
            <EvidencePlayer
              report={report}
              finding={sorted[selected] ?? null}
              workflow={workflow}
              seek={seek}
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
            {report ? plural(report.findings.length, 'finding') : '—'}
          </span>
        </header>
        <div id="findings">
          {!report ? (
            <div className="empty">No findings yet.</div>
          ) : sorted.length ? (
            sorted.map((f, i) => (
              <FindingCard
                key={f.step_id + ':' + i}
                rep={report}
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
