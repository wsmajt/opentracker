package browsercookies

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestSecureOpenCodeCookieFileUpgradesExistingPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "opentracker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "opencode-cookies.txt")
	if err := os.WriteFile(path, []byte("legacy data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SecureOpenCodeCookieFile(path); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{{dir, 0o700}, {path, 0o600}} {
		info, err := os.Stat(item.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != item.mode {
			t.Errorf("%s mode = %o, want %o", item.path, got, item.mode)
		}
	}
}

func TestSecureOpenCodeCookieFileRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("do-not-touch"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "opencode-cookies.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := SecureOpenCodeCookieFile(link); err == nil {
		t.Fatal("expected symlink cookie file to be rejected")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "do-not-touch" {
		t.Fatalf("symlink target changed: %q, %v", data, err)
	}
}

func TestIsRequiredOpenCodeSessionCookie(t *testing.T) {
	for _, cookie := range []*http.Cookie{
		{Name: "auth", Value: "auth-secret", Domain: "opencode.ai"},
		{Name: "__Host-auth", Value: "auth-secret", Domain: "opencode.ai"},
		{Name: "__Host-console_session", Domain: "opencode.ai"},
		{Name: "__Host-console_session", Value: "secret", Domain: "example.com"},
		{Name: "oc_locale", Value: "en", Domain: "opencode.ai"},
	} {
		if isRequiredOpenCodeSessionCookie(cookie) {
			t.Errorf("%#v should not count as the required session cookie", cookie)
		}
	}
	if !isRequiredOpenCodeSessionCookie(&http.Cookie{Name: "__Host-console_session", Value: "secret", Domain: ".opencode.ai"}) {
		t.Error("nonempty console session cookie scoped to opencode.ai should be accepted")
	}
}

func TestFirstProfileWithConsoleSessionSkipsAuthOnlyProfile(t *testing.T) {
	profiles := [][]*http.Cookie{
		{{Name: "auth", Value: "auth-secret", Domain: "opencode.ai"}},
		{{Name: "__Host-console_session", Value: "console-secret", Domain: "opencode.ai"}},
	}
	if got := firstProfileWithConsoleSession(profiles); got != 1 {
		t.Fatalf("firstProfileWithConsoleSession() = %d, want later profile index 1", got)
	}
}
