package main

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"processlens/internal/graph"
	"processlens/internal/tracking"
	"processlens/internal/verify"
)

func requireFFmpeg(t *testing.T) string {
	t.Helper()
	ff, err := tracking.HasFFmpeg()
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	return ff
}

func requireFFprobe(t *testing.T) string {
	t.Helper()
	fp, err := tracking.HasFFprobe()
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	return fp
}

// makeTestClip renders a synthetic testsrc clip into dir. Callers pass
// t.TempDir() so nothing touches the repo.
func makeTestClip(t *testing.T, dir, name string, seconds int) string {
	t.Helper()
	ff := requireFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out := filepath.Join(dir, name)
	if err := tracking.MakeTestVideo(ctx, ff, out, seconds); err != nil {
		t.Fatalf("make test video: %v", err)
	}
	return out
}

func uploadData(t *testing.T, s *Server, route, filename string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("video", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
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

func learnName(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("learn code = %d: %s", rec.Code, rec.Body.String())
	}
	var wf graph.Workflow
	if err := json.Unmarshal(rec.Body.Bytes(), &wf); err != nil {
		t.Fatal(err)
	}
	return wf.SourceVideo
}

func verifyName(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("verify code = %d: %s", rec.Code, rec.Body.String())
	}
	var rep verify.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	return rep.SourceVideo
}

type tracksBody struct {
	Video     string                 `json:"video"`
	Available bool                   `json:"available"`
	Width     int                    `json:"width"`
	Height    int                    `json:"height"`
	FPS       float64                `json:"fps"`
	Frames    []tracking.FrameTracks `json:"frames"`
}

func getTracks(t *testing.T, s *Server, query string) (int, tracksBody) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/tracks"+query, nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	var body tracksBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode tracks: %v: %s", err, rec.Body.String())
		}
	}
	return rec.Code, body
}

// TestVideoServeAndRange uploads a synthetic testsrc clip through the real
// endpoint (so the file on disk is server-stored in TempDir) and proves full
// and ranged serving.
func TestVideoServeAndRange(t *testing.T) {
	s := mockServer(t)
	src := makeTestClip(t, t.TempDir(), "src.mp4", 2)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	name := learnName(t, uploadData(t, s, "/api/learn", "clip.mp4", data))

	req := httptest.NewRequest(http.MethodGet, "/videos/"+name, nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("full GET code = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("Content-Type = %q, want video/mp4", ct)
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatal("missing Accept-Ranges: bytes")
	}
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Fatal("full body differs from stored bytes")
	}

	req = httptest.NewRequest(http.MethodGet, "/videos/"+name, nil)
	req.Header.Set("Range", "bytes=0-99")
	rec = httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("range code = %d, want 206", rec.Code)
	}
	if cr := rec.Header().Get("Content-Range"); !strings.HasPrefix(cr, "bytes 0-99/") {
		t.Fatalf("Content-Range = %q", cr)
	}
	if !bytes.Equal(rec.Body.Bytes(), data[:100]) {
		t.Fatal("range body differs from bytes 0-99")
	}
}

// TestVideoTraversal rejects escapes, subdirs, dotfiles and missing files.
// Direct handler calls bypass ServeMux path cleaning so the whitelist itself
// is exercised.
func TestVideoTraversal(t *testing.T) {
	s := mockServer(t)
	paths := []string{
		"/videos/", "/videos/.", "/videos/..", "/videos/...",
		"/videos/../main.go", "/videos/../../etc/passwd",
		"/videos/sub/dir.mp4", "/videos//etc/passwd",
		"/videos/.hidden.mp4", `/videos/..\secret.mp4`,
		"/videos/missing.mp4",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, "/videos/", nil)
		req.URL.Path = p
		rec := httptest.NewRecorder()
		s.handleVideo(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s code = %d, want 404", p, rec.Code)
		}
	}
	// A file living outside the uploads dir is not servable by its basename.
	other := filepath.Join(t.TempDir(), "outside.mp4")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/videos/outside.mp4", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outside file code = %d, want 404", rec.Code)
	}
}

// TestTracksLearnMock checks the remembered learn bundle after a mock Learn.
func TestTracksLearnMock(t *testing.T) {
	s := mockServer(t)
	name := learnName(t, upload(t, s, "/api/learn", "video1-clean.mp4"))
	code, body := getTracks(t, s, "?video=learn")
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	if !body.Available || body.Video != name {
		t.Fatalf("bundle = %+v, want available video %q", body, name)
	}
	if len(body.Frames) != 6 {
		t.Fatalf("frames = %d, want 6 from tracks1.json", len(body.Frames))
	}
	if got := body.Frames[0].Detections[0].TrackID; got != "t-red" {
		t.Fatalf("first track = %q, want t-red", got)
	}
}

// TestTracksLearnMockFallback serves the fixture tracks before any Learn.
func TestTracksLearnMockFallback(t *testing.T) {
	s := mockServer(t)
	code, body := getTracks(t, s, "?video=learn")
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	if !body.Available || len(body.Frames) != 6 {
		t.Fatalf("fallback = available=%v frames=%d, want 6 fixture frames",
			body.Available, len(body.Frames))
	}
}

// TestTracksVerifyUnavailable proves mock mode never invents verify tracks.
func TestTracksVerifyUnavailable(t *testing.T) {
	s := mockServer(t)
	code, body := getTracks(t, s, "?video=verify")
	if code != http.StatusOK || body.Available || len(body.Frames) != 0 {
		t.Fatalf("fresh verify = code %d %+v, want unavailable+empty", code, body)
	}
	name := verifyName(t, upload(t, s, "/api/verify", "video2-errors.mp4"))
	code, body = getTracks(t, s, "?video=verify")
	if code != http.StatusOK {
		t.Fatalf("code = %d", code)
	}
	if body.Available || len(body.Frames) != 0 || body.Video != name {
		t.Fatalf("after verify = %+v, want unavailable marker for %q", body, name)
	}
}

func TestTracksBadParam(t *testing.T) {
	s := mockServer(t)
	for _, q := range []string{"", "?video=", "?video=foo", "?video=learnx"} {
		req := httptest.NewRequest(http.MethodGet, "/api/tracks"+q, nil)
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("query %q code = %d, want 400", q, rec.Code)
		}
	}
}

// TestTracksLearnRealDims learns a real synthetic clip via video_path and
// checks remembered dimensions (320x240 @ ~10fps testsrc).
func TestTracksLearnRealDims(t *testing.T) {
	requireFFprobe(t)
	s := mockServer(t)
	src := makeTestClip(t, t.TempDir(), "real.mp4", 2)
	payload, _ := json.Marshal(map[string]string{"video_path": src})
	req := httptest.NewRequest(http.MethodPost, "/api/learn", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	name := learnName(t, rec)
	if name != "real.mp4" {
		t.Fatalf("video = %q, want real.mp4", name)
	}
	code, body := getTracks(t, s, "?video=learn")
	if code != http.StatusOK || !body.Available {
		t.Fatalf("code = %d available = %v", code, body.Available)
	}
	if body.Width != 320 || body.Height != 240 {
		t.Fatalf("dims = %dx%d, want 320x240", body.Width, body.Height)
	}
	if body.FPS < 9.5 || body.FPS > 10.5 {
		t.Fatalf("fps = %v, want ~10", body.FPS)
	}
}

// TestFrameJPEG extracts a frame from a stored synthetic clip, asserting
// JPEG magic bytes and that the cache file lands in the injected temp dir.
func TestFrameJPEG(t *testing.T) {
	requireFFprobe(t)
	s := mockServer(t)
	cache := t.TempDir()
	s.SetFrameCacheDir(cache)
	src := makeTestClip(t, t.TempDir(), "src.mp4", 2)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	name := learnName(t, uploadData(t, s, "/api/learn", "clip.mp4", data))

	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet,
			"/api/frame?video="+name+"&t=0.5", nil)
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		return rec
	}
	rec := get()
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg", ct)
	}
	b := rec.Body.Bytes()
	if len(b) < 3 || b[0] != 0xFF || b[1] != 0xD8 || b[2] != 0xFF {
		t.Fatalf("first bytes = %x, want JPEG magic FFD8FF", b[:min(8, len(b))])
	}
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".jpg") {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("cache = %q, want one .jpg", names)
	}
	// Second request serves the cached file identically.
	if rec2 := get(); rec2.Code != http.StatusOK || !bytes.Equal(rec2.Body.Bytes(), b) {
		t.Fatalf("cached code = %d, bytes differ = %v", rec2.Code,
			!bytes.Equal(rec2.Body.Bytes(), b))
	}
}

func TestFrameRejects(t *testing.T) {
	requireFFprobe(t)
	s := mockServer(t)
	s.SetFrameCacheDir(t.TempDir())
	src := makeTestClip(t, t.TempDir(), "src.mp4", 2)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	name := learnName(t, uploadData(t, s, "/api/learn", "clip.mp4", data))

	bad := []string{
		"", "?video=" + name, "?t=0.5", // missing params
		"?video=" + name + "&t=-1", "?video=" + name + "&t=abc",
		"?video=" + name + "&t=nan", "?video=" + name + "&t=9999", // out of range
	}
	for _, q := range bad {
		req := httptest.NewRequest(http.MethodGet, "/api/frame"+q, nil)
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("query %q code = %d, want 400", q, rec.Code)
		}
	}
	missing := []string{
		"/api/frame?video=missing.mp4&t=0.5",
		"/api/frame?video=..%2Fmain.go&t=0.5",
		"/api/frame?video=sub%2Fdir.mp4&t=0.5",
	}
	for _, u := range missing {
		req := httptest.NewRequest(http.MethodGet, u, nil)
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s code = %d, want 404", u, rec.Code)
		}
	}
}

// TestFrameNeedsFFmpeg proves a clear 503 when the binaries are absent.
func TestFrameNeedsFFmpeg(t *testing.T) {
	s := mockServer(t)
	s.SetFrameCacheDir(t.TempDir())
	s.ffmpeg, s.ffprobe = "", ""
	rec := upload(t, s, "/api/learn", "clip.mp4")
	name := learnName(t, rec)
	req := httptest.NewRequest(http.MethodGet, "/api/frame?video="+name+"&t=0.5", nil)
	got := httptest.NewRecorder()
	s.routes().ServeHTTP(got, req)
	if got.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", got.Code)
	}
}

// TestFrameCacheDefaultOutsideRepo locks the production cache location to
// the OS temp dir without writing anything there.
func TestFrameCacheDefaultOutsideRepo(t *testing.T) {
	s := NewServer(repoRoot(t))
	want := filepath.Join(os.TempDir(), "processlens-frames")
	if got := s.frameCachePath(); got != want {
		t.Fatalf("default cache = %q, want %q", got, want)
	}
	rel, err := filepath.Rel(s.root, want)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		t.Fatalf("default cache %q sits inside repo root %q", want, s.root)
	}
}
