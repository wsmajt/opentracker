package app

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"opentracker/internal/cache"
	"opentracker/internal/config"
	"opentracker/internal/model"
	"opentracker/internal/output"
	"opentracker/internal/provider"
	"opentracker/internal/provider/claude"
	"opentracker/internal/provider/cliproxyapi"
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
	return a.fetchOneWithProviderMode(ctx, providerName, force, prompt, getProvider, true)
}

func (a *App) fetchOneWithProviderMode(ctx context.Context, providerName string, force bool, prompt bool, getProvider func(string, *config.Config) (provider.Provider, error), combineProxy bool) error {
	if combineProxy && providerName == "cliproxyapi" {
		return a.fetchCodexAccounts(ctx, false, force, getProvider)
	}
	if combineProxy && providerName == "codex" && cliproxyapi.ConfiguredWithConfig(a.config) {
		return a.fetchCodexAccounts(ctx, true, force, getProvider)
	}
	if combineProxy && providerName == "claude" && cliproxyapi.ConfiguredWithConfig(a.config) {
		return a.fetchClaudeAccounts(ctx, true, force, getProvider)
	}
	isOpenCode := providerName == "opencode-go" || providerName == "opencode-zen"
	cacheKey := providerName
	var p provider.Provider
	if providerName == "claude" {
		var err error
		p, err = getProvider(providerName, a.config)
		if err != nil {
			return err
		}
		if scoped, ok := p.(interface{ CacheIdentity() string }); ok {
			cacheKey = ClaudeCacheKey(scoped.CacheIdentity())
		}
	}

	if isOpenCode && !a.isConfigured(providerName) {
		return fmt.Errorf("opencode auth is not configured; run 'opentracker login opencode'")
	}

	// Ensure provider is configured before reading cache so auth-sensitive providers
	// do not return data for a stale or different account.
	if providerName != "claude" && !a.isConfigured(providerName) {
		if !prompt {
			return fmt.Errorf("provider is not configured")
		}
		if err := a.promptSetup(providerName); err != nil {
			return err
		}
	}

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
	if (providerName == "codex" || providerName == "cliproxyapi") && cliproxyapi.ConfiguredWithConfig(a.config) {
		return true
	}
	if providerName == "claude" && (claude.HasConfiguredAuth() || cliproxyapi.ConfiguredWithConfig(a.config)) {
		return true
	}
	_, ok := a.config.Providers[providerName]
	return ok
}

// ClaudeCacheKey scopes cached usage to the active OAuth token without storing it.
func ClaudeCacheKey(identity string) string {
	return fmt.Sprintf("claude-%x", sha256.Sum256([]byte(identity)))
}

func (a *App) fetchAll(ctx context.Context, force bool) error {
	return a.fetchAllWithProvider(ctx, force, provider.Get)
}

func (a *App) fetchAllWithProvider(ctx context.Context, force bool, getProvider func(string, *config.Config) (provider.Provider, error)) error {
	names := provider.List()
	if len(names) == 0 {
		return fmt.Errorf("no providers registered")
	}
	sort.Strings(names)
	proxyConfigured := cliproxyapi.ConfiguredWithConfig(a.config)
	failed := false
	localCodexFetched := false
	localCodexID := ""
	for _, name := range names {
		if name == "cliproxyapi" {
			continue
		}
		if proxyConfigured && name == "codex" {
			if !codex.HasConfiguredAuth() {
				continue
			}
			p, err := getProvider("codex", a.config)
			if err == nil {
				var data string
				data, err = p.Fetch(ctx)
				if err == nil {
					var usage interface{}
					usage, err = p.Parse(data)
					if err == nil {
						err = output.Print([]model.ProviderResult{{Provider: "codex", Usage: usage}})
						if err == nil {
							localCodexFetched = true
							localCodexID = codex.ConfiguredAccountID()
						}
					}
				}
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "error fetching local Codex account: %v\n", err)
				failed = true
			}
			continue
		}
		if proxyConfigured && name == "claude" {
			configured, authErr := claude.AuthStatus()
			if authErr != nil {
				fmt.Fprintf(os.Stderr, "error fetching local Claude account: %v\n", authErr)
				failed = true
				continue
			}
			if configured {
				if err := a.fetchOneWithProviderMode(ctx, name, force, false, getProvider, false); err != nil {
					fmt.Fprintf(os.Stderr, "error fetching %s: %v\n", name, err)
					failed = true
				}
			}
			continue
		}
		if !a.isConfigured(name) {
			fmt.Fprintf(os.Stderr, "skipping %s: provider is not configured\n", name)
			continue
		}
		if err := a.fetchOneWithProvider(ctx, name, force, false, getProvider); err != nil {
			fmt.Fprintf(os.Stderr, "error fetching %s: %v\n", name, err)
		}
	}
	if proxyConfigured {
		accounts, accountFailures, err := cliproxyapi.FetchWithConfig(ctx, a.config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error fetching CLIProxyAPI accounts: %v\n", err)
			failed = true
		} else {
			codexResults := make([]model.ProviderResult, 0)
			claudeResults := make([]model.ProviderResult, 0)
			for _, account := range accounts {
				switch account.AccountProvider {
				case "codex":
					if localCodexFetched && localCodexID != "" && account.ID == localCodexID {
						continue
					}
					codexResults = append(codexResults, model.ProviderResult{Provider: "cliproxyapi", Usage: account})
				case "claude":
					claudeResults = append(claudeResults, model.ProviderResult{Provider: "cliproxyapi", Usage: account})
				}
			}
			for _, failure := range accountFailures {
				fmt.Fprintf(os.Stderr, "error fetching CLIProxyAPI %s account #%d: %v\n", failure.Provider, failure.Account, failure.Err)
				failed = true
			}
			if len(codexResults) > 0 {
				if err := output.Print(codexResults); err != nil {
					return err
				}
			}
			if len(claudeResults) > 0 {
				if err := output.Print(claudeResults); err != nil {
					return err
				}
			}
		}
	}
	if failed {
		return fmt.Errorf("one or more providers or accounts failed")
	}
	return nil
}

func (a *App) fetchCodexAccounts(ctx context.Context, includeLocal bool, force bool, getProvider func(string, *config.Config) (provider.Provider, error)) error {
	_ = force
	results := make([]model.ProviderResult, 0)
	failures := 0
	localID := codex.ConfiguredAccountID()
	localFetched := false
	if includeLocal && codex.HasConfiguredAuth() {
		p, err := getProvider("codex", a.config)
		if err == nil {
			var data string
			data, err = p.Fetch(ctx)
			if err == nil {
				var usage interface{}
				usage, err = p.Parse(data)
				if err == nil {
					results = append(results, model.ProviderResult{Provider: "codex", Usage: usage})
					localFetched = true
				}
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error fetching local Codex account: %v\n", err)
			failures++
		}
	}
	if cliproxyapi.ConfiguredWithConfig(a.config) {
		var accounts []cliproxyapi.Account
		var accountFailures []cliproxyapi.Failure
		var err error
		if includeLocal {
			accounts, accountFailures, err = cliproxyapi.FetchCodexWithConfig(ctx, a.config)
		} else {
			accounts, accountFailures, err = cliproxyapi.FetchWithConfig(ctx, a.config)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error fetching CLIProxyAPI accounts: %v\n", err)
			failures++
		} else {
			seenIDs := make(map[string]struct{}, len(accounts))
			for _, account := range accounts {
				if localFetched && includeLocal && account.ID != "" && localID != "" && account.ID == localID {
					continue
				}
				if account.ID != "" {
					if _, exists := seenIDs[account.ID]; exists {
						continue
					}
					seenIDs[account.ID] = struct{}{}
				}
				results = append(results, model.ProviderResult{Provider: "cliproxyapi", Usage: account})
			}
			for _, failure := range accountFailures {
				fmt.Fprintf(os.Stderr, "error fetching CLIProxyAPI %s account #%d: %v\n", failure.Provider, failure.Account, failure.Err)
				failures++
			}
		}
	}
	if !includeLocal && !cliproxyapi.ConfiguredWithConfig(a.config) {
		return fmt.Errorf("CLIProxyAPI key is not configured; run 'opentracker login cliproxyapi' or set %s", cliproxyapi.EnvKey)
	}
	if len(results) == 0 {
		return fmt.Errorf("no CLIProxyAPI usage accounts were fetched")
	}
	if err := output.Print(results); err != nil {
		return err
	}
	if failures > 0 {
		return fmt.Errorf("one or more CLIProxyAPI accounts failed")
	}
	return nil
}

func (a *App) fetchClaudeAccounts(ctx context.Context, includeLocal bool, force bool, getProvider func(string, *config.Config) (provider.Provider, error)) error {
	_ = force
	results := make([]model.ProviderResult, 0)
	failures := 0
	if includeLocal {
		configured, authErr := claude.AuthStatus()
		if authErr != nil {
			fmt.Fprintf(os.Stderr, "error fetching local Claude account: %v\n", authErr)
			failures++
		} else if configured {
			p, err := getProvider("claude", a.config)
			if err == nil {
				var data string
				data, err = p.Fetch(ctx)
				if err == nil {
					var usage interface{}
					usage, err = p.Parse(data)
					if err == nil {
						results = append(results, model.ProviderResult{Provider: "claude", Usage: usage})
					}
				}
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "error fetching local Claude account: %v\n", err)
				failures++
			}
		}
	}
	if cliproxyapi.ConfiguredWithConfig(a.config) {
		accounts, accountFailures, err := cliproxyapi.FetchClaudeWithConfig(ctx, a.config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error fetching CLIProxyAPI Claude accounts: %v\n", err)
			failures++
		} else {
			for _, account := range accounts {
				results = append(results, model.ProviderResult{Provider: "cliproxyapi", Usage: account})
			}
			for _, failure := range accountFailures {
				fmt.Fprintf(os.Stderr, "error fetching CLIProxyAPI %s account #%d: %v\n", failure.Provider, failure.Account, failure.Err)
				failures++
			}
		}
	}
	if len(results) == 0 {
		return fmt.Errorf("no Claude usage accounts were fetched")
	}
	if err := output.Print(results); err != nil {
		return err
	}
	if failures > 0 {
		return fmt.Errorf("one or more Claude accounts failed")
	}
	return nil
}

func (a *App) promptSetup(providerName string) error {
	fmt.Printf("Provider %q is not configured.\n", providerName)
	if providerName == "codex" {
		return fmt.Errorf("codex auth is not configured; run 'opentracker login codex' or 'codex login'")
	}
	if providerName == "claude" {
		return fmt.Errorf("claude Code OAuth credentials are not configured; run 'claude' and sign in, or set OPENTRACKER_CLAUDE_ACCESS_TOKEN")
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
