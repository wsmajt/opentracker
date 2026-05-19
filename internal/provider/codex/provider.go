package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"opentracker/internal/config"
	"opentracker/internal/provider"
)

const (
	usageURL       = "https://chatgpt.com/backend-api/wham/usage"
	acceptLanguage = "en-US,en;q=0.9"
)

func init() {
	provider.Register("codex", func(c *config.Config) (provider.Provider, error) {
		return NewProvider(c)
	})
}

type CodexProvider struct {
	client *http.Client
}

func NewProvider(appCfg *config.Config) (provider.Provider, error) {
	_ = appCfg

	return &CodexProvider{
		client: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (o *CodexProvider) Name() string {
	return "codex"
}

func (o *CodexProvider) Fetch(ctx context.Context) (string, error) {
	auth, err := loadAuth()
	if err != nil {
		return "", fmt.Errorf("cannot read Codex auth: %w", err)
	}
	if auth.AccessToken == "" {
		return "", fmt.Errorf("codex auth not configured; run 'opentracker login codex', 'codex login', or set %s", envAccessToken)
	}

	body, err := o.get(ctx, usageURL, auth)
	if err != nil {
		return "", err
	}

	var api UsageResponse
	if err := json.Unmarshal(body, &api); err != nil {
		return "", fmt.Errorf("cannot decode Codex usage response: %w", err)
	}

	envelope, err := json.Marshal(fetchEnvelope{UsageResponse: api})
	if err != nil {
		return "", fmt.Errorf("cannot encode Codex usage envelope: %w", err)
	}
	return string(envelope), nil
}

func (o *CodexProvider) Parse(data string) (interface{}, error) {
	var envelope fetchEnvelope
	if err := json.Unmarshal([]byte(data), &envelope); err != nil {
		return nil, fmt.Errorf("cannot decode Codex usage envelope: %w", err)
	}
	return mapUsageResponse(envelope.UsageResponse, envelope.AccountEmail, time.Now()), nil
}

func (o *CodexProvider) get(ctx context.Context, url string, auth authSource) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+auth.AccessToken)
	if auth.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", auth.AccountID)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", acceptLanguage)
	req.Header.Set("User-Agent", "codex-cli")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cannot read response: %w", err)
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return body, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("codex authentication expired or unauthorized (HTTP %d); run 'opentracker login codex' or 'codex login'", resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("codex rate limited the request (HTTP 429); retry later")
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("codex server error (HTTP %d)", resp.StatusCode)
	default:
		return nil, fmt.Errorf("codex request failed (HTTP %d)", resp.StatusCode)
	}
}

func HasConfiguredAuth() bool {
	if strings.TrimSpace(os.Getenv(envAccessToken)) != "" {
		return true
	}
	if _, err := loadCodexAuth(defaultCodexAuthFile()); err == nil {
		return true
	}
	return false
}
