package fetcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDomainMatches_Exact(t *testing.T) {
	if !domainMatches("opencode.ai", "opencode.ai") {
		t.Error("expected exact match")
	}
}

func TestDomainMatches_Wildcard(t *testing.T) {
	if !domainMatches("app.opencode.ai", ".opencode.ai") {
		t.Error("expected wildcard match for subdomain")
	}
	if domainMatches("other.com", ".opencode.ai") {
		t.Error("expected no match for different domain")
	}
}

func TestDomainMatches_NoMatch(t *testing.T) {
	if domainMatches("opencode.ai", "other.com") {
		t.Error("expected no match")
	}
}

func TestLoadNetscapeCookies(t *testing.T) {
	// Netscape format: domain flag path secure expiration name value
	content := `# Netscape HTTP Cookie File
# This is a generated file! Do not edit.

opencode.ai	FALSE	/	TRUE	1893456000	auth	token123
.app.opencode.ai	TRUE	/	FALSE	1893456000	session	abc456
#HttpOnly_.opencode.ai	FALSE	/	TRUE	1893456000	host_auth	xyz789
`

	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	cookies, err := loadNetscapeCookies(path)
	if err != nil {
		t.Fatalf("loadNetscapeCookies failed: %v", err)
	}

	if len(cookies) != 3 {
		t.Fatalf("expected 3 cookies, got %d", len(cookies))
	}

	names := make(map[string]string)
	for _, c := range cookies {
		names[c.Name] = c.Value
	}

	if names["auth"] != "token123" {
		t.Errorf("auth cookie = %q, want token123", names["auth"])
	}
	if names["session"] != "abc456" {
		t.Errorf("session cookie = %q, want abc456", names["session"])
	}
	if names["host_auth"] != "xyz789" {
		t.Errorf("host_auth cookie = %q, want xyz789", names["host_auth"])
	}
}

func TestLoadNetscapeCookies_MissingFile(t *testing.T) {
	cookies, err := loadNetscapeCookies("/nonexistent/path/cookies.txt")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if len(cookies) != 0 {
		t.Errorf("expected 0 cookies, got %d", len(cookies))
	}
}

func TestCookieHeader(t *testing.T) {
	content := `opencode.ai	FALSE	/	TRUE	1893456000	auth	token123
.app.opencode.ai	TRUE	/	FALSE	1893456000	session	abc456
other.com	FALSE	/	TRUE	1893456000	other	val
`

	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	f, err := New(path)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	header := f.CookieHeader("opencode.ai")
	if header == "" {
		t.Fatal("expected non-empty cookie header")
	}
	// opencode.ai matches exact domain; .app.opencode.ai is a separate subdomain scope
	if header != "auth=token123" {
		t.Errorf("cookie header = %q, want auth=token123", header)
	}

	headerApp := f.CookieHeader("app.opencode.ai")
	if headerApp != "session=abc456" {
		t.Errorf("app.opencode.ai cookie header = %q, want session=abc456", headerApp)
	}

	headerOther := f.CookieHeader("other.com")
	if headerOther != "other=val" {
		t.Errorf("other.com cookie header = %q, want other=val", headerOther)
	}
}
