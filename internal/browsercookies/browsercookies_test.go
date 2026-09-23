package browsercookies

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveOpenCodeCookiesPrivateAndReplacesOldSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "opentracker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "opencode-cookies.txt")
	if err := os.WriteFile(path, []byte("old-session-secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	cookies := []*http.Cookie{{Name: "__Host-console_session", Value: "new-session-secret", Domain: "opencode.ai", Path: "/", Secure: true}}
	if err := SaveOpenCodeCookies(cookies); err != nil {
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
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "new-session-secret") || strings.Contains(string(data), "old-session-secret") {
		t.Fatal("saved cookies did not replace the old session")
	}
}

func TestSecureOpenCodeCookieFileUpgradesExistingPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "opentracker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "opencode-cookies.txt")
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
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

func TestIsOpenCodeSessionCookie(t *testing.T) {
	for _, name := range []string{"auth", "__Host-auth", "__Host-console_session"} {
		if !isOpenCodeSessionCookie(name) {
			t.Errorf("%q should count as a session cookie", name)
		}
	}
	if isOpenCodeSessionCookie("oc_locale") {
		t.Error("locale is not a session cookie")
	}
}
