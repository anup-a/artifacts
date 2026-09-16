package store

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

func ArtifactCursor(a Artifact) string {
	return base64.RawURLEncoding.EncodeToString([]byte(a.UpdatedAt.UTC().Format(time.RFC3339Nano) + "|" + a.ID))
}
func ParseArtifactCursor(raw string) (time.Time, string, error) {
	if raw == "" {
		return time.Time{}, "", nil
	}
	// Existing API clients may still send a v1 timestamp cursor.
	if at, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return at, "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, "", err
	}
	ts, id, ok := strings.Cut(string(decoded), "|")
	if !ok || id == "" {
		return time.Time{}, "", errors.New("invalid cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	return at, id, err
}
