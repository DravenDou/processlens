package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleRun(id string) Run {
	return Run{
		ID: id, CreatedAt: time.Now().UTC(),
		WorkflowID: "wf-1", WorkflowName: "block-tower", SourceVideo: "video2-errors.mp4",
		Passed: false, FindingsCount: 4, DurationMS: 1234,
		ReportJSON: `{"passed":false}`, TracksJSON: `{"available":false}`,
	}
}

func TestSaveGetRoundTrip(t *testing.T) {
	s := openTemp(t)
	want := sampleRun(NewRunID())
	if err := s.SaveRun(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRun(want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.WorkflowName != want.WorkflowName ||
		got.SourceVideo != want.SourceVideo || got.Passed != want.Passed ||
		got.FindingsCount != want.FindingsCount || got.DurationMS != want.DurationMS ||
		got.ReportJSON != want.ReportJSON || got.TracksJSON != want.TracksJSON {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("created_at = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
}

func TestGetUnknownIsNotFound(t *testing.T) {
	s := openTemp(t)
	if _, err := s.GetRun("run-nope"); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListNewestFirst(t *testing.T) {
	s := openTemp(t)
	base := time.Now().UTC().Truncate(time.Second)
	for i, id := range []string{"run-a", "run-b", "run-c"} {
		r := sampleRun(id)
		r.CreatedAt = base.Add(time.Duration(i) * time.Second)
		if err := s.SaveRun(r); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].ID != "run-c" || list[1].ID != "run-b" || list[2].ID != "run-a" {
		t.Fatalf("order wrong: %+v", list)
	}
}

func TestListRespectsLimit(t *testing.T) {
	s := openTemp(t)
	for _, id := range []string{"run-a", "run-b", "run-c"} {
		if err := s.SaveRun(sampleRun(id)); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListRuns(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
}

func TestSaveRejectsBadInput(t *testing.T) {
	s := openTemp(t)
	bad := sampleRun("")
	if err := s.SaveRun(bad); err == nil {
		t.Fatal("empty id accepted")
	}
	bad = sampleRun(NewRunID())
	bad.ReportJSON = "{oops"
	if err := s.SaveRun(bad); err == nil {
		t.Fatal("invalid report JSON accepted")
	}
	bad.ReportJSON = `{}`
	bad.TracksJSON = "{oops"
	if err := s.SaveRun(bad); err == nil {
		t.Fatal("invalid tracks JSON accepted")
	}
	ok := sampleRun(NewRunID())
	if err := s.SaveRun(ok); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRun(ok); err == nil {
		t.Fatal("duplicate id accepted")
	}
}

func TestPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id := NewRunID()
	if err := s.SaveRun(sampleRun(id)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err := s2.GetRun(id); err != nil {
		t.Fatalf("after reopen: %v", err)
	}
}

func TestNewRunIDUnique(t *testing.T) {
	a, b := NewRunID(), NewRunID()
	if a == b || !strings.HasPrefix(a, "run-") {
		t.Fatalf("ids not unique/sorted: %q %q", a, b)
	}
}

func TestOpenCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deep", "nested", "runs.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}

func TestSaveRunHashesChain(t *testing.T) {
	s := openTemp(t)
	base := time.Now().UTC().Truncate(time.Second)
	ids := []string{"run-a", "run-b", "run-c"}
	for i, id := range ids {
		r := sampleRun(id)
		r.CreatedAt = base.Add(time.Duration(i) * time.Second)
		if err := s.SaveRun(r); err != nil {
			t.Fatal(err)
		}
	}
	var prev string
	for i, id := range ids {
		got, err := s.GetRun(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.RunHash) != 64 {
			t.Fatalf("%s run_hash = %q, want 64 hex chars", id, got.RunHash)
		}
		wantPrev := "GENESIS"
		if i > 0 {
			wantPrev = prev
		}
		if got.PrevHash != wantPrev {
			t.Fatalf("%s prev_hash = %q, want %q", id, got.PrevHash, wantPrev)
		}
		prev = got.RunHash
	}
	list, err := s.ListRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].RunHash == "" || list[0].PrevHash == "" {
		t.Fatalf("list summaries must expose hashes: %+v", list)
	}
	count, badID, err := s.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 || badID != "" {
		t.Fatalf("VerifyChain = (%d, %q), want (3, \"\")", count, badID)
	}
}

func TestVerifyChainDetectsTamper(t *testing.T) {
	s := openTemp(t)
	base := time.Now().UTC().Truncate(time.Second)
	for i, id := range []string{"run-a", "run-b"} {
		r := sampleRun(id)
		r.CreatedAt = base.Add(time.Duration(i) * time.Second)
		if err := s.SaveRun(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE runs SET report_json = '{"passed":true}' WHERE id = 'run-b'`); err != nil {
		t.Fatal(err)
	}
	count, badID, err := s.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || badID != "run-b" {
		t.Fatalf("VerifyChain = (%d, %q), want (2, \"run-b\")", count, badID)
	}
}

func TestTamperSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second)
	for i, id := range []string{"run-a", "run-b"} {
		r := sampleRun(id)
		r.CreatedAt = base.Add(time.Duration(i) * time.Second)
		if err := s.SaveRun(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Tamper behind the store's back, then reopen: migration must seal
	// legacy rows only, never re-seal (and thereby hide) tampering.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE runs SET report_json = '{"passed":true}' WHERE id = 'run-a'`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	count, badID, err := s2.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || badID != "run-a" {
		t.Fatalf("after reopen VerifyChain = (%d, %q), want (2, \"run-a\")", count, badID)
	}
}

// legacySchema is the pre-chain runs table: no prev_hash/run_hash columns.
const legacySchema = `
CREATE TABLE runs (
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
);`

func TestOpenMigratesLegacyDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(legacySchema); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.Exec(
		`INSERT INTO runs (id, created_at, workflow_id, workflow_name, source_video,
			passed, findings_count, duration_ms, report_json, tracks_json)
		VALUES ('run-old', '2024-01-02T03:04:05Z', 'wf-1', 'block-tower', 'v.mp4',
			0, 4, 1234, '{"passed":false}', '{"available":false}')`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetRun("run-old")
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkflowName != "block-tower" || got.FindingsCount != 4 || got.DurationMS != 1234 {
		t.Fatalf("migration lost data: %+v", got)
	}
	if got.PrevHash != "GENESIS" || len(got.RunHash) != 64 {
		t.Fatalf("backfilled hashes = %q/%q", got.PrevHash, got.RunHash)
	}
	count, badID, err := s.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || badID != "" {
		t.Fatalf("VerifyChain = (%d, %q), want (1, \"\")", count, badID)
	}
}
