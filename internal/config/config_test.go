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

func TestSave_AtomicReplacementAndPrivatePermissions(t *testing.T) {
	home := t.TempDir()
	restore := patchHomeDir(t, home)
	defer restore()

	configDir := filepath.Join(home, ".config", "opentracker")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":{"old":{}}}`), 0644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	oldFile, err := os.Open(configPath)
	if err != nil {
		t.Fatalf("cannot open old config: %v", err)
	}
	defer oldFile.Close()
	oldInfo, err := oldFile.Stat()
	if err != nil {
		t.Fatalf("cannot stat old config: %v", err)
	}

	cfg := &Config{Providers: map[string]json.RawMessage{"new": json.RawMessage(`{"ok":true}`)}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	newInfo, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("cannot stat saved config: %v", err)
	}
	if os.SameFile(oldInfo, newInfo) {
		t.Fatal("config was modified in place instead of atomically replaced")
	}
	if mode := newInfo.Mode().Perm(); mode != 0600 {
		t.Errorf("config mode = %04o, want 0600", mode)
	}
	dirInfo, err := os.Stat(configDir)
	if err != nil {
		t.Fatalf("cannot stat config directory: %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0700 {
		t.Errorf("config directory mode = %04o, want 0700", mode)
	}
}

func TestSave_RejectsSymlinkConfigPath(t *testing.T) {
	home := t.TempDir()
	restore := patchHomeDir(t, home)
	defer restore()
	configDir := filepath.Join(home, ".config", "opentracker")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "target.json")
	const original = `{"keep":true}`
	if err := os.WriteFile(target, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(configDir, "config.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := (&Config{Providers: map[string]json.RawMessage{}}).Save(); err == nil {
		t.Fatal("expected symlink config path to be rejected")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("symlink target changed: got %q", data)
	}
}

func TestSave_RejectsSymlinkConfigDirectory(t *testing.T) {
	home := t.TempDir()
	restore := patchHomeDir(t, home)
	defer restore()
	configParent := filepath.Join(home, ".config")
	if err := os.MkdirAll(configParent, 0700); err != nil {
		t.Fatal(err)
	}
	targetDir := filepath.Join(home, "outside")
	if err := os.Mkdir(targetDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetDir, filepath.Join(configParent, "opentracker")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := (&Config{Providers: map[string]json.RawMessage{}}).Save(); err == nil {
		t.Fatal("expected symlink config directory to be rejected")
	}
	if _, err := os.Stat(filepath.Join(targetDir, "config.json")); !os.IsNotExist(err) {
		t.Fatalf("unexpected config was written through symlink: %v", err)
	}
}

func TestSave_PreRenameFailurePreservesExistingConfig(t *testing.T) {
	home := t.TempDir()
	restore := patchHomeDir(t, home)
	defer restore()
	configDir := filepath.Join(home, ".config", "opentracker")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.json")
	const original = `{"providers":{"keep":{"value":1}}}`
	if err := os.WriteFile(configPath, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Providers: map[string]json.RawMessage{"invalid": json.RawMessage(`{`)}}
	if err := cfg.Save(); err == nil {
		t.Fatal("expected marshal failure")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("existing config changed after pre-rename failure: got %q", data)
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
