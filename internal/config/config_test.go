package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFromEnv_defaults(t *testing.T) {
	t.Setenv("SUNSHINE_ENDPOINT", "https://sunshine.example.com")
	t.Setenv("CLUSTER_ID", "prod-use1")
	t.Setenv("SUNSHINE_TOKEN", "sunhs_secret")

	c, err := FromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.PollInterval != 60*time.Second {
		t.Errorf("PollInterval = %v, want 60s", c.PollInterval)
	}
	if !c.DryRun {
		t.Error("DryRun should default to true")
	}
	if c.MetricsAddr != ":9090" {
		t.Errorf("MetricsAddr = %q, want :9090", c.MetricsAddr)
	}
	if c.Token != "sunhs_secret" {
		t.Errorf("Token = %q", c.Token)
	}
}

func TestFromEnv_tokenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("  file_token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUNSHINE_ENDPOINT", "https://s.example.com")
	t.Setenv("CLUSTER_ID", "c")
	t.Setenv("SUNSHINE_TOKEN_FILE", path)

	c, err := FromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Token != "file_token" {
		t.Errorf("Token = %q, want trimmed file_token", c.Token)
	}
}

func TestFromEnv_missingEndpoint(t *testing.T) {
	t.Setenv("SUNSHINE_ENDPOINT", "")
	t.Setenv("CLUSTER_ID", "c")
	t.Setenv("SUNSHINE_TOKEN", "x")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected an error for missing endpoint")
	}
}

func TestFromEnv_invalidInterval(t *testing.T) {
	t.Setenv("SUNSHINE_ENDPOINT", "https://s")
	t.Setenv("CLUSTER_ID", "c")
	t.Setenv("SUNSHINE_TOKEN", "x")
	t.Setenv("POLL_INTERVAL_SECONDS", "-5")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected an error for invalid interval")
	}
}
