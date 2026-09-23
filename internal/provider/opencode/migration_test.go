package opencode

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"opentracker/internal/config"
)

const legacyCookieLine = "# Netscape HTTP Cookie File\nopencode.ai\tFALSE\t/\tTRUE\t1893456000\t__Host-console_session\tlegacy-secret\n"
const legacyAuthCookieLine = "# Netscape HTTP Cookie File\nopencode.ai\tFALSE\t/\tTRUE\t1893456000\tauth\tlegacy-auth-secret\n"

func matchingWorkspaceLister(_ []*http.Cookie) ([]string, error) {
	return []string{"wrk_account"}, nil
}

type putCountingCredentialStore struct {
	*fakeCredentialStore
	putCalls int
}

func (s *putCountingCredentialStore) Put(id, value string) error {
	s.putCalls++
	return s.fakeCredentialStore.Put(id, value)
}

func prepareLegacy(t *testing.T) (*config.Config, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cookiePath := filepath.Join(home, ".config", "opentracker", "opencode-cookies.txt")
	if err := os.MkdirAll(filepath.Dir(cookiePath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cookiePath, []byte(legacyCookieLine), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &OpenCodeConfig{Workspace: "wrk_account"}
	raw, _ := json.Marshal(cfg)
	appCfg := &config.Config{Providers: map[string]json.RawMessage{"opencode": raw}}
	if err := appCfg.UpdateProvider("opencode", raw); err != nil {
		t.Fatal(err)
	}
	return appCfg, cookiePath
}

func TestNewProviderMigratesLegacyCookies(t *testing.T) {
	appCfg, cookiePath := prepareLegacy(t)
	store := newFakeCredentialStore()
	if _, err := newProviderWithWorkspaceLister(appCfg, "go", store, matchingWorkspaceLister); err != nil {
		t.Fatalf("newProvider() error = %v", err)
	}
	if _, err := os.Stat(cookiePath); !os.IsNotExist(err) {
		t.Fatalf("legacy cookie file still present (stat error %v)", err)
	}
	persisted, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseConfig(persisted.Providers["opencode"])
	if err != nil || cfg.CredentialID == "" {
		t.Fatalf("persisted config = %#v, err %v", cfg, err)
	}
	cookies, err := LoadCredential(store, cfg.CredentialID, cfg.Workspace)
	if err != nil || len(cookies) != 1 || cookies[0].Value != "legacy-secret" {
		t.Fatalf("migrated cookies = %#v, err %v", cookies, err)
	}
}

func TestNewProviderMigrationFailuresPreserveLegacyAndConfig(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*fakeCredentialStore)
		file  string
	}{
		{name: "keyring unavailable", setup: func(s *fakeCredentialStore) { s.putErr = errors.New("unavailable") }, file: legacyCookieLine},
		{name: "keyring locked during verify", setup: func(s *fakeCredentialStore) { s.getErr = errors.New("locked") }, file: legacyCookieLine},
		{name: "parse failure", file: "not a Netscape cookie"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCfg, path := prepareLegacy(t)
			if tt.file != legacyCookieLine {
				if err := os.WriteFile(path, []byte(tt.file), 0600); err != nil {
					t.Fatal(err)
				}
			}
			store := newFakeCredentialStore()
			if tt.setup != nil {
				tt.setup(store)
			}
			if _, err := newProviderWithWorkspaceLister(appCfg, "go", store, matchingWorkspaceLister); err == nil {
				t.Fatal("newProvider() unexpectedly succeeded")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("legacy cookie file not preserved: %v", err)
			}
			persisted, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			var after OpenCodeConfig
			if err := json.Unmarshal(persisted.Providers["opencode"], &after); err != nil {
				t.Fatal(err)
			}
			if after.CredentialID != "" {
				t.Fatalf("config unexpectedly migrated: %q", after.CredentialID)
			}
		})
	}
}

func TestAuthOnlyLegacyMigrationSaveFailurePreservesFile(t *testing.T) {
	appCfg, path := prepareLegacy(t)
	if err := os.WriteFile(path, []byte(legacyAuthCookieLine), 0600); err != nil {
		t.Fatal(err)
	}
	store := newFakeCredentialStore()
	store.putErr = errors.New("keyring unavailable")
	if _, err := newProviderWithWorkspaceLister(appCfg, "go", store, matchingWorkspaceLister); err == nil {
		t.Fatal("newProvider() unexpectedly succeeded with an auth-only legacy cookie")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("auth-only legacy cookie file was removed after migration failure: %v", err)
	}
}

func TestLegacyMigrationRequiresWorkspaceVerification(t *testing.T) {
	tests := []struct {
		name    string
		list    func([]*http.Cookie) ([]string, error)
		wantErr string
	}{
		{
			name: "workspace mismatch",
			list: func([]*http.Cookie) ([]string, error) {
				return []string{"wrk_someone_else", "org_other"}, nil
			},
			wantErr: "run 'opentracker login opencode'",
		},
		{
			name: "detector error",
			list: func([]*http.Cookie) ([]string, error) {
				return nil, errors.New("network unavailable")
			},
			wantErr: "run 'opentracker login opencode'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			appCfg, path := prepareLegacy(t)
			originalFile, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			originalMemoryConfig := append([]byte(nil), appCfg.Providers["opencode"]...)
			configPath := filepath.Join(os.Getenv("HOME"), ".config", "opentracker", "config.json")
			originalDiskConfig, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			store := &putCountingCredentialStore{fakeCredentialStore: newFakeCredentialStore()}

			if _, err := newProviderWithWorkspaceLister(appCfg, "go", store, tt.list); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("newProviderWithWorkspaceLister() error = %v, want actionable verification error", err)
			}
			if store.putCalls != 0 {
				t.Fatalf("credential store Put called %d times before verification", store.putCalls)
			}
			fileAfter, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("legacy cookie file not preserved: %v", err)
			}
			if !bytes.Equal(fileAfter, originalFile) {
				t.Fatal("legacy cookie file contents changed")
			}
			var originalCfg, currentCfg OpenCodeConfig
			if err := json.Unmarshal(originalMemoryConfig, &originalCfg); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(appCfg.Providers["opencode"], &currentCfg); err != nil {
				t.Fatal(err)
			}
			if currentCfg != originalCfg {
				t.Fatalf("in-memory config changed: got %#v, want %#v", currentCfg, originalCfg)
			}
			diskConfigAfter, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(diskConfigAfter, originalDiskConfig) {
				t.Fatal("persisted config changed")
			}
		})
	}
}

func TestLegacyMigrationAcceptsConfiguredWorkspaceAmongMultiple(t *testing.T) {
	appCfg, path := prepareLegacy(t)
	store := &putCountingCredentialStore{fakeCredentialStore: newFakeCredentialStore()}
	lister := func([]*http.Cookie) ([]string, error) {
		return []string{"org_other", "wrk_account"}, nil
	}
	if _, err := newProviderWithWorkspaceLister(appCfg, "go", store, lister); err != nil {
		t.Fatalf("migration with configured workspace membership failed: %v", err)
	}
	if store.putCalls != 1 {
		t.Fatalf("credential store Put called %d times, want 1", store.putCalls)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("successfully migrated legacy cookie file remains (stat error %v)", err)
	}
}

func TestLegacyMigrationHonorsNewerPersistedCredential(t *testing.T) {
	appCfg, path := prepareLegacy(t)
	store := newFakeCredentialStore()
	newID, err := SaveCredential(store, "wrk_new_account", validCookies("new-login-secret"))
	if err != nil {
		t.Fatal(err)
	}
	newRaw, err := json.Marshal(&OpenCodeConfig{Workspace: "wrk_new_account", CredentialID: newID})
	if err != nil {
		t.Fatal(err)
	}
	if err := appCfg.UpdateProvider("opencode", newRaw); err != nil {
		t.Fatal(err)
	}
	// Retain stale caller memory as if it was loaded before the login committed.
	stale := &config.Config{Providers: map[string]json.RawMessage{"opencode": json.RawMessage(`{"workspace":"wrk_account"}`)}}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".config", "opentracker", "config.json"), mustMarshalConfig(t, appCfg), 0600); err != nil {
		t.Fatal(err)
	}
	provider, err := newProviderWithWorkspaceLister(stale, "go", store, func([]*http.Cookie) ([]string, error) {
		t.Fatal("workspace lister called despite a newer persisted credential")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("newProviderWithWorkspaceLister() error = %v", err)
	}
	if got := provider.(*OpenCodeProvider).cfg; got.Workspace != "wrk_new_account" || got.CredentialID != newID {
		t.Fatalf("provider config = %#v, want latest persisted credential", got)
	}
	if raw := string(stale.Providers["opencode"]); raw != string(newRaw) {
		t.Fatalf("in-memory config = %s, want latest persisted config %s", raw, newRaw)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("verified legacy cookie file remains (stat error %v)", err)
	}
}

func TestConcurrentLegacyMigrationsSelectSingleCredential(t *testing.T) {
	initial, _ := prepareLegacy(t)
	staleRaw := append(json.RawMessage(nil), initial.Providers["opencode"]...)
	store := newFakeCredentialStore()
	start := make(chan struct{})
	results := make(chan *OpenCodeProvider, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			appCfg := &config.Config{Providers: map[string]json.RawMessage{"opencode": append(json.RawMessage(nil), staleRaw...)}}
			<-start
			p, err := newProviderWithWorkspaceLister(appCfg, "go", store, matchingWorkspaceLister)
			if err != nil {
				errs <- err
				return
			}
			results <- p.(*OpenCodeProvider)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent migration failed: %v", err)
	}
	var selectedID string
	count := 0
	for p := range results {
		count++
		if selectedID == "" {
			selectedID = p.cfg.CredentialID
		}
		if p.cfg.CredentialID != selectedID {
			t.Fatalf("concurrent providers selected different IDs: %q and %q", selectedID, p.cfg.CredentialID)
		}
	}
	if count != 2 || selectedID == "" {
		t.Fatalf("got %d providers and selected ID %q; want 2 providers sharing an ID", count, selectedID)
	}
	if len(store.values) != 1 {
		t.Fatalf("stored %d credentials, want exactly one migration", len(store.values))
	}
}

func mustMarshalConfig(t *testing.T, cfg *config.Config) []byte {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCredentialIDNeverFallsBackToLegacyFile(t *testing.T) {
	appCfg, path := prepareLegacy(t)
	raw, _ := json.Marshal(&OpenCodeConfig{Workspace: "wrk_account", CredentialID: "unavailable-id"})
	appCfg.Providers["opencode"] = raw
	if err := appCfg.UpdateProvider("opencode", raw); err != nil {
		t.Fatal(err)
	}
	store := newFakeCredentialStore()
	store.getErr = errors.New("keyring locked")
	if _, err := newProvider(appCfg, "go", store); err == nil {
		t.Fatal("expected locked keyring error")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("legacy cookie file was touched or removed: %v", err)
	}
}

func TestConfiguredCredentialCleansResidualLegacyFileAfterVerifiedRead(t *testing.T) {
	appCfg, path := prepareLegacy(t)
	store := newFakeCredentialStore()
	id, err := SaveCredential(store, "wrk_account", validCookies("keyring-secret"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(&OpenCodeConfig{Workspace: "wrk_account", CredentialID: id})
	if err := appCfg.UpdateProvider("opencode", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := newProvider(appCfg, "go", store); err != nil {
		t.Fatalf("newProvider() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("residual cookie file remains (stat error %v)", err)
	}
}

func TestAmbiguousConfigSaveFailurePreservesLegacy(t *testing.T) {
	appCfg, path := prepareLegacy(t)
	originalUpdater := updateOpenCodeProviderConfig
	updateOpenCodeProviderConfig = func(*config.Config, json.RawMessage) error {
		return errors.New("ambiguous save failure")
	}
	t.Cleanup(func() { updateOpenCodeProviderConfig = originalUpdater })
	if _, err := newProviderWithWorkspaceLister(appCfg, "go", newFakeCredentialStore(), matchingWorkspaceLister); err == nil {
		t.Fatal("newProvider() unexpectedly accepted an unpersisted credential ID")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("legacy cookie file not preserved after ambiguous failure: %v", err)
	}
	persisted, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseConfig(persisted.Providers["opencode"])
	if err != nil || cfg.CredentialID != "" {
		t.Fatalf("persisted config = %#v, err %v; want previous config", cfg, err)
	}
}

func TestCredentialWorkspaceMismatchFailsClosed(t *testing.T) {
	appCfg, path := prepareLegacy(t)
	store := newFakeCredentialStore()
	id, err := SaveCredential(store, "wrk_other", validCookies("secret"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(&OpenCodeConfig{Workspace: "wrk_account", CredentialID: id})
	appCfg.Providers["opencode"] = raw
	if err := appCfg.UpdateProvider("opencode", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := newProvider(appCfg, "go", store); err == nil {
		t.Fatal("workspace mismatch unexpectedly succeeded")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("legacy file unexpectedly removed: %v", err)
	}
}
