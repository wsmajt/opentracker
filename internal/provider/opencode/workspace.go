package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const workspaceListURL = "https://opencode.ai/console/api/orgs"
const maxWorkspaceResponseSize = 1 << 20

// ListWorkspaceIDsFromCookies lists organizations available to an OpenCode
// console session without relying on the possibly stale configured workspace.
func ListWorkspaceIDsFromCookies(cookies []*http.Cookie) ([]string, error) {
	return listWorkspaceIDs(cookies, workspaceListURL, &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	})
}

func listWorkspaceIDs(cookies []*http.Cookie, endpoint string, client *http.Client) ([]string, error) {
	var session *http.Cookie
	for _, cookie := range cookies {
		if cookie != nil && cookie.Name == "__Host-console_session" && cookie.Value != "" && cookie.Domain == "opencode.ai" {
			session = cookie
			break
		}
	}
	if session == nil {
		return nil, fmt.Errorf("no OpenCode console session cookie found; run 'opentracker login opencode'")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot create workspace request: %w", err)
	}
	req.AddCookie(&http.Cookie{Name: session.Name, Value: session.Value})
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("workspace API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("session expired; run 'opentracker login opencode'")
		}
		return nil, fmt.Errorf("workspace API returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWorkspaceResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read workspace API response: %w", err)
	}
	if len(body) > maxWorkspaceResponseSize {
		return nil, fmt.Errorf("workspace API response exceeds %d bytes", maxWorkspaceResponseSize)
	}
	var entries []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("malformed workspace API response: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("workspace API returned no organizations")
	}
	seen := make(map[string]struct{}, len(entries))
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !isValidWorkspaceID(entry.ID) && !isValidOrganizationID(entry.ID) {
			return nil, fmt.Errorf("workspace API returned invalid organization ID %q", entry.ID)
		}
		if _, ok := seen[entry.ID]; !ok {
			seen[entry.ID] = struct{}{}
			ids = append(ids, entry.ID)
		}
	}
	return ids, nil
}

// DetectWorkspaceIDFromCookies is retained for compatibility. It can only
// return a workspace when the session has exactly one unambiguous result.
func DetectWorkspaceIDFromCookies(cookies []*http.Cookie) (string, error) {
	ids, err := ListWorkspaceIDsFromCookies(cookies)
	return singleWorkspaceID(ids, err)
}

func singleWorkspaceID(ids []string, err error) (string, error) {
	if err != nil {
		return "", err
	}
	if len(ids) != 1 {
		return "", fmt.Errorf("multiple OpenCode workspaces found; select a workspace by logging in again")
	}
	return ids[0], nil
}

func isValidWorkspaceID(id string) bool {
	return validOrganizationIDPrefix(id, "wrk_")
}

func isValidOrganizationID(id string) bool {
	return validOrganizationIDPrefix(id, "org_")
}

func validOrganizationIDPrefix(id, prefix string) bool {
	if !strings.HasPrefix(id, prefix) || len(id) == len(prefix) {
		return false
	}
	for _, r := range id[len(prefix):] {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
