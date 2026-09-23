package opencode

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

type fakeCredentialStore struct {
	values      map[string]string
	putErr      error
	getErr      error
	getOverride string
}

func newFakeCredentialStore() *fakeCredentialStore {
	return &fakeCredentialStore{values: make(map[string]string)}
}

func (s *fakeCredentialStore) Put(id, value string) error {
	if s.putErr != nil {
		return s.putErr
	}
	s.values[id] = value
	return nil
}

func (s *fakeCredentialStore) Get(id string) (string, error) {
	if s.getErr != nil {
		return "", s.getErr
	}
	if s.getOverride != "" {
		return s.getOverride, nil
	}
	value, ok := s.values[id]
	if !ok {
		return "", errors.New("not found")
	}
	return value, nil
}

func (s *fakeCredentialStore) Delete(id string) error {
	delete(s.values, id)
	return nil
}

func validCookies(value string) []*http.Cookie {
	return []*http.Cookie{{Name: sessionCookieName, Value: value, Domain: "opencode.ai", Path: "/"}}
}

func TestSaveAndLoadCredential(t *testing.T) {
	store := newFakeCredentialStore()
	cookies := validCookies("secret-session")
	id, err := SaveCredential(store, "wrk_account_a", cookies)
	if err != nil {
		t.Fatalf("SaveCredential() error = %v", err)
	}
	if id == "" {
		t.Fatal("SaveCredential() returned empty ID")
	}
	got, err := LoadCredential(store, id, "wrk_account_a")
	if err != nil {
		t.Fatalf("LoadCredential() error = %v", err)
	}
	if len(got) != 1 || got[0].Value != "secret-session" {
		t.Fatalf("loaded cookies = %#v", got)
	}
}

func TestSaveCredentialNormalizesMillisecondExpirationWithoutMutatingCookies(t *testing.T) {
	store := newFakeCredentialStore()
	cookies := validCookies("secret-session")
	cookies[0].Expires = time.Unix(1_700_000_000_123, 0)
	originalExpiration := cookies[0].Expires

	id, err := SaveCredential(store, "wrk_a", cookies)
	if err != nil {
		t.Fatalf("SaveCredential() error = %v", err)
	}
	if !cookies[0].Expires.Equal(originalExpiration) {
		t.Fatal("SaveCredential() mutated the imported cookie expiration")
	}

	loaded, err := LoadCredential(store, id, "wrk_a")
	if err != nil {
		t.Fatalf("LoadCredential() error = %v", err)
	}
	if got := loaded[0].Expires; got.Year() != 2023 || got.Unix() != 1_700_000_000 || got.Nanosecond() != 123_000_000 {
		t.Fatalf("loaded expiration = %v, want 2023 timestamp with 123ms", got)
	}
}

func TestSaveCredentialRejectsUnrepresentableExpirationBeforePut(t *testing.T) {
	store := newFakeCredentialStore()
	cookies := validCookies("secret-session")
	cookies[0].Expires = time.Unix(1_000_000_000_000_000, 0)

	if id, err := SaveCredential(store, "wrk_a", cookies); err == nil || id != "" {
		t.Fatalf("SaveCredential() = (%q, %v), want sanitized error", id, err)
	}
	if len(store.values) != 0 {
		t.Fatalf("store received %d Put(s), want none", len(store.values))
	}
}

func TestSaveCredentialPutFailure(t *testing.T) {
	store := newFakeCredentialStore()
	store.putErr = errors.New("locked")
	if id, err := SaveCredential(store, "wrk_a", validCookies("session")); err == nil || id != "" {
		t.Fatalf("SaveCredential() = (%q, %v), want error", id, err)
	}
}

func TestSaveCredentialGetFailureDoesNotOverwritePreviousID(t *testing.T) {
	store := newFakeCredentialStore()
	oldID, err := SaveCredential(store, "wrk_a", validCookies("old-secret"))
	if err != nil {
		t.Fatal(err)
	}
	oldRecord := store.values[oldID]
	store.getErr = errors.New("keyring unavailable")
	if id, err := SaveCredential(store, "wrk_b", validCookies("new-secret")); err == nil || id != "" {
		t.Fatalf("SaveCredential() = (%q, %v), want verification error", id, err)
	}
	if store.values[oldID] != oldRecord {
		t.Fatal("previous credential was overwritten")
	}
}

func TestSaveCredentialRejectsVerificationMismatch(t *testing.T) {
	store := newFakeCredentialStore()
	store.getOverride = "different value"
	if id, err := SaveCredential(store, "wrk_a", validCookies("secret")); err == nil || id != "" {
		t.Fatalf("SaveCredential() = (%q, %v), want mismatch error", id, err)
	}
}

func TestLoadCredentialFailsClosed(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		workspace string
		getErr    error
	}{
		{name: "missing", workspace: "wrk_a"},
		{name: "locked", workspace: "wrk_a", getErr: errors.New("locked")},
		{name: "corrupt", value: "not-json", workspace: "wrk_a"},
		{name: "workspace mismatch", value: mustRecord(t, 1, "wrk_b", validCookies("secret")), workspace: "wrk_a"},
		{name: "version mismatch", value: mustRecord(t, 2, "wrk_a", validCookies("secret")), workspace: "wrk_a"},
		{name: "no session", value: mustRecord(t, 1, "wrk_a", []*http.Cookie{{Name: "theme", Value: "dark"}}), workspace: "wrk_a"},
		{name: "empty session", value: mustRecord(t, 1, "wrk_a", validCookies("")), workspace: "wrk_a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeCredentialStore()
			store.values["requested-id"] = tt.value
			store.getErr = tt.getErr
			if cookies, err := LoadCredential(store, "requested-id", tt.workspace); err == nil || cookies != nil {
				t.Fatalf("LoadCredential() = (%#v, %v), want failure", cookies, err)
			}
		})
	}
}

func TestCredentialsAreAccountIsolatedWithNoFallback(t *testing.T) {
	store := newFakeCredentialStore()
	idA, err := SaveCredential(store, "wrk_account_a", validCookies("account-a"))
	if err != nil {
		t.Fatal(err)
	}
	idB, err := SaveCredential(store, "wrk_account_b", validCookies("account-b"))
	if err != nil {
		t.Fatal(err)
	}
	if idA == idB {
		t.Fatal("credential IDs are not unique")
	}
	loaded, err := LoadCredential(store, idB, "wrk_account_b")
	if err != nil {
		t.Fatal(err)
	}
	if loaded[0].Value != "account-b" {
		t.Fatalf("loaded account B cookie = %q", loaded[0].Value)
	}
	if _, err := LoadCredential(store, idA, "wrk_account_b"); err == nil {
		t.Fatal("cross-account credential load succeeded")
	}
	store.values[idB] = ""
	if cookies, err := LoadCredential(store, "unknown-id", "wrk_account_b"); err == nil || cookies != nil {
		t.Fatalf("missing requested ID fell back: cookies=%#v err=%v", cookies, err)
	}
}

func TestSaveCredentialRejectsInvalidAuthCookies(t *testing.T) {
	for _, cookies := range [][]*http.Cookie{
		nil,
		{},
		{nil},
		{{Name: "theme", Value: "dark"}},
		{{Name: sessionCookieName, Value: "session", Domain: "other.example"}},
		validCookies(""),
	} {
		if id, err := SaveCredential(newFakeCredentialStore(), "wrk_a", cookies); err == nil || id != "" {
			t.Fatalf("SaveCredential(%#v) = (%q, %v), want invalid cookie error", cookies, id, err)
		}
	}
}

func mustRecord(t *testing.T, version int, workspace string, cookies []*http.Cookie) string {
	t.Helper()
	value, err := json.Marshal(credentialRecord{Version: version, Workspace: workspace, Cookies: cookies})
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}
