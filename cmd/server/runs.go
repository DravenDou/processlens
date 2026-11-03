package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"processlens/internal/store"
	"processlens/internal/verify"
)

// runsPath is the SQLite evidence file. Tests never use it: they inject a
// TempDir store via SetStore.
func (s *Server) runsPath() string {
	return filepath.Join(s.root, "data", "processlens.db")
}

// SetStore injects the runs database (tests point it at a TempDir file).
func (s *Server) SetStore(st *store.Store) {
	s.runs = st
}

// openRuns opens the production runs database. main calls it once and fails
// fast when the evidence trail cannot start.
func (s *Server) openRuns() error {
	st, err := store.Open(s.runsPath())
	if err != nil {
		return err
	}
	s.runs = st
	return nil
}

// currentWorkflowID returns the id of the workflow Verify checks against:
// the last learned-and-saved or explicitly selected id, or "" for a session
// workflow that never reached disk.
func (s *Server) currentWorkflowID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeWorkflowID
}

func (s *Server) setActiveWorkflowID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeWorkflowID = id
}

// persistRun records a finished verification as one evidence run. Like
// persistLearned, a disk failure must not fail the Verify itself: it is
// logged and the response carries no run id.
func (s *Server) persistRun(w http.ResponseWriter, workflowID, workflowName, video string, rep verify.Report, duration time.Duration) {
	if s.runs == nil {
		return
	}
	repJSON, err := json.Marshal(rep)
	if err != nil {
		log.Printf("save run: marshal report: %v", err)
		return
	}
	tracksJSON, err := json.Marshal(s.verifyTracksSnapshot(video))
	if err != nil {
		log.Printf("save run: marshal tracks: %v", err)
		return
	}
	run := store.Run{
		ID: store.NewRunID(), CreatedAt: time.Now().UTC(),
		WorkflowID: workflowID, WorkflowName: workflowName, SourceVideo: video,
		Passed: rep.Passed, FindingsCount: len(rep.Findings),
		DurationMS: duration.Milliseconds(),
		ReportJSON: string(repJSON), TracksJSON: string(tracksJSON),
	}
	if err := s.runs.SaveRun(run); err != nil {
		log.Printf("save run: %v", err)
		return
	}
	w.Header().Set("X-Run-ID", run.ID)
}

// verifyTracksSnapshot copies the remembered verify tracks for storage. The
// copy is detached from later runs: re-verifying never rewrites history.
func (s *Server) verifyTracksSnapshot(video string) tracksJSON {
	ts := s.verifyTracks()
	if ts == nil {
		return unavailableTracks(video)
	}
	return ts.toJSON()
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if s.runs == nil {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("runs store unavailable"))
		return
	}
	limit := 50
	if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n <= 0 {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid limit %q: want a positive integer", q))
			return
		}
		limit = n
	}
	list, err := s.runs.ListRuns(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// runRecord is the GET /api/runs/{id} response: the stored meta plus the
// report and tracks blobs decoded back into their API shapes.
type runRecord struct {
	store.Summary
	Report verify.Report `json:"report"`
	Tracks tracksJSON    `json:"tracks"`
}

// runsVerify is the GET /api/runs/verify response: the hash-chain verdict.
type runsVerify struct {
	OK    bool   `json:"ok"`
	Count int    `json:"count"`
	BadID string `json:"bad_id"`
}

func (s *Server) handleRunsVerify(w http.ResponseWriter, r *http.Request) {
	if s.runs == nil {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("runs store unavailable"))
		return
	}
	count, badID, err := s.runs.VerifyChain()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, runsVerify{OK: badID == "", Count: count, BadID: badID})
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if s.runs == nil {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("runs store unavailable"))
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("missing run id"))
		return
	}
	run, err := s.runs.GetRun(id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var rec runRecord
	rec.Summary = run.Summary()
	if err := json.Unmarshal([]byte(run.ReportJSON), &rec.Report); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("stored report for run %s is corrupt: %w", id, err))
		return
	}
	if run.TracksJSON == "" {
		rec.Tracks = unavailableTracks(run.SourceVideo)
	} else if err := json.Unmarshal([]byte(run.TracksJSON), &rec.Tracks); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("stored tracks for run %s are corrupt: %w", id, err))
		return
	}
	writeJSON(w, http.StatusOK, rec)
}
