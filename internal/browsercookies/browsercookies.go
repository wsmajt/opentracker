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

	for _, store := range stores {
		defer store.Close()

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
		hasAuth := false

		for cookie := range cookiesSeq {
			if cookie == nil {
				continue
			}

			if !domainMatches(cookie.Domain, cookieDomains) {
				continue
			}

			cookies = append(cookies, &cookie.Cookie)
			if cookie.Name == "auth" || cookie.Name == "__Host-auth" {
				hasAuth = true
			}
		}

		if len(cookies) > 0 {
			if logger != nil {
				logger(fmt.Sprintf("  Found %d cookies for opencode.ai", len(cookies)))
			}
			if hasAuth {
				if logger != nil {
					logger(fmt.Sprintf("  Found auth cookie in %s", source))
				}
				return cookies, source, nil
			}
			if logger != nil {
				logger(fmt.Sprintf("  Skipping %s: missing auth cookie", source))
			}
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

		cookies, hasAuth := readFirefoxStore(ctx, path, logger)
		if len(cookies) > 0 && hasAuth {
			if logger != nil {
				logger(fmt.Sprintf("Found auth cookie in %s", source))
			}
			return cookies, source, nil
		}
		if len(cookies) > 0 {
			if logger != nil {
				logger(fmt.Sprintf("Found %d cookies but no auth in %s", len(cookies), source))
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

		cookies, hasAuth := readFirefoxStore(ctx, path, logger)
		if len(cookies) > 0 && hasAuth {
			if logger != nil {
				logger(fmt.Sprintf("Found auth cookie in %s", source))
			}
			return cookies, source, nil
		}
		if len(cookies) > 0 {
			if logger != nil {
				logger(fmt.Sprintf("Found %d cookies but no auth in %s", len(cookies), source))
			}
		}
	}

	return nil, "", fmt.Errorf("no valid OpenCode cookies in Firefox")
}

func readFirefoxStore(ctx context.Context, path string, logger func(string)) ([]*http.Cookie, bool) {
	cookiesSeq := firefox.TraverseCookies(path, kooky.Valid, kooky.DomainContains("opencode.ai")).OnlyCookies()
	var cookies []*http.Cookie
	hasAuth := false

	for cookie := range cookiesSeq {
		if cookie == nil {
			continue
		}

		if !domainMatches(cookie.Domain, cookieDomains) {
			continue
		}

		cookies = append(cookies, &cookie.Cookie)
		if cookie.Name == "auth" || cookie.Name == "__Host-auth" {
			hasAuth = true
		}
	}

	return cookies, hasAuth
}

// SaveOpenCodeCookies persists cookies to the Netscape cookie file used by opentracker.
func SaveOpenCodeCookies(cookies []*http.Cookie) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	dir := filepath.Join(home, ".config", "opentracker")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create config directory: %w", err)
	}

	path := filepath.Join(dir, "opencode-cookies.txt")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("cannot open cookie file: %w", err)
	}
	defer f.Close()

	kooky.ExportCookies(context.Background(), f, cookies)
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
