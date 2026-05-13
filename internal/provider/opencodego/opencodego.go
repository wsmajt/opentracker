package opencodego

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"opentracker/internal/config"
	"opentracker/internal/fetcher"
	"opentracker/internal/model"
	"opentracker/internal/provider"
	"opentracker/internal/provider/opencode"
)

func init() {
	provider.Register("opencode-go", func(cfg *config.Config) (provider.Provider, error) {
		return New(cfg, "go")
	})
}

// OpenCodeGo implements provider.Provider for opencode.ai Go plan.
type OpenCodeGo struct {
	cfg        *opencode.OpenCodeConfig
	fetcher    *fetcher.Fetcher
	cookieFile string
	plan       string
}

// New creates a new OpenCodeGo provider for the given plan.
func New(appCfg *config.Config, plan string) (provider.Provider, error) {
	raw, ok := appCfg.Providers["opencode"]
	if !ok {
		return nil, fmt.Errorf("opencode not configured")
	}

	cfg, err := opencode.ParseConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid opencode config: %w", err)
	}

	if cfg.Workspace == "" {
		return nil, fmt.Errorf("opencode workspace not set")
	}

	home, _ := os.UserHomeDir()
	cookieFile := filepath.Join(home, ".config", "opentracker", "opencode-cookies.txt")

	f, err := fetcher.New(cookieFile)
	if err != nil {
		return nil, fmt.Errorf("cannot create fetcher: %w", err)
	}

	return &OpenCodeGo{
		cfg:        cfg,
		fetcher:    f,
		cookieFile: cookieFile,
		plan:       plan,
	}, nil
}

// Name returns the provider name.
func (o *OpenCodeGo) Name() string {
	return "opencode-" + o.plan
}

// Fetch downloads the HTML page with usage data.
func (o *OpenCodeGo) Fetch(ctx context.Context) (string, error) {
	url := fmt.Sprintf("https://opencode.ai/workspace/%s/%s", o.cfg.Workspace, o.plan)

	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36",
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
	if !strings.Contains(html, `data-slot="usage-item"`) {
		return "", fmt.Errorf("session expired or no usage data found; run 'opentracker login %s'", o.Name())
	}

	return html, nil
}

// Parse extracts usage data from HTML.
func (o *OpenCodeGo) Parse(html string) (model.Usage, error) {
	return opencode.ParseHTML(html)
}
