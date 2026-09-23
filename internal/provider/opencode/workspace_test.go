package opencode

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"opentracker/internal/fetcher"
)

func TestDetectWorkspaceIDFromCurrentCookiesIgnoresLegacyCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "opentracker")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "opencode-workspace.txt"), []byte("wrk_old"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Server-Id") != workspacesServerID {
			t.Error("missing workspace server ID")
		}
		session, err := r.Cookie("__Host-console_session")
		if err != nil {
			http.Error(w, "no session", http.StatusUnauthorized)
			return
		}
		_, _ = fmt.Fprintf(w, `id:"wrk_%s"`, session.Value)
	}))
	defer server.Close()

	for _, account := range []string{"first", "second"} {
		cookies := []*http.Cookie{{Name: "__Host-console_session", Value: account, Domain: "opencode.ai", Path: "/"}}
		header := fetcher.FromCookies(cookies).CookieHeader("opencode.ai")
		id, err := detectWorkspaceID(header, server.URL, server.Client())
		if err != nil {
			t.Fatal(err)
		}
		if id != "wrk_"+account {
			t.Errorf("session %s resolved %q, want wrk_%s", account, id, account)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "opencode-workspace.txt")); err != nil {
		t.Errorf("legacy cache should not be modified by detection: %v", err)
	}
}

func TestDetectWorkspaceIDRejectsUnauthenticatedSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()
	client := &http.Client{Timeout: time.Second}
	if _, err := detectWorkspaceID("auth=stale", server.URL, client); err == nil {
		t.Fatal("expected error for expired session")
	}
}

func TestIsValidWorkspaceID(t *testing.T) {
	tests := []struct {
		id    string
		valid bool
	}{
		{"wrk_123", true},
		{"wrk_", false},
		{"", false},
		{"abc_123", false},
		{"wrk", false},
		{"wrk_1234567890abcdef", true},
	}

	for _, tt := range tests {
		got := isValidWorkspaceID(tt.id)
		if got != tt.valid {
			t.Errorf("isValidWorkspaceID(%q) = %v, want %v", tt.id, got, tt.valid)
		}
	}
}

func TestExtractWorkspaceID_FromJS(t *testing.T) {
	text := `somePrefix id: "wrk_abc123", otherField`
	got := extractWorkspaceID(text)
	if got != "wrk_abc123" {
		t.Errorf("extractWorkspaceID = %q, want wrk_abc123", got)
	}
}

func TestExtractWorkspaceID_FromPath(t *testing.T) {
	text := `href="/workspace/wrk_xyz999/settings"`
	got := extractWorkspaceID(text)
	if got != "wrk_xyz999" {
		t.Errorf("extractWorkspaceID = %q, want wrk_xyz999", got)
	}
}

func TestExtractWorkspaceID_NotFound(t *testing.T) {
	text := `no workspace id here`
	got := extractWorkspaceID(text)
	if got != "" {
		t.Errorf("extractWorkspaceID = %q, want empty string", got)
	}
}
