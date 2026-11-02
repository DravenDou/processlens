package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"processlens/internal/graph"
)

// TestWorkflowRoundTrip: Learn saves a file, GET lists it, POST select
// makes it current — all inside a TempDir.
func TestWorkflowRoundTrip(t *testing.T) {
	s := mockServer(t)
	rec := upload(t, s, "/api/learn", "video1-clean.mp4")
	if rec.Code != http.StatusOK {
		t.Fatalf("learn code = %d: %s", rec.Code, rec.Body.String())
	}
	id := rec.Header().Get("X-Workflow-ID")
	if id == "" {
		t.Fatal("learn response carries no X-Workflow-ID")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workflows", nil)
	got := httptest.NewRecorder()
	s.routes().ServeHTTP(got, req)
	if got.Code != http.StatusOK {
		t.Fatalf("list code = %d", got.Code)
	}
	var entries []workflowEntry
	if err := json.Unmarshal(got.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.ID != id {
		t.Fatalf("entry id = %q, want header id %q", e.ID, id)
	}
	if e.Name != "block-tower" || e.Steps != 6 {
		t.Fatalf("entry = %+v", e)
	}
	if !strings.Contains(e.SourceVideo, "video1-clean.mp4") {
		t.Fatalf("source_video = %q", e.SourceVideo)
	}
	if e.SavedAt <= 0 {
		t.Fatalf("saved_at = %d, want a unix timestamp", e.SavedAt)
	}

	// Select it on a fresh server sharing only the workflows dir: this
	// proves the selection comes from disk, surviving a restart.
	s2 := mockServer(t)
	s2.SetWorkflowsDir(s.workflowsPath())
	body := strings.NewReader(`{"id":` + quoteJSON(t, id) + `}`)
	req = httptest.NewRequest(http.MethodPost, "/api/workflows/select", body)
	sel := httptest.NewRecorder()
	s2.routes().ServeHTTP(sel, req)
	if sel.Code != http.StatusOK {
		t.Fatalf("select code = %d: %s", sel.Code, sel.Body.String())
	}
	var wf graph.Workflow
	if err := json.Unmarshal(sel.Body.Bytes(), &wf); err != nil {
		t.Fatal(err)
	}
	if wf.Name != "block-tower" || len(wf.Steps) != 6 {
		t.Fatalf("selected workflow = %q (%d steps)", wf.Name, len(wf.Steps))
	}
	if s2.learnedWorkflow() == nil {
		t.Fatal("select did not set the current workflow")
	}
}

func quoteJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestWorkflowListSkipsInvalid: corrupt and schema-invalid files never
// appear in the list, and no temp files leak from saves.
func TestWorkflowListSkipsInvalid(t *testing.T) {
	s := mockServer(t)
	if rec := upload(t, s, "/api/learn", "video1-clean.mp4"); rec.Code != http.StatusOK {
		t.Fatalf("learn code = %d", rec.Code)
	}
	dir := s.workflowsPath()
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wrong-shape.json"), []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/workflows", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	var entries []workflowEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want only the 1 valid save", len(entries))
	}
	for _, n := range listNames(t, dir) {
		if strings.HasPrefix(n, ".tmp-") {
			t.Fatalf("temp file leaked: %q", n)
		}
	}
}

// TestWorkflowSelectRejects: unknown ids conflict, traversal and malformed
// ids are bad requests, and nothing outside the workflows dir is read.
func TestWorkflowSelectRejects(t *testing.T) {
	s := mockServer(t)
	selectAs := func(id string) *httptest.ResponseRecorder {
		body := strings.NewReader(`{"id":` + quoteJSON(t, id) + `}`)
		req := httptest.NewRequest(http.MethodPost, "/api/workflows/select", body)
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		return rec
	}
	if rec := selectAs("9999-nope.mp4"); rec.Code != http.StatusConflict {
		t.Fatalf("unknown id code = %d, want 409", rec.Code)
	}
	for _, bad := range []string{"", "..", ".", "../x", "a/b", `a\b`, ".hidden", "a b", "á"} {
		if rec := selectAs(bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("id %q code = %d, want 400", bad, rec.Code)
		}
	}
	if s.learnedWorkflow() != nil {
		t.Fatal("rejected select set a current workflow")
	}
}

// TestWorkflowSaveSanitizes: hostile video names produce safe bare
// filenames inside the workflows dir, never traversal.
func TestWorkflowSaveSanitizes(t *testing.T) {
	s := mockServer(t)
	rec := upload(t, s, "/api/learn", "video1-clean.mp4")
	if rec.Code != http.StatusOK {
		t.Fatalf("learn code = %d", rec.Code)
	}
	id := rec.Header().Get("X-Workflow-ID")
	if _, err := workflowFilename(id); err != nil {
		t.Fatalf("saved id %q is not itself selectable: %v", id, err)
	}
	for _, n := range listNames(t, s.workflowsPath()) {
		if n != filepath.Base(n) || strings.Contains(n, `\`) {
			t.Fatalf("unsafe filename %q", n)
		}
	}
	if got := sanitizeWorkflowBase("../../etc/passwd"); got != "etc_passwd" {
		t.Fatalf("sanitize = %q, want %q", got, "etc_passwd")
	}
	if got := sanitizeWorkflowBase(""); got != "workflow" {
		t.Fatalf("sanitize empty = %q", got)
	}
}
