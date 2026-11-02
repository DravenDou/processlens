package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"processlens/internal/graph"
	"processlens/internal/meta"
	"processlens/internal/tracking"
	"processlens/internal/verify"
)

func TestDefaultPrompts(t *testing.T) {
	// Locks the "toy brick" phrasing: bare "<color> brick" makes the
	// hosted API return empty output (masonry bias), verified live.
	want := []string{
		"red toy brick", "blue toy brick", "yellow toy brick",
		"orange toy brick", "white toy brick", "black toy brick",
	}
	if !reflect.DeepEqual(defaultPrompts, want) {
		t.Fatalf("defaultPrompts = %q", defaultPrompts)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepathAbs("../../")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func mockServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("MOCK_API", "1")
	t.Setenv("META_API_KEY", "")
	s := NewServer(repoRoot(t))
	if !s.mock {
		t.Fatal("want mock mode")
	}
	// All uploads go to a temp dir: tests must never write to or delete
	// the real repo data/uploads (which may hold the user's videos).
	s.SetUploadsDir(t.TempDir())
	// Same for saved workflows: Learn persists to disk now.
	s.SetWorkflowsDir(t.TempDir())
	return s
}

// liveServerNoLearn builds a live-mode Server with no learned workflow and a
// nil meta client: any code path that reaches the network client panics, so a
// passing test proves the network was never touched.
func liveServerNoLearn(t *testing.T) *Server {
	t.Helper()
	s := &Server{root: repoRoot(t), mock: false, meta: nil}
	s.SetUploadsDir(t.TempDir())
	s.SetWorkflowsDir(t.TempDir())
	return s
}

func filepathAbs(p string) (string, error) {
	if len(p) >= 1 && p[0] == '/' {
		return p, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return cwd + "/" + p, nil
}

func upload(t *testing.T, s *Server, route, filename string) *httptest.ResponseRecorder {
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
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, route, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	return rec
}

func TestHealthMock(t *testing.T) {
	s := mockServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" || body["mode"] != "mock" {
		t.Fatalf("body = %v", body)
	}
}

// staticFixture builds a mock server serving a TempDir UI fixture: an index
// plus fingerprinted-style assets. Tests never touch the real web/dist.
func staticFixture(t *testing.T) *Server {
	t.Helper()
	s := mockServer(t)
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", "<title>ProcessLens</title><div id=\"root\"></div>")
	write("assets/app-abc123.css", ":root{--hue:#1d5fbf}")
	write("assets/app-abc123.js", "window.addEventListener(\"hashchange\",route)")
	s.SetStaticDir(dir)
	return s
}

func TestStaticIndex(t *testing.T) {
	s := staticFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if !bytes.Contains(bytes.ToLower(rec.Body.Bytes()), []byte("processlens")) {
		t.Fatal("index does not mention ProcessLens")
	}
}

func TestStaticAssets(t *testing.T) {
	s := staticFixture(t)
	for path, needle := range map[string]string{
		"/assets/app-abc123.css": "--hue",
		"/assets/app-abc123.js":  "hashchange",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s code = %d", path, rec.Code)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte(needle)) {
			t.Fatalf("%s missing %q", path, needle)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/nope code = %d, want 404", rec.Code)
	}
}

func TestStaticRejectsTraversal(t *testing.T) {
	s := staticFixture(t)
	// Call the handler directly: ServeMux cleans ".." before routing, so only
	// the handler itself can prove containment.
	for _, p := range []string{"/../../etc/passwd", "/assets/../../../../etc/hostname"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.URL.Path = p
		rec := httptest.NewRecorder()
		s.handleStatic(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s code = %d, want 404", p, rec.Code)
		}
	}
}

func TestStaticRejectsSymlink(t *testing.T) {
	s := staticFixture(t)
	dir := s.staticPath()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/link.txt", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("symlink code = %d, want 404", rec.Code)
	}
}

func TestStaticMissingBuild(t *testing.T) {
	s := mockServer(t)
	s.SetStaticDir(filepath.Join(t.TempDir(), "no-such-dir"))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("pnpm --dir web build")) {
		t.Fatal("503 body does not explain how to build the UI")
	}
}

func TestDemoEndpoints(t *testing.T) {
	s := mockServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/demo/workflow", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	var wf graph.Workflow
	if err := json.Unmarshal(rec.Body.Bytes(), &wf); err != nil {
		t.Fatal(err)
	}
	if len(wf.Steps) != 6 {
		t.Fatalf("steps = %d", len(wf.Steps))
	}

	req = httptest.NewRequest(http.MethodGet, "/api/demo/report", nil)
	rec = httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	var rep verify.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Passed || len(rep.Findings) != 4 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestLearnMock(t *testing.T) {
	s := mockServer(t)
	rec := upload(t, s, "/api/learn", "video1-clean.mp4")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body.String())
	}
	var wf graph.Workflow
	if err := json.Unmarshal(rec.Body.Bytes(), &wf); err != nil {
		t.Fatal(err)
	}
	if wf.Name != "block-tower" || len(wf.Steps) != 6 {
		t.Fatalf("workflow = %+v", wf.Name)
	}
	if !bytes.Contains([]byte(wf.SourceVideo), []byte("video1-clean.mp4")) {
		t.Fatalf("source = %q", wf.SourceVideo)
	}
}

func TestVerifyMock(t *testing.T) {
	s := mockServer(t)
	rec := upload(t, s, "/api/verify", "video2-errors.mp4")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body.String())
	}
	var rep verify.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Passed || len(rep.Findings) != 4 {
		t.Fatalf("report = %+v", rep)
	}
}

// listNames returns the sorted entry names of dir, or nil when dir is missing.
func listNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// TestRealUploadsUntouched is the regression test for the destructive-test
// incident: exercising learn+verify must leave the real repo data/uploads
// byte-identical (uploads go to t.TempDir() instead).
func TestRealUploadsUntouched(t *testing.T) {
	s := mockServer(t)
	real := filepath.Join(s.root, "data", "uploads")
	before := listNames(t, real)
	realWf := filepath.Join(s.root, "data", "workflows")
	beforeWf := listNames(t, realWf)

	if rec := upload(t, s, "/api/learn", "video1-clean.mp4"); rec.Code != http.StatusOK {
		t.Fatalf("learn code = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := upload(t, s, "/api/verify", "video2-errors.mp4"); rec.Code != http.StatusOK {
		t.Fatalf("verify code = %d: %s", rec.Code, rec.Body.String())
	}

	if after := listNames(t, real); !reflect.DeepEqual(before, after) {
		t.Fatalf("real data/uploads changed: before=%q after=%q", before, after)
	}
	if after := listNames(t, realWf); !reflect.DeepEqual(beforeWf, after) {
		t.Fatalf("real data/workflows changed: before=%q after=%q", beforeWf, after)
	}
	if got := listNames(t, s.uploadsPath()); len(got) != 2 {
		t.Fatalf("temp uploads = %q, want the 2 test uploads redirected here", got)
	}
}

// TestVerifyLiveRequiresLearn: in live mode, verify without a prior learn
// fails with a clear error and never reaches the network (meta client is nil).
func TestVerifyLiveRequiresLearn(t *testing.T) {
	s := liveServerNoLearn(t)
	rec := upload(t, s, "/api/verify", "video2-errors.mp4")
	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body["error"], "Learn") {
		t.Fatalf("error %q does not name Learn", body["error"])
	}
}

// TestVerifyMockUsesFixture: mock mode keeps the old behavior — verify works
// without a prior learn by comparing against the demo fixture.
func TestVerifyMockUsesFixture(t *testing.T) {
	s := mockServer(t)
	if s.lastLearned != nil {
		t.Fatal("want no learned workflow")
	}
	rec := upload(t, s, "/api/verify", "video2-errors.mp4")
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body.String())
	}
	var rep verify.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Passed || len(rep.Findings) != 4 {
		t.Fatalf("report = %+v", rep)
	}
}

// TestUploadSanitizesBackslash: a backslash in the client filename must not
// survive into the stored name, or /videos/ would refuse to serve it.
func TestUploadSanitizesBackslash(t *testing.T) {
	s := mockServer(t)
	rec := upload(t, s, "/api/learn", `a\b.mp4`)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body.String())
	}
	var wf graph.Workflow
	if err := json.Unmarshal(rec.Body.Bytes(), &wf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(wf.SourceVideo, "\\") {
		t.Fatalf("stored name %q contains a backslash", wf.SourceVideo)
	}
	req := httptest.NewRequest(http.MethodGet, "/videos/"+wf.SourceVideo, nil)
	got := httptest.NewRecorder()
	s.routes().ServeHTTP(got, req)
	if got.Code != http.StatusOK {
		t.Fatalf("serve stored video: code = %d", got.Code)
	}
}

// TestVideoRejectsSymlink: a symlink planted in the uploads dir must never
// be served (resolveStoredFile uses Lstat, not Stat).
func TestVideoRejectsSymlink(t *testing.T) {
	t.Setenv("MOCK_API", "1")
	t.Setenv("META_API_KEY", "")
	dir := t.TempDir()
	s := NewServer(repoRoot(t))
	s.SetUploadsDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "real.mp4"), []byte("fake-video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.mp4", filepath.Join(dir, "link.mp4")); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/videos/link.mp4", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("symlink: code = %d, want 404", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/videos/real.mp4", nil)
	rec = httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("regular file: code = %d, want 200", rec.Code)
	}
}

// TestLearnRepairsInvalidWorkflow: when the model's first workflow reply is
// invalid, Learn attempts exactly one repair before succeeding (stub only,
// no live API).
func TestLearnRepairsInvalidWorkflow(t *testing.T) {
	ff, err := tracking.HasFFmpeg()
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	fp, err := tracking.HasFFprobe()
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	dir := t.TempDir()
	video := filepath.Join(dir, "clip.mp4")
	if err := tracking.MakeTestVideo(ctx, ff, video, 2); err != nil {
		t.Fatalf("make test video: %v", err)
	}
	goodWorkflow, err := os.ReadFile(filepath.Join(repoRoot(t), "testdata", "workflow.json"))
	if err != nil {
		t.Fatal(err)
	}
	grammar := "<1f>0<|box;x1=0;y1=0;x2=9;y2=9;w=10;h=10|><|mask;x=0;y=0;data=5,5,!!!!!x|>\n"
	chatHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/files":
			io.WriteString(w, `{"id":"file-repair","object":"file"}`)
		case "/responses":
			resp := map[string]any{
				"status": "completed",
				"output": []map[string]any{
					{"type": "message", "content": []map[string]any{
						{"type": "output_text", "text": grammar},
					}},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "/chat/completions":
			chatHits++
			content := "this is not json"
			if chatHits > 1 {
				content = string(goodWorkflow)
			}
			resp := map[string]any{
				"choices": []map[string]any{
					{"message": map[string]any{"role": "assistant", "content": content}},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("path = %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	mc := &meta.Client{BaseURL: srv.URL, APIKey: "test-key", MuseModel: "m", SamModel: "s", HTTP: srv.Client()}
	mc.SamConcurrency = 1
	s := &Server{root: repoRoot(t), mock: false, meta: mc, ffmpeg: ff, ffprobe: fp}
	s.SetUploadsDir(t.TempDir())
	s.SetWorkflowsDir(t.TempDir())
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("video", "clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	clip, err := os.ReadFile(video)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(clip); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/learn", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body.String())
	}
	var wf graph.Workflow
	if err := json.Unmarshal(rec.Body.Bytes(), &wf); err != nil {
		t.Fatal(err)
	}
	if wf.Name != "block-tower" {
		t.Fatalf("workflow = %q", wf.Name)
	}
	if chatHits != 2 {
		t.Fatalf("chat hits = %d, want exactly 2 (initial + one repair)", chatHits)
	}
}
