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

const (
	workspacesServerID = "def39973159c7f0483d8793a822b8dbb10d067e12c65455fcb4608459ba0234f"
	workspaceCacheFile = "opencode-workspace.txt"
)

// DetectWorkspaceID tries to find the workspace ID via the OpenCode API.
// It first checks the cache file, then calls the workspaces endpoint.
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

	// 2. Load cookies
	f, err := fetcher.New(cookieFile)
	if err != nil {
		return "", fmt.Errorf("cannot create fetcher: %w", err)
	}

	cookieHeader := f.CookieHeader("opencode.ai")
	if cookieHeader == "" {
		return "", fmt.Errorf("no cookies found for opencode.ai; run 'opentracker login opencode'")
	}

	// 3. Call API endpoint
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	url := "https://opencode.ai/_server?id=" + workspacesServerID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("cannot create request: %w", err)
	}

	req.Header.Set("Cookie", cookieHeader)
	req.Header.Set("X-Server-Id", workspacesServerID)
	req.Header.Set("X-Server-Instance", "server-fn:123e4567-e89b-12d3-a456-426614174000")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:150.0) Gecko/20100101 Firefox/150.0")
	req.Header.Set("Origin", "https://opencode.ai")
	req.Header.Set("Referer", "https://opencode.ai")
	req.Header.Set("Accept", "text/javascript, application/json;q=0.9, */*;q=0.8")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("cannot read body: %w", err)
	}

	text := string(body)

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return "", fmt.Errorf("session expired; run 'opentracker login opencode'")
		}
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// 4. Extract workspace ID
	id := extractWorkspaceID(text)
	if id == "" {
		return "", fmt.Errorf("no workspace ID found in API response")
	}

	// 5. Save to cache
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

func extractWorkspaceID(text string) string {
	// Try embedded JS first (most reliable)
	if m := regexp.MustCompile(`id\s*:\s*"(wrk_[^"]+)"`).FindStringSubmatch(text); m != nil {
		return m[1]
	}
	// Try URL path
	if m := regexp.MustCompile(`/workspace/(wrk_[A-Za-z0-9]+)`).FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}
