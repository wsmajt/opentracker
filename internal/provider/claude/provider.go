package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"opentracker/internal/config"
	"opentracker/internal/provider"
)

const usageURL = "https://api.anthropic.com/api/oauth/usage"

func init() {
	provider.Register("claude", func(c *config.Config) (provider.Provider, error) { return NewProvider(c) })
}

type ClaudeProvider struct {
	client      *http.Client
	url         string
	cooldownDir string
	auth        auth
}

func NewProvider(_ *config.Config) (provider.Provider, error) {
	a, err := loadAuth()
	if err != nil {
		return nil, err
	}
	if a.token == "" {
		return nil, fmt.Errorf("claude Code OAuth credentials not found; run 'claude' and sign in, or set %s", accessTokenEnv)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	return &ClaudeProvider{client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, url: usageURL, cooldownDir: filepath.Join(home, ".cache", "opentracker", "claude-cooldowns"), auth: a}, nil
}

func (p *ClaudeProvider) Name() string { return "claude" }

// CacheIdentity returns the opaque identity of the credential captured at construction.
func (p *ClaudeProvider) CacheIdentity() string { return p.auth.key }

func (p *ClaudeProvider) Fetch(ctx context.Context) (string, error) {
	a := p.auth
	if err := p.checkCooldown(a.key, time.Now()); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return "", fmt.Errorf("cannot create Claude usage request")
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "claude-cli/2.1.0")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("claude usage request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("cannot read Claude usage response")
	}
	switch resp.StatusCode {
	case http.StatusOK:
		var check map[string]json.RawMessage
		if err := json.Unmarshal(body, &check); err != nil || check == nil {
			return "", fmt.Errorf("cannot decode Claude usage response")
		}
		return string(body), nil
	case http.StatusUnauthorized:
		return "", fmt.Errorf("claude OAuth token was rejected (HTTP 401); run 'claude' and sign in again")
	case http.StatusForbidden:
		return "", fmt.Errorf("claude OAuth token lacks access to usage data (HTTP 403); sign in with Claude Code again")
	case http.StatusTooManyRequests:
		until := retryAt(resp.Header.Get("Retry-After"), time.Now())
		if err := p.saveCooldown(a.key, until); err != nil {
			return "", fmt.Errorf("claude rate limit reached; retry after %s (cooldown could not be saved)", until.UTC().Format(time.RFC3339))
		}
		return "", fmt.Errorf("claude rate limit reached; retry after %s", until.UTC().Format(time.RFC3339))
	default:
		return "", fmt.Errorf("claude usage request failed (HTTP %d)", resp.StatusCode)
	}
}

func (p *ClaudeProvider) Parse(data string) (interface{}, error) {
	snapshot, err := parseUsage(data, p.auth.plan, time.Now())
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (p *ClaudeProvider) checkCooldown(key string, now time.Time) error {
	path := filepath.Join(p.cooldownDir, key+".json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot read Claude rate-limit state")
	}
	var state struct {
		Until time.Time `json:"until"`
	}
	if json.Unmarshal(data, &state) != nil {
		return nil
	}
	if state.Until.After(now) {
		return fmt.Errorf("claude rate limit is active until %s", state.Until.UTC().Format(time.RFC3339))
	}
	_ = os.Remove(path)
	return nil
}

func (p *ClaudeProvider) saveCooldown(key string, until time.Time) error {
	if err := os.MkdirAll(p.cooldownDir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		Until time.Time `json:"until"`
	}{until})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.cooldownDir, key+".json"), data, 0600)
}

func retryAt(value string, now time.Time) time.Time {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds > 0 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if parsed, err := http.ParseTime(value); err == nil && parsed.After(now) {
		return parsed
	}
	return now.Add(5 * time.Minute)
}
