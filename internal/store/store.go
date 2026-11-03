// Package store persists verification runs (the evidence trail) in a local
// SQLite file. Writes are append-only: a run is never updated or deleted,
// so the history stays a faithful audit trail.
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned by GetRun for an unknown id.
var ErrNotFound = errors.New("run not found")

// Run is one recorded verification: the report plus the tracks snapshot and
// the workflow/video it checked. ReportJSON and TracksJSON hold the exact
// response bytes the API served. PrevHash/RunHash link the run into the
// hash-chained evidence trail; SaveRun computes them.
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
	PrevHash      string `json:"prev_hash"`
	RunHash       string `json:"run_hash"`
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
	PrevHash      string    `json:"prev_hash"`
	RunHash       string    `json:"run_hash"`
}

// Summary projects r to its list-view form.
func (r Run) Summary() Summary {
	return Summary{
		ID: r.ID, CreatedAt: r.CreatedAt, WorkflowID: r.WorkflowID,
		WorkflowName: r.WorkflowName, SourceVideo: r.SourceVideo,
		Passed: r.Passed, FindingsCount: r.FindingsCount, DurationMS: r.DurationMS,
		PrevHash: r.PrevHash, RunHash: r.RunHash,
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
	tracks_json TEXT NOT NULL DEFAULT '',
	prev_hash TEXT NOT NULL DEFAULT '',
	run_hash TEXT NOT NULL DEFAULT ''
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
	if err := migrateHashChain(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// genesisHash is the prev_hash of the first run in an empty chain.
const genesisHash = "GENESIS"

// migrateHashChain adds the hash-chain columns to pre-chain databases and
// seals legacy rows (empty run_hash) in (created_at, id) order. Sealed rows
// are NEVER rewritten here: a mismatch is tamper evidence for VerifyChain,
// and re-sealing it on restart would destroy that evidence. Fresh databases
// already carry the columns and no rows, so this is a no-op.
func migrateHashChain(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(runs)`)
	if err != nil {
		return fmt.Errorf("runs db migrate: %w", err)
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("runs db migrate: %w", err)
		}
		cols[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("runs db migrate: %w", err)
	}
	for _, col := range []string{"prev_hash", "run_hash"} {
		if !cols[col] {
			if _, err := db.Exec(`ALTER TABLE runs ADD COLUMN ` + col + ` TEXT NOT NULL DEFAULT ''`); err != nil {
				return fmt.Errorf("runs db migrate: add %s: %w", col, err)
			}
		}
	}
	chain, err := loadChain(db)
	if err != nil {
		return err
	}
	prev := genesisHash
	for _, c := range chain {
		if c.runHash == "" {
			h := chainHash(c.id, c.created, c.workflowID, c.workflowName, c.sourceVideo,
				c.passed, c.findingsCount, c.durationMS, c.reportJSON, c.tracksJSON, prev)
			if _, err := db.Exec(`UPDATE runs SET prev_hash = ?, run_hash = ? WHERE id = ?`,
				prev, h, c.id); err != nil {
				return fmt.Errorf("runs db migrate: backfill %s: %w", c.id, err)
			}
			prev = h
			continue
		}
		// Sealed: advance on the STORED hash, never the recomputed one, so a
		// tampered row cannot shift its successors' links either.
		prev = c.runHash
	}
	return nil
}

// chainHash is the sha256 hex of the run's canonical bytes: every stored
// field plus the previous link, joined by newlines.
func chainHash(id, created, workflowID, workflowName, sourceVideo string, passed, findingsCount int, durationMS int64, reportJSON, tracksJSON, prevHash string) string {
	canonical := strings.Join([]string{
		id, created, workflowID, workflowName, sourceVideo,
		strconv.Itoa(passed), strconv.Itoa(findingsCount), strconv.FormatInt(durationMS, 10),
		reportJSON, tracksJSON, prevHash,
	}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

// chainRow is one stored run as raw column values, for hashing.
type chainRow struct {
	id                                    string
	created                               string
	workflowID, workflowName, sourceVideo string
	passed, findingsCount                 int
	durationMS                            int64
	reportJSON, tracksJSON                string
	prevHash, runHash                     string
}

// loadChain reads every run in chain order: (created_at, id) ascending.
func loadChain(db *sql.DB) ([]chainRow, error) {
	rows, err := db.Query(
		`SELECT id, created_at, workflow_id, workflow_name, source_video,
			passed, findings_count, duration_ms, report_json, tracks_json,
			prev_hash, run_hash
		FROM runs ORDER BY created_at ASC, id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("load chain: %w", err)
	}
	defer rows.Close()
	var out []chainRow
	for rows.Next() {
		var c chainRow
		if err := rows.Scan(&c.id, &c.created, &c.workflowID, &c.workflowName,
			&c.sourceVideo, &c.passed, &c.findingsCount, &c.durationMS,
			&c.reportJSON, &c.tracksJSON, &c.prevHash, &c.runHash); err != nil {
			return nil, fmt.Errorf("load chain: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load chain: %w", err)
	}
	return out, nil
}

// VerifyChain recomputes every link in (created_at, id) order. It returns
// the row count and the first mismatching id, or an empty id when the
// whole chain verifies.
func (s *Store) VerifyChain() (int, string, error) {
	chain, err := loadChain(s.db)
	if err != nil {
		return 0, "", err
	}
	prev := genesisHash
	for _, c := range chain {
		h := chainHash(c.id, c.created, c.workflowID, c.workflowName, c.sourceVideo,
			c.passed, c.findingsCount, c.durationMS, c.reportJSON, c.tracksJSON, prev)
		if c.prevHash != prev || c.runHash != h {
			return len(chain), c.id, nil
		}
		prev = h
	}
	return len(chain), "", nil
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
// It also links r into the hash chain: prev is the latest run_hash (or
// GENESIS), and the computed hashes are stored with the row.
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
	created := r.CreatedAt.UTC().Format(time.RFC3339Nano)
	prev := genesisHash
	var latest sql.NullString
	err := s.db.QueryRow(
		`SELECT run_hash FROM runs ORDER BY created_at DESC, id DESC LIMIT 1`,
	).Scan(&latest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("save run %s: latest hash: %w", r.ID, err)
	}
	if latest.Valid && latest.String != "" {
		prev = latest.String
	}
	passed := boolInt(r.Passed)
	r.PrevHash, r.RunHash = prev, chainHash(r.ID, created, r.WorkflowID,
		r.WorkflowName, r.SourceVideo, passed, r.FindingsCount, r.DurationMS,
		r.ReportJSON, r.TracksJSON, prev)
	_, err = s.db.Exec(
		`INSERT INTO runs (id, created_at, workflow_id, workflow_name, source_video,
			passed, findings_count, duration_ms, report_json, tracks_json,
			prev_hash, run_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, created, r.WorkflowID, r.WorkflowName,
		r.SourceVideo, passed, r.FindingsCount, r.DurationMS,
		r.ReportJSON, r.TracksJSON, r.PrevHash, r.RunHash,
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
			passed, findings_count, duration_ms, prev_hash, run_hash
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
			&sm.SourceVideo, &passed, &sm.FindingsCount, &sm.DurationMS,
			&sm.PrevHash, &sm.RunHash); err != nil {
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
			passed, findings_count, duration_ms, report_json, tracks_json,
			prev_hash, run_hash
		FROM runs WHERE id = ?`, id,
	).Scan(&r.ID, &created, &r.WorkflowID, &r.WorkflowName, &r.SourceVideo,
		&passed, &r.FindingsCount, &r.DurationMS, &r.ReportJSON, &r.TracksJSON,
		&r.PrevHash, &r.RunHash)
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
