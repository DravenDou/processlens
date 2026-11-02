import type { Workflow } from '../types.ts'
import { brickColor } from '../bricks.ts'

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
  return (
    <section data-pane="workflow">
      <div className="title-row">
        <div>
          <h1>Workflow</h1>
          <p className="sub" id="workflow-sub">
            {workflow ? `\u201C${workflow.name}\u201D · source ${workflow.source_video}` : 'Nothing learned yet.'}
          </p>
        </div>
        <span className="pill warn" data-demo-badge hidden={!demo}>
          Demo data
        </span>
      </div>
      <div className="panel">
        <header>
          <h2>Steps</h2>
          <span className="meta" id="workflow-meta">
            {workflow ? `${workflow.steps.length} steps · ${workflow.checkpoints.length} checkpoints` : '—'}
          </span>
        </header>
        <div id="workflow-body">
          {workflow ? (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th className="n">#</th>
                    <th>Step</th>
                    <th>Objects</th>
                    <th>Checkpoint</th>
                  </tr>
                </thead>
                <tbody>
                  {workflow.steps.map((s) => (
                    <tr key={s.id}>
                      <td className="n">{s.index}</td>
                      <td>
                        <div>{s.title}</div>
                        <span className="sub">{s.description}</span>
                      </td>
                      <td>
                        {(s.objects || []).map((o, i) => (
                          <span key={o}>
                            {i > 0 && <br />}
                            <span className="chip" style={{ background: brickColor(o) }} />
                            <span className="mono">{o}</span>
                          </span>
                        ))}
                      </td>
                      <td>
                        {(cps[s.id] || []).map((it, i) => (
                          <span key={it.object_id}>
                            {i > 0 && <br />}
                            <span className="mono">
                              {it.object_id} ({it.col},{it.row},L{it.level})
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
