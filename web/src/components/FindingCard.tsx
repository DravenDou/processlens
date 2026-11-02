import type { KeyboardEvent } from 'react'
import type { Finding, Report, Workflow } from '../types.ts'
import { fmtDur } from '../format.ts'
import { checkpointText, detailOf, hasMoment, reviewText, stepById } from '../findings.ts'

interface Props {
  rep: Report
  finding: Finding
  index: number
  selected: boolean
  workflow: Workflow | null
  onSelect: (i: number, seek: boolean) => void
}

function pillFor(type: string) {
  const cls = type === 'missing' ? 'warn' : 'neg'
  return <span className={'pill ' + cls}>{type}</span>
}

export default function FindingCard({ rep, finding: f, index, selected, workflow, onSelect }: Props) {
  const step = stepById(workflow, f.step_id)
  const expected = step
    ? `Step ${step.index}: ${step.title}. ${step.description}${checkpointText(workflow, f.step_id)}`
    : `Step ${f.step_id} (workflow not loaded, showing the reported id only).`
  const support = hasMoment(f)
    ? `Reported at ${fmtDur(f.t_seconds as number)} in ${rep.source_video}.`
    : 'No matching observation in the checked execution.'
  return (
    <article
      className="finding"
      tabIndex={0}
      data-step={f.step_id}
      aria-current={selected ? 'true' : undefined}
      onClick={() => onSelect(index, true)}
      onKeyDown={(e: KeyboardEvent) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onSelect(index, true)
        }
      }}
    >
      <div className="finding-head">
        {pillFor(f.type)}
        {hasMoment(f) ? <span className="pill">detected</span> : <span className="pill warn">not-observed</span>}
        <span className="mono">{f.step_id}</span>
      </div>
      <div className="ev-grid">
        <div className="ev-row">
          <span className="k">Expected step</span>
          <span className="val">{expected}</span>
        </div>
        <div className="ev-row">
          <span className="k">Observed result</span>
          <span className="val">{detailOf(f)}</span>
        </div>
        <div className="ev-row">
          <span className="k">Supporting evidence</span>
          <span className="val">{support}</span>
        </div>
        <div className="ev-row">
          <span className="k">Video moment</span>
          <span className="val">
            {hasMoment(f) ? (
              <>
                <span className="num">{fmtDur(f.t_seconds as number) + ' in ' + rep.source_video}</span>
                <button
                  className="btn tertiary finding-seek"
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation()
                    onSelect(index, true)
                  }}
                >
                  Play from {fmtDur(f.t_seconds as number)}
                </button>
              </>
            ) : (
              'No video moment recorded for this finding.'
            )}
          </span>
        </div>
        <div className="ev-row">
          <span className="k">What to review</span>
          <span className="val">{reviewText(f)}</span>
        </div>
      </div>
    </article>
  )
}
