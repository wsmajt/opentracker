package cliproxyapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
	"opentracker/internal/config"
)

type fakeStore struct {
	values  map[string]string
	getErr  error
	putErr  error
	deleted []string
}

func newFakeStore() *fakeStore { return &fakeStore{values: make(map[string]string)} }
func (s *fakeStore) Put(id, value string) error {
	if s.putErr != nil {
		return s.putErr
	}
	s.values[id] = value
	return nil
}
func (s *fakeStore) Get(id string) (string, error) {
	if s.getErr != nil {
		return "", s.getErr
	}
	value, ok := s.values[id]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}
func (s *fakeStore) Delete(id string) error {
	s.deleted = append(s.deleted, id)
	delete(s.values, id)
	return nil
}

func TestResolveManagementKeyDistinguishesMissingAndLockedKeyring(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(EnvKey, "")
	t.Setenv(EnvURL, "")
	cfg := &config.Config{Providers: map[string]json.RawMessage{}}
	store := newFakeStore()
	if _, configured, err := ResolveManagementKey(cfg, store); err != nil || configured {
		t.Fatalf("missing config = (%v,%v)", configured, err)
	}
	cfg.Providers["cliproxyapi"] = json.RawMessage(`{"credentialId":"id"}`)
	store.getErr = keyring.ErrNotFound
	if _, configured, err := ResolveManagementKey(cfg, store); !configured || err == nil || !strings.Contains(err.Error(), "missing from the system keyring") {
		t.Fatalf("missing secret = (%v,%v)", configured, err)
	}
	store.getErr = errors.New("secret-store-unavailable")
	if _, configured, err := ResolveManagementKey(cfg, store); !configured || err == nil || !strings.Contains(err.Error(), "unlock or configure") || strings.Contains(err.Error(), "secret-store-unavailable") {
		t.Fatalf("locked secret = (%v,%v)", configured, err)
	}
	t.Setenv(EnvKey, "environment-key")
	key, configured, err := ResolveManagementKey(cfg, store)
	if err != nil || !configured || key != "environment-key" {
		t.Fatalf("environment override = (%q,%v,%v)", key, configured, err)
	}
}

func TestSaveManagementKeySwitchesOnlyAfterKeyringVerification(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(EnvKey, "")
	t.Setenv(EnvURL, "")
	store := newFakeStore()
	oldRef, _ := json.Marshal(credentialConfig{CredentialID: "old-id"})
	cfg := &config.Config{Providers: map[string]json.RawMessage{"cliproxyapi": oldRef}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	store.values["old-id"] = "old-secret"
	if err := SaveManagementKey(cfg, "new-secret", store); err != nil {
		t.Fatal(err)
	}
	var selected credentialConfig
	if err := json.Unmarshal(cfg.Providers["cliproxyapi"], &selected); err != nil {
		t.Fatal(err)
	}
	if selected.CredentialID == "old-id" || store.values[selected.CredentialID] != "new-secret" {
		t.Fatal("new keyring credential was not selected")
	}
	if _, ok := store.values["old-id"]; ok {
		t.Fatal("previous keyring credential was not retired after commit")
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	key, configured, err := ResolveManagementKey(reloaded, store)
	if err != nil || !configured || key != "new-secret" {
		t.Fatalf("reloaded key = (%q,%v,%v)", key, configured, err)
	}
}

func TestSaveManagementKeyConfigFailureKeepsPreviousCredential(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvKey, "")
	t.Setenv(EnvURL, "")
	store := newFakeStore()
	store.values["old-id"] = "old-secret"
	oldRef, _ := json.Marshal(credentialConfig{CredentialID: "old-id"})
	if err := (&config.Config{Providers: map[string]json.RawMessage{"cliproxyapi": oldRef}}).Save(); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".config", "opentracker", "config.json")
	original, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "saved-config.json")
	if err := os.WriteFile(target, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, configPath); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	err = SaveManagementKey(cfg, "new-secret", store)
	if err == nil {
		t.Fatal("expected config write failure")
	}
	reloaded, loadErr := config.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	var selected credentialConfig
	if json.Unmarshal(reloaded.Providers["cliproxyapi"], &selected) != nil || selected.CredentialID != "old-id" {
		t.Fatalf("existing selector changed: %+v", selected)
	}
	if store.values["old-id"] != "old-secret" {
		t.Fatal("previous keyring secret was changed")
	}
	if len(store.values) != 1 {
		t.Fatalf("failed new credential was not rolled back: %#v", store.values)
	}
}

func TestSavedManagementKeyWorksAfterConfigReload(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(EnvKey, "")
	t.Setenv(EnvURL, "")
	const secret = "stored-management-key"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Errorf("stored key was not loaded for request")
		}
		_, _ = fmt.Fprint(w, `{"files":[]}`)
	}))
	defer server.Close()
	t.Setenv(EnvURL, server.URL)
	store := newFakeStore()
	if err := SaveManagementKey(&config.Config{Providers: map[string]json.RawMessage{}}, secret, store); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	accounts, failures, err := FetchWithConfigAndStore(t.Context(), reloaded, store)
	if err != nil || len(accounts) != 0 || len(failures) != 0 {
		t.Fatalf("fetch=(%v,%v,%v)", accounts, failures, err)
	}
	if calls != 1 {
		t.Fatalf("management requests=%d, want 1", calls)
	}
}
