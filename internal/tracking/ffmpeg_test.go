package tracking

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractFramesBadFPS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := ExtractFrames(ctx, "ffmpeg", "x.mp4", t.TempDir(), 0); err == nil {
		t.Fatal("want error for fps=0")
	}
}

func TestExtractFramesRoundTrip(t *testing.T) {
	ff, err := HasFFmpeg()
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := t.TempDir()
	video := filepath.Join(dir, "in.mp4")
	if err := MakeTestVideo(ctx, ff, video, 2); err != nil {
		t.Fatalf("make test video: %v", err)
	}
	frames, err := ExtractFrames(ctx, ff, video, filepath.Join(dir, "frames"), 2)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(frames) < 2 {
		t.Fatalf("want >=2 frames, got %d", len(frames))
	}
	for _, f := range frames {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("frame missing: %v", err)
		}
	}
}

func TestProbeVideo(t *testing.T) {
	ff, err := HasFFmpeg()
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	fp, err := HasFFprobe()
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	video := filepath.Join(t.TempDir(), "in.mp4")
	if err := MakeTestVideo(ctx, ff, video, 2); err != nil {
		t.Fatalf("make test video: %v", err)
	}
	info, err := ProbeVideo(ctx, fp, video)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if info.Width != 320 || info.Height != 240 {
		t.Fatalf("dims = %dx%d", info.Width, info.Height)
	}
	if info.FPS < 9.5 || info.FPS > 10.5 || info.Duration < 1.5 || info.Duration > 2.5 {
		t.Fatalf("info = %+v", info)
	}
}
