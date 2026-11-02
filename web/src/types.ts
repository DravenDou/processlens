// JSON shapes served by the Go backend. Field names match the Go struct
// tags exactly; see internal/graph, internal/verify, internal/tracking and
// cmd/server (playback.go, progress.go, workflows.go).

export interface SnapshotItem {
  object_id: string
  label: string
  col: number
  row: number
  level: number
}

export interface WFObject {
  id: string
  label: string
  color: string
  kind: string
}

export interface Step {
  id: string
  index: number
  title: string
  description: string
  objects: string[]
}

export interface Checkpoint {
  step_id: string
  snapshot: SnapshotItem[]
}

export interface Workflow {
  name: string
  source_video: string
  objects: WFObject[]
  steps: Step[]
  checkpoints: Checkpoint[]
}

// The demo fixture spells the detail "Detail" while the live API spells it
// "detail"; both arrive here. t_seconds is omitempty in Go, so a finding
// without a video moment carries no key at all.
export interface Finding {
  type: string
  step_id: string
  detail?: string
  Detail?: string
  t_seconds?: number
}

export interface Report {
  workflow_name: string
  source_video: string
  passed: boolean
  findings: Finding[]
}

export interface Box {
  x: number
  y: number
  w: number
  h: number
}

export interface Detection {
  track_id: string
  label: string
  box: Box
  t_seconds: number
}

export interface FrameTracks {
  frame: number
  t_seconds: number
  detections: Detection[]
}

export interface TracksJSON {
  video: string
  available: boolean
  frames: FrameTracks[]
}

export interface Progress {
  stage: string
  detail: string
  done: number
  total: number
}

export interface WorkflowEntry {
  id: string
  name: string
  source_video: string
  steps: number
  saved_at: number
}

export interface Health {
  mode: string
  ffmpeg: boolean
}

// A seek request for the evidence player: the moment plus a nonce so two
// seeks to the same second still re-fire the effect.
export interface SeekRequest {
  t: number
  n: number
}
