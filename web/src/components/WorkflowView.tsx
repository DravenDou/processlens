import type { Report, Workflow } from '../types.ts'
import { brickColor, inkOn, objectColor, paleTint } from '../bricks.ts'
import { cellWordsText, objectById } from '../findings.ts'
import EvidencePlayer from './EvidencePlayer.tsx'

function LayoutGrid({ workflow, stepId }: { workflow: Workflow; stepId: string }) {
  const cp = workflow.checkpoints.find((c) => c.step_id === stepId)
  const items = cp?.snapshot ?? []
  return (
    <span className="layout-grid" aria-hidden="true">
      {[0, 1, 2].map((row) =>
        [0, 1, 2].map((col) => {
          const hit = items.filter((it) => it.col === col && it.row === row)
          return (
            <span key={`${col}-${row}`} className="layout-cell">
              {hit.map((it) => (
                <span
                  key={it.object_id}
                  className="layout-dot"
                  title={`${it.object_id} (${col},${row},L${it.level})`}
                  style={{ background: objectColor(it.object_id, it.label) }}
                />
              ))}
            </span>
          )
        }),
      )}
    </span>
  )
}

interface Props {
  workflow: Workflow | null
  demo: boolean
}

export default function WorkflowView({ workflow, demo }: Props) {
  if (workflow) {
    console.assert(
      workflow.steps.length === workflow.checkpoints.length,
      'steps and checkpoints disagree',
    )
  }
  const cps: Record<string, { object_id: string; col: number; row: number; level: number }[]> = {}
  ;(workflow?.checkpoints || []).forEach((c) => {
    cps[c.step_id] = c.snapshot || []
  })
  const learnReport: Report | null = workflow
    ? { workflow_name: workflow.name, source_video: workflow.source_video, passed: true, findings: [] }
    : null
  return (
    <section data-pane="workflow">
      <div className="title-row">
        <div>
          <h1>Workflow</h1>
          <p className="sub" id="workflow-sub">
            {workflow ? `\u201C${workflow.name}\u201D · source ${workflow.source_video}` : 'Nothing learned yet.'}
          </p>
          {workflow && (
            <p className="sub" id="workflow-prompts">
              {workflow.prompts && workflow.prompts.length > 0
                ? `Tracked objects: ${workflow.prompts.join(', ')}`
                : 'Tracked objects: default demo object set'}
            </p>
          )}
        </div>
        <span className="pill warn" data-demo-badge hidden={!demo}>
          Demo data
        </span>
      </div>
      {workflow && learnReport && (
        <div className="panel" style={{ marginBottom: '12px' }}>
          <header>
            <h2>Reference</h2>
            <span className="meta">{workflow.source_video}</span>
          </header>
          <div id="learn-evidence">
            <EvidencePlayer
              key={workflow.source_video}
              report={learnReport}
              finding={null}
              workflow={workflow}
              seek={null}
              stored={null}
              mode="learn"
            />
          </div>
        </div>
      )}
      <div className="panel">
        <header>
          <h2>Steps</h2>
          <span className="meta" id="workflow-meta">
            {workflow ? `${workflow.steps.length} steps · ${workflow.checkpoints.length} checkpoints` : '—'}
          </span>
        </header>
        {workflow && workflow.objects.length > 0 && (
          <p className="obj-legend" aria-label="Tracked objects">
            {workflow.objects.map((o) => (
              <span
                key={o.id}
                className="obj-tag"
                style={{ background: paleTint(objectColor(o.id, o.label)), color: '#141414' }}
              >
                <span className="chip" style={{ background: objectColor(o.id, o.label) }} />
                {o.label}
              </span>
            ))}
          </p>
        )}
        <div id="workflow-body">
          {workflow ? (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th className="n">#</th>
                    <th>Step</th>
                    <th>Objects</th>
                    <th>Expected layout</th>
                  </tr>
                </thead>
                <tbody>
                  {workflow.steps.map((s) => (
                    <tr key={s.id}>
                      <td className="n">
                        <span
                          className="step-num"
                          style={{
                            background: objectColor(
                              (s.objects || [])[0] || s.id,
                              objectById(workflow, (s.objects || [])[0] || '')?.label,
                            ),
                            color: inkOn(
                              objectColor(
                                (s.objects || [])[0] || s.id,
                                objectById(workflow, (s.objects || [])[0] || '')?.label,
                              ),
                            ),
                          }}
                        >
                          {s.index}
                        </span>
                      </td>
                      <td>
                        <div>{s.title}</div>
                        <span className="sub">{s.description}</span>
                      </td>
                      <td>
                        {(s.objects || []).map((o, i) => (
                          <span key={o}>
                            {i > 0 && <br />}
                            <span
                              className={brickColor(o) === '#999' ? 'chip neutral' : 'chip'}
                              style={{ background: objectColor(o, objectById(workflow, o)?.label) }}
                            />
                            <span>{objectById(workflow, o)?.label ?? o}</span>
                          </span>
                        ))}
                      </td>
                      <td>
                        <LayoutGrid workflow={workflow} stepId={s.id} />{' '}
                        {(cps[s.id] || []).map((it, i) => (
                          <span key={it.object_id}>
                            {i > 0 && <br />}
                            <span>
                              {objectById(workflow, it.object_id)?.label ?? it.object_id} —{' '}
                              {cellWordsText(it.col, it.row, it.level)}
                            </span>
                          </span>
                        ))}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="empty">Learn from a video to fill this view.</div>
          )}
        </div>
      </div>
    </section>
  )
}
