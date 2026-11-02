import { useEffect, useRef, useState } from 'react'

export type View = 'run' | 'workflow' | 'report'

function readHash(): View {
  const v = (location.hash || '#/run').replace('#/', '')
  return v === 'workflow' || v === 'report' ? v : 'run'
}

export function useHashRoute(onNavigate?: () => void): View {
  const [view, setView] = useState<View>(readHash)
  const cbRef = useRef(onNavigate)
  useEffect(() => {
    cbRef.current = onNavigate
  })
  useEffect(() => {
    const onChange = () => {
      setView(readHash())
      cbRef.current?.()
    }
    window.addEventListener('hashchange', onChange)
    return () => window.removeEventListener('hashchange', onChange)
  }, [])
  return view
}

export function go(view: View): void {
  location.hash = `#/${view}`
}
