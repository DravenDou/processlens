import { useEffect, useState } from 'react'
import type { Dispatch, DragEvent, SetStateAction } from 'react'
import { api } from '../api.ts'
import type { Posted } from '../api.ts'
import type { Report, Workflow, WorkflowEntry } from '../types.ts'
import { fmtClock, stamp } from '../format.ts'

interface ProcState {
  label: string
  stage: string | null
  done?: number
  total?: number
}

interface ErrState {
  msg: string
  retry: () => void
}

export interface RunViewProps {
  saved: WorkflowEntry[]
  sessionId: string
  activeId: string
  sessionName: string
  activeName: string | null
  onLearned: (wf: Workflow, savedId: string, stampText: string) => void
  onVerified: (rep: Report, stampText: string, runId: string) => void
  // '' means back to the session workflow. Rejects with the select error.
  onSelectSaved: (id: string) => Promise<void>
  onRefresh: () => void
  stampText: string
  sessionPrompts?: string[]
}

function ProcView({ proc }: { proc: ProcState }) {
  const [elapsed, setElapsed] = useState(0)
  useEffect(() => {
    const t0 = Date.now()
    const iv = window.setInterval(() => {
      setElapsed(Math.floor((Date.now() - t0) / 1000))
    }, 500)
    return () => window.clearInterval(iv)
  }, [])
  const det =
    typeof proc.done === 'number' &&
    typeof proc.total === 'number' &&
    proc.total > 0 &&
    proc.done >= 0
  const width = det
    ? Math.min(100, Math.max(0, (100 * (proc.done as number)) / (proc.total as number))).toFixed(1) + '%'
    : undefined
  return (
    <div className="proc">
      <div className="proc-bar skeleton" aria-hidden="true">
        <span className={det ? 'proc-fill det' : 'proc-fill'} style={width ? { width } : undefined} />
      </div>
      <div className="proc-row">
        <span role="status">{proc.stage ?? proc.label}</span>
        <span className="proc-time num">{fmtClock(elapsed)}</span>
      </div>
      <p className="proc-note">Processing video takes a few minutes. Keep this page open.</p>
    </div>
  )
}

function Errbox({ err }: { err: ErrState }) {
  return (
    <div className="errbox">
      <span>{err.msg}</span>
      <button className="btn tertiary retry" onClick={err.retry}>
        Retry
      </button>
    </div>
  )
}

interface DropProps {
  id: string
  inputId: string
  full: string
  short: string
  file: File | null
  drag: boolean
  onDrag: (v: boolean) => void
  onFile: (f: File | null) => void
}

function Drop(p: DropProps) {
  const over = (e: DragEvent) => {
    e.preventDefault()
    p.onDrag(true)
  }
  const leave = (e: DragEvent) => {
    e.preventDefault()
    p.onDrag(false)
  }
  return (
    <label
      className={p.drag ? 'drop over' : 'drop'}
      id={p.id}
      onDragOver={over}
      onDragEnter={over}
      onDragLeave={leave}
      onDrop={(e: DragEvent) => {
        e.preventDefault()
        p.onDrag(false)
        if (e.dataTransfer.files.length) {
          // React state is the source of truth; the input mirrors the dialog only.
          p.onFile(e.dataTransfer.files[0])
        }
      }}
    >
      <span className="drop-copy-full">{p.full}</span>
      <span className="drop-copy-short">{p.short}</span>
      {p.file && (
        <div className="file-row">
          <span className="n">{p.file.name}</span>
          <span className="num">{(p.file.size / 1048576).toFixed(1) + ' MB'}</span>
        </div>
      )}
      <input
        type="file"
        id={p.inputId}
        accept="video/*"
        onChange={(e) => p.onFile(e.target.files?.[0] ?? null)}
      />
    </label>
  )
}

export default function RunView(p: RunViewProps) {
  const [learnFile, setLearnFile] = useState<File | null>(null)
  const [verifyFile, setVerifyFile] = useState<File | null>(null)
  const [learnDrag, setLearnDrag] = useState(false)
  const [verifyDrag, setVerifyDrag] = useState(false)
  const [learnPrompts, setLearnPrompts] = useState('')
  const [learnProc, setLearnProc] = useState<ProcState | null>(null)
  const [verifyProc, setVerifyProc] = useState<ProcState | null>(null)
  const [learnErr, setLearnErr] = useState<ErrState | null>(null)
  const [verifyErr, setVerifyErr] = useState<ErrState | null>(null)

  const setProc = (op: 'learn' | 'verify'): Dispatch<SetStateAction<ProcState | null>> =>
    op === 'learn' ? setLearnProc : setVerifyProc
  const setErr = (op: 'learn' | 'verify'): Dispatch<SetStateAction<ErrState | null>> =>
    op === 'learn' ? setLearnErr : setVerifyErr

  async function runUpload<T>(
    op: 'learn' | 'verify',
    route: string,
    label: string,
    submit: () => Promise<Posted<T>>,
    onDone: (body: T, savedId: string, runId: string) => void,
  ): Promise<void> {
    async function attempt(): Promise<void> {
      setProc(op)({ label, stage: null })
      setErr(op)(null)
      // Poll the real server stage once a second while the blocking POST runs.
      // Stale or idle answers keep the honest overlay; polling stops with it.
      const poll = () => {
        void api.progress(op).then((pr) => {
          if (!pr || typeof pr !== 'object' || !pr.stage || pr.stage === 'idle') return
          setProc(op)((prev) =>
            prev && { ...prev, stage: pr.stage + (pr.detail ? ' - ' + pr.detail : ''), done: pr.done, total: pr.total },
          )
        })
      }
      const timer = window.setInterval(poll, 1000)
      poll()
      try {
        const out = await submit()
        window.clearInterval(timer)
        setProc(op)(null)
        onDone(out.body, out.savedId, out.runId)
      } catch (err) {
        window.clearInterval(timer)
        setProc(op)(null)
        const msg = `${route} failed: ${(err as Error).message} (last good: ${stamp()})`
        setErr(op)({ msg, retry: () => void attempt() })
      }
    }
    return attempt()
  }

  function startLearn() {
    if (!learnFile) {
      setLearnErr({ msg: 'Choose a video file first.', retry: startLearn })
      return
    }
    const file = learnFile
    const prompts = learnPrompts
      .split('\n')
      .map((s) => s.trim())
      .filter((s) => s.length > 0)
    void runUpload('learn', '/api/learn', 'Learning workflow from video 1.', () => api.learn(file, prompts), (wf, savedId) => {
      p.onLearned(wf, savedId, `Learned ${wf.steps.length} steps at ${stamp()}`)
      p.onRefresh()
    })
  }

  function startVerify() {
    if (!verifyFile) {
      setVerifyErr({ msg: 'Choose a video file first.', retry: startVerify })
      return
    }
    const file = verifyFile
    void runUpload('verify', '/api/verify', 'Checking video 2 against the workflow.', () => api.verify(file), (rep, _savedId, runId) => {
      void _savedId
      p.onVerified(rep, `Checked at ${stamp()} · ${rep.findings.length} ${rep.findings.length === 1 ? 'finding' : 'findings'}`, runId)
    })
  }

  function loadDemoLearn() {
    setLearnErr(null)
    api.demoWorkflow().then(
      (wf) => p.onLearned(wf, '', 'Demo workflow loaded'),
      () => setLearnErr({ msg: 'Demo workflow failed to load.', retry: loadDemoLearn }),
    )
  }

  function loadDemoReport() {
    setVerifyErr(null)
    api.demoReport().then(
      (rep) => p.onVerified(rep, 'Demo report loaded', ''),
      () => setVerifyErr({ msg: 'Demo report failed to load.', retry: loadDemoReport }),
    )
  }

  function handleSelect(id: string) {
    p.onSelectSaved(id).catch((err: unknown) => {
      setVerifyErr({ msg: 'Select failed: ' + (err as Error).message, retry: () => handleSelect(id) })
    })
  }

  // First option is always the current session workflow, then the saved ones
  // newest first. Falls back to the session row when the kept id is gone.
  const keep = p.activeId || p.sessionId || ''
  const sessionValue = p.sessionId || ''
  const keepKnown = keep === '' || keep === sessionValue || p.saved.some((e) => e.id === keep)
  const selectValue = keepKnown ? keep : sessionValue

  return (
    <section data-pane="run">
      <div className="title-row">
        <div>
          <h1>Run</h1>
          <p className="sub">Teach the workflow from video 1, then check video 2 against it.</p>
        </div>
        <span className="stamp" id="run-stamp">
          {p.stampText}
        </span>
      </div>
      <div className="grid top">
        <div className="panel" style={{ gridColumn: 'span 6' }}>
          <header>
            <h2>Learn</h2>
            <span className="meta">video 1 · clean build</span>
          </header>
          <Drop
            id="learn-drop"
            inputId="learn-file"
            full="Drop video 1 here, or choose a file"
            short="Choose video 1"
            file={learnFile}
            drag={learnDrag}
            onDrag={setLearnDrag}
            onFile={setLearnFile}
          />
          <label className="sub" htmlFor="learn-prompts" style={{ display: 'block', marginBottom: '4px' }}>
            Objects to track (one per line, blank = block-tower set)
          </label>
          <textarea
            className="input"
            id="learn-prompts"
            rows={3}
            style={{ width: '100%', marginBottom: '8px' }}
            value={learnPrompts}
            onChange={(e) => setLearnPrompts(e.target.value)}
          />
          <div style={{ display: 'flex', gap: '8px', flexWrap: 'wrap' }}>
            <button className="btn primary" id="learn-btn" disabled={learnProc !== null} onClick={startLearn}>
              Learn workflow
            </button>
            <button className="btn tertiary" id="learn-demo" onClick={loadDemoLearn}>
              Load demo
            </button>
          </div>
          <div id="learn-status" style={{ marginTop: '12px' }}>
            {learnProc ? <ProcView proc={learnProc} /> : learnErr ? <Errbox err={learnErr} /> : null}
          </div>
        </div>
        <div className="panel" style={{ gridColumn: 'span 6' }}>
          <header>
            <h2>Verify</h2>
            <span className="meta">video 2 · new execution</span>
          </header>
          <Drop
            id="verify-drop"
            inputId="verify-file"
            full="Drop video 2 here, or choose a file"
            short="Choose video 2"
            file={verifyFile}
            drag={verifyDrag}
            onDrag={setVerifyDrag}
            onFile={setVerifyFile}
          />
          <label className="sub" htmlFor="verify-workflow" style={{ display: 'block', marginBottom: '4px' }}>
            Workflow to verify against
          </label>
          <select
            className="input"
            id="verify-workflow"
            style={{ width: '100%', marginBottom: '4px' }}
            value={selectValue}
            onChange={(e) => handleSelect(e.target.value)}
          >
            <option value={sessionValue}>
              {p.sessionName ? 'Current session: ' + p.sessionName : 'Current session: nothing learned yet'}
            </option>
            {p.saved.map((e) => (
              <option key={e.id} value={e.id}>
                {e.name + ' · ' + e.source_video + ' (' + e.steps + ' steps)'}
                {e.saved_at ? ' · ' + new Date(e.saved_at * 1000).toLocaleString() : ''}
              </option>
            ))}
          </select>
          {selectValue === sessionValue && p.sessionPrompts && p.sessionPrompts.length > 0 && (
            <p className="sub" style={{ margin: '0 0 4px' }}>
              {`Checking ${p.sessionPrompts.length} custom objects.`}
            </p>
          )}
          <p className="sub" id="verify-workflow-name" role="status" style={{ margin: '0 0 8px' }}>
            {p.activeName ? 'Active workflow: ' + p.activeName : 'Active workflow: none — learn or pick a saved one.'}
          </p>
          <div style={{ display: 'flex', gap: '8px', flexWrap: 'wrap' }}>
            <button className="btn secondary" id="verify-btn" disabled={verifyProc !== null} onClick={startVerify}>
              Verify execution
            </button>
            <button className="btn tertiary" id="verify-demo" onClick={loadDemoReport}>
              Load demo
            </button>
          </div>
          <div id="verify-status" style={{ marginTop: '12px' }}>
            {verifyProc ? <ProcView proc={verifyProc} /> : verifyErr ? <Errbox err={verifyErr} /> : null}
          </div>
        </div>
      </div>
    </section>
  )
}
