import { useCallback, useEffect, useState } from 'react'
import { api } from './api.ts'
import type { Health, Report, Workflow, WorkflowEntry } from './types.ts'
import { go, useHashRoute } from './hooks/useHashRoute.ts'
import type { View } from './hooks/useHashRoute.ts'
import RunView from './components/RunView.tsx'
import WorkflowView from './components/WorkflowView.tsx'
import ReportView from './components/ReportView.tsx'
import HistoryView from './components/HistoryView.tsx'

const TITLES: Record<View, string> = { run: 'Run', workflow: 'Workflow', report: 'Report', history: 'History' }

export default function App() {
  const [menuOpen, setMenuOpen] = useState(false)
  const view = useHashRoute(() => setMenuOpen(false))
  const [health, setHealth] = useState<Health | null>(null)
  const [healthDown, setHealthDown] = useState(false)
  const [workflow, setWorkflow] = useState<Workflow | null>(null)
  const [report, setReport] = useState<Report | null>(null)
  const [saved, setSaved] = useState<WorkflowEntry[]>([])
  const [sessionId, setSessionId] = useState('')
  const [activeId, setActiveId] = useState('')
  const [activeName, setActiveName] = useState<string | null>(null)
  const [runStamp, setRunStamp] = useState('No runs yet')
  const [runKey, setRunKey] = useState(0)
  const [lastRunId, setLastRunId] = useState('')
  // Run the Report view should open (from History); null tracks the fresh check.
  const [reportPick, setReportPick] = useState<{ id: string; n: number } | null>(null)

  useEffect(() => {
    api.health().then(setHealth, () => setHealthDown(true))
  }, [])

  const refresh = useCallback(() => {
    void api.workflows().then(setSaved)
  }, [])

  useEffect(() => {
    refresh()
  }, [refresh])

  function handleLearned(wf: Workflow, savedId: string, stampText: string): void {
    if (savedId) {
      setSessionId(savedId)
      setActiveId(savedId)
    }
    setWorkflow(wf)
    setActiveName(wf.name)
    setRunStamp(stampText)
    go('workflow')
  }

  function handleVerified(rep: Report, stampText: string, runId: string): void {
    setReport(rep)
    setRunKey((k) => k + 1)
    setRunStamp(stampText)
    setLastRunId(runId)
    setReportPick(null)
    go('report')
  }

  async function handleSelectSaved(id: string): Promise<void> {
    if (!id) {
      if (workflow) {
        setActiveId('')
        setActiveName(workflow.name + ' (session)')
      }
      return
    }
    const wf = await api.selectWorkflow(id)
    setActiveId(id)
    setWorkflow(wf)
    setActiveName(wf.name)
    setRunStamp('Selected saved workflow ' + wf.name)
  }

  function handleOpenWorkflow(id: string): Promise<void> {
    return handleSelectSaved(id).then(() => {
      go('workflow')
    })
  }

  function handleOpenRun(id: string): void {
    setReportPick((prev) => ({ id, n: (prev?.n ?? 0) + 1 }))
    go('report')
  }

  function handlePickChange(id: string): void {
    // Syncs the trail select back: '' tracks the fresh check again.
    setReportPick((prev) => (id ? { id, n: prev?.n ?? 0 } : null))
  }

  const demo = health?.mode === 'mock'

  return (
    <>
      <div className="phonebar">
        <button
          className="btn ghost menu-toggle"
          id="menu-btn"
          aria-label={menuOpen ? 'Close menu' : 'Open menu'}
          onClick={() => setMenuOpen((o) => !o)}
        >
          {menuOpen ? 'Close' : 'Menu'}
        </button>
        <span className="t" id="phone-title">
          {TITLES[view]}
        </span>
      </div>
      <div className="app">
        <aside className={menuOpen ? 'sidebar open' : 'sidebar'} id="sidebar" aria-label="Primary">
          <div className="brand">
            <svg viewBox="0 0 24 18" aria-hidden="true">
              <rect x="1" y="5" width="22" height="12" rx="2" fill="#141414" />
              <g fill="#141414">
                <circle cx="5" cy="3" r="1.8" />
                <circle cx="10" cy="3" r="1.8" />
                <circle cx="14.5" cy="3" r="1.8" />
                <circle cx="19.5" cy="3" r="1.8" />
              </g>
            </svg>
            <div>
              <div className="name">ProcessLens</div>
              <div className="scope">
                {workflow?.prompts?.length ? 'Custom procedure' : workflow ? workflow.name : 'Any manual process'}
              </div>
            </div>
          </div>
          <div className="group-label">Workspace</div>
          <a className="nav-item" href="#/run" data-view="run" aria-current={view === 'run' ? 'page' : undefined}>
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor">
              <path d="M4.5 3.5v9l7-4.5z" strokeLinejoin="round" />
            </svg>
            <span>Run</span>
          </a>
          <a
            className="nav-item"
            href="#/workflow"
            data-view="workflow"
            aria-current={view === 'workflow' ? 'page' : undefined}
          >
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor">
              <path d="M2.5 4.5h11M2.5 8h11M2.5 11.5h11" strokeLinecap="round" />
            </svg>
            <span>Workflow</span>
          </a>
          <a
            className="nav-item"
            href="#/report"
            data-view="report"
            aria-current={view === 'report' ? 'page' : undefined}
          >
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor">
              <path
                d="M8 1.8 14 4.5v3.3c0 3.4-2.4 5.4-6 6.7-3.6-1.3-6-3.3-6-6.7V4.5z"
                strokeLinejoin="round"
              />
              <path d="m5.8 7.8 1.6 1.6 3-3.2" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
            <span>Report</span>
          </a>
          <a
            className="nav-item"
            href="#/history"
            data-view="history"
            aria-current={view === 'history' ? 'page' : undefined}
          >
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor">
              <circle cx="8" cy="8" r="5.7" />
              <path d="M8 5v3.2l2.2 1.4" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
            <span>History</span>
          </a>
          <div className="bottom">
            <div className="group-label">Session</div>
            <div className="side-meta">
              <div className="line">
                <span>Mode</span>
                <span className="v" id="meta-mode">
                  {healthDown ? 'unreachable' : (health?.mode ?? '…')}
                </span>
              </div>
              <div className="line">
                <span>ffmpeg</span>
                <span className="v" id="meta-ffmpeg">
                  {health ? (health.ffmpeg ? 'yes' : 'no') : '…'}
                </span>
              </div>
              <div className="line">
                <span>Learned</span>
                <span className="v" id="meta-learned">
                  {workflow?.source_video ?? '—'}
                </span>
              </div>
              <div className="line">
                <span>Checked</span>
                <span className="v" id="meta-checked">
                  {report?.source_video ?? '—'}
                </span>
              </div>
            </div>
          </div>
        </aside>
        <main className="canvas">
          <div className="wrap">
            {view === 'run' && (
              <RunView
                saved={saved}
                sessionId={sessionId}
                activeId={activeId}
                sessionName={workflow?.name ?? ''}
                activeName={activeName}
                onLearned={handleLearned}
                onVerified={handleVerified}
                onSelectSaved={handleSelectSaved}
                onRefresh={refresh}
                stampText={runStamp}
                sessionPrompts={workflow?.prompts}
              />
            )}
            {view === 'workflow' && <WorkflowView workflow={workflow} demo={demo} />}
            {view === 'report' && (
              <ReportView
                key={`${runKey}:${reportPick?.n ?? 0}`}
                report={report}
                workflow={workflow}
                demo={demo}
                freshRunId={lastRunId}
                initialHistoryId={reportPick?.id ?? ''}
                onPickChange={handlePickChange}
              />
            )}
            {view === 'history' && <HistoryView onOpenWorkflow={handleOpenWorkflow} onOpenRun={handleOpenRun} />}
          </div>
        </main>
      </div>
    </>
  )
}
