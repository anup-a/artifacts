package apierr

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
)

func TestWriteEnvelope(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(WithRequestID(r.Context(), "req-1"))
	Write(w, r, NotFound("artifact"))
	if w.Code != 404 {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "NOT_FOUND" || body.Error.RequestID != "req-1" || body.Error.Message != "artifact not found" {
		t.Fatalf("%+v", body)
	}
}

func TestUnknownErrorIsInternal(t *testing.T) {
	w := httptest.NewRecorder()
	Write(w, httptest.NewRequest("GET", "/", nil), errors.New("boom"))
	if w.Code != 500 || !contains(w.Body.String(), `"INTERNAL"`) || contains(w.Body.String(), "boom") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestRateLimitedSetsRetryAfter(t *testing.T) {
	w := httptest.NewRecorder()
	Write(w, httptest.NewRequest("GET", "/", nil), RateLimited(7))
	if w.Header().Get("Retry-After") != "7" || w.Code != 429 {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (func() bool { for i := 0; i+len(sub) <= len(s); i++ { if s[i:i+len(sub)] == sub { return true } }; return false })() }
