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

// Grid convention (see the builder prompt in internal/graph): col 0 = left,
// row 0 = far side, level 0 = resting on the mat. Out-of-range values fall
// back to raw numbers instead of inventing directions.
const COL_WORD = ['left', 'center', 'right']
const ROW_WORD = ['far side', 'middle', 'near side']

function colWord(col: number): string {
  return COL_WORD[col] ?? `column ${col}`
}

function rowWord(row: number): string {
  return ROW_WORD[row] ?? `row ${row}`
}

function levelWord(level: number): string {
  if (level === 0) return 'on the mat'
  if (level > 0) return `layer ${level + 1}`
  return `level ${level}`
}

function cellWords(col: number, row: number, level: number): string {
  return `${colWord(col)}, ${rowWord(row)}, ${levelWord(level)}`
}

function ordinal(n: number): string {
  if (n === 1) return '1st'
  if (n === 2) return '2nd'
  if (n === 3) return '3rd'
  return `${n}th`
}

function objectLabel(workflow: Workflow | null, id: string): string {
  const o = objectById(workflow, id)
  return o?.label || id
}

// Plain-language "where it went wrong" derived from the finding detail and
// the workflow. Returns null when the detail shape is unrecognized, in which
// case the card omits the row and keeps the raw detail below.
export function whereText(f: Finding, workflow: Workflow | null): string | null {
  const detail = detailOf(f)
  if (f.type === 'misplaced') {
    const m = /object "([^"]+)" at \((\d+),(\d+),L(\d+)\), expected \((\d+),(\d+),L(\d+)\)/.exec(detail)
    if (m) {
      const name = objectLabel(workflow, m[1])
      const got = cellWords(Number(m[2]), Number(m[3]), Number(m[4]))
      const want = cellWords(Number(m[5]), Number(m[6]), Number(m[7]))
      return `${name} is at ${got}; expected ${want}.`
    }
    const absent = /object "([^"]+)" absent after step "([^"]+)"/.exec(detail)
    if (absent) {
      const name = objectLabel(workflow, absent[1])
      return `${name} is not visible anymore after this step — it may have been removed, knocked out of frame, or hidden behind another object.`
    }
    return null
  }
  if (f.type === 'missing') {
    const step = stepById(workflow, f.step_id)
    const oids = step ? step.objects || [] : []
    if (oids.length > 0) {
      const names = oids.map((oid) => objectLabel(workflow, oid)).join(', ')
      return `None of this step's objects (${names}) were seen being placed.`
    }
    return 'This step was never observed in the execution.'
  }
  if (f.type === 'wrong-order') {
    const m = /expected position (\d+)/.exec(detail)
    const total = workflow ? workflow.steps.length : 0
    const tail = 'Play the video moment to see what ran before and after it.'
    if (m && total > 0) {
      return `This step ran out of sequence — expected ${ordinal(Number(m[1]))} of ${total}. ${tail}`
    }
    if (m) {
      return `This step ran out of sequence — expected at position ${m[1]}. ${tail}`
    }
    return `This step ran out of sequence. ${tail}`
  }
  return null
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
