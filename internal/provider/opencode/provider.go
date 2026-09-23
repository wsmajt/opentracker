package opencode

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"opentracker/internal/config"
	"opentracker/internal/fetcher"
	"opentracker/internal/provider"
)

func init() {
	provider.Register("opencode-zen", func(c *config.Config) (provider.Provider, error) {
		return NewProvider(c, "zen")
	})
	provider.Register("opencode-go", func(c *config.Config) (provider.Provider, error) {
		return NewProvider(c, "go")
	})
}

// OpenCodeProvider implements provider.Provider for opencode.ai.
type OpenCodeProvider struct {
	cfg     *OpenCodeConfig
	fetcher *fetcher.Fetcher
	plan    string
}

// NewProvider creates a new OpenCode provider for the given plan.
func NewProvider(appCfg *config.Config, plan string) (provider.Provider, error) {
	return newProvider(appCfg, plan, NewCredentialStore())
}

func newProvider(appCfg *config.Config, plan string, store CredentialStore) (provider.Provider, error) {
	return newProviderWithWorkspaceLister(appCfg, plan, store, ListWorkspaceIDsFromCookies)
}

func newProviderWithWorkspaceLister(appCfg *config.Config, plan string, store CredentialStore, listWorkspaces func([]*http.Cookie) ([]string, error)) (provider.Provider, error) {
	raw, ok := appCfg.Providers["opencode"]
	if !ok {
		return nil, fmt.Errorf("opencode not configured")
	}

	cfg, err := ParseConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid opencode config: %w", err)
	}

	var cookies []*http.Cookie
	if cfg.CredentialID != "" {
		if cfg.Workspace == "" {
			return nil, fmt.Errorf("opencode workspace not set; run 'opentracker login opencode'")
		}
		cookies, err = LoadCredential(store, cfg.CredentialID, cfg.Workspace)
		if err != nil {
			return nil, err
		}
		cleanupResidualLegacyCookieFile()
	} else {
		cookies, err = migrateLegacyCookies(appCfg, cfg, store, listWorkspaces)
		if err != nil {
			return nil, err
		}
	}
	f := fetcher.FromCookies(cookies)

	return &OpenCodeProvider{
		cfg:     cfg,
		fetcher: f,
		plan:    plan,
	}, nil
}

// Name returns the provider name.
func (o *OpenCodeProvider) Name() string {
	return "opencode-" + o.plan
}

// Fetch downloads the usage payload from the OpenCode console API.
// The console is a client-side SPA since the site redesign, so the data is
// read from the same JSON API the console itself uses (session cookies plus
// the x-org-id workspace header).
func (o *OpenCodeProvider) Fetch(ctx context.Context) (string, error) {
	url := o.apiURL()
	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (X11; Linux x86_64; rv:150.0) Gecko/20100101 Firefox/150.0",
		"Accept":     "application/json",
		"x-org-id":   o.cfg.Workspace,
	}

	const maxAttempts = 3
	backoff := 500 * time.Millisecond

	statusCode := 0
	var body []byte
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		code, data, err := o.get(ctx, url, headers)
		if err != nil {
			return "", fmt.Errorf("fetch failed: %w", err)
		}
		statusCode, body = code, data

		if statusCode == http.StatusOK || !isRetryableStatus(statusCode) || attempt == maxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 3
	}

	if statusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", statusCode)
	}

	payload := string(body)
	if !isValidResponse(payload) {
		return "", fmt.Errorf("session expired or no usage data found; run 'opentracker login %s'", o.Name())
	}

	return payload, nil
}

func (o *OpenCodeProvider) get(ctx context.Context, url string, headers map[string]string) (int, []byte, error) {
	resp, err := o.fetcher.Get(ctx, url, headers)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("cannot read body: %w", err)
	}
	return resp.StatusCode, body, nil
}

// apiURL returns the console API endpoint with usage data for the plan.
func (o *OpenCodeProvider) apiURL() string {
	if o.plan == "zen" {
		return "https://opencode.ai/console/api/billing/status"
	}
	return "https://opencode.ai/console/api/go/status"
}

func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func isValidResponse(payload string) bool {
	if len(payload) == 0 {
		return false
	}
	if containsAny(payload, []string{
		`"meters"`,
		`usedMicroCents`,
		`balanceMicroCents`,
		`billingMode`,
		`rollingUsage`,
		`weeklyUsage`,
		`usagePercent`,
	}) {
		return true
	}
	// Legacy server-rendered page markers.
	if containsAny(payload, []string{
		`data-slot="usage-item"`,
	}) {
		return true
	}
	return containsAny(payload, []string{"id:", "wrk_"})
}

func containsAny(s string, substrs []string) bool {
	for _, sub := range substrs {
		if contains(s, sub) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || findSubstr(s, substr)))
}

func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Parse extracts usage data from the fetched payload. The console API returns
// JSON; legacy server-rendered HTML pages are still handled as a fallback.
func (o *OpenCodeProvider) Parse(payload string) (interface{}, error) {
	if strings.HasPrefix(strings.TrimSpace(payload), "{") {
		if o.plan == "zen" {
			return ParseZenBillingJSON(payload)
		}
		return ParseGoStatusJSON(payload)
	}
	return ParseHTML(payload)
}
