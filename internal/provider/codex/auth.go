package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	envAccessToken = "OPENTRACKER_CODEX_ACCESS_TOKEN"
	envAccountID   = "OPENTRACKER_CODEX_ACCOUNT_ID"
	envCodexHome   = "CODEX_HOME"
)

type authSource struct {
	AccessToken string
	AccountID   string
	Source      string
}

type codexAuthFile struct {
	Tokens *struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

func loadAuth() (authSource, error) {
	if token := strings.TrimSpace(os.Getenv(envAccessToken)); token != "" {
		return authSource{
			AccessToken: token,
			AccountID:   strings.TrimSpace(os.Getenv(envAccountID)),
			Source:      "env-oauth",
		}, nil
	}

	if auth, err := loadCodexAuth(defaultCodexAuthFile()); err == nil {
		return auth, nil
	}

	return authSource{}, nil
}

func loadCodexAuth(path string) (authSource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return authSource{}, err
	}

	var auth codexAuthFile
	if err := json.Unmarshal(data, &auth); err != nil {
		return authSource{}, fmt.Errorf("cannot parse Codex auth file: %w", err)
	}
	if auth.Tokens == nil || strings.TrimSpace(auth.Tokens.AccessToken) == "" {
		return authSource{}, fmt.Errorf("Codex auth file has no access token")
	}

	return authSource{
		AccessToken: strings.TrimSpace(auth.Tokens.AccessToken),
		AccountID:   strings.TrimSpace(auth.Tokens.AccountID),
		Source:      "codex-oauth",
	}, nil
}

func defaultCodexAuthFile() string {
	if codexHome := strings.TrimSpace(os.Getenv(envCodexHome)); codexHome != "" {
		return filepath.Join(codexHome, "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "auth.json")
}
