package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"opentracker/internal/cache"
	"opentracker/internal/config"
	"opentracker/internal/model"
	"opentracker/internal/provider"
)

func TestOpenCodeProviderErrorPrecedesLegacyCache(t *testing.T) {
	cacheDir := t.TempDir()
	c := cache.New(cacheDir)
	legacy := []model.ProviderResult{{Provider: "opencode-go", Usage: "stale"}}
	if err := c.Set("opencode-go", legacy, time.Minute); err != nil {
		t.Fatalf("seed legacy cache: %v", err)
	}

	app := &App{
		config: &config.Config{Providers: map[string]json.RawMessage{
			"opencode": json.RawMessage(`{"workspace":"workspace-a","credentialId":"credential-a"}`),
		}},
		cache: c,
	}
	lockedErr := errors.New("credential store is locked")
	called := false
	err := app.fetchOneWithProvider(context.Background(), "opencode-go", false, false, func(name string, _ *config.Config) (provider.Provider, error) {
		called = true
		return nil, lockedErr
	})
	if !called {
		t.Fatal("expected provider authentication to be checked")
	}
	if !errors.Is(err, lockedErr) {
		t.Fatalf("expected locked credential error, got %v", err)
	}
}

func TestOpenCodeCacheKeyScopesWorkspaceAndCredential(t *testing.T) {
	base := OpenCodeCacheKey("opencode-go", "workspace-a", "credential-a")
	if got := OpenCodeCacheKey("opencode-go", "workspace-a", "credential-b"); got == base {
		t.Fatal("cache key did not change for a different credential")
	}
	if got := OpenCodeCacheKey("opencode-go", "workspace-b", "credential-a"); got == base {
		t.Fatal("cache key did not change for a different workspace")
	}
	if got := OpenCodeCacheKey("opencode-zen", "workspace-a", "credential-a"); got == base {
		t.Fatal("cache key did not change for a different plan")
	}
}

func TestUnconfiguredOpenCodeFetchRequiresLogin(t *testing.T) {
	stdin, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdin pipe: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close stdin writer: %v", err)
	}
	previousStdin := os.Stdin
	os.Stdin = stdin
	defer func() {
		os.Stdin = previousStdin
		_ = stdin.Close()
	}()

	cfg := &config.Config{Providers: make(map[string]json.RawMessage)}
	app := &App{config: cfg, cache: cache.New(t.TempDir())}
	err = app.fetchOneWithProvider(context.Background(), "opencode-go", false, true, func(string, *config.Config) (provider.Provider, error) {
		t.Fatal("provider should not be constructed without OpenCode configuration")
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "run 'opentracker login opencode'") {
		t.Fatalf("expected actionable login error, got %v", err)
	}
	if _, exists := cfg.Providers["opencode"]; exists {
		t.Fatal("fetch unexpectedly persisted a workspace-only OpenCode configuration")
	}
}
