// Real staged progress for Learn/Verify. Handlers set the record at real
// stages only; GET /api/progress reports it. Done/Total are real counts
// for countable stages (SAM concepts) and -1 everywhere else: nothing
// here is estimated and no percentage is ever invented.
package main

import (
	"fmt"
	"net/http"
)

// progress is the server-side state of one operation ("learn" or "verify").
type progress struct {
	Stage  string `json:"stage"`
	Detail string `json:"detail"`
	Done   int    `json:"done"`
	Total  int    `json:"total"`
}

// idleProgress is reported for an operation that never ran.
var idleProgress = progress{Stage: "idle", Done: -1, Total: -1}

// setProgress records the current stage of op. Callers must pass real
// stages only: the UI shows this text verbatim.
func (s *Server) setProgress(op, stage, detail string, done, total int) {
	s.progMu.Lock()
	defer s.progMu.Unlock()
	if s.progress == nil {
		s.progress = map[string]progress{}
	}
	s.progress[op] = progress{Stage: stage, Detail: detail, Done: done, Total: total}
}

func (s *Server) getProgress(op string) progress {
	s.progMu.Lock()
	defer s.progMu.Unlock()
	if p, ok := s.progress[op]; ok {
		return p
	}
	return idleProgress
}

// handleProgress reports the stage of the running (or last) op.
func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	switch op := r.URL.Query().Get("op"); op {
	case "learn", "verify":
		writeJSON(w, http.StatusOK, s.getProgress(op))
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("query op must be learn or verify"))
	}
}
