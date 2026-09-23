package app

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"opentracker/internal/cache"
	"opentracker/internal/config"
	"opentracker/internal/model"
	"opentracker/internal/output"
	"opentracker/internal/provider"
	"opentracker/internal/provider/codex"
	"opentracker/internal/provider/opencode"
)

// App orchestrates configuration, caching, and provider execution.
type App struct {
	config *config.Config
	cache  *cache.Cache
}

// New creates a new App instance.
func New() (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("cannot load config: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot get home dir: %w", err)
	}

	cacheDir := filepath.Join(home, ".cache", "opentracker")
	c := cache.New(cacheDir)

	return &App{
		config: cfg,
		cache:  c,
	}, nil
}

// Fetch retrieves usage data for the given provider.
func (a *App) Fetch(ctx context.Context, providerName string, force bool) error {
	if providerName == "all" {
		return a.fetchAll(ctx, force)
	}

	return a.fetchOne(ctx, providerName, force, true)
}

func (a *App) fetchOne(ctx context.Context, providerName string, force bool, prompt bool) error {
	return a.fetchOneWithProvider(ctx, providerName, force, prompt, provider.Get)
}

func (a *App) fetchOneWithProvider(ctx context.Context, providerName string, force bool, prompt bool, getProvider func(string, *config.Config) (provider.Provider, error)) error {
	isOpenCode := providerName == "opencode-go" || providerName == "opencode-zen"
	cacheKey := providerName

	if isOpenCode && !a.isConfigured(providerName) {
		return fmt.Errorf("opencode auth is not configured; run 'opentracker login opencode'")
	}

	// Ensure provider is configured before reading cache so auth-sensitive providers
	// do not return data for a stale or different account.
	if !a.isConfigured(providerName) {
		if !prompt {
			return fmt.Errorf("provider is not configured")
		}
		if err := a.promptSetup(providerName); err != nil {
			return err
		}
	}

	var p provider.Provider
	if isOpenCode {
		var err error
		p, err = getProvider(providerName, a.config)
		if err != nil {
			return err
		}

		// Get may migrate legacy cookies and persist CredentialID. Read the config
		// only after provider construction so the cache is scoped to the active credential.
		opencodeConfig, err := opencode.ParseConfig(a.config.Providers["opencode"])
		if err != nil {
			return fmt.Errorf("invalid opencode config: %w", err)
		}
		cacheKey = OpenCodeCacheKey(providerName, opencodeConfig.Workspace, opencodeConfig.CredentialID)
	}

	if !force {
		var cached []model.ProviderResult
		if a.cache.Get(cacheKey, &cached) {
			return output.Print(cached)
		}
	}

	if p == nil {
		var err error
		p, err = getProvider(providerName, a.config)
		if err != nil {
			return err
		}
	}

	html, err := p.Fetch(ctx)
	if err != nil {
		return fmt.Errorf("fetch failed for %s: %w", providerName, err)
	}

	usage, err := p.Parse(html)
	if err != nil {
		return fmt.Errorf("parse failed for %s: %w", providerName, err)
	}

	results := []model.ProviderResult{
		{Provider: providerName, Usage: usage},
	}

	if err := a.cache.Set(cacheKey, results, 90*time.Second); err != nil {
		// Non-fatal: log and continue
		fmt.Fprintf(os.Stderr, "warning: cannot save cache: %v\n", err)
	}

	return output.Print(results)
}

// OpenCodeCacheKey returns the account-scoped cache key for an OpenCode plan.
func OpenCodeCacheKey(providerName, workspace, credentialID string) string {
	identity, _ := json.Marshal([3]string{providerName, workspace, credentialID})
	return fmt.Sprintf("opencode-%x", sha256.Sum256(identity))
}

// isConfigured checks if a provider has valid config.
func (a *App) isConfigured(providerName string) bool {
	// For opencode plans, check shared "opencode" config first
	if strings.HasPrefix(providerName, "opencode-") {
		if _, ok := a.config.Providers["opencode"]; ok {
			return true
		}
	}
	if providerName == "codex" && codex.HasConfiguredAuth() {
		return true
	}
	_, ok := a.config.Providers[providerName]
	return ok
}

func (a *App) fetchAll(ctx context.Context, force bool) error {
	names := provider.List()
	if len(names) == 0 {
		return fmt.Errorf("no providers registered")
	}

	var results []model.ProviderResult
	for _, name := range names {
		if !a.isConfigured(name) {
			fmt.Fprintf(os.Stderr, "skipping %s: provider is not configured\n", name)
			continue
		}
		if err := a.fetchOne(ctx, name, force, false); err != nil {
			fmt.Fprintf(os.Stderr, "error fetching %s: %v\n", name, err)
			continue
		}
		// We can't easily collect results from fetchOne since it prints directly.
		// For simplicity, fetchAll prints per-provider. If you want aggregated JSON,
		// refactor fetchOne to return results instead of printing.
	}
	_ = results
	return nil
}

func (a *App) promptSetup(providerName string) error {
	fmt.Printf("Provider %q is not configured.\n", providerName)
	if providerName == "codex" {
		return fmt.Errorf("codex auth is not configured; run 'opentracker login codex' or 'codex login'")
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Workspace ID: ")
	workspace, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("cannot read input: %w", err)
	}
	workspace = strings.TrimSpace(workspace)

	if workspace == "" {
		return fmt.Errorf("workspace cannot be empty")
	}

	raw, _ := json.Marshal(opencode.OpenCodeConfig{Workspace: workspace})

	// For opencode plans, use shared "opencode" config key
	configKey := providerName
	if strings.HasPrefix(providerName, "opencode-") {
		configKey = "opencode"
	}

	a.config.Providers[configKey] = raw

	if err := a.config.Save(); err != nil {
		return fmt.Errorf("cannot save config: %w", err)
	}

	fmt.Println("Config saved.")
	return nil
}
