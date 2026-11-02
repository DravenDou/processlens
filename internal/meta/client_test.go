package meta

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"processlens/internal/tracking"
)

func TestNewFromEnvMock(t *testing.T) {
	t.Setenv(EnvMock, "1")
	t.Setenv(EnvAPIKey, "x")
	if _, ok := NewFromEnv(); ok {
		t.Fatal("MOCK_API=1 must force mock mode")
	}
}

func TestNewFromEnvLive(t *testing.T) {
	t.Setenv(EnvMock, "")
	t.Setenv(EnvAPIKey, "test-key")
	c, ok := NewFromEnv()
	if !ok {
		t.Fatal("key set must enable live mode")
	}
	if c.BaseURL != DefaultBaseURL || c.MuseModel == "" || c.SamModel == "" {
		t.Fatalf("bad defaults: %+v", c)
	}
}

func TestNewFromEnvNoKey(t *testing.T) {
	t.Setenv(EnvMock, "")
	t.Setenv(EnvAPIKey, "")
	if _, ok := NewFromEnv(); ok {
		t.Fatal("no key must mean mock mode")
	}
}

func TestListModelsStub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %s, want /models", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("missing bearer auth")
		}
		io.WriteString(w, `{"data":[{"id":"muse-spark-1.3"},{"id":"sam-3.1"}]}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
	ids, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "muse-spark-1.3" || ids[1] != "sam-3.1" {
		t.Fatalf("ids = %v", ids)
	}
}

func TestChatStub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var payload struct {
			Model    string        `json:"model"`
			Messages []ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		if payload.Model != "m" || len(payload.Messages) != 2 {
			t.Errorf("payload = %+v", payload)
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"{\"ok\":true}"}}]}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "k", MuseModel: "m", HTTP: srv.Client()}
	out, err := c.Chat(context.Background(), "sys", "user")
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"ok":true}` {
		t.Fatalf("out = %q", out)
	}
}

func TestTrackFilesAndResponses(t *testing.T) {
	ff, err := tracking.HasFFmpeg()
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := tracking.HasFFprobe(); err != nil {
		t.Skip("ffprobe not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	video := filepath.Join(t.TempDir(), "clip.mp4")
	if err := tracking.MakeTestVideo(ctx, ff, video, 2); err != nil {
		t.Fatalf("make test video: %v", err)
	}
	uploads := 0
	grammar := "<1f>0<|box;x1=0;y1=0;x2=9;y2=9;w=10;h=10|><|mask;x=0;y=0;data=5,5,!!!!!x|>\n" +
		"<3f>0<|box;x1=0;y1=0;x2=9;y2=9;w=10;h=10|><|mask;x=0;y=0;data=5,5,!!!!!x|>\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/files":
			uploads++
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				t.Errorf("multipart: %v", err)
				return
			}
			if r.FormValue("purpose") != "user_data" {
				t.Errorf("purpose = %q", r.FormValue("purpose"))
			}
			f, _, err := r.FormFile("file")
			if err != nil {
				t.Errorf("file part: %v", err)
				return
			}
			defer f.Close()
			io.WriteString(w, `{"id":"file-test1","object":"file"}`)
		case "/responses":
			var payload struct {
				Model string `json:"model"`
				Input []struct {
					Content []struct {
						Type   string `json:"type"`
						Text   string `json:"text"`
						FileID string `json:"file_id"`
					} `json:"content"`
				} `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode: %v", err)
				return
			}
			if payload.Model != "s" {
				t.Errorf("model = %q", payload.Model)
			}
			seenVideo := false
			for _, item := range payload.Input {
				for _, c := range item.Content {
					if c.Type == "input_video" && c.FileID == "file-test1" {
						seenVideo = true
					}
				}
			}
			if !seenVideo {
				t.Errorf("no input_video with file-test1")
			}
			resp := map[string]any{
				"status": "completed",
				"output": []map[string]any{
					{"type": "message", "content": []map[string]any{
						{"type": "output_text", "text": grammar},
					}},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			t.Errorf("path = %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "k", SamModel: "s", HTTP: srv.Client()}
	tracks, err := c.Track(ctx, video, []string{"red brick", "blue brick"})
	if err != nil {
		t.Fatal(err)
	}
	if uploads != 1 {
		t.Fatalf("uploads = %d, want 1", uploads)
	}
	if len(tracks) != 2 || tracks[0].Frame != 1 || tracks[1].Frame != 3 {
		t.Fatalf("tracks = %+v", tracks)
	}
	for _, fr := range tracks {
		if len(fr.Detections) != 2 {
			t.Fatalf("frame %d detections = %d", fr.Frame, len(fr.Detections))
		}
		if fr.Detections[0].TrackID != "blue brick-0" || fr.Detections[1].TrackID != "red brick-0" {
			t.Fatalf("track ids = %q, %q", fr.Detections[0].TrackID, fr.Detections[1].TrackID)
		}
		b := fr.Detections[0].Box
		if b.X != 0 || b.Y != 0 || b.W != 1 || b.H != 1 {
			t.Fatalf("box = %+v", b)
		}
	}
	// testsrc runs at 10 fps: frame 3 lands at ~0.3 s.
	if ts := tracks[1].TSeconds; ts < 0.28 || ts > 0.32 {
		t.Fatalf("t_seconds = %v", ts)
	}
}
