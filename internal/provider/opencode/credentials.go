package opencode

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	credentialService       = "opentracker"
	credentialUserPrefix    = "opencode:"
	credentialRecordVersion = 1
	sessionCookieName       = "__Host-console_session"
)

// CredentialStore is the minimal storage interface required for credentials.
type CredentialStore interface {
	Put(id, value string) error
	Get(id string) (string, error)
	Delete(id string) error
}

type keyringCredentialStore struct{}

// NewCredentialStore returns the OS-keyring-backed OpenCode credential store.
func NewCredentialStore() CredentialStore { return keyringCredentialStore{} }

func credentialUser(id string) string { return credentialUserPrefix + id }

func (keyringCredentialStore) Put(id, value string) error {
	return keyring.Set(credentialService, credentialUser(id), value)
}

func (keyringCredentialStore) Get(id string) (string, error) {
	return keyring.Get(credentialService, credentialUser(id))
}

func (keyringCredentialStore) Delete(id string) error {
	return keyring.Delete(credentialService, credentialUser(id))
}

type credentialRecord struct {
	Version   int            `json:"version"`
	Workspace string         `json:"workspace"`
	Cookies   []*http.Cookie `json:"cookies"`
}

// SaveCredential stores cookies as a versioned record under a fresh, immutable ID.
// It verifies the stored value before returning the ID. A failed verification may
// leave an orphaned keyring entry, but never modifies a previously issued ID.
func SaveCredential(store CredentialStore, workspace string, cookies []*http.Cookie) (string, error) {
	if store == nil {
		return "", errors.New("credential store is nil")
	}
	if workspace == "" || !hasUsableSessionCookie(cookies) {
		return "", errors.New("invalid OpenCode credentials")
	}

	normalizedCookies, err := normalizeCredentialCookies(cookies)
	if err != nil {
		return "", err
	}
	record := credentialRecord{Version: credentialRecordVersion, Workspace: workspace, Cookies: normalizedCookies}
	encoded, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("cannot encode OpenCode credentials: %w", err)
	}

	rawID := make([]byte, 32)
	if _, err := rand.Read(rawID); err != nil {
		return "", fmt.Errorf("cannot generate credential ID: %w", err)
	}
	id := hex.EncodeToString(rawID)
	if err := store.Put(id, string(encoded)); err != nil {
		return "", errors.New("cannot save OpenCode credentials")
	}
	stored, err := store.Get(id)
	if err != nil || stored != string(encoded) {
		return "", errors.New("cannot verify saved OpenCode credentials")
	}
	return id, nil
}

func normalizeCredentialCookies(cookies []*http.Cookie) ([]*http.Cookie, error) {
	normalized := make([]*http.Cookie, len(cookies))
	for i, cookie := range cookies {
		if cookie == nil {
			continue
		}
		clone := *cookie
		if !clone.Expires.IsZero() && (clone.Expires.Year() < 0 || clone.Expires.Year() > 9999) {
			unix := clone.Expires.Unix()
			if clone.Expires.Year() <= 9999 || unix < 1e12 || unix >= 1e13 {
				return nil, errors.New("OpenCode credential cookie has an unrepresentable expiration")
			}
			clone.Expires = time.Unix(unix/1000, (unix%1000)*int64(time.Millisecond)).UTC()
		}
		normalized[i] = &clone
	}
	return normalized, nil
}

// LoadCredential retrieves credentials only when their record and workspace
// match. It never falls back to another ID or credential source.
func LoadCredential(store CredentialStore, id, workspace string) ([]*http.Cookie, error) {
	if store == nil || id == "" || workspace == "" {
		return nil, errors.New("invalid OpenCode credential reference")
	}
	encoded, err := store.Get(id)
	if err != nil {
		return nil, errors.New("OpenCode credentials are unavailable")
	}
	var record credentialRecord
	if err := json.Unmarshal([]byte(encoded), &record); err != nil {
		return nil, errors.New("stored OpenCode credentials are malformed")
	}
	if record.Version != credentialRecordVersion {
		return nil, errors.New("stored OpenCode credentials have an unsupported version")
	}
	if record.Workspace != workspace {
		return nil, errors.New("stored OpenCode credentials belong to a different workspace")
	}
	if !hasUsableSessionCookie(record.Cookies) {
		return nil, errors.New("stored OpenCode credentials have no usable session cookie")
	}
	return record.Cookies, nil
}

func hasUsableSessionCookie(cookies []*http.Cookie) bool {
	for _, cookie := range cookies {
		if cookie != nil && cookie.Name == sessionCookieName && cookie.Domain == "opencode.ai" && cookie.Value != "" {
			return true
		}
	}
	return false
}
