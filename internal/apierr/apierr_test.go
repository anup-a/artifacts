package apierr

import (
	"encoding/json"
	"errors"
	"fmt"
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

func TestConstructorsCarryCodeAndStatus(t *testing.T) {
	cause := errors.New("db down")
	cases := []struct {
		err    *Error
		code   string
		status int
	}{
		{Invalid("bad"), "INVALID_ARGUMENT", 400},
		{Unauthorized(), "UNAUTHORIZED", 401},
		{Forbidden("no"), "FORBIDDEN", 403},
		{NotFound("thing"), "NOT_FOUND", 404},
		{Gone(), "GONE", 410},
		{TooLarge("big"), "PAYLOAD_TOO_LARGE", 413},
		{RateLimited(3), "RATE_LIMITED", 429},
		{Quota("full"), "QUOTA_EXCEEDED", 429},
		{Unavailable("later"), "UNAVAILABLE", 503},
		{Internal(cause), "INTERNAL", 500},
	}
	for _, c := range cases {
		if c.err.Code != c.code || c.err.Status != c.status {
			t.Fatalf("%+v", c.err)
		}
		if !contains(c.err.Error(), c.code+": ") {
			t.Fatalf("Error(): %s", c.err.Error())
		}
	}
	if !errors.Is(Internal(cause), cause) {
		t.Fatal("Internal does not unwrap to its cause")
	}
	if Invalid("x").Unwrap() != nil {
		t.Fatal("plain errors have no cause")
	}
}

func TestWriteMapsWrappedErrors(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	Write(w, r, fmt.Errorf("handler: %w", Forbidden("not yours")))
	if w.Code != 403 || !contains(w.Body.String(), `"FORBIDDEN"`) || !contains(w.Body.String(), "not yours") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if RequestID(r.Context()) != "" {
		t.Fatal("request id without middleware should be empty")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
