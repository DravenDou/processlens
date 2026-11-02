// Saved workflows: Learn writes each workflow as one JSON file under
// <root>/data/workflows so Verify can reuse it after a server restart.
// Writes are atomic (temp file + rename); reads validate with graph.Parse
// and skip invalid files. Stdlib only.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"processlens/internal/graph"
)

// workflowEntry is one row of the GET /api/workflows response.
type workflowEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SourceVideo string `json:"source_video"`
	Steps       int    `json:"steps"`
	SavedAt     int64  `json:"saved_at"`
}

// SetWorkflowsDir overrides where saveWorkflow writes. Tests point it at
// t.TempDir() so they never touch the real repo data/workflows.
func (s *Server) SetWorkflowsDir(dir string) {
	s.workflowsDir = dir
}

func (s *Server) workflowsPath() string {
	if s.workflowsDir != "" {
		return s.workflowsDir
	}
	return filepath.Join(s.root, "data", "workflows")
}

// sanitizeWorkflowBase keeps a filename-safe stem of a video name: only
// ASCII letters, digits, dot, dash and underscore survive; anything else
// becomes an underscore. The result is never empty.
func sanitizeWorkflowBase(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if len(out) > 64 {
		out = out[:64]
	}
	if out == "" {
		out = "workflow"
	}
	return out
}

// workflowID splits a saved id back into its filename. The id is the
// filename without the .json suffix; it must be a bare safe name so a
// crafted id can never escape the workflows dir.
func workflowFilename(id string) (string, error) {
	if id == "" || id == "." || id == ".." ||
		strings.HasPrefix(id, ".") ||
		id != filepath.Base(id) ||
		strings.Contains(id, `\`) {
		return "", fmt.Errorf("invalid workflow id %q", id)
	}
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			continue
		}
		return "", fmt.Errorf("invalid workflow id %q", id)
	}
	return id + ".json", nil
}

// saveWorkflow persists wf as <unix>-<sanitized-video-base>.json via an
// atomic temp-file-plus-rename write, and returns the saved id (the
// filename without .json). The dir is created lazily.
func (s *Server) saveWorkflow(wf *graph.Workflow) (string, error) {
	dir := s.workflowsPath()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	base := sanitizeWorkflowBase(wf.SourceVideo)
	id := fmt.Sprintf("%d-%s", time.Now().Unix(), base)
	tmp, err := os.CreateTemp(dir, ".tmp-*.json")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	// On any failure below, remove the temp file so no partial file or
	// leftover temp file ever remains beside the saved workflows.
	failed := true
	defer func() {
		if failed {
			_ = os.Remove(tmpName)
		}
	}()
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(wf); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, id+".json")); err != nil {
		return "", err
	}
	failed = false
	return id, nil
}

// savedAt parses the <unix> prefix of a saved id; unparseable ids fall
// back to the file mtime so listing still sorts them sanely.
func savedAt(id string, fi os.FileInfo) int64 {
	if i := strings.IndexByte(id, '-'); i > 0 {
		if ts, err := strconv.ParseInt(id[:i], 10, 64); err == nil && ts > 0 {
			return ts
		}
	}
	if fi != nil {
		return fi.ModTime().Unix()
	}
	return 0
}

// listWorkflows reads every saved workflow, newest first. A missing dir is
// an empty list, not an error; unreadable or invalid files are skipped.
func (s *Server) listWorkflows() []workflowEntry {
	dir := s.workflowsPath()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []workflowEntry{}
	}
	var out []workflowEntry
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		wf, err := graph.Parse(b)
		if err != nil {
			continue
		}
		fi, _ := e.Info()
		id := strings.TrimSuffix(name, ".json")
		out = append(out, workflowEntry{
			ID:          id,
			Name:        wf.Name,
			SourceVideo: wf.SourceVideo,
			Steps:       len(wf.Steps),
			SavedAt:     savedAt(id, fi),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SavedAt != out[j].SavedAt {
			return out[i].SavedAt > out[j].SavedAt
		}
		return out[i].ID > out[j].ID
	})
	if out == nil {
		return []workflowEntry{}
	}
	return out
}

// selectWorkflow loads a saved workflow by id and makes it the current one
// for Verify. Unknown ids and invalid files are a 409-style conflict: the
// caller asked to verify against something that is not usable.
func (s *Server) selectWorkflow(id string) (*graph.Workflow, error) {
	name, err := workflowFilename(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(s.workflowsPath(), name))
	if err != nil {
		return nil, errUnknownWorkflow
	}
	wf, err := graph.Parse(b)
	if err != nil {
		return nil, errUnknownWorkflow
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastLearned = wf
	s.activeWorkflowID = id
	// No tracking data travels with a saved workflow: clear the learn
	// bundle rather than serve another video's tracks under this name.
	s.lastLearnTracks = nil
	return wf, nil
}

var errUnknownWorkflow = fmt.Errorf("unknown workflow id: select a saved workflow from GET /api/workflows")

func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.listWorkflows())
}

func (s *Server) handleWorkflowSelect(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("decode id: %w", err))
		return
	}
	payload.ID = strings.TrimSpace(payload.ID)
	wf, err := s.selectWorkflow(payload.ID)
	if err != nil {
		if _, ferr := workflowFilename(payload.ID); ferr != nil {
			writeErr(w, http.StatusBadRequest, ferr)
			return
		}
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, wf)
}

// persistLearned saves a freshly learned workflow and reports its id back
// via the X-Workflow-ID header. A disk failure must not fail the Learn
// itself, so it is logged and the response carries no id.
func (s *Server) persistLearned(w http.ResponseWriter, wf *graph.Workflow) {
	id, err := s.saveWorkflow(wf)
	if err != nil {
		log.Printf("save workflow: %v", err)
		return
	}
	s.setActiveWorkflowID(id)
	w.Header().Set("X-Workflow-ID", id)
}
