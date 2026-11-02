// Package tracking extracts video frames with ffmpeg (CPU only, no GPU)
// and defines the track types returned by hosted SAM 3.1.
package tracking

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Box is a normalized bounding box (0..1 coordinates).
type Box struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// Detection is one tracked object in one frame.
type Detection struct {
	TrackID  string  `json:"track_id"`
	Label    string  `json:"label"`
	Box      Box     `json:"box"`
	TSeconds float64 `json:"t_seconds"`
}

// FrameTracks holds all detections of one sampled frame.
type FrameTracks struct {
	Frame      int         `json:"frame"`
	TSeconds   float64     `json:"t_seconds"`
	Detections []Detection `json:"detections"`
}

// HasFFmpeg returns the ffmpeg binary path, or an error if missing.
func HasFFmpeg() (string, error) {
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", fmt.Errorf("ffmpeg not found in PATH: %w", err)
	}
	return p, nil
}

// ExtractFrames samples videoPath at fps frames/second into outDir as
// frame-%04d.jpg. CPU only: no hwaccel flags are ever passed.
func ExtractFrames(ctx context.Context, ffmpegPath, videoPath, outDir string, fps int) ([]string, error) {
	if fps < 1 || fps > 30 {
		return nil, fmt.Errorf("fps %d out of range 1..30", fps)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("create frames dir: %w", err)
	}
	pattern := filepath.Join(outDir, "frame-%04d.jpg")
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", videoPath,
		"-vf", fmt.Sprintf("fps=%d", fps),
		pattern,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg extract: %w: %s", err, string(out))
	}
	frames, err := filepath.Glob(filepath.Join(outDir, "frame-*.jpg"))
	if err != nil {
		return nil, err
	}
	sort.Strings(frames)
	if len(frames) == 0 {
		return nil, fmt.Errorf("ffmpeg produced no frames for %s", videoPath)
	}
	return frames, nil
}

// ExtractFrame renders a single frame at tSeconds as a JPEG.
// CPU only: no hwaccel flags are ever passed.
func ExtractFrame(ctx context.Context, ffmpegPath, videoPath, outPath string, tSeconds float64) error {
	if math.IsNaN(tSeconds) || math.IsInf(tSeconds, 0) || tSeconds < 0 {
		return fmt.Errorf("t_seconds %v out of range", tSeconds)
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("create frame dir: %w", err)
	}
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-hide_banner", "-loglevel", "error", "-y",
		"-ss", strconv.FormatFloat(tSeconds, 'f', 3, 64),
		"-i", videoPath,
		"-frames:v", "1",
		"-q:v", "3",
		outPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg frame: %w: %s", err, string(out))
	}
	return nil
}

// VideoInfo holds stream metadata from ffprobe.
type VideoInfo struct {
	Width    int
	Height   int
	FPS      float64
	Duration float64
}

// HasFFprobe returns the ffprobe binary path, or an error if missing.
func HasFFprobe() (string, error) {
	p, err := exec.LookPath("ffprobe")
	if err != nil {
		return "", fmt.Errorf("ffprobe not found in PATH: %w", err)
	}
	return p, nil
}

// ProbeVideo reads dimensions, frame rate and duration with ffprobe.
func ProbeVideo(ctx context.Context, ffprobePath, videoPath string) (VideoInfo, error) {
	var info VideoInfo
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height,avg_frame_rate,duration",
		"-of", "default=noprint_wrappers=1",
		videoPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return info, fmt.Errorf("ffprobe: %w", err)
	}
	kv := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			kv[k] = v
		}
	}
	if _, err := fmt.Sscan(kv["width"], &info.Width); err != nil || info.Width <= 0 {
		return info, fmt.Errorf("ffprobe: bad width %q", kv["width"])
	}
	if _, err := fmt.Sscan(kv["height"], &info.Height); err != nil || info.Height <= 0 {
		return info, fmt.Errorf("ffprobe: bad height %q", kv["height"])
	}
	num, den := 0, 0
	if _, err := fmt.Sscanf(kv["avg_frame_rate"], "%d/%d", &num, &den); err != nil || num <= 0 || den <= 0 {
		return info, fmt.Errorf("ffprobe: bad frame rate %q", kv["avg_frame_rate"])
	}
	info.FPS = float64(num) / float64(den)
	_, _ = fmt.Sscan(kv["duration"], &info.Duration)
	return info, nil
}

// MakeTestVideo renders a tiny synthetic clip (testsrc) for tests.
// CPU only.
func MakeTestVideo(ctx context.Context, ffmpegPath, outPath string, seconds int) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=size=320x240:rate=10:duration=%d", seconds),
		"-pix_fmt", "yuv420p",
		outPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg testsrc: %w: %s", err, string(out))
	}
	return nil
}
