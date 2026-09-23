package opencode

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"opentracker/internal/browsercookies"
	"opentracker/internal/fetcher"
)

const workspacesServerID = "def39973159c7f0483d8793a822b8dbb10d067e12c65455fcb4608459ba0234f"

// DetectWorkspaceID resolves the workspace from the current cookie file.
// Never trust the legacy opencode-workspace.txt cache: it belongs to a prior login.
func DetectWorkspaceID(cookieFile string) (string, error) {
	if err := browsercookies.SecureOpenCodeCookieFile(cookieFile); err != nil {
		return "", err
	}
	f, err := fetcher.New(cookieFile)
	if err != nil {
		return "", fmt.Errorf("cannot create fetcher: %w", err)
	}
	return detectWorkspaceID(f.CookieHeader("opencode.ai"),
		"https://opencode.ai/_server?id="+workspacesServerID,
		&http.Client{Timeout: 15 * time.Second})
}

// DetectWorkspaceIDFromCookies verifies a freshly imported browser session
// before replacing the cookies and workspace belonging to the previous login.
func DetectWorkspaceIDFromCookies(cookies []*http.Cookie) (string, error) {
	f := fetcher.FromCookies(cookies)
	return detectWorkspaceID(f.CookieHeader("opencode.ai"),
		"https://opencode.ai/_server?id="+workspacesServerID,
		&http.Client{Timeout: 15 * time.Second})
}

func detectWorkspaceID(cookieHeader, endpoint string, client *http.Client) (string, error) {
	if cookieHeader == "" {
		return "", fmt.Errorf("no cookies found for opencode.ai; run 'opentracker login opencode'")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
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

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

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

	id := extractWorkspaceID(text)
	if id == "" {
		return "", fmt.Errorf("no workspace ID found in API response")
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
