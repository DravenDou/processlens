package meta

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

func progressStub(t *testing.T, prompts []string) *httptest.Server {
	t.Helper()
	ids := map[string]int{}
	for i, p := range prompts {
		ids[p] = i
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		responsesOK(t, w, grammarFor(ids[responsesConcept(t, r)]))
	}))
}

// TestSegmentAllProgressSequential: the callback sees done 1..N in order,
// each with the real total.
func TestSegmentAllProgressSequential(t *testing.T) {
	prompts := []string{"red brick", "blue brick", "green brick"}
	srv := progressStub(t, prompts)
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "k", SamModel: "s", HTTP: srv.Client(), SamConcurrency: 1, RetryBase: time.Millisecond}
	var got []int
	texts, err := c.segmentAllProgress(context.Background(), "file-x", prompts, func(done, total int) {
		if total != len(prompts) {
			t.Errorf("total = %d, want %d", total, len(prompts))
		}
		got = append(got, done)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != len(prompts) {
		t.Fatalf("texts = %d, want %d", len(texts), len(prompts))
	}
	if !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("done sequence = %v, want 1..3", got)
	}
}

// TestSegmentAllProgressParallel: every completion is reported exactly once
// (order is completion order, so sort before comparing).
func TestSegmentAllProgressParallel(t *testing.T) {
	prompts := []string{"red brick", "blue brick", "green brick"}
	srv := progressStub(t, prompts)
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "k", SamModel: "s", HTTP: srv.Client(), SamConcurrency: 3, RetryBase: time.Millisecond}
	var mu sync.Mutex
	var got []int
	_, err := c.segmentAllProgress(context.Background(), "file-x", prompts, func(done, total int) {
		if total != len(prompts) {
			t.Errorf("total = %d, want %d", total, len(prompts))
		}
		mu.Lock()
		got = append(got, done)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Ints(got)
	if !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("done values = %v, want 1..3 exactly once", got)
	}
}
