package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
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
