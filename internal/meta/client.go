// Package meta is a client for the Meta Model API (OpenAI-compatible
// surface at https://api.meta.ai/v1). All ML runs hosted: this package
// never shells out to local models and never touches the GPU.
//
// SAM 3.1 flow (per Meta's meta-model-cookbook 07_sam/01_api_basics):
// upload the MP4 once to the Files API (purpose=user_data), then call
// POST /v1/responses once per concept with an input_video part, and parse
// the record grammar from the output text.
package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"processlens/internal/tracking"
)

// DefaultBaseURL is the Meta Model API endpoint.
const DefaultBaseURL = "https://api.meta.ai/v1"

// Env vars: META_API_KEY (required for live mode), META_BASE_URL,
// META_MUSE_MODEL, META_SAM_MODEL, META_SAM_CONCURRENCY (max parallel
// SegmentConcept calls, default 3, 1 = sequential), MOCK_API=1 to force mock.
const (
	EnvAPIKey         = "META_API_KEY"
	EnvBaseURL        = "META_BASE_URL"
	EnvMuseModel      = "META_MUSE_MODEL"
	EnvSamModel       = "META_SAM_MODEL"
	EnvMock           = "MOCK_API"
	EnvSamConcurrency = "META_SAM_CONCURRENCY"
)

// Defaults for SAM parallelism and HTTP retries.
const (
	DefaultSamConcurrency = 3
	DefaultMaxAttempts    = 3
	DefaultRetryBase      = 200 * time.Millisecond
)

// Client talks to the Meta Model API.
type Client struct {
	BaseURL   string
	APIKey    string
	MuseModel string
	SamModel  string
	HTTP      *http.Client
	// SamConcurrency bounds parallel SegmentConcept calls in Track.
	// <=0 resolves via SamConcurrencyFromEnv (env or default).
	SamConcurrency int
	// MaxAttempts is the total HTTP attempts per call (1 = no retry).
	// <=0 means DefaultMaxAttempts.
	MaxAttempts int
	// RetryBase is the base backoff between attempts (exponential + jitter).
	// <=0 means DefaultRetryBase.
	RetryBase time.Duration
}

// SamConcurrencyFromEnv parses META_SAM_CONCURRENCY: empty, invalid, or <1
// falls back to DefaultSamConcurrency; 1 means sequential.
func SamConcurrencyFromEnv() int {
	raw := strings.TrimSpace(os.Getenv(EnvSamConcurrency))
	if raw == "" {
		return DefaultSamConcurrency
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return DefaultSamConcurrency
	}
	return n
}

func (c *Client) samConcurrency() int {
	if c != nil && c.SamConcurrency > 0 {
		return c.SamConcurrency
	}
	return SamConcurrencyFromEnv()
}

func (c *Client) maxAttempts() int {
	if c != nil && c.MaxAttempts > 0 {
		return c.MaxAttempts
	}
	return DefaultMaxAttempts
}

func (c *Client) retryBase() time.Duration {
	if c != nil && c.RetryBase > 0 {
		return c.RetryBase
	}
	return DefaultRetryBase
}

// NewFromEnv builds a Client from the environment.
// ok=false means mock mode (no key or MOCK_API=1): callers must use fixtures.
func NewFromEnv() (c *Client, ok bool) {
	if os.Getenv(EnvMock) == "1" {
		return nil, false
	}
	key := strings.TrimSpace(os.Getenv(EnvAPIKey))
	if key == "" {
		return nil, false
	}
	base := strings.TrimSpace(os.Getenv(EnvBaseURL))
	if base == "" {
		base = DefaultBaseURL
	}
	muse := strings.TrimSpace(os.Getenv(EnvMuseModel))
	if muse == "" {
		muse = "muse-spark-1.3-contributor" // user default, confirmed visible via ListModels
	}
	sam := strings.TrimSpace(os.Getenv(EnvSamModel))
	if sam == "" {
		sam = "sam-3.1" // confirm exact id via ListModels
	}
	return &Client{
		BaseURL:        strings.TrimSuffix(base, "/"),
		APIKey:         key,
		MuseModel:      muse,
		SamModel:       sam,
		HTTP:           &http.Client{Timeout: 5 * time.Minute},
		SamConcurrency: SamConcurrencyFromEnv(),
	}, true
}

func (c *Client) auth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
}

// retryableStatus reports whether an HTTP status is worth retrying:
// 429 and 5xx only. Other 4xx (including 401/403 auth errors) fail fast.
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || (code >= 500 && code <= 599)
}

// backoff returns base*2^attempt plus uniform jitter in [0, base].
func backoff(base time.Duration, attempt int) time.Duration {
	d := base << attempt
	if d <= 0 {
		d = base
	}
	return d + time.Duration(rand.Int63n(int64(base)+1))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// do executes one logical HTTP call with limited retries for transient
// failures only (429, 5xx, timeouts/network errors): at most maxAttempts
// total attempts, then the last failure is returned. build must return a
// fresh request per attempt. Context cancellation is never retried.
func (c *Client) do(ctx context.Context, build func() (*http.Request, error), maxBody int64) (body []byte, status int, err error) {
	attempts := c.maxAttempts()
	base := c.retryBase()
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		req, err := build()
		if err != nil {
			return nil, 0, err
		}
		resp, err := c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			if attempt+1 >= attempts {
				return nil, 0, err
			}
			if err := sleepCtx(ctx, backoff(base, attempt)); err != nil {
				return nil, 0, err
			}
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		resp.Body.Close()
		if !retryableStatus(resp.StatusCode) || attempt+1 >= attempts {
			return body, resp.StatusCode, nil
		}
		if err := sleepCtx(ctx, backoff(base, attempt)); err != nil {
			return nil, 0, err
		}
	}
}

// ListModels returns the model ids visible to this key (GET /v1/models).
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	body, status, err := c.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/models", nil)
		if err != nil {
			return nil, err
		}
		c.auth(req)
		return req, nil
	}, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("list models: status %d: %s", status, short(body))
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("list models: decode: %w", err)
	}
	ids := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// ChatMessage is one OpenAI-style chat message.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Chat sends a system+user prompt to the Muse model and returns the text.
func (c *Client) Chat(ctx context.Context, system, user string) (string, error) {
	payload := map[string]any{
		"model": c.MuseModel,
		"messages": []ChatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body, status, err := c.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			c.BaseURL+"/chat/completions", bytes.NewReader(buf))
		if err != nil {
			return nil, err
		}
		c.auth(req)
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}, 8<<20)
	if err != nil {
		return "", fmt.Errorf("chat: %w", err)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("chat: status %d: %s", status, short(body))
	}
	var parsed struct {
		Choices []struct {
			Message ChatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("chat: decode: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("chat: empty choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

// ChatRepair re-asks the model exactly once to repair a reply that failed to
// parse, including the parse error and the bad output in the prompt. Callers
// own the parse/retry policy; this helper performs one extra Chat call.
func (c *Client) ChatRepair(ctx context.Context, system, user, badOutput string, parseErr error) (string, error) {
	why := "unknown parse error"
	if parseErr != nil {
		why = parseErr.Error()
	}
	fixed := user + "\n\nYour previous reply failed to parse (" + why + "). Previous reply:\n" + badOutput + "\nReply again with only the corrected output."
	out, err := c.Chat(ctx, system, fixed)
	if err != nil {
		return "", fmt.Errorf("chat repair: %w", err)
	}
	return out, nil
}

// UploadVideo uploads a local video to the Files API (purpose=user_data)
// and returns its file id.
func (c *Client) UploadVideo(ctx context.Context, videoPath string) (string, error) {
	video, err := os.Open(videoPath)
	if err != nil {
		return "", fmt.Errorf("upload video: %w", err)
	}
	defer video.Close()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("purpose", "user_data"); err != nil {
		return "", err
	}
	part, err := w.CreateFormFile("file", filepath.Base(videoPath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, video); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	form := buf.Bytes()
	contentType := w.FormDataContentType()
	body, status, err := c.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/files", bytes.NewReader(form))
		if err != nil {
			return nil, err
		}
		c.auth(req)
		req.Header.Set("Content-Type", contentType)
		return req, nil
	}, 1<<20)
	if err != nil {
		return "", fmt.Errorf("upload video: %w", err)
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return "", fmt.Errorf("upload video: status %d: %s", status, short(body))
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("upload video: decode: %w", err)
	}
	if !strings.HasPrefix(parsed.ID, "file-") {
		return "", fmt.Errorf("upload video: invalid file id %q", parsed.ID)
	}
	return parsed.ID, nil
}

// SegmentConcept runs hosted SAM 3.1 over an uploaded video for one short
// noun phrase and returns the raw output text (record grammar).
func (c *Client) SegmentConcept(ctx context.Context, fileID, concept string) (string, error) {
	payload := map[string]any{
		"model": c.SamModel,
		"input": []map[string]any{
			{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": concept},
					{"type": "input_video", "file_id": fileID},
				},
			},
		},
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body, status, err := c.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			c.BaseURL+"/responses", bytes.NewReader(buf))
		if err != nil {
			return nil, err
		}
		c.auth(req)
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}, 64<<20)
	if err != nil {
		return "", fmt.Errorf("segment %q: %w", concept, err)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("segment %q: status %d: %s", concept, status, short(body))
	}
	return responsesText(concept, body)
}

// responsesText extracts concatenated output_text from a Responses API object.
func responsesText(concept string, body []byte) (string, error) {
	var parsed struct {
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("segment %q: decode: %w", concept, err)
	}
	if parsed.Status != "" && parsed.Status != "completed" {
		return "", fmt.Errorf("segment %q: response status %q: %s", concept, parsed.Status, short(body))
	}
	var sb strings.Builder
	for _, item := range parsed.Output {
		if item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			if part.Type == "output_text" {
				sb.WriteString(part.Text)
			}
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("segment %q: empty output text: %s", concept, short(body))
	}
	return sb.String(), nil
}

// ProgressFunc receives real progress callbacks: stage names a real step,
// detail adds honest context, and done/total are real counts for countable
// stages (SAM concepts) or -1 elsewhere. It must be safe for concurrent
// use: concept completions arrive from worker goroutines.
type ProgressFunc func(stage, detail string, done, total int)

// Track runs hosted SAM 3.1 over a video: one upload, then one request per
// text prompt (up to META_SAM_CONCURRENCY in flight), merging the parsed
// boxes into per-frame tracks with normalized coordinates. Frame timestamps
// assume the source frame rate.
func (c *Client) Track(ctx context.Context, videoPath string, prompts []string) ([]tracking.FrameTracks, error) {
	return c.TrackWithProgress(ctx, videoPath, prompts, nil)
}

// TrackWithProgress is Track with real progress callbacks at each stage:
// probing metadata, uploading the video, and one countable update per
// finished SAM concept. A nil onProgress disables callbacks.
func (c *Client) TrackWithProgress(ctx context.Context, videoPath string, prompts []string, onProgress ProgressFunc) ([]tracking.FrameTracks, error) {
	if len(prompts) == 0 {
		return nil, fmt.Errorf("track: no prompts")
	}
	report := func(stage, detail string, done, total int) {
		if onProgress != nil {
			onProgress(stage, detail, done, total)
		}
	}
	report("probing metadata", "", -1, -1)
	fp, err := tracking.HasFFprobe()
	if err != nil {
		return nil, fmt.Errorf("track: %w", err)
	}
	info, err := tracking.ProbeVideo(ctx, fp, videoPath)
	if err != nil {
		return nil, fmt.Errorf("track: %w", err)
	}
	report("uploading video", "", -1, -1)
	fileID, err := c.UploadVideo(ctx, videoPath)
	if err != nil {
		return nil, err
	}
	texts, err := c.segmentAllProgress(ctx, fileID, prompts, func(done, total int) {
		report("segmenting concepts", fmt.Sprintf("%d of %d", done, total), done, total)
	})
	if err != nil {
		return nil, err
	}
	return mergeSegments(texts, prompts, info.FPS)
}

// segmentAll runs one SegmentConcept per prompt with bounded parallelism,
// returning raw output texts in prompt order. Concurrency 1 (or less) is a
// sequential fallback. Any per-concept error fails the whole call with the
// concept named (lowest prompt index wins); context cancellation stops
// feeding workers and returns the context error.
func (c *Client) segmentAll(ctx context.Context, fileID string, prompts []string) ([]string, error) {
	return c.segmentAllProgress(ctx, fileID, prompts, nil)
}

// segmentAllProgress is segmentAll with one onConcept callback per finished
// concept (done counts completions, total is len(prompts)). Completions
// arrive in prompt order sequentially, in completion order when parallel.
// A nil onConcept disables callbacks.
func (c *Client) segmentAllProgress(ctx context.Context, fileID string, prompts []string, onConcept func(done, total int)) ([]string, error) {
	texts := make([]string, len(prompts))
	total := len(prompts)
	n := c.samConcurrency()
	if n <= 1 {
		for i, concept := range prompts {
			text, err := c.SegmentConcept(ctx, fileID, concept)
			if err != nil {
				return nil, err
			}
			texts[i] = text
			if onConcept != nil {
				onConcept(i+1, total)
			}
		}
		return texts, nil
	}
	if n > len(prompts) {
		n = len(prompts)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sem := make(chan struct{}, n)
	errs := make([]error, len(prompts))
	var done atomic.Int32
	var wg sync.WaitGroup
	for i, concept := range prompts {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil, fmt.Errorf("track: %w", ctx.Err())
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(idx int, con string) {
			defer wg.Done()
			defer func() { <-sem }()
			text, err := c.SegmentConcept(ctx, fileID, con)
			if err != nil {
				errs[idx] = err
				return
			}
			texts[idx] = text
			if onConcept != nil {
				onConcept(int(done.Add(1)), total)
			}
		}(i, concept)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("track: %w", err)
	}
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return texts, nil
}

// mergeSegments parses one SAM output per prompt and merges the boxes into
// per-frame tracks, sorted by frame and track id as before.
func mergeSegments(texts []string, prompts []string, fps float64) ([]tracking.FrameTracks, error) {
	byFrame := map[int][]tracking.Detection{}
	totalSkipped := 0
	for i, concept := range prompts {
		boxes, skipped := tracking.ParseSamOutput(texts[i])
		totalSkipped += skipped
		for _, b := range boxes {
			w := b.Right - b.Left
			h := b.Bottom - b.Top
			if w < 0 {
				w = 0
			}
			if h < 0 {
				h = 0
			}
			det := tracking.Detection{
				TrackID:  concept + "-" + b.ObjectID,
				Label:    concept,
				Box:      tracking.Box{X: b.Left, Y: b.Top, W: w, H: h},
				TSeconds: float64(b.Frame) / fps,
			}
			byFrame[b.Frame] = append(byFrame[b.Frame], det)
		}
	}
	if len(byFrame) == 0 {
		return nil, fmt.Errorf("track: no boxes parsed (%d records skipped)", totalSkipped)
	}
	frames := make([]int, 0, len(byFrame))
	for f := range byFrame {
		frames = append(frames, f)
	}
	sort.Ints(frames)
	out := make([]tracking.FrameTracks, 0, len(frames))
	for _, f := range frames {
		dets := byFrame[f]
		sort.Slice(dets, func(i, j int) bool { return dets[i].TrackID < dets[j].TrackID })
		out = append(out, tracking.FrameTracks{
			Frame:      f,
			TSeconds:   float64(f) / fps,
			Detections: dets,
		})
	}
	return out, nil
}

func short(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 2000 {
		s = s[:2000] + "…"
	}
	return s
}
