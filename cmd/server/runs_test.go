package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"processlens/internal/store"
	"processlens/internal/verify"
)

func getJSON(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	return rec
}

func TestVerifyMockSavesRun(t *testing.T) {
	s := mockServer(t)
	rec := upload(t, s, "/api/verify", "video2-errors.mp4")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify code = %d: %s", rec.Code, rec.Body.String())
	}
	id := rec.Header().Get("X-Run-ID")
	if id == "" {
		t.Fatal("missing X-Run-ID header")
	}

	lrec := getJSON(t, s, "/api/runs")
	if lrec.Code != http.StatusOK {
		t.Fatalf("list code = %d", lrec.Code)
	}
	var list []store.Summary
	if err := json.Unmarshal(lrec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != id {
		t.Fatalf("list = %+v, want one run %s", list, id)
	}
	if list[0].Passed || list[0].FindingsCount != 4 ||
		!strings.HasSuffix(list[0].SourceVideo, "video2-errors.mp4") {
		t.Fatalf("summary = %+v", list[0])
	}

	grec := getJSON(t, s, "/api/runs/"+id)
	if grec.Code != http.StatusOK {
		t.Fatalf("get code = %d", grec.Code)
	}
	var got runRecord
	if err := json.Unmarshal(grec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.Report.Passed || len(got.Report.Findings) != 4 {
		t.Fatalf("record = %+v", got)
	}
	if !strings.HasSuffix(got.Tracks.Video, "video2-errors.mp4") || got.Tracks.Available {
		t.Fatalf("stored tracks = %+v", got.Tracks)
	}
	if got.WorkflowName != "block-tower" {
		t.Fatalf("workflow name = %q", got.WorkflowName)
	}
}

func TestRunsListLimit(t *testing.T) {
	s := mockServer(t)
	base := time.Now().UTC().Truncate(time.Second)
	for i, id := range []string{"run-a", "run-b", "run-c"} {
		r := store.Run{
			ID: id, CreatedAt: base.Add(time.Duration(i) * time.Second),
			WorkflowName: "w", SourceVideo: "v.mp4",
			ReportJSON: `{"workflow_name":"w","source_video":"v.mp4","passed":true,"findings":[]}`,
			TracksJSON: `{"video":"v.mp4","available":false,"frames":[]}`,
		}
		if err := s.runs.SaveRun(r); err != nil {
			t.Fatal(err)
		}
	}
	rec := getJSON(t, s, "/api/runs?limit=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	var list []store.Summary
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "run-c" || list[1].ID != "run-b" {
		t.Fatalf("list = %+v", list)
	}
	for _, q := range []string{"/api/runs?limit=nope", "/api/runs?limit=0", "/api/runs?limit=-3"} {
		if rec := getJSON(t, s, q); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s code = %d, want 400", q, rec.Code)
		}
	}
}

func TestRunNotFound(t *testing.T) {
	s := mockServer(t)
	if rec := getJSON(t, s, "/api/runs/run-nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

func TestRunsUnavailableWithoutStore(t *testing.T) {
	s := mockServer(t)
	s.SetStore(nil)
	if rec := getJSON(t, s, "/api/runs"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("list code = %d, want 503", rec.Code)
	}
	if rec := getJSON(t, s, "/api/runs/run-x"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("get code = %d, want 503", rec.Code)
	}
}

func TestVerifyWithoutStoreStillSucceeds(t *testing.T) {
	s := mockServer(t)
	s.SetStore(nil)
	rec := upload(t, s, "/api/verify", "video2-errors.mp4")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify code = %d", rec.Code)
	}
	if id := rec.Header().Get("X-Run-ID"); id != "" {
		t.Fatalf("X-Run-ID = %q, want empty without a store", id)
	}
	var rep verify.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Passed || len(rep.Findings) != 4 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestLearnTracksActiveWorkflowID(t *testing.T) {
	s := mockServer(t)
	if id := s.currentWorkflowID(); id != "" {
		t.Fatalf("initial id = %q, want empty", id)
	}
	rec := upload(t, s, "/api/learn", "video1-clean.mp4")
	if rec.Code != http.StatusOK {
		t.Fatalf("learn code = %d: %s", rec.Code, rec.Body.String())
	}
	want := rec.Header().Get("X-Workflow-ID")
	if want == "" {
		t.Fatal("missing X-Workflow-ID")
	}
	if got := s.currentWorkflowID(); got != want {
		t.Fatalf("active id = %q, want %q", got, want)
	}
	// A verify right after learn records that same workflow id.
	vrec := upload(t, s, "/api/verify", "video2-errors.mp4")
	runID := vrec.Header().Get("X-Run-ID")
	if runID == "" {
		t.Fatal("missing X-Run-ID")
	}
	grec := getJSON(t, s, "/api/runs/"+runID)
	var got runRecord
	if err := json.Unmarshal(grec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.WorkflowID != want {
		t.Fatalf("run workflow id = %q, want %q", got.WorkflowID, want)
	}
}
