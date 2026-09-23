package browsercookies

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/browserutils/kooky"
	_ "github.com/browserutils/kooky/browser/all" // register cookie store finders
	"github.com/browserutils/kooky/browser/firefox"
)

var cookieDomains = []string{"opencode.ai", "app.opencode.ai"}

// ImportOpenCode searches installed browsers for valid OpenCode session cookies.
// It returns the cookies, a source label, and an error.
func ImportOpenCode(ctx context.Context, logger func(string)) ([]*http.Cookie, string, error) {
	if logger != nil {
		logger("Scanning browsers for OpenCode cookies...")
	}

	// Phase 1: Auto-discovery via kooky (Chrome, Firefox, Edge, etc.)
	cookies, source, err := importViaKooky(ctx, logger)
	if err == nil {
		return cookies, source, nil
	}
	if logger != nil {
		logger(fmt.Sprintf("Auto-discovery failed: %v", err))
	}

	// Phase 2: Fallback to Zen Browser paths
	cookies, source, err = importViaZenFallback(ctx, logger)
	if err == nil {
		return cookies, source, nil
	}
	if logger != nil {
		logger(fmt.Sprintf("Zen fallback failed: %v", err))
	}

	// Phase 3: Fallback to Firefox paths
	cookies, source, err = importViaFirefoxFallback(ctx, logger)
	if err == nil {
		return cookies, source, nil
	}
	if logger != nil {
		logger(fmt.Sprintf("Firefox fallback failed: %v", err))
	}

	return nil, "", fmt.Errorf("no OpenCode session cookies found in any browser")
}

func importViaKooky(ctx context.Context, logger func(string)) ([]*http.Cookie, string, error) {
	stores := kooky.FindAllCookieStores(ctx)
	if len(stores) == 0 {
		return nil, "", fmt.Errorf("no browser cookie stores found")
	}

	profiles := make([][]*http.Cookie, 0, len(stores))
	sources := make([]string, 0, len(stores))
	for _, store := range stores {
		defer func() { _ = store.Close() }()

		browserName := store.Browser()
		profile := store.Profile()
		filePath := store.FilePath()

		source := browserName
		if profile != "" {
			source += " (" + profile + ")"
		}

		if logger != nil {
			logger(fmt.Sprintf("Checking %s at %s", source, filePath))
		}

		cookiesSeq := store.TraverseCookies(kooky.Valid, kooky.DomainContains("opencode.ai")).OnlyCookies()
		var cookies []*http.Cookie

		for cookie := range cookiesSeq {
			if cookie == nil {
				continue
			}

			if !domainMatches(cookie.Domain, cookieDomains) {
				continue
			}

			cookies = append(cookies, &cookie.Cookie)
		}

		if len(cookies) > 0 {
			if logger != nil {
				logger(fmt.Sprintf("  Found %d cookies for opencode.ai", len(cookies)))
			}
			profiles = append(profiles, cookies)
			sources = append(sources, source)
		}
	}

	if index := firstProfileWithConsoleSession(profiles); index >= 0 {
		if logger != nil {
			logger(fmt.Sprintf("  Found __Host-console_session cookie in %s", sources[index]))
		}
		return profiles[index], sources[index], nil
	}
	if logger != nil {
		for _, source := range sources {
			logger(fmt.Sprintf("  Skipping %s: missing nonempty __Host-console_session cookie", source))
		}
	}

	return nil, "", fmt.Errorf("no valid OpenCode cookies in auto-discovered browsers")
}

func importViaZenFallback(ctx context.Context, logger func(string)) ([]*http.Cookie, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", fmt.Errorf("cannot find home dir: %w", err)
	}

	pattern := filepath.Join(home, ".config", "zen", "*", "cookies.sqlite")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, "", fmt.Errorf("glob error: %w", err)
	}

	if len(matches) == 0 {
		return nil, "", fmt.Errorf("no Zen cookie stores found")
	}

	for _, path := range matches {
		profile := filepath.Base(filepath.Dir(path))
		source := "zen (" + profile + ")"

		if logger != nil {
			logger(fmt.Sprintf("Trying Zen fallback: %s", source))
		}

		cookies, hasSession := readFirefoxStore(ctx, path, logger)
		if len(cookies) > 0 && hasSession {
			if logger != nil {
				logger(fmt.Sprintf("Found __Host-console_session cookie in %s", source))
			}
			return cookies, source, nil
		}
		if len(cookies) > 0 {
			if logger != nil {
				logger(fmt.Sprintf("Found %d cookies but no nonempty __Host-console_session in %s", len(cookies), source))
			}
		}
	}

	return nil, "", fmt.Errorf("no valid OpenCode cookies in Zen")
}

func importViaFirefoxFallback(ctx context.Context, logger func(string)) ([]*http.Cookie, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", fmt.Errorf("cannot find home dir: %w", err)
	}

	pattern := filepath.Join(home, ".mozilla", "firefox", "*", "cookies.sqlite")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, "", fmt.Errorf("glob error: %w", err)
	}

	if len(matches) == 0 {
		return nil, "", fmt.Errorf("no Firefox cookie stores found")
	}

	for _, path := range matches {
		profile := filepath.Base(filepath.Dir(path))
		source := "firefox (" + profile + ")"

		if logger != nil {
			logger(fmt.Sprintf("Trying Firefox fallback: %s", source))
		}

		cookies, hasSession := readFirefoxStore(ctx, path, logger)
		if len(cookies) > 0 && hasSession {
			if logger != nil {
				logger(fmt.Sprintf("Found __Host-console_session cookie in %s", source))
			}
			return cookies, source, nil
		}
		if len(cookies) > 0 {
			if logger != nil {
				logger(fmt.Sprintf("Found %d cookies but no nonempty __Host-console_session in %s", len(cookies), source))
			}
		}
	}

	return nil, "", fmt.Errorf("no valid OpenCode cookies in Firefox")
}

func readFirefoxStore(ctx context.Context, path string, logger func(string)) ([]*http.Cookie, bool) {
	cookiesSeq := firefox.TraverseCookies(path, kooky.Valid, kooky.DomainContains("opencode.ai")).OnlyCookies()
	var cookies []*http.Cookie
	hasSession := false

	for cookie := range cookiesSeq {
		if cookie == nil {
			continue
		}

		if !domainMatches(cookie.Domain, cookieDomains) {
			continue
		}

		cookies = append(cookies, &cookie.Cookie)
		if isRequiredOpenCodeSessionCookie(&cookie.Cookie) {
			hasSession = true
		}
	}

	return cookies, hasSession
}

func isRequiredOpenCodeSessionCookie(cookie *http.Cookie) bool {
	return cookie != nil && cookie.Name == "__Host-console_session" && cookie.Value != "" && domainMatches(cookie.Domain, cookieDomains)
}

func firstProfileWithConsoleSession(profiles [][]*http.Cookie) int {
	for i, cookies := range profiles {
		for _, cookie := range cookies {
			if isRequiredOpenCodeSessionCookie(cookie) {
				return i
			}
		}
	}
	return -1
}

// SecureOpenCodeCookieFile restricts the config directory and an existing
// cookie file to the current user. It also upgrades permissions from older
// OpenTracker releases before the file is read or replaced.
func SecureOpenCodeCookieFile(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create cookie directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("cookie directory %q is not a directory", dir)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("cannot secure cookie directory: %w", err)
	}
	info, err = os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("cookie file %q is not a regular file", path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("cannot secure cookie file: %w", err)
	}
	return nil
}

func domainMatches(domain string, candidates []string) bool {
	domain = strings.ToLower(domain)
	for _, cand := range candidates {
		cand = strings.ToLower(cand)
		if domain == cand || strings.HasSuffix(domain, "."+cand) {
			return true
		}
	}
	return false
}
