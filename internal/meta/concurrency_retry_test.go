package meta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"processlens/internal/tracking"
)

// grammarFor returns a one-record SAM grammar line with the given object id.
func grammarFor(id int) string {
	return fmt.Sprintf("<1f>%d<|box;x1=0;y1=0;x2=9;y2=9;w=10;h=10|><|mask;x=0;y=0;data=5,5,!!!!!x|>\n", id)
}

func responsesOK(t *testing.T, w http.ResponseWriter, text string) {
	t.Helper()
	resp := map[string]any{
		"status": "completed",
		"output": []map[string]any{
			{"type": "message", "content": []map[string]any{
				{"type": "output_text", "text": text},
			}},
		},
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		t.Errorf("encode responses: %v", err)
	}
}

// responsesConcept extracts the input_text concept from a /responses payload.
func responsesConcept(t *testing.T, r *http.Request) string {
	t.Helper()
	var payload struct {
		Input []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		t.Errorf("decode responses payload: %v", err)
		return ""
	}
	for _, item := range payload.Input {
		for _, c := range item.Content {
			if c.Type == "input_text" {
				return c.Text
			}
		}
	}
	return ""
}

func chatOK(t *testing.T, w http.ResponseWriter, content string) {
	t.Helper()
	resp := map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"role": "assistant", "content": content}},
		},
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		t.Errorf("encode chat: %v", err)
	}
}

func TestSamConcurrencyFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		want int
	}{
		{"unset", "", DefaultSamConcurrency},
		{"one", "1", 1},
		{"five", "5", 5},
		{"zero", "0", DefaultSamConcurrency},
		{"negative", "-2", DefaultSamConcurrency},
		{"garbage", "abc", DefaultSamConcurrency},
		{"spaces", " 2 ", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvSamConcurrency, tc.env)
			if got := SamConcurrencyFromEnv(); got != tc.want {
				t.Fatalf("SamConcurrencyFromEnv() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestNewFromEnvSamConcurrency(t *testing.T) {
	t.Setenv(EnvMock, "")
	t.Setenv(EnvAPIKey, "test-key")
	t.Setenv(EnvSamConcurrency, "7")
	c, ok := NewFromEnv()
	if !ok {
		t.Fatal("key set must enable live mode")
	}
	if c.SamConcurrency != 7 {
		t.Fatalf("SamConcurrency = %d, want 7", c.SamConcurrency)
	}
}

func TestSegmentAllOverlapAndOrder(t *testing.T) {
	prompts := []string{"red brick", "blue brick", "green brick"}
	ids := map[string]int{"red brick": 0, "blue brick": 1, "green brick": 2}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond) // stub latency per concept
		responsesOK(t, w, grammarFor(ids[responsesConcept(t, r)]))
	}))
	defer srv.Close()

	par := &Client{BaseURL: srv.URL, APIKey: "k", SamModel: "s", HTTP: srv.Client(), SamConcurrency: 3, RetryBase: time.Millisecond}
	start := time.Now()
	parTexts, err := par.segmentAll(context.Background(), "file-x", prompts)
	parElapsed := time.Since(start)
	if err != nil {
		t.Fatalf("parallel segmentAll: %v", err)
	}
	// Sequential floor is 900ms of pure stub sleep; parallel must be well under.
	if parElapsed >= 800*time.Millisecond {
		t.Fatalf("parallel 3 concepts took %v, want well under 900ms", parElapsed)
	}

	seq := &Client{BaseURL: srv.URL, APIKey: "k", SamModel: "s", HTTP: srv.Client(), SamConcurrency: 1, RetryBase: time.Millisecond}
	start = time.Now()
	seqTexts, err := seq.segmentAll(context.Background(), "file-x", prompts)
	seqElapsed := time.Since(start)
	if err != nil {
		t.Fatalf("sequential segmentAll: %v", err)
	}
	if seqElapsed < 850*time.Millisecond {
		t.Fatalf("sequential 3 concepts took only %v, stub sleep sanity check failed", seqElapsed)
	}

	if !reflect.DeepEqual(parTexts, seqTexts) {
		t.Fatalf("parallel output differs from sequential:\n%q\n%q", parTexts, seqTexts)
	}
	for i := range prompts {
		if want := fmt.Sprintf(">%d<|box", i); !strings.Contains(parTexts[i], want) {
			t.Fatalf("texts[%d] missing %q (order not preserved): %q", i, want, parTexts[i])
		}
	}
	parMerged, err := mergeSegments(parTexts, prompts, 10)
	if err != nil {
		t.Fatal(err)
	}
	seqMerged, err := mergeSegments(seqTexts, prompts, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parMerged, seqMerged) {
		t.Fatalf("merged parallel output differs from sequential")
	}
	t.Logf("parallel=%v sequential=%v", parElapsed.Round(time.Millisecond), seqElapsed.Round(time.Millisecond))
}

func TestSegmentAllCancel(t *testing.T) {
	prompts := []string{"red brick", "blue brick", "green brick"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Second):
			responsesOK(t, w, grammarFor(0))
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "k", SamModel: "s", HTTP: srv.Client(), SamConcurrency: 3, RetryBase: time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.segmentAll(ctx, "file-x", prompts)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context deadline exceeded", err)
	}
	// Full parallel work would take 2s; cancellation must stop it early.
	if elapsed >= 1500*time.Millisecond {
		t.Fatalf("canceled run took %v, want early stop", elapsed)
	}
}

func TestSegmentAllErrorNamesConcept(t *testing.T) {
	prompts := []string{"red brick", "bad brick", "blue brick"}
	var hits atomic.Int64
	var badHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if responsesConcept(t, r) == "bad brick" {
			badHits.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"unknown concept"}`)
			return
		}
		responsesOK(t, w, grammarFor(0))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "k", SamModel: "s", HTTP: srv.Client(), SamConcurrency: 3, RetryBase: time.Millisecond}
	_, err := c.segmentAll(context.Background(), "file-x", prompts)
	if err == nil || !strings.Contains(err.Error(), "bad brick") {
		t.Fatalf("err = %v, want failure naming bad brick", err)
	}
	if badHits.Load() != 1 {
		t.Fatalf("bad-concept hits = %d, want 1 (400 must not retry)", badHits.Load())
	}
	if hits.Load() != int64(len(prompts)) {
		t.Fatalf("total hits = %d, want %d", hits.Load(), len(prompts))
	}
}

func TestChatRetryFailTwiceThenSucceed(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"error":"boom"}`)
			return
		}
		chatOK(t, w, `{"ok":true}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "k", MuseModel: "m", HTTP: srv.Client(), RetryBase: time.Millisecond}
	out, err := c.Chat(context.Background(), "sys", "user")
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"ok":true}` {
		t.Fatalf("out = %q", out)
	}
	if hits.Load() != 3 {
		t.Fatalf("hits = %d, want 3", hits.Load())
	}
}

func TestChatRetry429ThenSucceed(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":"slow down"}`)
			return
		}
		chatOK(t, w, "hi")
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "k", MuseModel: "m", HTTP: srv.Client(), RetryBase: time.Millisecond}
	out, err := c.Chat(context.Background(), "sys", "user")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hi" || hits.Load() != 2 {
		t.Fatalf("out=%q hits=%d, want hi/2", out, hits.Load())
	}
}

func TestChatRetryExhausted(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, `{"error":"down"}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "k", MuseModel: "m", HTTP: srv.Client(), RetryBase: time.Millisecond}
	_, err := c.Chat(context.Background(), "sys", "user")
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err = %v, want status 502", err)
	}
	if hits.Load() != 3 {
		t.Fatalf("hits = %d, want exactly 3 attempts", hits.Load())
	}
}

func TestChatNoRetryOnClientError(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var hits atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(code)
				io.WriteString(w, `{"error":"nope"}`)
			}))
			defer srv.Close()
			c := &Client{BaseURL: srv.URL, APIKey: "k", MuseModel: "m", HTTP: srv.Client(), RetryBase: time.Millisecond}
			_, err := c.Chat(context.Background(), "sys", "user")
			if err == nil || !strings.Contains(err.Error(), fmt.Sprint(code)) {
				t.Fatalf("err = %v, want status %d", err, code)
			}
			if hits.Load() != 1 {
				t.Fatalf("hits = %d, want 1 (no retry on %d)", hits.Load(), code)
			}
		})
	}
}

func TestChatRetryTimeout(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			time.Sleep(200 * time.Millisecond) // exceeds client timeout
		}
		chatOK(t, w, "slow hi")
	}))
	defer srv.Close()
	httpClient := srv.Client()
	httpClient.Timeout = 50 * time.Millisecond
	c := &Client{BaseURL: srv.URL, APIKey: "k", MuseModel: "m", HTTP: httpClient, RetryBase: time.Millisecond}
	out, err := c.Chat(context.Background(), "sys", "user")
	if err != nil {
		t.Fatal(err)
	}
	if out != "slow hi" {
		t.Fatalf("out = %q", out)
	}
	if hits.Load() != 3 {
		t.Fatalf("hits = %d, want 3", hits.Load())
	}
}

func TestChatRepair(t *testing.T) {
	var hits atomic.Int64
	var gotUser atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		var payload struct {
			Messages []ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		for _, m := range payload.Messages {
			if m.Role == "user" {
				gotUser.Store(m.Content)
			}
		}
		chatOK(t, w, `{"fixed":true}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "k", MuseModel: "m", HTTP: srv.Client(), RetryBase: time.Millisecond}
	parseErr := errors.New("unexpected end of JSON input")
	bad := `{"a":1,`
	out, err := c.ChatRepair(context.Background(), "sys", "make a workflow", bad, parseErr)
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"fixed":true}` {
		t.Fatalf("out = %q", out)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want exactly 1 repair call", hits.Load())
	}
	user, _ := gotUser.Load().(string)
	if !strings.Contains(user, parseErr.Error()) || !strings.Contains(user, bad) {
		t.Fatalf("repair prompt missing parse error or bad output: %q", user)
	}
}

func TestTrackParallelMatchesSequential(t *testing.T) {
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
	prompts := []string{"red brick", "blue brick", "green brick"}
	ids := map[string]int{"red brick": 0, "blue brick": 1, "green brick": 2}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/files":
			io.WriteString(w, `{"id":"file-test1","object":"file"}`)
		case "/responses":
			responsesOK(t, w, grammarFor(ids[responsesConcept(t, r)]))
		default:
			t.Errorf("path = %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	seq := &Client{BaseURL: srv.URL, APIKey: "k", SamModel: "s", HTTP: srv.Client(), SamConcurrency: 1, RetryBase: time.Millisecond}
	seqTracks, err := seq.Track(ctx, video, prompts)
	if err != nil {
		t.Fatalf("sequential Track: %v", err)
	}
	par := &Client{BaseURL: srv.URL, APIKey: "k", SamModel: "s", HTTP: srv.Client(), SamConcurrency: 3, RetryBase: time.Millisecond}
	parTracks, err := par.Track(ctx, video, prompts)
	if err != nil {
		t.Fatalf("parallel Track: %v", err)
	}
	if !reflect.DeepEqual(parTracks, seqTracks) {
		t.Fatalf("parallel Track output differs from sequential:\n%+v\n%+v", parTracks, seqTracks)
	}
	if len(parTracks) == 0 {
		t.Fatal("no tracks merged")
	}
}
