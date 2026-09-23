package opencode

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"opentracker/internal/browsercookies"
	"opentracker/internal/config"
	"opentracker/internal/fetcher"
)

var updateOpenCodeProviderConfig = func(appCfg *config.Config, raw json.RawMessage) error {
	return appCfg.UpdateProvider("opencode", raw)
}

func migrateLegacyCookies(appCfg *config.Config, cfg *OpenCodeConfig, store CredentialStore, listWorkspaces func([]*http.Cookie) ([]string, error)) ([]*http.Cookie, error) {
	var cookies []*http.Cookie
	err := config.WithCredentialLock(func() error {
		var err error
		cookies, err = migrateLegacyCookiesLocked(appCfg, cfg, store, listWorkspaces)
		return err
	})
	if err != nil {
		return nil, err
	}
	return cookies, nil
}

func migrateLegacyCookiesLocked(appCfg *config.Config, cfg *OpenCodeConfig, store CredentialStore, listWorkspaces func([]*http.Cookie) ([]string, error)) ([]*http.Cookie, error) {
	latest, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("cannot reload OpenCode config under credential lock: %w", err)
	}
	latestRaw, ok := latest.Providers["opencode"]
	if !ok {
		return nil, fmt.Errorf("opencode not configured")
	}
	latestCfg, err := ParseConfig(latestRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid opencode config: %w", err)
	}
	setInMemoryOpenCodeConfig(appCfg, cfg, latestRaw, latestCfg)
	if latestCfg.CredentialID != "" {
		cookies, err := LoadCredential(store, latestCfg.CredentialID, latestCfg.Workspace)
		if err != nil {
			return nil, err
		}
		removeLegacyCookieFile(legacyCookiePath())
		return cookies, nil
	}
	if latestCfg.Workspace == "" {
		return nil, fmt.Errorf("opencode workspace not set; run 'opentracker login opencode'")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	path := filepath.Join(home, ".config", "opentracker", "opencode-cookies.txt")
	if err := browsercookies.SecureOpenCodeCookieFile(path); err != nil {
		return nil, fmt.Errorf("cannot secure OpenCode cookies: %w", err)
	}
	cookies, err := fetcher.LoadNetscapeCookies(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read legacy OpenCode cookies: %w", err)
	}
	if len(cookies) == 0 {
		return nil, fmt.Errorf("no legacy OpenCode cookies found")
	}
	// The verifier uses a 15-second HTTP timeout. It runs inside the credential
	// lock so another login cannot change the selected workspace mid-migration.
	workspaces, err := listWorkspaces(cookies)
	if err != nil {
		return nil, fmt.Errorf("cannot verify legacy OpenCode cookies; run 'opentracker login opencode': %w", err)
	}
	workspaceFound := false
	for _, workspace := range workspaces {
		if workspace == latestCfg.Workspace {
			workspaceFound = true
			break
		}
	}
	if !workspaceFound {
		return nil, fmt.Errorf("legacy OpenCode cookies do not include configured workspace %q; run 'opentracker login opencode'", latestCfg.Workspace)
	}

	id, err := SaveCredential(store, latestCfg.Workspace, cookies)
	if err != nil {
		return nil, err
	}
	latestCfg.CredentialID = id
	raw, err := json.Marshal(latestCfg)
	if err != nil {
		return nil, fmt.Errorf("cannot encode OpenCode config: %w", err)
	}
	if err := updateOpenCodeProviderConfig(appCfg, raw); err != nil {
		// A failed save can still have committed, or another login may have
		// become the selector. Re-read the selector before deciding whether the
		// legacy source is safe to remove.
		activeCookies, persistedRaw, persistedCfg, verifyErr := loadCurrentConfiguredCredential(store)
		if verifyErr != nil {
			if persistedRaw != nil && persistedCfg != nil {
				setInMemoryOpenCodeConfig(appCfg, cfg, persistedRaw, persistedCfg)
			}
			return nil, fmt.Errorf("cannot persist OpenCode credential reference: %w (verification failed: %v)", err, verifyErr)
		}
		setInMemoryOpenCodeConfig(appCfg, cfg, persistedRaw, persistedCfg)
		removeLegacyCookieFile(path)
		return activeCookies, nil
	}

	// Re-read disk state: always honor the selector that is actually persisted.
	activeCookies, persistedRaw, persistedCfg, err := loadCurrentConfiguredCredential(store)
	if err != nil {
		return nil, err
	}
	setInMemoryOpenCodeConfig(appCfg, cfg, persistedRaw, persistedCfg)
	removeLegacyCookieFile(path)
	return activeCookies, nil
}

func loadCurrentConfiguredCredential(store CredentialStore) ([]*http.Cookie, json.RawMessage, *OpenCodeConfig, error) {
	persisted, err := config.Load()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot verify persisted OpenCode config: %w", err)
	}
	persistedRaw, ok := persisted.Providers["opencode"]
	if !ok {
		return nil, nil, nil, fmt.Errorf("persisted OpenCode config is missing")
	}
	persistedCfg, err := ParseConfig(persistedRaw)
	if err != nil {
		return nil, persistedRaw, nil, fmt.Errorf("cannot parse persisted OpenCode config: %w", err)
	}
	if persistedCfg.CredentialID == "" || persistedCfg.Workspace == "" {
		return nil, persistedRaw, persistedCfg, fmt.Errorf("persisted OpenCode config has no active credential reference")
	}
	cookies, err := LoadCredential(store, persistedCfg.CredentialID, persistedCfg.Workspace)
	if err != nil {
		return nil, persistedRaw, persistedCfg, fmt.Errorf("cannot verify active OpenCode credentials: %w", err)
	}
	return cookies, persistedRaw, persistedCfg, nil
}

func setInMemoryOpenCodeConfig(appCfg *config.Config, cfg *OpenCodeConfig, raw json.RawMessage, parsed *OpenCodeConfig) {
	if appCfg.Providers == nil {
		appCfg.Providers = make(map[string]json.RawMessage)
	}
	appCfg.Providers["opencode"] = append(json.RawMessage(nil), raw...)
	*cfg = *parsed
}

func legacyCookiePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "opentracker", "opencode-cookies.txt")
}

func removeLegacyCookieFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("warning: cannot remove OpenCode cookie file %q: %v; cleanup will be retried on the next successful keyring read", path, err)
	}
}

// cleanupResidualLegacyCookieFile removes the obsolete cookie file only after
// the configured keyring credential has already been loaded and verified.
func cleanupResidualLegacyCookieFile() {
	path := legacyCookiePath()
	if path == "" {
		log.Printf("warning: cannot locate legacy OpenCode cookie file for cleanup: cannot determine home directory")
		return
	}
	removeLegacyCookieFile(path)
}
