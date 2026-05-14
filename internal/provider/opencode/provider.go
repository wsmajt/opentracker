package opencode

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

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
	cfg        *OpenCodeConfig
	fetcher    *fetcher.Fetcher
	cookieFile string
	plan       string
}

// NewProvider creates a new OpenCode provider for the given plan.
func NewProvider(appCfg *config.Config, plan string) (provider.Provider, error) {
	raw, ok := appCfg.Providers["opencode"]
	if !ok {
		return nil, fmt.Errorf("opencode not configured")
	}

	cfg, err := ParseConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid opencode config: %w", err)
	}

	if cfg.Workspace == "" {
		return nil, fmt.Errorf("opencode workspace not set; run 'opentracker login opencode'")
	}

	home, _ := os.UserHomeDir()
	cookieFile := filepath.Join(home, ".config", "opentracker", "opencode-cookies.txt")

	f, err := fetcher.New(cookieFile)
	if err != nil {
		return nil, fmt.Errorf("cannot create fetcher: %w", err)
	}

	return &OpenCodeProvider{
		cfg:        cfg,
		fetcher:    f,
		cookieFile: cookieFile,
		plan:       plan,
	}, nil
}

// Name returns the provider name.
func (o *OpenCodeProvider) Name() string {
	return "opencode-" + o.plan
}

// Fetch downloads the HTML page or API data with usage data.
func (o *OpenCodeProvider) Fetch(ctx context.Context) (string, error) {
	url := fmt.Sprintf("https://opencode.ai/workspace/%s/%s", o.cfg.Workspace, o.plan)

	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (X11; Linux x86_64; rv:150.0) Gecko/20100101 Firefox/150.0",
		"Accept":     "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	}

	resp, err := o.fetcher.Get(ctx, url, headers)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("cannot read body: %w", err)
	}

	html := string(body)
	if !isValidResponse(html) {
		return "", fmt.Errorf("session expired or no usage data found; run 'opentracker login %s'", o.Name())
	}

	return html, nil
}

func isValidResponse(html string) bool {
	return len(html) > 0 && (containsAny(html, []string{
		`data-slot="usage-item"`,
		`rollingUsage`,
		`weeklyUsage`,
		`usagePercent`,
	}) || containsAny(html, []string{
		"id:",
		"wrk_",
	}))
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

// Parse extracts usage data from HTML.
func (o *OpenCodeProvider) Parse(html string) (interface{}, error) {
	return ParseHTML(html)
}
