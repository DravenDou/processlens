import { useEffect, useRef, useState } from 'react'
import { api } from '../api.ts'
import type { Finding, FrameTracks, Report, SeekRequest, TracksJSON, Workflow } from '../types.ts'
import { fmtDur, plural } from '../format.ts'
import { highlightKeys, isHighlighted } from '../findings.ts'

interface Props {
  report: Report
  finding: Finding | null
  workflow: Workflow | null
  seek: SeekRequest | null
  // A stored run replays its own snapshot instead of fetching the live one.
  stored: TracksJSON | null
}

// Tracks are usable only when the backend has data for this exact video.
// Anything else draws nothing and earns the honest empty note.
function usableTracks(tracks: TracksJSON | null, video: string): TracksJSON | null {
  if (!tracks || !tracks.available) return null
  if (tracks.video !== video) return null
  if (!Array.isArray(tracks.frames) || !tracks.frames.length) return null
  return tracks
}

function nearestFrame(frames: FrameTracks[], cur: number): FrameTracks | null {
  let best: FrameTracks | null = null
  let bd = Infinity
  frames.forEach((fr) => {
    if (!fr || fr.t_seconds === undefined || fr.t_seconds === null) return
    const d = Math.abs(Number(fr.t_seconds) - cur)
    if (d < bd) {
      bd = d
      best = fr
    }
  })
  return best
}

export default function EvidencePlayer({ report, finding, workflow, seek, stored }: Props) {
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const rafRef = useRef(0)
  const tokenRef = useRef(0)
  const refreshRef = useRef(() => {})
  const [tracks, setTracks] = useState<TracksJSON | null>(null)
  const [tracksFailed, setTracksFailed] = useState(false)
  const [videoError, setVideoError] = useState(false)
  const [status, setStatus] = useState('Loading video and tracking data.')

  function paint(): FrameTracks | null {
    const video = videoRef.current
    const canvas = canvasRef.current
    if (!video || !canvas || !video.videoWidth || !video.clientWidth) return null
    const dpr = window.devicePixelRatio || 1
    const cw = video.clientWidth
    const ch = video.clientHeight
    const bw = Math.round(cw * dpr)
    const bh = Math.round(ch * dpr)
    if (canvas.width !== bw || canvas.height !== bh) {
      canvas.width = bw
      canvas.height = bh
    }
    const ctx = canvas.getContext('2d')
    if (!ctx) return null
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, cw, ch)
    const t = usableTracks(stored ?? tracks, report.source_video)
    if (!t || videoError) return null
    const fr = nearestFrame(t.frames, video.currentTime || 0)
    if (!fr || !fr.detections || !fr.detections.length) return null
    // Normalized box to displayed pixels. The video is contained in its
    // element, so scale by the limiting axis and offset the bars.
    const vw = video.videoWidth
    const vh = video.videoHeight
    const scale = Math.min(cw / vw, ch / vh)
    const dw = vw * scale
    const dh = vh * scale
    const ox = (cw - dw) / 2
    const oy = (ch - dh) / 2
    const keys = highlightKeys(workflow, finding)
    ctx.textBaseline = 'top'
    ctx.font = '12px system-ui, sans-serif'
    fr.detections.forEach((d) => {
      if (!d || !d.box) return
      const x = ox + d.box.x * dw
      const y = oy + d.box.y * dh
      const w = d.box.w * dw
      const h = d.box.h * dh
      const sel = isHighlighted(d, keys)
      ctx.lineWidth = sel ? 5 : 4
      ctx.strokeStyle = 'rgba(0,0,0,0.65)'
      ctx.strokeRect(x, y, w, h)
      ctx.lineWidth = sel ? 3 : 2
      ctx.strokeStyle = sel ? '#1d5fbf' : 'rgba(255,255,255,0.95)'
      ctx.strokeRect(x, y, w, h)
      const tag = String(d.label || d.track_id || '')
      if (tag) {
        const tw = ctx.measureText(tag).width + 12
        const tx = Math.min(Math.max(x, ox), ox + dw - tw)
        let ty = y - 20
        if (ty < oy) ty = y + 4
        ctx.fillStyle = sel ? '#1d5fbf' : 'rgba(0,0,0,0.75)'
        ctx.fillRect(tx, ty, tw, 18)
        ctx.fillStyle = '#ffffff'
        ctx.fillText(tag, tx + 6, ty + 3)
      }
    })
    return fr
  }

  function statusText(fr: FrameTracks | null): string {
    if (videoError) {
      return (
        `Video not available for playback (source: ${report.source_video}). ` +
        'The findings below still list the reported moments.'
      )
    }
    if (tracksFailed) return 'Tracking data could not be loaded.'
    const t = usableTracks(stored ?? tracks, report.source_video)
    if (!t) {
      return (
        'No tracked objects for this video. ' +
        'Boxes appear only when tracking data exists for the checked execution.'
      )
    }
    if (!fr) return 'Tracking data loaded.'
    const n = (fr.detections || []).length
    const cur = videoRef.current ? videoRef.current.currentTime || 0 : 0
    let s = `${plural(n, 'box', 'boxes')} from the sample at ${fmtDur(fr.t_seconds)} (nearest to ${fmtDur(cur)}).`
    let hl = 0
    const keys = highlightKeys(workflow, finding)
    ;(fr.detections || []).forEach((d) => {
      if (isHighlighted(d, keys)) hl++
    })
    if (hl > 0) s += ` ${hl}${hl === 1 ? ' matches' : ' match'} the selected finding.`
    return s
  }

  function refresh(): void {
    const s = statusText(paint())
    setStatus((prev) => (prev === s ? prev : s))
  }

  function startTick(): void {
    if (rafRef.current) return
    const loop = () => {
      rafRef.current = 0
      const v = videoRef.current
      if (!v || v.paused || v.ended) return
      refreshRef.current()
      rafRef.current = requestAnimationFrame(loop)
    }
    rafRef.current = requestAnimationFrame(loop)
  }

  function stopTick(): void {
    if (rafRef.current) {
      cancelAnimationFrame(rafRef.current)
      rafRef.current = 0
    }
  }

  // Fetch tracks once per checked video. The token drops a late answer when
  // a newer verify supersedes it.
  useEffect(() => {
    if (stored) return
    // Fresh mount per verify (the parent keys on the run), so the initial
    // state above is already the reset state; only the fetch runs here.
    const token = (tokenRef.current = tokenRef.current + 1)
    void api.tracks('verify').then(
      (body) => {
        if (token !== tokenRef.current) return
        if (body && typeof body === 'object') setTracks(body)
        else setTracksFailed(true)
      },
      () => {
        if (token !== tokenRef.current) return
        setTracksFailed(true)
      },
    )
  }, [report.source_video, stored])

  // Redraw after every commit: selection, tracks, errors and resizes all
  // funnel through here, like the drawOverlay calls did.
  useEffect(() => {
    refreshRef.current = refresh
    refreshRef.current()
  })

  useEffect(() => {
    const onResize = () => refreshRef.current()
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  useEffect(
    () => () => {
      if (rafRef.current) cancelAnimationFrame(rafRef.current)
    },
    [],
  )

  // Imperative seek driven by the finding cards' nonce.
  useEffect(() => {
    if (!seek) return
    const v = videoRef.current
    if (v && !videoError) {
      let t = Number(seek.t) || 0
      if (isFinite(v.duration) && v.duration > 0) {
        t = Math.min(Math.max(t, 0), Math.max(0, v.duration - 0.05))
      }
      try {
        v.currentTime = t
      } catch {
        /* short clip or unload race */
      }
      if (v.scrollIntoView) v.scrollIntoView({ block: 'nearest' })
    }
    refreshRef.current()
  }, [seek, videoError])

  return (
    <>
      <div className="player-wrap">
        <video
          id="ev-video"
          ref={videoRef}
          controls
          preload="metadata"
          src={'/videos/' + encodeURIComponent(report.source_video)}
          aria-label={'Checked execution: ' + report.source_video}
          onError={() => {
            setVideoError(true)
            stopTick()
          }}
          onLoadedMetadata={() => refreshRef.current()}
          onSeeked={() => refreshRef.current()}
          onTimeUpdate={() => refreshRef.current()}
          onPlay={startTick}
          onPause={() => {
            stopTick()
            refreshRef.current()
          }}
          onEnded={() => {
            stopTick()
            refreshRef.current()
          }}
        />
        <canvas id="ev-overlay" ref={canvasRef} aria-hidden="true" />
      </div>
      <p className="ev-note" id="ev-status" role="status">
        {status}
      </p>
      <p className="ev-note">
        Timestamps are approximate: each moment marks the nearest sampled frame, not the exact frame.
      </p>
    </>
  )
}
