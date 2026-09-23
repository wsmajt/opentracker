package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opentracker/internal/app"
	"opentracker/internal/cache"
	"opentracker/internal/config"
	"opentracker/internal/provider/opencode"
)

type testCredentialStore struct {
	values    map[string]string
	putCount  int
	putErr    error
	getErr    error
	deleteErr error
	deleted   []string
}

func newTestCredentialStore() *testCredentialStore {
	return &testCredentialStore{values: make(map[string]string)}
}

func (s *testCredentialStore) Put(id, value string) error {
	s.putCount++
	if s.putErr != nil {
		return s.putErr
	}
	s.values[id] = value
	return nil
}

func TestLoginOpenCodeWithWorkspaceListSelection(t *testing.T) {
	tests := []struct {
		name          string
		workspaceIDs  []string
		choice        string
		listErr       error
		wantWorkspace string
		wantError     bool
	}{
		{name: "single workspace auto-select", workspaceIDs: []string{"wrk_only"}, wantWorkspace: "wrk_only"},
		{name: "organization ID auto-select", workspaceIDs: []string{"org_only"}, wantWorkspace: "org_only"},
		{name: "multiple workspace valid choice", workspaceIDs: []string{"wrk_first", "wrk_second"}, choice: "2\n", wantWorkspace: "wrk_second"},
		{name: "multiple workspace invalid choice", workspaceIDs: []string{"wrk_first", "wrk_second"}, choice: "wrk_attacker\n", wantError: true},
		{name: "multiple workspace out of range", workspaceIDs: []string{"wrk_first", "wrk_second"}, choice: "3\n", wantError: true},
		{name: "endpoint failure", listErr: errors.New("/console/api/orgs failed"), wantError: true},
		{name: "empty endpoint result", workspaceIDs: []string{}, wantError: true},
		{name: "selection EOF", workspaceIDs: []string{"wrk_first", "wrk_second"}, choice: "2", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			store := newTestCredentialStore()
			store.values["old-id"] = "old keyring value"
			old := json.RawMessage(`{"workspace":"wrk_old","credentialId":"old-id"}`)
			if err := (&config.Config{Providers: map[string]json.RawMessage{"opencode": old}}).Save(); err != nil {
				t.Fatal(err)
			}
			legacy := filepath.Join(home, ".config", "opentracker", "opencode-cookies.txt")
			if err := os.WriteFile(legacy, []byte("legacy plaintext"), 0o600); err != nil {
				t.Fatal(err)
			}
			cookies := []*http.Cookie{{Name: "__Host-console_session", Value: "new-session", Domain: "opencode.ai"}}
			got, err := loginOpenCodeWithWorkspaceList(cookies, store, func([]*http.Cookie) ([]string, error) {
				return tt.workspaceIDs, tt.listErr
			}, strings.NewReader(tt.choice))
			if tt.wantError {
				if err == nil || got != "" {
					t.Fatalf("login result = %q, %v; want selection/detection failure", got, err)
				}
				if store.putCount != 0 {
					t.Fatalf("credential store Put called %d times on failed selection/detection", store.putCount)
				}
				cfg, err := config.Load()
				if err != nil {
					t.Fatal(err)
				}
				var selected struct {
					Workspace string `json:"workspace"`
					ID        string `json:"credentialId"`
				}
				if err := json.Unmarshal(cfg.Providers["opencode"], &selected); err != nil {
					t.Fatal(err)
				}
				if selected.Workspace != "wrk_old" || selected.ID != "old-id" {
					t.Fatalf("failed selection/detection changed config: %+v", selected)
				}
				if _, err := os.Stat(legacy); err != nil {
					t.Fatalf("failed selection/detection changed legacy cookie file: %v", err)
				}
				if _, ok := store.values["old-id"]; !ok || len(store.deleted) != 0 {
					t.Fatalf("failed selection/detection changed old keyring entry: values=%v deleted=%v", store.values, store.deleted)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.wantWorkspace {
				t.Fatalf("selected workspace = %q, want %q", got, tt.wantWorkspace)
			}
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			var selected struct {
				Workspace string `json:"workspace"`
			}
			if err := json.Unmarshal(cfg.Providers["opencode"], &selected); err != nil {
				t.Fatal(err)
			}
			if selected.Workspace != tt.wantWorkspace {
				t.Fatalf("config workspace = %q, want %q", selected.Workspace, tt.wantWorkspace)
			}
		})
	}
}

func (s *testCredentialStore) Get(id string) (string, error) {
	if s.getErr != nil {
		return "", s.getErr
	}
	value, ok := s.values[id]
	if !ok {
		return "", errors.New("not found")
	}
	return value, nil
}

func (s *testCredentialStore) Delete(id string) error {
	s.deleted = append(s.deleted, id)
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.values, id)
	return nil
}

func TestCompleteOpenCodeLoginSwitchesVerifiedSessionAndCleansOldCredential(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestCredentialStore()
	store.values["old-id"] = "old credential"
	old, _ := json.Marshal(struct {
		Workspace string `json:"workspace"`
		ID        string `json:"credentialId"`
	}{"wrk_old", "old-id"})
	if err := (&config.Config{Providers: map[string]json.RawMessage{"opencode": old}}).Save(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".config", "opentracker")
	cookieFile := filepath.Join(dir, "opencode-cookies.txt")
	legacyWorkspace := filepath.Join(dir, "opencode-workspace.txt")
	for _, path := range []string{cookieFile, legacyWorkspace} {
		if err := os.WriteFile(path, []byte("legacy secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	usageCache := cache.New(filepath.Join(home, ".cache", "opentracker"))
	for _, plan := range []string{"opencode-go", "opencode-zen"} {
		for _, key := range []string{plan, app.OpenCodeCacheKey(plan, "wrk_old", "old-id")} {
			if err := usageCache.Set(key, "old-account-usage", 90*time.Second); err != nil {
				t.Fatal(err)
			}
		}
	}
	cookies := []*http.Cookie{{Name: "__Host-console_session", Value: "new-session", Domain: "opencode.ai", Path: "/", Secure: true}}
	if err := completeOpenCodeLoginWithStore(cookies, "wrk_new", store); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var selected struct {
		Workspace string `json:"workspace"`
		ID        string `json:"credentialId"`
	}
	if err := json.Unmarshal(got.Providers["opencode"], &selected); err != nil {
		t.Fatal(err)
	}
	if selected.Workspace != "wrk_new" || selected.ID == "" || selected.ID == "old-id" {
		t.Fatalf("active selector = %+v; want new workspace and immutable credential ID", selected)
	}
	if _, err := opencode.LoadCredential(store, selected.ID, "wrk_new"); err != nil {
		t.Fatalf("active credential is not readable: %v", err)
	}
	if _, ok := store.values["old-id"]; ok {
		t.Fatal("old credential was not deleted after successful switch")
	}
	for _, path := range []string{cookieFile, legacyWorkspace} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("legacy file %s remains: %v", path, err)
		}
	}
	for _, plan := range []string{"opencode-go", "opencode-zen"} {
		for _, key := range []string{plan, app.OpenCodeCacheKey(plan, "wrk_old", "old-id")} {
			var cached string
			if usageCache.Get(key, &cached) {
				t.Errorf("%s cache key %q still serves old-account usage", plan, key)
			}
		}
	}
}

func TestCompleteOpenCodeLoginBlockedStoreLeavesConfigAndLegacyFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := json.RawMessage(`{"workspace":"wrk_old","credentialId":"old-id"}`)
	if err := (&config.Config{Providers: map[string]json.RawMessage{"opencode": old}}).Save(); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, ".config", "opentracker", "opencode-cookies.txt")
	if err := os.WriteFile(legacy, []byte("legacy plaintext"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newTestCredentialStore()
	store.putErr = errors.New("keyring locked")
	usageCache := cache.New(filepath.Join(home, ".cache", "opentracker"))
	for _, plan := range []string{"opencode-go", "opencode-zen"} {
		for _, key := range []string{plan, app.OpenCodeCacheKey(plan, "wrk_old", "old-id")} {
			if err := usageCache.Set(key, "old-account-usage", 90*time.Second); err != nil {
				t.Fatal(err)
			}
		}
	}
	cookies := []*http.Cookie{{Name: "__Host-console_session", Value: "new", Domain: "opencode.ai"}}
	if err := completeOpenCodeLoginWithStore(cookies, "wrk_new", store); err == nil {
		t.Fatal("expected keyring failure")
	}
	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var selected struct {
		Workspace string `json:"workspace"`
		ID        string `json:"credentialId"`
	}
	if err := json.Unmarshal(got.Providers["opencode"], &selected); err != nil {
		t.Fatal(err)
	}
	if selected.Workspace != "wrk_old" || selected.ID != "old-id" {
		t.Fatalf("config changed despite blocked store: %+v", selected)
	}
	if data, err := os.ReadFile(legacy); err != nil || string(data) != "legacy plaintext" {
		t.Fatalf("legacy cookie file changed: %q, %v", data, err)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("deleted credentials after store failure: %v", store.deleted)
	}
	for _, plan := range []string{"opencode-go", "opencode-zen"} {
		for _, key := range []string{plan, app.OpenCodeCacheKey(plan, "wrk_old", "old-id")} {
			var cached string
			if !usageCache.Get(key, &cached) || cached != "old-account-usage" {
				t.Errorf("cache key %q was not preserved after keyring failure", key)
			}
		}
	}
}

func TestCompleteOpenCodeLoginDeleteFailureDoesNotRollBackSwitch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestCredentialStore()
	store.values["old-id"] = "old credential"
	store.deleteErr = errors.New("keyring unavailable")
	old := json.RawMessage(`{"workspace":"wrk_old","credentialId":"old-id"}`)
	if err := (&config.Config{Providers: map[string]json.RawMessage{"opencode": old}}).Save(); err != nil {
		t.Fatal(err)
	}
	cookies := []*http.Cookie{{Name: "__Host-console_session", Value: "new", Domain: "opencode.ai"}}
	if err := completeOpenCodeLoginWithStore(cookies, "wrk_new", store); err != nil {
		t.Fatalf("successful selector switch was rolled back after cleanup failure: %v", err)
	}
	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var selected struct {
		Workspace string `json:"workspace"`
	}
	if err := json.Unmarshal(got.Providers["opencode"], &selected); err != nil || selected.Workspace != "wrk_new" {
		t.Fatalf("active workspace = %+v, %v; want wrk_new", selected, err)
	}
	if _, ok := store.values["old-id"]; !ok {
		t.Fatal("old credential should remain when deletion fails")
	}
}

func TestCompleteOpenCodeLoginPreCommitErrorKeepsOldSelectionAndLegacyData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestCredentialStore()
	store.values["old-id"] = "old credential"
	old := json.RawMessage(`{"workspace":"wrk_old","credentialId":"old-id"}`)
	if err := (&config.Config{Providers: map[string]json.RawMessage{"opencode": old}}).Save(); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, ".config", "opentracker", "opencode-cookies.txt")
	if err := os.WriteFile(legacy, []byte("legacy plaintext"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("pre-commit failure")
	cookies := []*http.Cookie{{Name: "__Host-console_session", Value: "new", Domain: "opencode.ai"}}
	err := completeOpenCodeLoginWithStoreAndUpdate(cookies, "wrk_new", store, func(*config.Config, string, json.RawMessage) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want pre-commit error", err)
	}
	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var selected struct {
		Workspace string `json:"workspace"`
		ID        string `json:"credentialId"`
	}
	if err := json.Unmarshal(got.Providers["opencode"], &selected); err != nil {
		t.Fatal(err)
	}
	if selected.Workspace != "wrk_old" || selected.ID != "old-id" {
		t.Fatalf("pre-commit failure changed selector: %+v", selected)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("legacy cookie file was removed before commit: %v", err)
	}
	if _, ok := store.values["old-id"]; !ok || len(store.deleted) != 0 {
		t.Fatalf("old credential was deleted on failed commit: store=%v deleted=%v", store.values, store.deleted)
	}
}

func TestCompleteOpenCodeLoginPostRenameErrorRecognizesCommittedSwitch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestCredentialStore()
	store.values["old-id"] = "old credential"
	old := json.RawMessage(`{"workspace":"wrk_old","credentialId":"old-id"}`)
	if err := (&config.Config{Providers: map[string]json.RawMessage{"opencode": old}}).Save(); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, ".config", "opentracker", "opencode-cookies.txt")
	if err := os.WriteFile(legacy, []byte("legacy plaintext"), 0o600); err != nil {
		t.Fatal(err)
	}
	usageCache := cache.New(filepath.Join(home, ".cache", "opentracker"))
	for _, plan := range []string{"opencode-go", "opencode-zen"} {
		for _, key := range []string{plan, app.OpenCodeCacheKey(plan, "wrk_old", "old-id")} {
			if err := usageCache.Set(key, "old-account-usage", 90*time.Second); err != nil {
				t.Fatal(err)
			}
		}
	}
	postRenameErr := errors.New("directory sync failed after rename")
	cookies := []*http.Cookie{{Name: "__Host-console_session", Value: "new", Domain: "opencode.ai"}}
	err := completeOpenCodeLoginWithStoreAndUpdate(cookies, "wrk_new", store, func(cfg *config.Config, name string, raw json.RawMessage) error {
		if err := cfg.UpdateProvider(name, raw); err != nil {
			return err
		}
		return postRenameErr
	})
	if err != nil {
		t.Fatalf("verified post-rename commit was not treated as success: %v", err)
	}
	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var selected struct {
		Workspace string `json:"workspace"`
		ID        string `json:"credentialId"`
	}
	if err := json.Unmarshal(got.Providers["opencode"], &selected); err != nil {
		t.Fatal(err)
	}
	if selected.Workspace != "wrk_new" || selected.ID == "" {
		t.Fatalf("selector after recognized commit = %+v", selected)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy cookie file remains after confirmed commit: %v", err)
	}
	if _, ok := store.values["old-id"]; ok {
		t.Fatal("previous credential was not cleaned up after confirmed commit")
	}
	for _, plan := range []string{"opencode-go", "opencode-zen"} {
		for _, key := range []string{plan, app.OpenCodeCacheKey(plan, "wrk_old", "old-id")} {
			var cached string
			if usageCache.Get(key, &cached) {
				t.Errorf("old cache key %q survived confirmed commit", key)
			}
		}
	}
}

func TestCompleteOpenCodeLoginSequentialSwitchesUseLatestSelector(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestCredentialStore()
	store.values["initial-id"] = "initial credential"
	initial := json.RawMessage(`{"workspace":"wrk_initial","credentialId":"initial-id"}`)
	if err := (&config.Config{Providers: map[string]json.RawMessage{"opencode": initial}}).Save(); err != nil {
		t.Fatal(err)
	}
	cookies := []*http.Cookie{{Name: "__Host-console_session", Value: "session", Domain: "opencode.ai"}}
	if err := completeOpenCodeLoginWithStore(cookies, "wrk_first", store); err != nil {
		t.Fatal(err)
	}
	firstConfig, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var first struct {
		Workspace string `json:"workspace"`
		ID        string `json:"credentialId"`
	}
	if err := json.Unmarshal(firstConfig.Providers["opencode"], &first); err != nil {
		t.Fatal(err)
	}
	if first.Workspace != "wrk_first" || first.ID == "" || first.ID == "initial-id" {
		t.Fatalf("first selector = %+v", first)
	}
	if err := completeOpenCodeLoginWithStore(cookies, "wrk_final", store); err != nil {
		t.Fatal(err)
	}
	finalConfig, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var final struct {
		Workspace string `json:"workspace"`
		ID        string `json:"credentialId"`
	}
	if err := json.Unmarshal(finalConfig.Providers["opencode"], &final); err != nil {
		t.Fatal(err)
	}
	if final.Workspace != "wrk_final" || final.ID == "" || final.ID == first.ID {
		t.Fatalf("final selector = %+v; want latest sequential switch", final)
	}
	if _, err := opencode.LoadCredential(store, final.ID, "wrk_final"); err != nil {
		t.Fatalf("final credential not active: %v", err)
	}
	if _, ok := store.values["initial-id"]; ok {
		t.Fatal("initial credential remained after sequential switches")
	}
	if _, ok := store.values[first.ID]; ok {
		t.Fatal("intermediate credential remained after sequential switches")
	}
}
