package config

import "testing"

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

func TestOriginsDefaultToAddr(t *testing.T) {
	c, _ := FromEnv(env(map[string]string{"ADMIN_TOKEN": "x"}))
	if c.ShareOrigin != "http://localhost:8080" || c.APIOrigin != "http://localhost:8080" {
		t.Fatalf("origins: %q %q", c.ShareOrigin, c.APIOrigin)
	}
}
