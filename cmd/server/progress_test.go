package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func progressGet(t *testing.T, s *Server, query string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/progress"+query, nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode progress: %v (%q)", err, rec.Body.String())
	}
	return rec.Code, body
}

// TestProgressIdleShape locks the endpoint shape: exactly the four keys,
// with -1 counts for the uncountable idle stage.
func TestProgressIdleShape(t *testing.T) {
	s := mockServer(t)
	for _, op := range []string{"learn", "verify"} {
		code, body := progressGet(t, s, "?op="+op)
		if code != http.StatusOK {
			t.Fatalf("op=%s code = %d", op, code)
		}
		if len(body) != 4 {
			t.Fatalf("op=%s keys = %v, want exactly stage/detail/done/total", op, body)
		}
		if body["stage"] != "idle" || body["detail"] != "" ||
			body["done"] != float64(-1) || body["total"] != float64(-1) {
			t.Fatalf("op=%s body = %v", op, body)
		}
	}
}

func TestProgressUnknownOp(t *testing.T) {
	s := mockServer(t)
	for _, q := range []string{"?op=nope", "?op=", ""} {
		req := httptest.NewRequest(http.MethodGet, "/api/progress"+q, nil)
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("query %q code = %d, want 400", q, rec.Code)
		}
	}
}

// TestProgressMockLearnTransitions: the stages a Learn sets are observable
// through the endpoint, and a full mock Learn lands on the honest final
// stage without disturbing the verify record.
func TestProgressMockLearnTransitions(t *testing.T) {
	s := mockServer(t)
	s.setProgress("learn", "video received", "video1-clean.mp4", -1, -1)
	if _, body := progressGet(t, s, "?op=learn"); body["stage"] != "video received" ||
		body["detail"] != "video1-clean.mp4" {
		t.Fatalf("body = %v", body)
	}
	s.setProgress("learn", "segmenting concepts", "2 of 6", 2, 6)
	if _, body := progressGet(t, s, "?op=learn"); body["done"] != float64(2) ||
		body["total"] != float64(6) {
		t.Fatalf("body = %v", body)
	}
	rec := upload(t, s, "/api/learn", "video1-clean.mp4")
	if rec.Code != http.StatusOK {
		t.Fatalf("learn code = %d: %s", rec.Code, rec.Body.String())
	}
	if _, body := progressGet(t, s, "?op=learn"); body["stage"] != "workflow done" ||
		body["detail"] != "6 steps" {
		t.Fatalf("body = %v", body)
	}
	if _, body := progressGet(t, s, "?op=verify"); body["stage"] != "idle" {
		t.Fatalf("verify body = %v", body)
	}
}

func TestProgressMockVerifyDone(t *testing.T) {
	s := mockServer(t)
	rec := upload(t, s, "/api/verify", "video2-errors.mp4")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify code = %d: %s", rec.Code, rec.Body.String())
	}
	if _, body := progressGet(t, s, "?op=verify"); body["stage"] != "report done" ||
		body["detail"] != "4 findings" {
		t.Fatalf("body = %v", body)
	}
}
