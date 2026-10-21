package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

func TestClaudeCredentialsFailBeforeWarmCache(t *testing.T) {
	for _, tc := range []struct{ name, content, want string }{
		{name: "missing", want: "not found"},
		{name: "malformed", content: "not-json", want: "cannot parse"},
		{name: "expired", content: `{"claudeAiOauth":{"accessToken":"old","expiresAt":1}}`, want: "expired"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
			t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
			t.Setenv("OPENTRACKER_CLAUDE_ACCESS_TOKEN", "placeholder")
			if err := os.Unsetenv("OPENTRACKER_CLAUDE_ACCESS_TOKEN"); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, "claude", ".credentials.json")
			if tc.content != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			c := cache.New(t.TempDir())
			if err := c.Set("claude", []model.ProviderResult{{Provider: "claude", Usage: "stale"}}, time.Minute); err != nil {
				t.Fatal(err)
			}
			a := &App{config: &config.Config{Providers: map[string]json.RawMessage{}}, cache: c}
			err := a.fetchOne(context.Background(), "claude", false, false)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("fetch error = %v, want %q", err, tc.want)
			}
		})
	}
}

type fakeClaudeProvider struct {
	identity string
	calls    *int
}

func (p *fakeClaudeProvider) Name() string          { return "claude" }
func (p *fakeClaudeProvider) CacheIdentity() string { return p.identity }
func (p *fakeClaudeProvider) Fetch(context.Context) (string, error) {
	(*p.calls)++
	return p.identity, nil
}
func (p *fakeClaudeProvider) Parse(data string) (interface{}, error) { return data, nil }

func TestClaudeCacheFreshCachedForcedAndAccountSwitch(t *testing.T) {
	calls := 0
	a := &App{config: &config.Config{Providers: map[string]json.RawMessage{}}, cache: cache.New(t.TempDir())}
	fetch := func(identity string, force bool) error {
		return a.fetchOneWithProvider(context.Background(), "claude", force, false, func(string, *config.Config) (provider.Provider, error) {
			return &fakeClaudeProvider{identity: identity, calls: &calls}, nil
		})
	}
	if err := fetch("account-a", false); err != nil {
		t.Fatal(err)
	}
	if err := fetch("account-a", false); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("fresh/cache calls = %d, want 1", calls)
	}
	if err := fetch("account-b", false); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("account switch reused old cache, calls=%d", calls)
	}
	if err := fetch("account-b", true); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("forced fetch did not bypass cache, calls=%d", calls)
	}
}
