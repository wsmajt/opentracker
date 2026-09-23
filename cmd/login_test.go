package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"opentracker/internal/cache"
	"opentracker/internal/config"
	"opentracker/internal/provider/opencode"
)

func TestCompleteOpenCodeLoginRestoresWorkspaceIfCookieSaveFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := &config.Config{Providers: map[string]json.RawMessage{
		"opencode": json.RawMessage(`{"workspace":"wrk_old"}`),
	}}
	if err := old.Save(); err != nil {
		t.Fatal(err)
	}
	err := completeOpenCodeLoginWithSave(nil, "wrk_new", func([]*http.Cookie) error {
		return errors.New("disk full")
	})
	if err == nil {
		t.Fatal("expected cookie save failure")
	}
	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := opencode.ParseConfig(got.Providers["opencode"])
	if err != nil || parsed.Workspace != "wrk_old" {
		t.Fatalf("workspace = %+v, %v; want previous workspace", parsed, err)
	}
}

func TestCompleteOpenCodeLoginSwitchesAccountAndClearsCaches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := &config.Config{Providers: map[string]json.RawMessage{
		"opencode": json.RawMessage(`{"workspace":"wrk_old"}`),
	}}
	if err := old.Save(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".config", "opentracker")
	legacy := filepath.Join(dir, "opencode-workspace.txt")
	if err := os.WriteFile(legacy, []byte("wrk_old"), 0o644); err != nil {
		t.Fatal(err)
	}
	usageCache := cache.New(filepath.Join(home, ".cache", "opentracker"))
	for _, plan := range []string{"opencode-go", "opencode-zen"} {
		if err := usageCache.Set(plan, "old-account-usage", 90*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	cookies := []*http.Cookie{{Name: "__Host-console_session", Value: "new-session", Domain: "opencode.ai", Path: "/", Secure: true}}
	if err := completeOpenCodeLogin(cookies, "wrk_new"); err != nil {
		t.Fatal(err)
	}

	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := opencode.ParseConfig(got.Providers["opencode"])
	if err != nil || parsed.Workspace != "wrk_new" {
		t.Fatalf("workspace = %+v, %v; want wrk_new", parsed, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy workspace cache still present: %v", err)
	}
	for _, plan := range []string{"opencode-go", "opencode-zen"} {
		var cached string
		if usageCache.Get(plan, &cached) {
			t.Errorf("%s still serves old-account usage", plan)
		}
	}
	cookieFile := filepath.Join(dir, "opencode-cookies.txt")
	info, err := os.Stat(cookieFile)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cookie file = %v, %v; want mode 0600", info, err)
	}
	info, err = os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("config directory = %v, %v; want mode 0700", info, err)
	}
}
