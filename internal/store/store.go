// Package store persists verification runs (the evidence trail) in a local
// SQLite file. Writes are append-only: a run is never updated or deleted,
// so the history stays a faithful audit trail.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned by GetRun for an unknown id.
var ErrNotFound = errors.New("run not found")

// Run is one recorded verification: the report plus the tracks snapshot and
// the workflow/video it checked. ReportJSON and TracksJSON hold the exact
// response bytes the API served.
type Run struct {
	ID            string
	CreatedAt     time.Time
	WorkflowID    string
	WorkflowName  string
	SourceVideo   string
	Passed        bool
	FindingsCount int
	DurationMS    int64
	ReportJSON    string
	TracksJSON    string
}

// Summary is the list-view projection of a Run, without the blobs.
type Summary struct {
	ID            string    `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	WorkflowID    string    `json:"workflow_id"`
	WorkflowName  string    `json:"workflow_name"`
	SourceVideo   string    `json:"source_video"`
	Passed        bool      `json:"passed"`
	FindingsCount int       `json:"findings_count"`
	DurationMS    int64     `json:"duration_ms"`
}

// Summary projects r to its list-view form.
func (r Run) Summary() Summary {
	return Summary{
		ID: r.ID, CreatedAt: r.CreatedAt, WorkflowID: r.WorkflowID,
		WorkflowName: r.WorkflowName, SourceVideo: r.SourceVideo,
		Passed: r.Passed, FindingsCount: r.FindingsCount, DurationMS: r.DurationMS,
	}
}

// NewRunID returns a time-sortable unique run id.
func NewRunID() string {
	return fmt.Sprintf("run-%s-%06x", time.Now().UTC().Format("20060102T150405"), rand.IntN(1<<24))
}

const schema = `
CREATE TABLE IF NOT EXISTS runs (
	id TEXT PRIMARY KEY,
	created_at TEXT NOT NULL,
	workflow_id TEXT NOT NULL DEFAULT '',
	workflow_name TEXT NOT NULL,
	source_video TEXT NOT NULL,
	passed INTEGER NOT NULL,
	findings_count INTEGER NOT NULL,
	duration_ms INTEGER NOT NULL DEFAULT 0,
	report_json TEXT NOT NULL,
	tracks_json TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_runs_created ON runs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_workflow ON runs(workflow_id);
`

// Store is an open runs database.
type Store struct {
	db *sql.DB
}

// Open creates the parent dir if needed and opens (or creates) the database
// at path, applying the schema. One connection only: the demo writes one
// run at a time and never needs concurrent writers.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("runs db dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open runs db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		db.Close()
		return nil, fmt.Errorf("runs db pragma: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("runs db schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database.
func (s *Store) Close() error {
	return s.db.Close()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SaveRun appends r. The id must be set (see NewRunID); blobs must be valid
// JSON, tracks may be empty when there is no tracking data to snapshot.
func (s *Store) SaveRun(r Run) error {
	if r.ID == "" {
		return fmt.Errorf("save run: empty id")
	}
	if !json.Valid([]byte(r.ReportJSON)) {
		return fmt.Errorf("save run %s: report is not valid JSON", r.ID)
	}
	if r.TracksJSON != "" && !json.Valid([]byte(r.TracksJSON)) {
		return fmt.Errorf("save run %s: tracks are not valid JSON", r.ID)
	}
	_, err := s.db.Exec(
		`INSERT INTO runs (id, created_at, workflow_id, workflow_name, source_video,
			passed, findings_count, duration_ms, report_json, tracks_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.CreatedAt.UTC().Format(time.RFC3339Nano), r.WorkflowID, r.WorkflowName,
		r.SourceVideo, boolInt(r.Passed), r.FindingsCount, r.DurationMS,
		r.ReportJSON, r.TracksJSON,
	)
	if err != nil {
		return fmt.Errorf("save run %s: %w", r.ID, err)
	}
	return nil
}

const maxListLimit = 200

// ListRuns returns summaries newest first, up to limit (default 50 when
// limit <= 0, capped at 200).
func (s *Store) ListRuns(limit int) ([]Summary, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	rows, err := s.db.Query(
		`SELECT id, created_at, workflow_id, workflow_name, source_video,
			passed, findings_count, duration_ms
		FROM runs ORDER BY created_at DESC, id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var sm Summary
		var created string
		var passed int
		if err := rows.Scan(&sm.ID, &created, &sm.WorkflowID, &sm.WorkflowName,
			&sm.SourceVideo, &passed, &sm.FindingsCount, &sm.DurationMS); err != nil {
			return nil, fmt.Errorf("list runs: %w", err)
		}
		t, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("list runs: bad timestamp %q: %w", created, err)
		}
		sm.CreatedAt = t
		sm.Passed = passed != 0
		out = append(out, sm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	return out, nil
}

// GetRun returns the full run, or ErrNotFound for an unknown id.
func (s *Store) GetRun(id string) (Run, error) {
	var r Run
	var created string
	var passed int
	err := s.db.QueryRow(
		`SELECT id, created_at, workflow_id, workflow_name, source_video,
			passed, findings_count, duration_ms, report_json, tracks_json
		FROM runs WHERE id = ?`, id,
	).Scan(&r.ID, &created, &r.WorkflowID, &r.WorkflowName, &r.SourceVideo,
		&passed, &r.FindingsCount, &r.DurationMS, &r.ReportJSON, &r.TracksJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("get run %s: %w", id, err)
	}
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return Run{}, fmt.Errorf("get run %s: bad timestamp: %w", id, err)
	}
	r.CreatedAt = t
	r.Passed = passed != 0
	return r, nil
}
