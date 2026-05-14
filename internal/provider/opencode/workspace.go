package opencode

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"opentracker/internal/fetcher"
)

const workspaceCacheFile = "opencode-workspace.txt"

// DetectWorkspaceID tries to find the workspace ID automatically.
// It first checks the cache file, then fetches https://opencode.ai/go and scans the response.
func DetectWorkspaceID(cookieFile string) (string, error) {
	// 1. Check cache
	home, err := os.UserHomeDir()
	if err == nil {
		cachePath := filepath.Join(home, ".config", "opentracker", workspaceCacheFile)
		if data, err := os.ReadFile(cachePath); err == nil {
			id := string(data)
			if isValidWorkspaceID(id) {
				return id, nil
			}
		}
	}

	// 2. Fetch /go page
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	f, err := fetcher.New(cookieFile)
	if err != nil {
		return "", fmt.Errorf("cannot create fetcher: %w", err)
	}

	resp, err := f.Get(ctx, "https://opencode.ai/go", map[string]string{
		"User-Agent": "Mozilla/5.0 (X11; Linux x86_64; rv:150.0) Gecko/20100101 Firefox/150.0",
	})
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return "", fmt.Errorf("session expired; run 'opentracker login opencode'")
		}
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("cannot read body: %w", err)
	}

	// 3. Extract workspace ID
	html := string(body)
	id := extractWorkspaceID(html)
	if id == "" {
		return "", fmt.Errorf("no workspace ID found; ensure you're logged in to opencode.ai")
	}

	// 4. Save to cache
	if home != "" {
		dir := filepath.Join(home, ".config", "opentracker")
		os.MkdirAll(dir, 0o755)
		cachePath := filepath.Join(dir, workspaceCacheFile)
		os.WriteFile(cachePath, []byte(id), 0o644)
	}

	return id, nil
}

func isValidWorkspaceID(id string) bool {
	return id != "" && len(id) > 4 && id[:4] == "wrk_"
}

func extractWorkspaceID(html string) string {
	// Try embedded JS first (most reliable)
	if m := regexp.MustCompile(`id\s*:\s*"(wrk_[^"]+)"`).FindStringSubmatch(html); m != nil {
		return m[1]
	}
	// Try URL path
	if m := regexp.MustCompile(`/workspace/(wrk_[A-Za-z0-9]+)`).FindStringSubmatch(html); m != nil {
		return m[1]
	}
	return ""
}
