package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"processlens/internal/graph"
)

func TestParseObjectPrompts(t *testing.T) {
	long81 := strings.Repeat("x", 81)
	eight := `["p1","p2","p3","p4","p5","p6","p7","p8"]`
	nine := `["p1","p2","p3","p4","p5","p6","p7","p8","p9"]`
	cases := []struct {
		name    string
		raw     string
		want    []string
		wantErr string
	}{
		{"absent resolves to defaults", "", defaultPrompts, ""},
		{"blank resolves to defaults", "  \t ", defaultPrompts, ""},
		{"trims entries", `["screwdriver"," hammer "]`, []string{"screwdriver", "hammer"}, ""},
		{"drops empties and dedupes", `["a"," a ","","b","a"]`, []string{"a", "b"}, ""},
		{"exact dedupe keeps case variants", `["A","a"]`, []string{"A", "a"}, ""},
		{"eight is ok", eight, []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8"}, ""},
		{"not json", `nope`, nil, "invalid object_prompts"},
		{"not an array", `{"a":1}`, nil, "invalid object_prompts"},
		{"not strings", `[1,2]`, nil, "invalid object_prompts"},
		{"empty array", `[]`, nil, "want 1..8"},
		{"only empties", `["","  "]`, nil, "want 1..8"},
		{"nine is too many", nine, nil, "want 1..8"},
		{"too long", `["` + long81 + `"]`, nil, "exceeds 80"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseObjectPrompts(tc.raw)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolvePrompts(t *testing.T) {
	if got := resolvePrompts(nil); !reflect.DeepEqual(got, defaultPrompts) {
		t.Fatalf("nil workflow = %q, want defaults", got)
	}
	if got := resolvePrompts(&graph.Workflow{}); !reflect.DeepEqual(got, defaultPrompts) {
		t.Fatalf("no-prompts workflow = %q, want defaults", got)
	}
	custom := []string{"screwdriver", "hammer"}
	if got := resolvePrompts(&graph.Workflow{Prompts: custom}); !reflect.DeepEqual(got, custom) {
		t.Fatalf("custom workflow = %q, want %q", got, custom)
	}
}

// learnWithPrompts posts a multipart Learn with an optional object_prompts
// text field beside the video.
func learnWithPrompts(t *testing.T, s *Server, filename, prompts string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("video", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("fake-video-bytes")); err != nil {
		t.Fatal(err)
	}
	if prompts != "" {
		if err := w.WriteField("object_prompts", prompts); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/learn", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	return rec
}

func TestLearnMockCustomPrompts(t *testing.T) {
	s := mockServer(t)
	rec := learnWithPrompts(t, s, "video1-clean.mp4", `["screwdriver"," hammer ","screwdriver",""]`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body.String())
	}
	if id := rec.Header().Get("X-Workflow-ID"); id == "" {
		t.Fatal("missing X-Workflow-ID")
	}
	var wf graph.Workflow
	if err := json.Unmarshal(rec.Body.Bytes(), &wf); err != nil {
		t.Fatal(err)
	}
	if want := []string{"screwdriver", "hammer"}; !reflect.DeepEqual(wf.Prompts, want) {
		t.Fatalf("response prompts = %q, want %q", wf.Prompts, want)
	}
	// The saved file on disk carries the same prompts.
	names := listNames(t, s.workflowsPath())
	if len(names) != 1 {
		t.Fatalf("saved files = %q, want exactly 1", names)
	}
	b, err := os.ReadFile(filepath.Join(s.workflowsPath(), names[0]))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := graph.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved.Prompts, wf.Prompts) {
		t.Fatalf("saved prompts = %q, want %q", saved.Prompts, wf.Prompts)
	}
}

func TestLearnMockDefaultPrompts(t *testing.T) {
	s := mockServer(t)
	rec := learnWithPrompts(t, s, "video1-clean.mp4", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body.String())
	}
	var wf graph.Workflow
	if err := json.Unmarshal(rec.Body.Bytes(), &wf); err != nil {
		t.Fatal(err)
	}
	// Defaults are resolved but NOT recorded: an absent list keeps default
	// learns byte-identical to before and lets the UI (and Verify, via
	// resolvePrompts) tell them apart from custom ones.
	if len(wf.Prompts) != 0 {
		t.Fatalf("prompts = %q, want absent for a default learn", wf.Prompts)
	}
}

func TestLearnMockInvalidPromptsFailsClosed(t *testing.T) {
	s := mockServer(t)
	for _, raw := range []string{`nope`, `[]`, `["` + strings.Repeat("x", 81) + `"]`} {
		rec := learnWithPrompts(t, s, "video1-clean.mp4", raw)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("raw %q: code = %d, want 400", raw, rec.Code)
		}
		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body["error"], "object_prompts") {
			t.Fatalf("raw %q: error %q does not name object_prompts", raw, body["error"])
		}
	}
	if got := listNames(t, s.workflowsPath()); len(got) != 0 {
		t.Fatalf("invalid learn saved %q, want nothing", got)
	}
}
