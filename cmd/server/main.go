// Command server runs the ProcessLens demo backend and serves the local UI.
// It binds 127.0.0.1 only (never 0.0.0.0): this demo must not be reachable
// from the network. Without META_API_KEY it runs in mock mode on fixtures.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"processlens/internal/graph"
	"processlens/internal/meta"
	"processlens/internal/store"
	"processlens/internal/tracking"
	"processlens/internal/verify"
)

const (
	defaultPort = "8090"
	// bindAddr is hardcoded to loopback on purpose. Do not parameterize it.
	bindAddr  = "127.0.0.1"
	maxUpload = 200 << 20 // 200 MiB
)

// defaultPrompts are the text prompts sent to hosted SAM 3.1 for the demo build.
// Bare "<color> brick" returns empty (masonry bias); "toy brick" disambiguates.
var defaultPrompts = []string{
	"red toy brick", "blue toy brick", "yellow toy brick",
	"orange toy brick", "white toy brick", "black toy brick",
}

// Custom object prompts bound the optional 'object_prompts' learn field:
// at most maxPrompts labels of at most maxPromptLen characters each.
const (
	maxPromptLen = 80
	maxPrompts   = 8
)

// parseObjectPrompts validates the optional 'object_prompts' learn field, a
// JSON array of strings. Absent or blank means "not provided" and resolves
// to the default prompts; a present-but-invalid value fails closed with an
// explicit error. Entries are trimmed, empties dropped, exact duplicates
// removed, and the survivors must number 1..8 with ≤80 characters each.
func parseObjectPrompts(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return append([]string(nil), defaultPrompts...), nil
	}
	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("invalid object_prompts: want a JSON array of strings: %w", err)
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range items {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		if utf8.RuneCountInString(p) > maxPromptLen {
			return nil, fmt.Errorf("invalid object_prompts: prompt %q exceeds %d characters", p, maxPromptLen)
		}
		seen[p] = true
		out = append(out, p)
	}
	if len(out) < 1 || len(out) > maxPrompts {
		return nil, fmt.Errorf("invalid object_prompts: want 1..%d prompts, got %d", maxPrompts, len(out))
	}
	return out, nil
}

// resolvePrompts returns the tracking prompts for a workflow: its learned
// custom list when present, else the demo defaults.
func resolvePrompts(wf *graph.Workflow) []string {
	if wf != nil && len(wf.Prompts) > 0 {
		return append([]string(nil), wf.Prompts...)
	}
	return append([]string(nil), defaultPrompts...)
}

// Server holds demo state. lastLearned remembers the most recent /api/learn
// workflow so /api/verify can check against it. The last* fields are guarded
// by mu: handlers run concurrently.
type Server struct {
	mu          sync.RWMutex
	root        string
	meta        *meta.Client // nil in mock mode
	mock        bool
	ffmpeg      string
	ffprobe     string
	lastLearned *graph.Workflow
	// progress holds the real stage of each op ("learn", "verify"),
	// guarded by progMu. See progress.go.
	progMu     sync.Mutex
	progress   map[string]progress
	uploadsDir string
	// workflowsDir holds saved workflow JSON files; see workflows.go.
	workflowsDir string
	// Playback bundles remembered from the last Learn / Verify.
	lastLearnTracks  *trackSet
	lastVerifyTracks *trackSet
	frameCacheDir    string
	// staticDir overrides where handleStatic serves the UI from.
	staticDir string
	// runs is the SQLite evidence trail; see runs.go.
	runs *store.Store
	// activeWorkflowID is the saved id Verify checks against ("" when the
	// session workflow never reached disk). Guarded by mu.
	activeWorkflowID string
}

func main() {
	loadDotEnv(".env")
	root, err := os.Getwd()
	if err != nil {
		log.Fatalf("cwd: %v", err)
	}
	s := NewServer(root)
	if err := s.openRuns(); err != nil {
		log.Fatalf("runs db: %v", err)
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = defaultPort
	}
	addr := bindAddr + ":" + port
	mode := "mock"
	if !s.mock {
		mode = "live"
	}
	log.Printf("ProcessLens demo on http://%s (%s mode, ffmpeg=%v)", addr, mode, s.ffmpeg != "")
	log.Fatal(http.ListenAndServe(addr, s.routes()))
}

// NewServer builds a Server rooted at repoRoot. Uploads default to
// <root>/data/uploads, created with MkDirAll on first upload.
func NewServer(repoRoot string) *Server {
	mc, ok := meta.NewFromEnv()
	ff, _ := tracking.HasFFmpeg()
	fp, _ := tracking.HasFFprobe()
	return &Server{
		root:       repoRoot,
		meta:       mc,
		mock:       !ok,
		ffmpeg:     ff,
		ffprobe:    fp,
		uploadsDir: filepath.Join(repoRoot, "data", "uploads"),
	}
}

// SetUploadsDir overrides where storedVideo saves uploads. Tests point it at
// t.TempDir() so they never write to the real repo data/uploads.
func (s *Server) SetUploadsDir(dir string) {
	s.uploadsDir = dir
}

func (s *Server) uploadsPath() string {
	if s.uploadsDir != "" {
		return s.uploadsDir
	}
	return filepath.Join(s.root, "data", "uploads")
}

func (s *Server) mode() string {
	if s.mock {
		return "mock"
	}
	return "live"
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/learn", s.handleLearn)
	mux.HandleFunc("POST /api/verify", s.handleVerify)
	mux.HandleFunc("GET /api/progress", s.handleProgress)
	mux.HandleFunc("GET /api/workflows", s.handleWorkflows)
	mux.HandleFunc("POST /api/workflows/select", s.handleWorkflowSelect)
	mux.HandleFunc("GET /api/demo/workflow", s.handleDemoWorkflow)
	mux.HandleFunc("GET /api/demo/report", s.handleDemoReport)
	mux.HandleFunc("GET /api/runs", s.handleRuns)
	mux.HandleFunc("GET /api/runs/verify", s.handleRunsVerify)
	mux.HandleFunc("GET /api/runs/{id}", s.handleRun)
	mux.HandleFunc("GET /videos/", s.handleVideo)
	mux.HandleFunc("GET /api/tracks", s.handleTracks)
	mux.HandleFunc("GET /api/frame", s.handleFrame)
	mux.HandleFunc("GET /", s.handleStatic)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"mode":   s.mode(),
		"ffmpeg": s.ffmpeg != "",
	})
}

// storedVideo saves an uploaded "video" part (or resolves a {"video_path"})
// and returns the on-disk path plus a display name.
func (s *Server) storedVideo(r *http.Request, prefix string) (path, name string, err error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var payload struct {
			VideoPath string `json:"video_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return "", "", fmt.Errorf("decode video_path: %w", err)
		}
		p := filepath.Clean(strings.TrimSpace(payload.VideoPath))
		if p == "" || p == "." {
			return "", "", fmt.Errorf("empty video_path")
		}
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			return "", "", fmt.Errorf("video not found: %s", p)
		}
		return p, filepath.Base(p), nil
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxUpload)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		return "", "", fmt.Errorf("parse upload: %w", err)
	}
	f, hdr, err := r.FormFile("video")
	if err != nil {
		return "", "", fmt.Errorf("form field 'video': %w", err)
	}
	defer f.Close()
	base := filepath.Base(hdr.Filename)
	// filepath.Base ignores backslashes on Linux; a stored backslash would
	// later be refused by resolveStoredFile, so sanitize it at upload time.
	base = strings.ReplaceAll(base, "\\", "_")
	if base == "" || base == "." || base == "/" {
		base = "upload.mp4"
	}
	name = fmt.Sprintf("%s-%d-%s", prefix, time.Now().Unix(), base)
	dir := s.uploadsPath()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	path = filepath.Join(dir, name)
	out, err := os.Create(path)
	if err != nil {
		return "", "", err
	}
	defer out.Close()
	if _, err := io.Copy(out, f); err != nil {
		return "", "", err
	}
	return path, name, nil
}

func (s *Server) fixture(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.root, "testdata", name))
}

func (s *Server) handleLearn(w http.ResponseWriter, r *http.Request) {
	// fail records the honest error stage before answering. The UI stops
	// polling when this POST resolves, so the stage is for late pollers.
	fail := func(status int, err error) {
		s.setProgress("learn", "error", err.Error(), -1, -1)
		writeErr(w, status, err)
	}
	path, name, err := s.storedVideo(r, "learn")
	if err != nil {
		fail(http.StatusBadRequest, err)
		return
	}
	// storedVideo already parsed the multipart form, so the optional
	// custom-prompts field is readable here ("" for JSON uploads).
	rawPrompts := r.FormValue("object_prompts")
	prompts, err := parseObjectPrompts(rawPrompts)
	if err != nil {
		fail(http.StatusBadRequest, err)
		return
	}
	// Only custom lists are recorded on the workflow: absent prompts keep
	// files (and old runs) identical to before, and let the UI tell a
	// default learn from a custom one.
	customPrompts := strings.TrimSpace(rawPrompts) != ""
	s.setProgress("learn", "video received", name, -1, -1)
	if s.mock {
		s.setProgress("learn", "loading demo workflow", "", -1, -1)
		b, err := s.fixture("workflow.json")
		if err != nil {
			fail(http.StatusInternalServerError, err)
			return
		}
		wf, err := graph.Parse(b)
		if err != nil {
			fail(http.StatusInternalServerError, err)
			return
		}
		wf.SourceVideo = name
		if customPrompts {
			wf.Prompts = prompts
		}
		s.rememberLearn(wf, s.mockLearnTracks(r.Context(), path, name))
		s.setProgress("learn", "workflow done", fmt.Sprintf("%d steps", len(wf.Steps)), -1, -1)
		s.persistLearned(w, wf)
		writeJSON(w, http.StatusOK, wf)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	tracks, err := s.meta.TrackWithProgress(ctx, path, prompts,
		func(stage, detail string, done, total int) {
			s.setProgress("learn", stage, detail, done, total)
		})
	if err != nil {
		fail(http.StatusBadGateway, err)
		return
	}
	s.setProgress("learn", "building workflow", "", -1, -1)
	tracksJSON, _ := json.Marshal(tracks)
	learnUser := "Video: " + name + "\nTracked objects per sampled frame:\n" + string(tracksJSON)
	out, err := s.meta.Chat(ctx, graph.BuilderSystemPrompt(), learnUser)
	if err != nil {
		fail(http.StatusBadGateway, err)
		return
	}
	wf, err := graph.Parse([]byte(out))
	if err != nil {
		// One repair attempt before failing: ask the model to fix its reply.
		s.setProgress("learn", "repairing workflow", "model reply failed validation; one retry", -1, -1)
		fixed, rerr := s.meta.ChatRepair(ctx, graph.BuilderSystemPrompt(), learnUser, out, err)
		if rerr == nil {
			wf, err = graph.Parse([]byte(fixed))
		}
	}
	if err != nil {
		fail(http.StatusBadGateway, fmt.Errorf("model returned invalid workflow: %w", err))
		return
	}
	wf.SourceVideo = name
	if customPrompts {
		wf.Prompts = prompts
	}
	vw, vh, vfps := s.probeDims(ctx, path)
	s.rememberLearn(wf, &trackSet{Video: name, Available: true, Width: vw, Height: vh, FPS: vfps, Frames: tracks})
	s.setProgress("learn", "workflow done", fmt.Sprintf("%d steps", len(wf.Steps)), -1, -1)
	s.persistLearned(w, wf)
	writeJSON(w, http.StatusOK, wf)
}

// errNoLearnedWorkflow is returned in live mode when Verify runs before any
// Learn: live checks must never silently compare against the demo fixture.
var errNoLearnedWorkflow = errors.New("no learned workflow yet: run Learn (POST /api/learn) first")

func (s *Server) rememberLearn(wf *graph.Workflow, ts *trackSet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastLearned = wf
	s.lastLearnTracks = ts
}

func (s *Server) rememberVerify(ts *trackSet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastVerifyTracks = ts
}

func (s *Server) learnedWorkflow() *graph.Workflow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastLearned
}

func (s *Server) learnTracks() *trackSet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastLearnTracks
}

func (s *Server) verifyTracks() *trackSet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastVerifyTracks
}

func (s *Server) currentWorkflow() (*graph.Workflow, error) {
	if wf := s.learnedWorkflow(); wf != nil {
		return wf, nil
	}
	if !s.mock {
		return nil, errNoLearnedWorkflow
	}
	b, err := s.fixture("workflow.json")
	if err != nil {
		return nil, err
	}
	return graph.Parse(b)
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, err error) {
		s.setProgress("verify", "error", err.Error(), -1, -1)
		writeErr(w, status, err)
	}
	t0 := time.Now()
	path, name, err := s.storedVideo(r, "verify")
	if err != nil {
		fail(http.StatusBadRequest, err)
		return
	}
	s.setProgress("verify", "video received", name, -1, -1)
	wf, err := s.currentWorkflow()
	if err != nil {
		if errors.Is(err, errNoLearnedWorkflow) {
			fail(http.StatusConflict, err)
			return
		}
		fail(http.StatusInternalServerError, err)
		return
	}
	if s.mock {
		s.setProgress("verify", "loading demo report", "", -1, -1)
		b, err := s.fixture("report.json")
		if err != nil {
			fail(http.StatusInternalServerError, err)
			return
		}
		var rep verify.Report
		if err := json.Unmarshal(b, &rep); err != nil {
			fail(http.StatusInternalServerError, err)
			return
		}
		rep.SourceVideo = name
		// Mock verify invents nothing: remember the video with an explicit
		// unavailable marker.
		s.rememberVerify(&trackSet{Video: name, Frames: []tracking.FrameTracks{}})
		s.setProgress("verify", "report done", fmt.Sprintf("%d findings", len(rep.Findings)), -1, -1)
		s.persistRun(w, s.currentWorkflowID(), wf.Name, name, rep, time.Since(t0))
		writeJSON(w, http.StatusOK, rep)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	tracks, err := s.meta.TrackWithProgress(ctx, path, resolvePrompts(wf),
		func(stage, detail string, done, total int) {
			s.setProgress("verify", stage, detail, done, total)
		})
	if err != nil {
		fail(http.StatusBadGateway, err)
		return
	}
	s.setProgress("verify", "describing observed steps", "", -1, -1)
	wfJSON, _ := json.Marshal(wf)
	tracksJSON, _ := json.Marshal(tracks)
	describeUser := "Workflow:\n" + string(wfJSON) + "\nVideo: " + name +
		"\nTracked objects per sampled frame:\n" + string(tracksJSON)
	out, err := s.meta.Chat(ctx, verify.DescribeSystemPrompt(), describeUser)
	if err != nil {
		fail(http.StatusBadGateway, err)
		return
	}
	observed, err := verify.ParseObserved([]byte(out))
	if err != nil {
		// One repair attempt before failing: ask the model to fix its reply.
		s.setProgress("verify", "repairing observed steps", "model reply failed validation; one retry", -1, -1)
		fixed, rerr := s.meta.ChatRepair(ctx, verify.DescribeSystemPrompt(), describeUser, out, err)
		if rerr == nil {
			observed, err = verify.ParseObserved([]byte(fixed))
		}
	}
	if err != nil {
		fail(http.StatusBadGateway, fmt.Errorf("model returned invalid observed steps: %w", err))
		return
	}
	s.setProgress("verify", "comparing", "", -1, -1)
	vw, vh, vfps := s.probeDims(ctx, path)
	s.rememberVerify(&trackSet{Video: name, Available: true, Width: vw, Height: vh, FPS: vfps, Frames: tracks})
	rep := verify.Compare(wf, name, observed)
	s.setProgress("verify", "report done", fmt.Sprintf("%d findings", len(rep.Findings)), -1, -1)
	s.persistRun(w, s.currentWorkflowID(), wf.Name, name, rep, time.Since(t0))
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleDemoWorkflow(w http.ResponseWriter, r *http.Request) {
	b, err := s.fixture("workflow.json")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func (s *Server) handleDemoReport(w http.ResponseWriter, r *http.Request) {
	b, err := s.fixture("report.json")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// SetStaticDir overrides where handleStatic serves the UI from. Tests point
// it at a t.TempDir() fixture so they never depend on a real web/dist build.
func (s *Server) SetStaticDir(dir string) {
	s.staticDir = dir
}

// staticPath resolves the built UI directory: the override when it names an
// existing directory, otherwise <root>/web/dist when a
// `pnpm --dir web build` exists, else "".
func (s *Server) staticPath() string {
	if s.staticDir != "" {
		if st, err := os.Stat(s.staticDir); err == nil && st.IsDir() {
			return filepath.Clean(s.staticDir)
		}
		return ""
	}
	d := filepath.Join(s.root, "web", "dist")
	if st, err := os.Stat(d); err == nil && st.IsDir() {
		return d
	}
	return ""
}

// missingBuildHint tells the operator how to produce web/dist. The Go server
// never builds the frontend itself; in development `pnpm --dir web dev`
// serves the UI with an API proxy instead.
const missingBuildHint = "web UI not built: run `pnpm --dir web install && pnpm --dir web build`, " +
	"or `pnpm --dir web dev` for development with hot reload."

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	dir := s.staticPath()
	if dir == "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(missingBuildHint))
		return
	}
	// The UI uses hash routing, so the server only ever needs "/" plus
	// fingerprinted assets: no SPA fallback, unknown paths stay 404.
	rel := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if rel == "" || rel == "." {
		rel = "index.html"
	}
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if !strings.HasPrefix(full, dir+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	// Refuse missing paths, directories and symlinks (which could escape dir).
	if st, err := os.Lstat(full); err != nil || st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, full)
}

// loadDotEnv reads KEY=VALUE lines and sets missing env vars. No expansion,
// no quotes semantics beyond stripping one layer of surrounding quotes.
func loadDotEnv(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if k == "" || os.Getenv(k) != "" {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		_ = os.Setenv(k, v)
	}
}
