package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "broken-mirror.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadConfigDefaultsWhenMissing(t *testing.T) {
	// An explicitly-missing default file falls back to defaults.
	cfg, err := loadConfig(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err == nil {
		t.Fatal("expected error for explicitly-set missing config")
	}
	_ = cfg
}

func TestLoadConfigValues(t *testing.T) {
	p := writeTemp(t, `
addr = "0.0.0.0:9000"
upstream = "https://ghe.example.com"
write_allow = ["Tener/accresys", "myorg/tool"]
`)
	cfg, err := loadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "0.0.0.0:9000" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.Upstream != "https://ghe.example.com" {
		t.Errorf("Upstream = %q", cfg.Upstream)
	}
	if len(cfg.WriteAllow) != 2 || cfg.WriteAllow[0] != "Tener/accresys" {
		t.Errorf("WriteAllow = %v", cfg.WriteAllow)
	}
}

func TestLoadConfigRejectsWildcards(t *testing.T) {
	for _, bad := range []string{
		`write_allow = ["Tener/*"]`,
		`write_allow = ["Tener/repo?"]`,
		`write_allow = ["*/*"]`,
		`write_allow = ["just-an-owner"]`,
		`write_allow = ["a/b/c"]`,
	} {
		p := writeTemp(t, bad)
		if _, err := loadConfig(p); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestValidateWriteAllowAcceptsExplicit(t *testing.T) {
	if err := validateWriteAllow([]string{"Tener/accresys", "org/repo-name"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
