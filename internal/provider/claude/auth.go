package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const accessTokenEnv = "OPENTRACKER_CLAUDE_ACCESS_TOKEN"

type auth struct {
	token  string
	plan   string
	source string
	key    string
}

type credentialsFile struct {
	OAuth *struct {
		AccessToken      string   `json:"accessToken"`
		ExpiresAt        *int64   `json:"expiresAt"`
		Scopes           []string `json:"scopes"`
		SubscriptionType string   `json:"subscriptionType"`
		RateLimitTier    string   `json:"rateLimitTier"`
	} `json:"claudeAiOauth"`
}

func loadAuth() (auth, error) {
	if token, ok := os.LookupEnv(accessTokenEnv); ok {
		token = strings.TrimSpace(token)
		if token == "" {
			return auth{}, fmt.Errorf("%s is set but empty", accessTokenEnv)
		}
		return makeAuth(token, "env-oauth", ""), nil
	}
	path, err := credentialsPath()
	if err != nil {
		return auth{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return auth{}, nil
		}
		return auth{}, fmt.Errorf("cannot read Claude Code credentials at %s: %w", path, err)
	}
	var file credentialsFile
	if err := json.Unmarshal(data, &file); err != nil {
		return auth{}, fmt.Errorf("cannot parse Claude Code credentials at %s", path)
	}
	if file.OAuth == nil || strings.TrimSpace(file.OAuth.AccessToken) == "" {
		return auth{}, fmt.Errorf("claude Code credentials at %s contain no OAuth access token", path)
	}
	o := file.OAuth
	if o.ExpiresAt != nil {
		if *o.ExpiresAt <= 0 {
			return auth{}, fmt.Errorf("claude Code credentials contain an invalid OAuth expiry; run 'claude' and sign in again")
		}
		if time.Now().UnixMilli() >= *o.ExpiresAt {
			return auth{}, fmt.Errorf("claude Code OAuth credentials have expired; run 'claude' and sign in again")
		}
	}
	if o.Scopes != nil && !contains(o.Scopes, "user:profile") {
		return auth{}, fmt.Errorf("claude OAuth credentials lack user:profile scope; sign in with Claude Code again")
	}
	plan := o.SubscriptionType
	if plan == "" {
		plan = o.RateLimitTier
	}
	a := makeAuth(strings.TrimSpace(o.AccessToken), "claude-code-oauth", plan)
	return a, nil
}

func makeAuth(token, source, plan string) auth {
	sum := sha256.Sum256([]byte(token))
	return auth{token: token, source: source, plan: plan, key: hex.EncodeToString(sum[:])}
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func credentialsPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_SECURESTORAGE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, ".credentials.json"), nil
	}
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, ".credentials.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory for Claude Code credentials: %w", err)
	}
	return filepath.Join(home, ".claude", ".credentials.json"), nil
}

func HasConfiguredAuth() bool {
	a, err := loadAuth()
	return err == nil && a.token != ""
}

// ValidateAuth checks the active Claude Code credential source without exposing its token.
func ValidateAuth() error {
	a, err := loadAuth()
	if err != nil {
		return err
	}
	if a.token == "" {
		return fmt.Errorf("claude Code OAuth credentials were not found; run 'claude' and sign in, or set %s", accessTokenEnv)
	}
	return nil
}

func CacheIdentity() (string, error) {
	a, err := loadAuth()
	if err != nil {
		return "", err
	}
	if a.token == "" {
		return "", fmt.Errorf("claude Code OAuth credentials not found; run 'claude' and sign in, or set %s", accessTokenEnv)
	}
	return a.key, nil
}
