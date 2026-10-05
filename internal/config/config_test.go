package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c, err := FromEnv(env(map[string]string{"ADMIN_TOKEN": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DB != "sqlite" || c.Storage != "local" || c.Addr != ":8080" {
		t.Fatalf("bad defaults: %+v", c)
	}
	if c.Limits.VersionBytes != 26214400 || c.Limits.VersionsKept != 20 {
		t.Fatalf("bad limits: %+v", c.Limits)
	}
	if c.DataDir != "./data" {
		t.Fatalf("data dir: %q", c.DataDir)
	}
	if c.GoogleAnalyticsID != "" {
		t.Fatalf("GoogleAnalyticsID must default empty, got %q", c.GoogleAnalyticsID)
	}
}

func TestPostgresNeedsURL(t *testing.T) {
	_, err := FromEnv(env(map[string]string{"DB": "postgres"}))
	if err == nil {
		t.Fatal("expected error for missing DATABASE_URL")
	}
}

func TestS3NeedsVars(t *testing.T) {
	_, err := FromEnv(env(map[string]string{"STORAGE": "s3", "S3_BUCKET": "b"}))
	if err == nil {
		t.Fatal("expected error for missing S3_ENDPOINT")
	}
}

func TestLimitOverride(t *testing.T) {
	c, err := FromEnv(env(map[string]string{"LIMIT_FILES": "7", "ADMIN_TOKEN": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Limits.Files != 7 {
		t.Fatalf("want 7 got %d", c.Limits.Files)
	}
}

func TestTrustProxyHeadersDefaultsOff(t *testing.T) {
	c, _ := FromEnv(env(map[string]string{"ADMIN_TOKEN": "x"}))
	if c.TrustProxyHeaders {
		t.Fatal("TRUST_PROXY_HEADERS must default to false")
	}
	on, _ := FromEnv(env(map[string]string{"ADMIN_TOKEN": "x", "TRUST_PROXY_HEADERS": "true"}))
	if !on.TrustProxyHeaders {
		t.Fatal("TRUST_PROXY_HEADERS=true should enable it")
	}
}

func TestOriginsDefaultToAddr(t *testing.T) {
	c, _ := FromEnv(env(map[string]string{"ADMIN_TOKEN": "x"}))
	if c.ShareOrigin != "http://localhost:8080" || c.APIOrigin != "http://localhost:8080" {
		t.Fatalf("origins: %q %q", c.ShareOrigin, c.APIOrigin)
	}
}

func TestWebProviderConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name        string
		vars        map[string]string
		wantEnabled bool
		wantError   string
	}{
		{"admin only", map[string]string{"ADMIN_TOKEN": "test"}, false, ""},
		{"secret only", map[string]string{"KEY_SIGNING_SECRET": "test"}, false, ""},
		{"log", map[string]string{"EMAIL_PROVIDER": "log", "KEY_SIGNING_SECRET": "test"}, true, ""},
		{"github", map[string]string{"GITHUB_CLIENT_ID": "test", "GITHUB_CLIENT_SECRET": "test", "KEY_SIGNING_SECRET": "test"}, true, ""},
		{"log needs secret despite admin", map[string]string{"ADMIN_TOKEN": "test", "EMAIL_PROVIDER": "log"}, false, "KEY_SIGNING_SECRET"},
		{"missing github secret", map[string]string{"ADMIN_TOKEN": "test", "GITHUB_CLIENT_ID": "test"}, false, "GITHUB_CLIENT_SECRET"},
		{"missing github id", map[string]string{"ADMIN_TOKEN": "test", "GITHUB_CLIENT_SECRET": "test"}, false, "GITHUB_CLIENT_ID"},
		{"unknown email provider", map[string]string{"ADMIN_TOKEN": "test", "EMAIL_PROVIDER": "smtp"}, false, "EMAIL_PROVIDER"},
		{"resend needs key", map[string]string{"KEY_SIGNING_SECRET": "test", "EMAIL_PROVIDER": "resend", "EMAIL_FROM": "login@example.test"}, false, "RESEND_API_KEY"},
		{"resend needs from", map[string]string{"KEY_SIGNING_SECRET": "test", "EMAIL_PROVIDER": "resend", "RESEND_API_KEY": "test"}, false, "EMAIL_FROM"},
		{"resend", map[string]string{"KEY_SIGNING_SECRET": "test", "EMAIL_PROVIDER": "resend", "RESEND_API_KEY": "test", "EMAIL_FROM": "login@example.test"}, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := FromEnv(env(tc.vars))
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected configuration error mentioning %s", tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.WebEnabled() != tc.wantEnabled {
				t.Fatalf("WebEnabled = %t", cfg.WebEnabled())
			}
		})
	}
}

func TestTenantQuotaLegacyFallback(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	get := map[string]string{"ADMIN_TOKEN": "test", "LIMIT_ARTIFACTS_PER_KEY": "17"}
	got, err := FromEnv(env(get))
	if err != nil || got.Limits.ArtifactsPerTenant != 17 {
		t.Fatalf("legacy fallback failed: %v", err)
	}
	if !strings.Contains(logs.String(), "deprecated") {
		t.Fatal("missing deprecation log")
	}
	get["LIMIT_ARTIFACTS_PER_TENANT"] = "23"
	got, err = FromEnv(env(get))
	if err != nil || got.Limits.ArtifactsPerTenant != 23 {
		t.Fatalf("new tenant limit must take precedence: %v", err)
	}
	get["LIMIT_ARTIFACTS_PER_TENANT"] = "0"
	if _, err = FromEnv(env(get)); err == nil {
		t.Fatal("invalid tenant limit accepted")
	}
}

func TestGoogleAnalyticsID(t *testing.T) {
	c, err := FromEnv(env(map[string]string{"ADMIN_TOKEN": "x", "GOOGLE_ANALYTICS_ID": "G-DFTQ6Y5L6W"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.GoogleAnalyticsID != "G-DFTQ6Y5L6W" {
		t.Fatalf("GOOGLE_ANALYTICS_ID: got %q", c.GoogleAnalyticsID)
	}
	alias, err := FromEnv(env(map[string]string{"ADMIN_TOKEN": "x", "GA_MEASUREMENT_ID": "G-ALIAS"}))
	if err != nil {
		t.Fatal(err)
	}
	if alias.GoogleAnalyticsID != "G-ALIAS" {
		t.Fatalf("GA_MEASUREMENT_ID alias: got %q", alias.GoogleAnalyticsID)
	}
	prefer, err := FromEnv(env(map[string]string{
		"ADMIN_TOKEN": "x", "GOOGLE_ANALYTICS_ID": "G-PRIMARY", "GA_MEASUREMENT_ID": "G-ALIAS",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if prefer.GoogleAnalyticsID != "G-PRIMARY" {
		t.Fatalf("GOOGLE_ANALYTICS_ID should win over alias: got %q", prefer.GoogleAnalyticsID)
	}
}

func TestAccessMailCaps(t *testing.T) {
	c, err := FromEnv(env(map[string]string{"ADMIN_TOKEN": "x"}))
	if err != nil || c.Limits.AccessMailPerWorkspaceDay != 200 || c.Limits.AccessMailPerDay != 2000 {
		t.Fatalf("defaults: %v %+v", err, c.Limits)
	}
	c, err = FromEnv(env(map[string]string{"ADMIN_TOKEN": "x", "LIMIT_ACCESS_MAIL_PER_WORKSPACE_DAY": "5", "LIMIT_ACCESS_MAIL_PER_DAY": "50"}))
	if err != nil || c.Limits.AccessMailPerWorkspaceDay != 5 || c.Limits.AccessMailPerDay != 50 {
		t.Fatalf("override: %v %+v", err, c.Limits)
	}
	if _, err := FromEnv(env(map[string]string{"ADMIN_TOKEN": "x", "LIMIT_ACCESS_MAIL_PER_DAY": "0"})); err == nil {
		t.Fatal("zero accepted")
	}
}
