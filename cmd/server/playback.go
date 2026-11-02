// Evidence playback: serve stored videos, expose remembered tracks, and
// extract single-frame JPEGs. All handlers are stdlib-only.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"processlens/internal/tracking"
)

// errVideoNotFound covers both unknown names and rejected ones: callers must
// not learn which it was.
var errVideoNotFound = errors.New("video not found")

// trackSet is the playback bundle for one video: the FrameTracks plus the
// identity and dimensions the frontend needs to draw overlays.
type trackSet struct {
	Video     string
	Available bool
	Width     int
	Height    int
	FPS       float64
	Frames    []tracking.FrameTracks
}

// tracksJSON is the GET /api/tracks response shape.
type tracksJSON struct {
	Video     string                 `json:"video"`
	Available bool                   `json:"available"`
	Width     int                    `json:"width"`
	Height    int                    `json:"height"`
	FPS       float64                `json:"fps"`
	Frames    []tracking.FrameTracks `json:"frames"`
}

func (ts *trackSet) toJSON() tracksJSON {
	frames := ts.Frames
	if frames == nil {
		frames = []tracking.FrameTracks{}
	}
	return tracksJSON{
		Video:     ts.Video,
		Available: ts.Available,
		Width:     ts.Width,
		Height:    ts.Height,
		FPS:       ts.FPS,
		Frames:    frames,
	}
}

// unavailableTracks is the explicit empty marker for video-without-data.
// It never invents tracks.
func unavailableTracks(video string) tracksJSON {
	return tracksJSON{Video: video, Frames: []tracking.FrameTracks{}}
}

// resolveStoredFile whitelists a /videos/ or /api/frame name to a regular
// file directly inside the uploads dir. Anything else (traversal, absolute
// paths, subdirs, dotfiles, symlinks, missing files) is errVideoNotFound.
// Lstat (not Stat) ensures a planted symlink is never followed.
func (s *Server) resolveStoredFile(name string) (string, error) {
	if name == "" || name == "." || name == ".." ||
		strings.HasPrefix(name, ".") ||
		name != filepath.Base(name) ||
		strings.Contains(name, `\`) {
		return "", errVideoNotFound
	}
	dir := s.uploadsPath()
	p := filepath.Join(dir, name)
	if filepath.Dir(p) != dir {
		return "", errVideoNotFound
	}
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return "", errVideoNotFound
	}
	return p, nil
}

// handleVideo serves a stored upload with MP4 content type and byte-range
// support (ServeContent answers Range requests with 206 + Content-Range).
func (s *Server) handleVideo(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/videos/")
	p, err := s.resolveStoredFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

// handleTracks returns the remembered tracks for the last learn or verify.
func (s *Server) handleTracks(w http.ResponseWriter, r *http.Request) {
	switch which := r.URL.Query().Get("video"); which {
	case "learn":
		ts := s.learnTracks()
		if ts == nil {
			if !s.mock {
				writeJSON(w, http.StatusOK, unavailableTracks(""))
				return
			}
			// Mock fallback mirrors currentWorkflow: demo tracks until a
			// real Learn lands.
			ts = s.mockLearnTracks(r.Context(), "", "")
		}
		writeJSON(w, http.StatusOK, ts.toJSON())
	case "verify":
		ts := s.verifyTracks()
		if ts == nil {
			writeJSON(w, http.StatusOK, unavailableTracks(""))
			return
		}
		writeJSON(w, http.StatusOK, ts.toJSON())
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("query video must be learn or verify"))
	}
}

// mockLearnTracks builds the mock-mode learn bundle from the tracks fixture,
// probing the stored upload for dimensions on a best-effort basis (fake
// test bytes simply yield zero dimensions).
func (s *Server) mockLearnTracks(ctx context.Context, path, name string) *trackSet {
	ts := &trackSet{Video: name, Frames: []tracking.FrameTracks{}}
	b, err := s.fixture("tracks1.json")
	if err != nil {
		return ts
	}
	var frames []tracking.FrameTracks
	if err := json.Unmarshal(b, &frames); err != nil {
		return ts
	}
	ts.Frames = frames
	ts.Available = true
	if path != "" {
		ts.Width, ts.Height, ts.FPS = s.probeDims(ctx, path)
	}
	return ts
}

// probeDims reads video dimensions best-effort; zeros mean unknown.
func (s *Server) probeDims(ctx context.Context, path string) (int, int, float64) {
	if s.ffprobe == "" || path == "" {
		return 0, 0, 0
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	info, err := tracking.ProbeVideo(ctx, s.ffprobe, path)
	if err != nil {
		return 0, 0, 0
	}
	return info.Width, info.Height, info.FPS
}

// frameCachePath is where extracted JPEGs live: the OS temp dir in
// production, an injectable dir in tests. Never the repo.
func (s *Server) frameCachePath() string {
	if s.frameCacheDir != "" {
		return s.frameCacheDir
	}
	return filepath.Join(os.TempDir(), "processlens-frames")
}

// SetFrameCacheDir overrides the JPEG cache dir. Tests point it at
// t.TempDir().
func (s *Server) SetFrameCacheDir(dir string) {
	s.frameCacheDir = dir
}

// frameCacheKey derives a stable cache filename from the video identity,
// its size/mtime (so re-uploads never read stale frames), and t.
func frameCacheKey(name string, fi os.FileInfo, tsec float64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%.3f",
		name, fi.Size(), fi.ModTime().UnixNano(), tsec)))
	return hex.EncodeToString(sum[:]) + ".jpg"
}

// handleFrame extracts one JPEG frame at ?t= seconds from a stored video,
// caching it under the OS temp dir.
func (s *Server) handleFrame(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name, tstr := q.Get("video"), q.Get("t")
	if name == "" || tstr == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("query video and t are required"))
		return
	}
	tsec, err := strconv.ParseFloat(strings.TrimSpace(tstr), 64)
	if err != nil || math.IsNaN(tsec) || math.IsInf(tsec, 0) || tsec < 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid t=%q: must be seconds >= 0", tstr))
		return
	}
	p, err := s.resolveStoredFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if s.ffmpeg == "" || s.ffprobe == "" {
		writeErr(w, http.StatusServiceUnavailable, fmt.Errorf("frame extraction needs ffmpeg and ffprobe in PATH"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	info, err := tracking.ProbeVideo(ctx, s.ffprobe, p)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, fmt.Errorf("cannot probe video: %w", err))
		return
	}
	if info.Duration > 0 && tsec > info.Duration {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("t=%.3f out of range: duration %.3fs", tsec, info.Duration))
		return
	}
	fi, err := os.Stat(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	dir := s.frameCachePath()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := filepath.Join(dir, frameCacheKey(name, fi, tsec))
	if _, err := os.Stat(out); err != nil {
		if err := tracking.ExtractFrame(ctx, s.ffmpeg, p, out, tsec); err != nil {
			writeErr(w, http.StatusInternalServerError, fmt.Errorf("extract frame: %w", err))
			return
		}
	}
	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, out)
}
