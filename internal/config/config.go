// Package config reads server configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
)

type Limits struct {
	VersionBytes       int64
	Files              int
	VersionsKept       int
	PublishPerMin      int
	ArtifactsPerTenant int
	CommentBytes       int
	CommentsPerMin     int
	AnchorBytes        int
}

type Config struct {
	Addr               string
	DB                 string // sqlite | postgres
	DatabaseURL        string
	Storage            string // local | s3
	DataDir            string
	S3Endpoint         string
	S3Bucket           string
	S3AccessKey        string
	S3SecretKey        string
	S3UseSSL           bool
	AdminToken         string
	KeySecret          string
	ShareOrigin        string
	APIOrigin          string
	GitHubClientID     string
	GitHubClientSecret string
	EmailProvider      string
	ResendAPIKey       string
	EmailFrom          string
	// GoogleAnalyticsID is the GA4 Measurement ID for first-party app
	// surfaces (landing, login, /app/*). Empty disables the gtag snippet
	// and keeps CSP free of Google Analytics hosts. Prefer
	// GOOGLE_ANALYTICS_ID; GA_MEASUREMENT_ID is accepted as an alias.
	GoogleAnalyticsID string
	// TrustProxyHeaders makes rate limiting read CF-Connecting-IP /
	// X-Forwarded-For. Enable it only when the server is reachable exclusively
	// through a proxy that overwrites those headers; otherwise a client can
	// spoof them and escape per-IP limits.
	TrustProxyHeaders bool
	Limits            Limits
}

// WebEnabled reports whether at least one sign-in provider is configured.
func (c Config) WebEnabled() bool { return c.GitHubClientID != "" || c.EmailProvider != "" }

func FromEnv(getenv func(string) string) (Config, error) {
	get := func(k, def string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return def
	}
	gaID := getenv("GOOGLE_ANALYTICS_ID")
	if gaID == "" {
		gaID = getenv("GA_MEASUREMENT_ID")
	}
	c := Config{
		Addr:               get("ADDR", ":8080"),
		DB:                 get("DB", "sqlite"),
		DatabaseURL:        getenv("DATABASE_URL"),
		Storage:            get("STORAGE", "local"),
		DataDir:            get("DATA_DIR", "./data"),
		S3Endpoint:         getenv("S3_ENDPOINT"),
		S3Bucket:           getenv("S3_BUCKET"),
		S3AccessKey:        getenv("S3_ACCESS_KEY"),
		S3SecretKey:        getenv("S3_SECRET_KEY"),
		S3UseSSL:           get("S3_USE_SSL", "true") == "true",
		AdminToken:         getenv("ADMIN_TOKEN"),
		KeySecret:          getenv("KEY_SIGNING_SECRET"),
		ShareOrigin:        get("PUBLIC_SHARE_ORIGIN", "http://localhost:8080"),
		APIOrigin:          get("PUBLIC_API_ORIGIN", "http://localhost:8080"),
		GitHubClientID:     getenv("GITHUB_CLIENT_ID"),
		GitHubClientSecret: getenv("GITHUB_CLIENT_SECRET"),
		EmailProvider:      getenv("EMAIL_PROVIDER"),
		ResendAPIKey:       getenv("RESEND_API_KEY"),
		EmailFrom:          getenv("EMAIL_FROM"),
		GoogleAnalyticsID:  gaID,

		TrustProxyHeaders: get("TRUST_PROXY_HEADERS", "false") == "true",
	}
	var err error
	if c.Limits, err = limitsFromEnv(getenv); err != nil {
		return c, err
	}
	switch c.DB {
	case "sqlite":
	case "postgres":
		if c.DatabaseURL == "" {
			return c, errors.New("DB=postgres requires DATABASE_URL")
		}
	default:
		return c, fmt.Errorf("DB must be sqlite or postgres, got %q", c.DB)
	}
	switch c.Storage {
	case "local":
	case "s3":
		if c.S3Endpoint == "" || c.S3Bucket == "" || c.S3AccessKey == "" || c.S3SecretKey == "" {
			return c, errors.New("STORAGE=s3 requires S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY, S3_SECRET_KEY")
		}
	default:
		return c, fmt.Errorf("STORAGE must be local or s3, got %q", c.Storage)
	}
	if (c.GitHubClientID == "") != (c.GitHubClientSecret == "") {
		return c, errors.New("GitHub sign-in requires GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET")
	}
	switch c.EmailProvider {
	case "", "log":
	case "resend":
		if c.ResendAPIKey == "" || c.EmailFrom == "" {
			return c, errors.New("EMAIL_PROVIDER=resend requires RESEND_API_KEY and EMAIL_FROM")
		}
	default:
		return c, errors.New("EMAIL_PROVIDER must be log or resend, or unset to disable email sign-in")
	}
	if c.WebEnabled() && c.KeySecret == "" {
		return c, errors.New("web sign-in requires KEY_SIGNING_SECRET, including when ADMIN_TOKEN is set")
	}
	if c.AdminToken == "" && c.KeySecret == "" {
		return c, errors.New("set ADMIN_TOKEN (self-host) or KEY_SIGNING_SECRET (managed)")
	}
	return c, nil
}

func limitsFromEnv(getenv func(string) string) (Limits, error) {
	l := Limits{VersionBytes: 26214400, Files: 500, VersionsKept: 20, PublishPerMin: 10,
		ArtifactsPerTenant: 200, CommentBytes: 2048, CommentsPerMin: 20, AnchorBytes: 4096}
	set := func(k string, dst *int) error {
		v := getenv(k)
		if v == "" {
			return nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return fmt.Errorf("%s must be a positive integer, got %q", k, v)
		}
		*dst = n
		return nil
	}
	if v := getenv("LIMIT_VERSION_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return l, fmt.Errorf("LIMIT_VERSION_BYTES must be a positive integer, got %q", v)
		}
		l.VersionBytes = n
	}
	for k, dst := range map[string]*int{
		"LIMIT_FILES": &l.Files, "LIMIT_VERSIONS_KEPT": &l.VersionsKept,
		"LIMIT_PUBLISH_PER_MIN": &l.PublishPerMin,
		"LIMIT_COMMENT_BYTES":   &l.CommentBytes, "LIMIT_COMMENTS_PER_MIN": &l.CommentsPerMin,
		"LIMIT_ANCHOR_BYTES": &l.AnchorBytes,
	} {
		if err := set(k, dst); err != nil {
			return l, err
		}
	}
	quotaEnv := "LIMIT_ARTIFACTS_PER_TENANT"
	if getenv(quotaEnv) == "" && getenv("LIMIT_ARTIFACTS_PER_KEY") != "" {
		quotaEnv = "LIMIT_ARTIFACTS_PER_KEY"
		slog.Warn("LIMIT_ARTIFACTS_PER_KEY is deprecated; use LIMIT_ARTIFACTS_PER_TENANT")
	}
	if err := set(quotaEnv, &l.ArtifactsPerTenant); err != nil {
		return l, err
	}
	return l, nil
}
