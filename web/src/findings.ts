import type { Detection, Finding, WFObject, Workflow } from './types.ts'
import { fmtDur } from './format.ts'

export function detailOf(f: Finding): string {
  if (f.detail !== undefined && f.detail !== null) return String(f.detail)
  if (f.Detail !== undefined && f.Detail !== null) return String(f.Detail)
  return ''
}

export function hasMoment(f: Finding): boolean {
  return f.t_seconds !== undefined && f.t_seconds !== null
}

export function stepById(workflow: Workflow | null, id: string) {
  if (!workflow) return null
  return workflow.steps.find((s) => s.id === id) ?? null
}

export function objectById(workflow: Workflow | null, id: string): WFObject | null {
  if (!workflow) return null
  return workflow.objects.find((o) => o.id === id) ?? null
}

export function checkpointText(workflow: Workflow | null, stepId: string): string {
  if (!workflow) return ''
  const cp = workflow.checkpoints.find((c) => c.step_id === stepId)
  if (!cp || !cp.snapshot || !cp.snapshot.length) return ''
  const parts = cp.snapshot.map(
    (it) => `${it.object_id} at (${it.col},${it.row},L${it.level})`,
  )
  return ' Checkpoint: ' + parts.join('; ') + '.'
}

export function reviewText(f: Finding): string {
  if (f.type === 'missing') {
    return (
      'Watch the full execution. The step may have been occluded, out of frame, ' +
      'or not visible enough to detect, so a missing report is not proof the step was skipped.'
    )
  }
  if (!hasMoment(f)) return 'Check the video around the reported moment.'
  if (f.type === 'misplaced') {
    return `Compare the object position near ${fmtDur(f.t_seconds as number)} with the expected checkpoint.`
  }
  if (f.type === 'wrong-order') {
    return `Check the order of steps around ${fmtDur(f.t_seconds as number)}.`
  }
  return `Check the video around ${fmtDur(f.t_seconds as number)}.`
}

// Match keys for the selected finding: the color and label words of its step
// objects, plus any object id quoted in the detail. A detection is
// highlighted when its label contains one of these words.
export function highlightKeys(workflow: Workflow | null, f: Finding | null): string[] {
  if (!f || !workflow) return []
  const keys: string[] = []
  const step = stepById(workflow, f.step_id)
  let oids = step ? [...step.objects] : []
  const m = /object\s+"([^"]+)"/.exec(detailOf(f))
  if (m) oids = oids.concat([m[1]])
  oids.forEach((oid) => {
    const o = objectById(workflow, oid)
    if (!o) return
    if (o.color) keys.push(String(o.color).toLowerCase())
    if (o.label) keys.push(String(o.label).toLowerCase())
  })
  return keys
}

export function isHighlighted(d: Detection | null, keys: string[]): boolean {
  if (!keys || !keys.length || !d) return false
  const lab = String(d.label || '').toLowerCase()
  if (!lab) return false
  return keys.some((k) => k && lab.indexOf(k) !== -1)
}
