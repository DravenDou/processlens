# ProcessLens — learn a physical workflow from one video, verify the next one

Record a physical assembly task once; ProcessLens turns the video into a
structured workflow (steps + expected object layouts), then checks a second
execution against it and reports what is missing, out of order, or misplaced —
with the video moment attached to each finding.

This is a hackathon prototype. The demo domain is a 6-step interlocking-brick
tower; the pipeline itself (track → structure → compare) is what is being shown.

## The problem

Physical work — assembly, setup, maintenance — is still taught by shadowing an
expert and checked by having an expert watch. That does not scale: every new
trainee and every quality check costs expert hours, and mistakes are found late
or not at all. Video of the work exists everywhere, but nobody turns it into a
checkable procedure automatically.

## Who this is for

- **Shop-floor trainers and technical educators**, who want to turn one correct
  demonstration into repeatable, self-serve checks for trainees.
- **Small assembly / QA teams**, who cannot afford vision hardware or ML
  infrastructure but can record a phone video.
- **Anyone learning a hands-on procedure**, who benefits from "you skipped step
  3" instead of rewatching a whole video.

No users, pilots, or customers exist yet; the above is who the prototype is
designed for, not a claim of adoption.

## What the prototype does today

- **Learn** (`POST /api/learn`): upload a video of the clean 6-step build. The
  server tracks six colored bricks across frames, asks a reasoning model to
  structure the observations into a workflow (objects, ordered steps, one
  checkpoint per step with a 3×3 grid + stack level per object), validates it
  strictly, saves it to `data/workflows/`, and shows it in the Workflow view.
  The Workflow view also replays the reference video that taught it.
- **Custom tasks**: the Learn card accepts 1–8 object phrases (one per line,
  sent as the `object_prompts` field); blank keeps the block-tower set. The
  list is stored on the workflow and Verify reuses it, so any procedure with
  distinct visible objects — e.g. pack-a-box — works end to end. See
  [docs/VIDEO-AI.md](docs/VIDEO-AI.md) for generating example clips.
- **Verify** (`POST /api/verify`): upload a video of a new execution. The
  server tracks it, maps it to observed steps, and compares them against the
  learned workflow with a deterministic comparator. The Report view shows a
  verdict, a per-step rail, and one evidence card per finding
  (`missing` / `wrong-order` / `misplaced`), each with the expected step, the
  observed result, a video moment, and what to review.
- **Evidence playback**: the checked video is served back with a canvas overlay
  of tracked boxes; clicking a finding (or its "Play from M:SS" button) seeks
  the player to the reported moment. Boxes matching the selected finding are
  highlighted.
- **Real progress**: while a blocking Learn/Verify runs, the UI polls
  `GET /api/progress` and shows the server's actual stage plus a determinate
  bar only for countable stages (SAM concepts done/total). Nothing is estimated.
- **Saved workflows**: every Learn persists one JSON file; the Run view lists
  saved workflows newest-first and Verify can run against a selected one, so a
  server restart does not lose the learned process.
- **Mock mode**: without an API key the whole flow works offline on fixtures,
  with a "Demo data" badge wherever fixture data is shown.
- **Evidence trail**: every Verify appends one run to `data/processlens.db`
  (SQLite): report, tracks snapshot, workflow, video, timestamp. Runs link
  into a SHA-256 hash chain (`GENESIS` seed); `GET /api/runs/verify` reports
  `{ok, count, bad_id}`, the Report view shows the chain badge plus each
  run's short hash, and any past run can be reopened or downloaded as JSON.

## Architecture

```text
browser (React + Vite + TypeScript UI in web/, served from web/dist)
  │  multipart upload / JSON API / video + frame serving
  ▼
Go server (cmd/server, binds 127.0.0.1)
  ├── internal/meta     Meta Model API client: Files upload, sam-3.1
  │                     /v1/responses per concept, Muse /chat/completions,
  │                     bounded parallelism, retries, one model-repair attempt
  ├── internal/tracking ffprobe metadata + ffmpeg frames (CPU only),
  │                     SAM record-grammar parser, track types
  ├── internal/graph    workflow schema, strict validation, builder prompt
  ├── internal/verify   observed-step parsing, deterministic Compare, report
  └── internal/store    SQLite evidence trail (one append-only runs table)
  │
  ├── data/uploads/     stored videos (served back for playback)
  ├── data/workflows/   saved workflow JSON (atomic write + rename)
  ├── data/processlens.db  verification runs: report + tracks snapshot each
  └── testdata/         fixtures used by mock mode and tests
```

| Path | What |
|---|---|
| `cmd/server` | HTTP backend + static UI server (127.0.0.1 only) |
| `internal/meta` | Meta Model API client (chat + hosted sam-3.1 + mock detection) |
| `internal/tracking` | ffmpeg/ffprobe helpers (CPU only), SAM grammar parser, track types |
| `internal/graph` | workflow schema, validation, builder prompt |
| `internal/verify` | observed-vs-expected comparison, report |
| `internal/store` | SQLite evidence trail: append-only verification runs |
| `web/` | demo UI: React + Vite + TypeScript via pnpm (see `web/package.json` + lockfile); Go serves the `web/dist` build; dev via `vite dev` with API proxy (Geist via Google Fonts with system-font fallback) |
| `testdata/` | fixtures: workflow, tracks, observed steps, report, SAM sample |
| `scripts/verify-live.sh` | key check + model discovery + chat probe + responses route probe |
| `docs/RECORDING.md` | recording protocol (+ `RECORDING.es.md` in Spanish) |
| `docs/VIDEO-AI.md` | video-AI example kit: pack-a-box scripts + prompts (+ `VIDEO-AI.es.md`) |
| `docs/DEMO-90S.md` | 90-second demo runbook |
| `docs/AUDIT.md` | read-only technical audit (2026-11-02, in Spanish) |

## Meta models used

All ML runs hosted on the Meta Model API (`https://api.meta.ai/v1`). No local
models, no GPU anywhere in the pipeline.

- **`sam-3.1`** (object tracking): the video is uploaded once to the Files API
  (`purpose=user_data`), then `POST /v1/responses` runs once per concept with
  an `input_video` part plus a short noun phrase. The six default prompts are
  `"red toy brick"`, `"blue toy brick"`, `"yellow toy brick"`,
  `"orange toy brick"`, `"white toy brick"`, `"black toy brick"` ("toy brick"
  disambiguates from masonry). Boxes come from parsing the SAM record grammar
  in Go. Concept calls run with bounded parallelism (default 3 in flight,
  `META_SAM_CONCURRENCY=1` for sequential).
- **`muse-spark-1.3-contributor`** (reasoning, via `/chat/completions`):
  turns tracks into a workflow (`graph.BuilderSystemPrompt`) and turns the
  second video's tracks into observed steps (`verify.DescribeSystemPrompt`).
  Replies are validated strictly (unknown JSON fields rejected); on a parse
  failure the server makes exactly one repair call (`ChatRepair`) before
  returning an error. Override with `META_MUSE_MODEL` / `META_SAM_MODEL`
  after discovering ids with `scripts/verify-live.sh`.

The final comparison (`verify.Compare`) is pure Go, not a model call: the
models perceive and structure, the code scores.

## Technical differentiation

- **SAM wire protocol reimplemented in Go stdlib**: the record grammar
  (`<Nf>id<|box|><|mask|>`) is parsed without vendoring the reference parser,
  with half-open box semantics normalized by the per-box media dimensions.
  Mask payloads are validated and ignored — only boxes feed the pipeline.
  The parser is tested against conformance-style vectors and verbatim sample
  lines (`testdata/sam-sample.txt`).
- **Strict model-output contracts**: both prompts demand JSON-only replies and
  both parsers use `DisallowUnknownFields` plus semantic validation (contiguous
  1-based step indices, checkpoint-per-step, grid bounds). One repair attempt,
  then an honest error — never silent coercion.
- **Deterministic, non-cascading verifier**: `missing` (expected step never
  observed), `wrong-order` (expected index goes backwards in observed order),
  `misplaced` (a step's *own* objects differ from its checkpoint, so one slip
  does not cascade into every later step).
- **Honest progress and timestamps**: the UI shows server-reported stages
  verbatim and a determinate bar only where real counts exist; every finding
  timestamp is labeled approximate and seeks to the nearest sampled frame.
- **Localhost-first hardening**: the server cannot bind anything but
  `127.0.0.1`; video/frame names are whitelisted to regular files directly
  inside the uploads dir (traversal, absolute paths, dotfiles, and symlinks
  rejected — covered by tests); uploads capped at 200 MiB; the API key never
  appears in logs.
- **Minimal dependencies**: the backend adds one Go module for evidence
  storage (`modernc.org/sqlite`, pure Go, no cgo); the frontend pins React +
  Vite + TypeScript in `web/pnpm-lock.yaml`. `govulncheck` and `pnpm audit`
  report no known vulnerabilities at build time.

## Install + run

Requirements: Go toolchain, Node + pnpm, `ffmpeg` + `ffprobe` on PATH, a browser.

```bash
pnpm --dir web install   # frontend deps (pinned in web/pnpm-lock.yaml)
pnpm --dir web build     # typecheck + production build into web/dist
go run ./cmd/server
```

Open http://127.0.0.1:8090 in your browser. The Go server serves the built UI
from `web/dist`; without a build it answers `/` with a 503 explaining how to
build. For frontend development instead:

```bash
go run ./cmd/server      # terminal 1: API on 127.0.0.1:8090
pnpm --dir web dev       # terminal 2: UI on 127.0.0.1:5173, proxies /api + /videos
```

- **Learn**: upload video 1 (or press "Load demo").
- **Verify**: pick the workflow to check against, upload video 2 (or press
  "Load demo").
- The server binds `127.0.0.1` only. Override the port with `PORT=8090`.

Live mode (real API):

1. Copy `.env.example` to `.env` and set `META_API_KEY` (`.env` is gitignored).
2. Discover model ids and probe the API: `bash scripts/verify-live.sh`
   (default reasoning model `muse-spark-1.3-contributor`, override with
   `META_MUSE_MODEL`).
3. Restart the server (it reads `.env` on boot) and upload real videos.

Recording protocol (camera setup, the 6 steps, the 3 planted errors for
video 2): [docs/RECORDING.md](docs/RECORDING.md)
([versión en español](docs/RECORDING.es.md)).

## Real vs demo mode

|  | Live mode | Mock / demo mode |
|---|---|---|
| Trigger | `META_API_KEY` set, `MOCK_API` unset | no key, or `MOCK_API=1` |
| Learn | ffprobe → Files upload → 6× sam-3.1 → Muse → validated workflow, saved to disk | serves `testdata/workflow.json` labeled with the uploaded name, saved like a real one |
| Verify | track → Muse observed steps → `Compare` | serves `testdata/report.json` (4 findings) labeled with the uploaded name |
| Verify before Learn | `409` with "run Learn first" — never silently compares against the fixture | falls back to the workflow fixture (explicit demo behavior) |
| UI badge | none | "Demo data" badges on Workflow/Report views (driven by `/api/health` mode) |
| Tracking overlay | real boxes for the checked video | mock Learn bundle from `testdata/tracks1.json`; mock Verify shows the honest "no tracked objects" note |

"Load demo" buttons fetch `/api/demo/workflow` and `/api/demo/report` in either
mode and are the fastest path for a live demo with no waiting.

## Known limitations

- **Live runs take minutes; no timing numbers are promised.** Each Learn/Verify
  performs one Files upload, six sam-3.1 Responses calls (default 3 in flight;
  `META_SAM_CONCURRENCY=1` runs them sequentially), and one or two Muse chats
  over videos tens of seconds long. The UI shows real stages and an elapsed
  clock while you wait.
- **Timestamps are approximate.** Frame times assume the source frame rate
  (`t = frame / fps`), and the player seeks to the nearest sampled frame, not
  the exact frame — the UI states this next to the player.
- **Fixtures are mock-only.** In live mode there is no silent fixture fallback:
  Verify before Learn returns `409`, and unknown saved-workflow ids return
  `409`.
- **Single user, single machine.** Session state (last learned workflow and
  track bundles) lives in server memory, there is no authentication, and the
  server only listens on loopback.
- **Coarse spatial model.** Only bounding boxes are used (masks validated then
  discarded); checkpoints quantize positions to a 3×3 grid plus stack level;
  the camera is assumed fixed; brick identity comes from the color prompt
  phrase, so same-color bricks are interchangeable to the system.
- **Model fragility is bounded, not eliminated.** HTTP calls retry transient
  failures (429/5xx, timeouts) with backoff up to 3 attempts; invalid model
  JSON gets one repair call, then Learn/Verify fails with an explicit error.
- **Frontend has no unit tests** (typechecked with `tsc -b`, linted with
  `oxlint`, style with the project's lint script). The Geist webfont needs
  network access and falls back to system fonts offline.

## Test evidence

Run on 2026-11-02 from the repo root (results below are what these commands
printed; no other numbers are claimed):

```bash
export GOCACHE=/tmp/processlens-gocache
gofmt -l .            # clean: no output
go vet ./...          # ok, no findings
go test -count=1 ./...  # ok on all 6 packages
go run golang.org/x/vuln/cmd/govulncheck@latest ./internal/store/  # no vulnerabilities
pnpm --dir web lint   # oxlint: 0 warnings, 0 errors
pnpm --dir web build  # tsc -b clean, vite build ok
pnpm --dir web audit  # no known vulnerabilities
node .agents/skills/design-taste-frontend/scripts/lint.mjs --app web/  # 0 FAIL, 5 WARN
```

What the suites prove (from the test names and fixtures in the repo):

- `internal/verify`: a clean execution passes with 0 findings; the recorded
  error execution yields exactly the 4 planted findings (missing step-3,
  wrong-order step-4, misplaced step-5, misplaced step-6); `report.json`
  is byte-compared against live `Compare` output so the fixture cannot drift.
- `internal/tracking`: SAM grammar conformance vectors, malformed-record
  skipping, unknown-token tolerance, and a verbatim real-sample parse;
  ffmpeg frame extraction and ffprobe round-trip on a synthetic clip.
- `internal/meta`: client plumbing against a stub HTTP server (models list,
  chat, files+responses tracking), parallel-vs-sequential equivalence,
  per-concept error naming, cancellation, and retry behavior (429/5xx retried,
  4xx fails fast, exhaustion surfaces).
- `internal/graph`: the `workflow.json` fixture parses and validates; fenced
  (```json) replies tolerated; invalid workflows rejected; custom `prompts`
  round-trip while legacy files without the key still parse.
- `internal/store` + `cmd/server` runs: custom-prompts parsing table (fails
  closed on invalid input), hash-chain round-trip, tamper detection (also
  across reopen), legacy-DB migration backfill, and the `/api/runs/verify`
  shape.
- `cmd/server`: health/static/demo endpoints, mock Learn/Verify transitions
  with real progress stages, saved-workflow round-trip/list/select including
  invalid-file skipping and id rejection, video range serving with traversal
  and symlink rejection, frame extraction errors and cache placement outside
  the repo. `TestRealUploadsUntouched` is a regression test guaranteeing tests
  write only to temp dirs, never to `data/uploads/`.
- The lint result (0 FAIL, 5 WARN) is style-only: monospace-use notes on
  timestamps/ids and one em-dash note.

## Built during the hackathon

Built during the hackathon window, Oct 27 – Nov 4, 2026. Everything in this
repo was created in that window:

- Go backend (`cmd/server`): Learn/Verify handlers, Files+Responses tracking
  pipeline, staged progress API (`progress.go`), saved-workflow library
  (`workflows.go`), evidence playback — range-served video, remembered tracks,
  cached frame extraction (`playback.go`).
- Meta Model API client (`internal/meta`): chat, file upload, per-concept
  segmentation with bounded parallelism, retries with backoff, one-shot model
  repair.
- SAM record-grammar parser and ffmpeg/ffprobe helpers (`internal/tracking`).
- Workflow schema + strict validation (`internal/graph`) and the deterministic
  verifier (`internal/verify`).
- React + Vite + TypeScript web UI (`web/`, pnpm): Run/Workflow/Report views,
  processing overlay with real stages and elapsed clock, evidence player with
  tracked-box overlay and finding seek, saved-workflow selector, mock-mode
  badges; served from `web/dist`, dev via `vite dev` proxy.
- SQLite evidence trail (`internal/store`, `data/processlens.db`): append-only
  runs with report + tracks snapshots, SHA-256 hash chain with
  `GET /api/runs/verify`, history picker and JSON download in the Report view.
- Custom tasks: optional `object_prompts` on Learn (stored per workflow,
  reused by Verify), custom-objects note, tracked-objects line and reference
  playback in the Workflow view.
- Fixtures and protocol: `testdata/` (workflow, tracks, observed steps,
  report, SAM sample), `docs/RECORDING.md` (+ Spanish version),
  `docs/VIDEO-AI.md` video-AI example kit (+ Spanish version),
  `docs/AUDIT.md` read-only audit, `docs/DEMO-90S.md` demo runbook,
  `scripts/verify-live.sh`.

## Security notes

- Zero third-party dependencies (Go stdlib + three static files), so there is
  no supply chain to audit; if a dependency is ever added, review it first.
- The server never binds `0.0.0.0`; uploads stay under `data/uploads/`;
  `.env` is gitignored and the key never appears in logs or API responses.
- Do not commit `.env`, uploaded videos, or extracted frames; see
  `.gitignore`.
