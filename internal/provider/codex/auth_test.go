package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCodexAuth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	data := `{"tokens":{"access_token":"access-token","account_id":"account-123"}}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	auth, err := loadCodexAuth(path)
	if err != nil {
		t.Fatalf("loadCodexAuth() error = %v", err)
	}
	if auth.AccessToken != "access-token" {
		t.Fatalf("AccessToken = %q, want access-token", auth.AccessToken)
	}
	if auth.AccountID != "account-123" {
		t.Fatalf("AccountID = %q, want account-123", auth.AccountID)
	}
	if auth.Source != "codex-oauth" {
		t.Fatalf("Source = %q, want codex-oauth", auth.Source)
	}
}

func TestLoadCodexAuthMissingToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(`{"tokens":{}}`), 0600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	if _, err := loadCodexAuth(path); err == nil {
		t.Fatal("loadCodexAuth() error = nil, want error")
	}
}
