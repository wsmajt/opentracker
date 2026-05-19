package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func patchHomeDir(t *testing.T, dir string) func() {
	orig := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	return func() {
		os.Setenv("HOME", orig)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	home := t.TempDir()
	restore := patchHomeDir(t, home)
	defer restore()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Providers == nil {
		t.Fatal("expected non-nil Providers map")
	}
	if len(cfg.Providers) != 0 {
		t.Errorf("expected empty map, got %d entries", len(cfg.Providers))
	}
}

func TestLoad_ValidFile(t *testing.T) {
	home := t.TempDir()
	restore := patchHomeDir(t, home)
	defer restore()

	configDir := filepath.Join(home, ".config", "opentracker")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	data := `{"providers": {"opencode": {"workspace": "wrk_123"}}}`
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte(data), 0644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, ok := cfg.Providers["opencode"]
	if !ok {
		t.Fatal("expected 'opencode' provider in config")
	}

	var parsed struct {
		Workspace string `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("cannot parse provider config: %v", err)
	}
	if parsed.Workspace != "wrk_123" {
		t.Errorf("workspace = %q, want wrk_123", parsed.Workspace)
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	home := t.TempDir()
	restore := patchHomeDir(t, home)
	defer restore()

	configDir := filepath.Join(home, ".config", "opentracker")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte("not json"), 0644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSave(t *testing.T) {
	home := t.TempDir()
	restore := patchHomeDir(t, home)
	defer restore()

	cfg := &Config{Providers: make(map[string]json.RawMessage)}
	cfg.Providers["opencode"] = json.RawMessage(`{"workspace":"wrk_abc"}`)

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	configPath := filepath.Join(home, ".config", "opentracker", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("cannot read saved config: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("expected non-empty config file")
	}

	var loaded Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("cannot parse saved config: %v", err)
	}

	if _, ok := loaded.Providers["opencode"]; !ok {
		t.Fatal("expected 'opencode' provider in saved config")
	}
}

func TestUpdateProvider(t *testing.T) {
	home := t.TempDir()
	restore := patchHomeDir(t, home)
	defer restore()

	cfg := &Config{Providers: make(map[string]json.RawMessage)}

	raw := json.RawMessage(`{"workspace":"wrk_new"}`)
	if err := cfg.UpdateProvider("opencode", raw); err != nil {
		t.Fatalf("UpdateProvider failed: %v", err)
	}

	if string(cfg.Providers["opencode"]) != string(raw) {
		t.Errorf("provider config mismatch: got %s", cfg.Providers["opencode"])
	}
}
