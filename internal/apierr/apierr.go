// Package apierr defines the API error envelope and typed error constructors.
package apierr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
)

type Error struct {
	Code       string
	Status     int
	Message    string
	RetryAfter int
	cause      error
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }
func (e *Error) Unwrap() error { return e.cause }

func Invalid(msg string) *Error     { return &Error{"INVALID_ARGUMENT", 400, msg, 0, nil} }
func Unauthorized() *Error          { return &Error{"UNAUTHORIZED", 401, "missing or invalid API key", 0, nil} }
func Forbidden(msg string) *Error   { return &Error{"FORBIDDEN", 403, msg, 0, nil} }
func Unavailable(msg string) *Error { return &Error{"UNAVAILABLE", 503, msg, 0, nil} }
func NotFound(what string) *Error   { return &Error{"NOT_FOUND", 404, what + " not found", 0, nil} }
func Gone() *Error                  { return &Error{"GONE", 410, "artifact deleted", 0, nil} }
func TooLarge(msg string) *Error    { return &Error{"PAYLOAD_TOO_LARGE", 413, msg, 0, nil} }
func RateLimited(sec int) *Error    { return &Error{"RATE_LIMITED", 429, "rate limited", sec, nil} }
func Quota(msg string) *Error       { return &Error{"QUOTA_EXCEEDED", 429, msg, 0, nil} }
func Internal(err error) *Error     { return &Error{"INTERNAL", 500, "internal error", 0, err} }

type ctxKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

type envelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

// Write maps err to the envelope. Non-*Error values become INTERNAL and are logged.
func Write(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = Internal(err)
	}
	if e.Status == 500 {
		slog.Error("internal error", "request_id", RequestID(r.Context()), "err", fmt.Sprint(err))
	}
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	var env envelope
	env.Error.Code, env.Error.Message, env.Error.RequestID = e.Code, e.Message, RequestID(r.Context())
	WriteJSON(w, e.Status, env)
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
