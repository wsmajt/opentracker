package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
	"opentracker/internal/cache"
	"opentracker/internal/config"
	"opentracker/internal/model"
	"opentracker/internal/provider"
	"opentracker/internal/provider/cliproxyapi"
	"opentracker/internal/provider/codex"
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

type fakeCodexProvider struct {
	err   error
	calls *int
	usage interface{}
}

func (*fakeCodexProvider) Name() string { return "codex" }
func (p *fakeCodexProvider) Fetch(context.Context) (string, error) {
	if p.calls != nil {
		*p.calls++
	}
	return "local", p.err
}
func (p *fakeCodexProvider) Parse(string) (interface{}, error) {
	if p.usage != nil {
		return p.usage, nil
	}
	return map[string]string{"local": "usage"}, nil
}

func captureFetchOutput(t *testing.T, run func() error) (string, string, error) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = outW
	os.Stderr = errW
	result := run()
	_ = outW.Close()
	_ = errW.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr
	out, _ := io.ReadAll(outR)
	_ = outR.Close()
	stderr, _ := io.ReadAll(errR)
	_ = errR.Close()
	return string(out), string(stderr), result
}

func proxyTestServer(t *testing.T, credentials string, calls *int) *httptest.Server {
	return proxyTestServerWithResetCalls(t, credentials, calls, nil, true)
}

func proxyTestServerWithResetCalls(t *testing.T, credentials string, quotaCalls, resetCalls *int, includeQuotaResetCredits bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v8/management/credentials" {
			_, _ = fmt.Fprint(w, credentials)
			return
		}
		if r.URL.Path == "/v8/management/requests/api-call" {
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["url"] == "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits" {
				if resetCalls != nil {
					*resetCalls++
				}
				_, _ = fmt.Fprint(w, `{"status_code":200,"body":"{\"available_count\":7}"}`)
				return
			}
			if quotaCalls != nil {
				*quotaCalls++
			}
			if strings.Contains(fmt.Sprint(req["authIndex"]), "bad") {
				_, _ = fmt.Fprint(w, `{"status_code":500,"body":"ignored"}`)
				return
			}
			if req["url"] == "https://api.anthropic.com/api/oauth/usage" {
				_, _ = fmt.Fprint(w, `{"status_code":200,"body":"{\"five_hour\":{\"utilization\":37}}"}`)
				return
			}
			codexBody := `{"plan_type":"plus"}`
			if includeQuotaResetCredits {
				codexBody = `{"plan_type":"plus","rate_limit_reset_credits":{"available_count":3}}`
			}
			response, _ := json.Marshal(map[string]any{"status_code": 200, "body": codexBody})
			_, _ = w.Write(response)
			return
		}
		http.NotFound(w, r)
	}))
	t.Setenv(cliproxyapi.EnvURL, server.URL)
	t.Setenv(cliproxyapi.EnvKey, "test-management-key")
	t.Cleanup(server.Close)
	return server
}

func TestCodexProxyFallbackResetCreditsAreReturned(t *testing.T) {
	quotaCalls, resetCalls := 0, 0
	proxyTestServerWithResetCalls(t, `{"files":[{"auth_index":"codex-one","provider":"codex"}]}`, &quotaCalls, &resetCalls, false)
	a := &App{config: &config.Config{Providers: map[string]json.RawMessage{}}, cache: cache.New(t.TempDir())}
	out, _, err := captureFetchOutput(t, func() error {
		return a.fetchOneWithProvider(context.Background(), "cliproxyapi", false, false, provider.Get)
	})
	if err != nil {
		t.Fatal(err)
	}
	if quotaCalls != 1 || resetCalls != 1 {
		t.Fatalf("quota calls=%d reset-credit calls=%d, want 1/1", quotaCalls, resetCalls)
	}
	var results []model.ProviderResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results=%d, want 1: %s", len(results), out)
	}
	account := results[0].Usage.(map[string]any)
	usage := account["usage"].(map[string]any)
	resetCredits, ok := usage["resetCredits"].(map[string]any)
	if !ok || resetCredits["available"] != float64(7) {
		t.Fatalf("reset credits were not propagated: %#v", usage)
	}
}

func TestCodexCombinedFetchKeepsProxyResetDetailsOnLocalAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("OPENTRACKER_CODEX_ACCESS_TOKEN", "fake-local-token")
	t.Setenv("OPENTRACKER_CODEX_ACCOUNT_ID", "shared-account")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v8/management/credentials" {
			_, _ = fmt.Fprint(w, `{"files":[{"auth_index":"proxy-one","provider":"codex","chatgpt_account_id":"shared-account"}]}`)
			return
		}
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request["url"] == "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits" {
			_, _ = fmt.Fprint(w, `{"status_code":200,"body":"{\"available_count\":7,\"credits\":[{\"reset_type\":\"codex_rate_limits\",\"status\":\"available\",\"expires_at\":null}]}"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"status_code":200,"body":"{\"plan_type\":\"plus\"}"}`)
	}))
	t.Setenv(cliproxyapi.EnvURL, server.URL)
	t.Setenv(cliproxyapi.EnvKey, "test-management-key")
	t.Cleanup(server.Close)
	available := int64(2)
	localUsage := codex.UsageSnapshot{ResetCredits: &codex.ResetCredits{Available: &available}}
	a := &App{config: &config.Config{Providers: map[string]json.RawMessage{}}, cache: cache.New(t.TempDir())}
	out, _, err := captureFetchOutput(t, func() error {
		return a.fetchOneWithProvider(context.Background(), "codex", false, false, func(string, *config.Config) (provider.Provider, error) {
			return &fakeCodexProvider{usage: localUsage}, nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	var results []model.ProviderResult
	if err := json.Unmarshal([]byte(out), &results); err != nil || len(results) != 1 {
		t.Fatalf("combined output=%s err=%v", out, err)
	}
	usage := results[0].Usage.(map[string]any)
	resetCredits := usage["resetCredits"].(map[string]any)
	credits, ok := resetCredits["credits"].([]any)
	if resetCredits["available"] != float64(2) || !ok || len(credits) != 1 || credits[0].(map[string]any)["expiresAt"] != nil {
		t.Fatalf("local quota count was overwritten or details missing: %#v", resetCredits)
	}
}

func TestCodexProxyCombinationAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name, files, localID string
		localError           error
		wantProxy            int
		wantResults          int
		wantErr              bool
		distinctExpected     int
	}{
		{name: "matching local identity", files: `{"files":[{"auth_index":"same","provider":"codex","chatgpt_account_id":"local-id"}]}`, localID: "local-id", wantProxy: 1, wantResults: 1},
		{name: "local failure still returns proxy", files: `{"files":[{"auth_index":"same","provider":"codex","chatgpt_account_id":"local-id"}]}`, localID: "local-id", localError: errors.New("local unavailable"), wantProxy: 1, wantResults: 1, wantErr: true},
		{name: "email does not deduplicate distinct accounts", files: `{"files":[{"auth_index":"a","provider":"codex","email":"same@example.test","chatgpt_account_id":"account-a"},{"auth_index":"b","provider":"codex","email":"same@example.test","chatgpt_account_id":"account-b"}]}`, wantProxy: 2, wantResults: 2, distinctExpected: 2},
		{name: "matching proxy IDs deduplicate", files: `{"files":[{"auth_index":"a","provider":"codex","chatgpt_account_id":"account-a"},{"auth_index":"b","provider":"codex","chatgpt_account_id":"account-a"}]}`, wantProxy: 2, wantResults: 1},
		{name: "missing IDs remain separate", files: `{"files":[{"auth_index":"a","provider":"codex","email":"same@example.test"},{"auth_index":"b","provider":"codex","email":"same@example.test"}]}`, wantProxy: 2, wantResults: 2},
		{name: "partial proxy account failure", files: `{"files":[{"auth_index":"good","provider":"codex"},{"name":"private-marker-one","auth_index":"bad-one","provider":"codex"},{"name":"private-marker-two","auth_index":"bad-two","provider":"codex"}]}`, wantProxy: 3, wantResults: 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
			t.Setenv("OPENTRACKER_CODEX_ACCESS_TOKEN", "")
			if tc.localID != "" || tc.localError != nil {
				t.Setenv("OPENTRACKER_CODEX_ACCESS_TOKEN", "fake-local-token")
			}
			t.Setenv("OPENTRACKER_CODEX_ACCOUNT_ID", tc.localID)
			proxyCalls := 0
			proxyTestServer(t, tc.files, &proxyCalls)
			localCalls := 0
			a := &App{config: &config.Config{Providers: map[string]json.RawMessage{}}, cache: cache.New(t.TempDir())}
			out, stderr, err := captureFetchOutput(t, func() error {
				return a.fetchOneWithProvider(context.Background(), "codex", false, false, func(string, *config.Config) (provider.Provider, error) {
					return &fakeCodexProvider{err: tc.localError, calls: &localCalls}, nil
				})
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			var results []model.ProviderResult
			if e := json.Unmarshal([]byte(out), &results); e != nil {
				t.Fatalf("invalid stdout JSON %q: %v", out, e)
			}
			if len(results) != tc.wantResults || proxyCalls != tc.wantProxy {
				t.Fatalf("results=%d proxyCalls=%d want %d/%d", len(results), proxyCalls, tc.wantResults, tc.wantProxy)
			}
			if tc.localError != nil && !strings.Contains(stderr, "local Codex account") {
				t.Fatalf("missing local failure on stderr: %q", stderr)
			}
			if tc.name == "partial proxy account failure" {
				if !strings.Contains(stderr, "account #2") || !strings.Contains(stderr, "account #3") {
					t.Fatalf("missing distinct failure identifiers on stderr: %q", stderr)
				}
				if strings.Contains(stderr, "private-marker") {
					t.Fatalf("untrusted credential name leaked to stderr: %q", stderr)
				}
			}
			if strings.Contains(stderr, "test-management-key") {
				t.Fatal("management key leaked to stderr")
			}
		})
	}
}

func TestCodexProxyEmailLabelsMatchingLocalAccount(t *testing.T) {
	const email = "person@example.invalid"
	for _, tc := range []struct {
		name            string
		localID         string
		proxyID         string
		localEmail      string
		wantLocal       string
		wantProxy       bool
		localPointer    bool
		emptyProxyEmail bool
	}{
		{name: "matching ID fills empty local email", localID: "shared-id", proxyID: "shared-id", wantLocal: email},
		{name: "matching ID with empty proxy email", localID: "shared-id", proxyID: "shared-id", emptyProxyEmail: true},
		{name: "mismatched ID leaves local unlabeled", localID: "local-id", proxyID: "proxy-id", wantProxy: true},
		{name: "missing proxy ID leaves proxy account separate", localID: "local-id", proxyID: "", wantProxy: true},
		{name: "missing IDs leave proxy account separate", wantProxy: true},
		{name: "existing local email is preserved", localID: "shared-id", proxyID: "shared-id", localEmail: "existing@example.invalid", wantLocal: "existing@example.invalid", localPointer: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
			t.Setenv("OPENTRACKER_CODEX_ACCESS_TOKEN", "fake-local-token")
			t.Setenv("OPENTRACKER_CODEX_ACCOUNT_ID", tc.localID)
			proxyEmail := email
			if tc.emptyProxyEmail {
				proxyEmail = ""
			}
			credentials := fmt.Sprintf(`{"files":[{"auth_index":"proxy","provider":"codex","email":%q,"chatgpt_account_id":%q}]}`, proxyEmail, tc.proxyID)
			calls := 0
			proxyTestServer(t, credentials, &calls)
			var localUsage interface{} = codex.UsageSnapshot{AccountEmail: tc.localEmail}
			if tc.localPointer {
				localUsage = &codex.UsageSnapshot{AccountEmail: tc.localEmail}
			}
			a := &App{config: &config.Config{Providers: map[string]json.RawMessage{}}, cache: cache.New(t.TempDir())}
			out, stderr, err := captureFetchOutput(t, func() error {
				return a.fetchOneWithProvider(context.Background(), "codex", false, false, func(string, *config.Config) (provider.Provider, error) {
					return &fakeCodexProvider{usage: localUsage}, nil
				})
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stderr, email) || strings.Contains(stderr, "existing@example.invalid") {
				t.Fatalf("account email leaked to stderr: %q", stderr)
			}
			var results []model.ProviderResult
			if err := json.Unmarshal([]byte(out), &results); err != nil {
				t.Fatalf("decode output: %v; output=%q", err, out)
			}
			var localCount, proxyCount int
			for _, result := range results {
				switch result.Provider {
				case "codex":
					localCount++
					usage := result.Usage.(map[string]any)
					if got := usage["accountEmail"]; got != tc.wantLocal && (got != nil || tc.wantLocal != "") {
						t.Errorf("local accountEmail=%v, want %q", got, tc.wantLocal)
					}
				case "cliproxyapi":
					proxyCount++
					usage := result.Usage.(map[string]any)
					if got := usage["accountEmail"]; got != proxyEmail && (got != nil || proxyEmail != "") {
						t.Errorf("proxy accountEmail=%v, want %q", got, proxyEmail)
					}
				}
			}
			if localCount != 1 || (proxyCount == 1) != tc.wantProxy {
				t.Fatalf("local results=%d proxy results=%d, want one local and proxy=%t; output=%s", localCount, proxyCount, tc.wantProxy, out)
			}
		})
	}
}

func TestCodexProxyOnlyFetchIsNotCachedAndReportsMissingKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("OPENTRACKER_CODEX_ACCESS_TOKEN", "")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	t.Setenv("OPENTRACKER_CLAUDE_ACCESS_TOKEN", "placeholder")
	_ = os.Unsetenv("OPENTRACKER_CLAUDE_ACCESS_TOKEN")
	t.Setenv("OPENTRACKER_CODEX_ACCOUNT_ID", "")
	calls := 0
	proxyTestServer(t, `{"files":[{"auth_index":"one","type":"codex"}]}`, &calls)
	a := &App{config: &config.Config{Providers: map[string]json.RawMessage{}}, cache: cache.New(t.TempDir())}
	for _, force := range []bool{false, true, false} {
		_, _, err := captureFetchOutput(t, func() error {
			return a.fetchOneWithProvider(context.Background(), "cliproxyapi", force, false, provider.Get)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 {
		t.Fatalf("proxy API calls=%d, want 3 uncached fetches", calls)
	}
	t.Setenv(cliproxyapi.EnvKey, "")
	_, _, err := captureFetchOutput(t, func() error {
		return a.fetchOneWithProvider(context.Background(), "cliproxyapi", false, false, provider.Get)
	})
	if err == nil || !strings.Contains(err.Error(), "run 'opentracker login cliproxyapi'") || !strings.Contains(err.Error(), cliproxyapi.EnvKey) {
		t.Fatalf("missing actionable key error: %v", err)
	}
}

func TestLocalCodexFetchBypassesCacheWhenProxyIsUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("OPENTRACKER_CODEX_ACCESS_TOKEN", "fake-local-token")
	t.Setenv(cliproxyapi.EnvKey, "")
	t.Setenv(cliproxyapi.EnvURL, "")
	a := &App{config: &config.Config{Providers: map[string]json.RawMessage{}}, cache: cache.New(t.TempDir())}
	calls := 0
	fetch := func(force bool) (string, string, error) {
		return captureFetchOutput(t, func() error {
			return a.fetchOneWithProvider(context.Background(), "codex", force, false, func(string, *config.Config) (provider.Provider, error) { return &fakeCodexProvider{calls: &calls}, nil })
		})
	}
	out, stderr, err := fetch(false)
	if err != nil || stderr != "" {
		t.Fatalf("error=%v stderr=%q", err, stderr)
	}
	if _, _, err := fetch(false); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("unforced fetch calls=%d, want 2", calls)
	}
	if _, _, err := fetch(true); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("forced fetch calls=%d, want 3", calls)
	}
	var results []model.ProviderResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Provider != "codex" {
		t.Fatalf("unexpected local result: %s", out)
	}
}

func TestCodexFetchIgnoresLegacyCacheAcrossCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv(cliproxyapi.EnvKey, "")
	t.Setenv(cliproxyapi.EnvURL, "")
	c := cache.New(t.TempDir())
	legacy := []model.ProviderResult{{Provider: "codex", Usage: map[string]string{"account": "stale-account"}}}
	if err := c.Set("codex", legacy, time.Minute); err != nil {
		t.Fatalf("seed legacy cache: %v", err)
	}
	a := &App{config: &config.Config{Providers: map[string]json.RawMessage{}}, cache: c}
	calls := 0
	for _, tc := range []struct {
		credential string
		force      bool
	}{
		{credential: "credential-a"},
		{credential: "credential-a", force: true},
		{credential: "credential-b"},
		{credential: "credential-b", force: true},
	} {
		t.Setenv("OPENTRACKER_CODEX_ACCESS_TOKEN", tc.credential)
		out, _, err := captureFetchOutput(t, func() error {
			return a.fetchOneWithProvider(context.Background(), "codex", tc.force, false, func(string, *config.Config) (provider.Provider, error) {
				return &fakeCodexProvider{calls: &calls, usage: map[string]string{"account": tc.credential}}, nil
			})
		})
		if err != nil {
			t.Fatalf("fetch for %s (force=%t): %v", tc.credential, tc.force, err)
		}
		var results []model.ProviderResult
		if err := json.Unmarshal([]byte(out), &results); err != nil {
			t.Fatalf("decode fetch output: %v", err)
		}
		if len(results) != 1 || results[0].Usage.(map[string]any)["account"] != tc.credential {
			t.Fatalf("fetch returned wrong account for %s: %s", tc.credential, out)
		}
	}
	if calls != 4 {
		t.Fatalf("provider fetches=%d, want 4", calls)
	}
	var stillLegacy []model.ProviderResult
	if !c.Get("codex", &stillLegacy) || stillLegacy[0].Usage.(map[string]any)["account"] != "stale-account" {
		t.Fatal("Codex fetch replaced or removed the legacy cache entry")
	}
}

func TestStoredCLIProxyKeyringFailureDoesNotHideLocalCodexResult(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("OPENTRACKER_CODEX_ACCESS_TOKEN", "fake-local-token")
	t.Setenv("OPENTRACKER_CODEX_ACCOUNT_ID", "")
	t.Setenv(cliproxyapi.EnvKey, "")
	t.Setenv(cliproxyapi.EnvURL, "")
	cfg := &config.Config{Providers: map[string]json.RawMessage{"cliproxyapi": json.RawMessage(`{"credentialId":"saved-id"}`)}}
	keyring.MockInitWithError(errors.New("keyring locked"))
	defer keyring.MockInit()
	a := &App{config: cfg, cache: cache.New(t.TempDir())}
	out, stderr, err := captureFetchOutput(t, func() error {
		return a.fetchOneWithProvider(context.Background(), "codex", false, false, func(string, *config.Config) (provider.Provider, error) { return &fakeCodexProvider{}, nil })
	})
	if err == nil {
		t.Fatalf("fetch error=%v", err)
	}
	if !strings.Contains(stderr, "unlock or configure the keyring") || strings.Contains(stderr, "keyring locked") {
		t.Fatalf("keyring failure was hidden or raw detail leaked: %q", stderr)
	}
	var results []model.ProviderResult
	if decodeErr := json.Unmarshal([]byte(out), &results); decodeErr != nil || len(results) != 1 || results[0].Provider != "codex" {
		t.Fatalf("local result missing after stored key error: %s (%v)", out, decodeErr)
	}
}

func TestFetchAllSkipsSeparateCLIProxyAPIProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv("OPENTRACKER_CODEX_ACCESS_TOKEN", "")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	t.Setenv("OPENTRACKER_CLAUDE_ACCESS_TOKEN", "placeholder")
	_ = os.Unsetenv("OPENTRACKER_CLAUDE_ACCESS_TOKEN")
	calls := 0
	proxyTestServer(t, `{"files":[{"auth_index":"one","type":"codex"},{"auth_index":"two","type":"claude"}]}`, &calls)
	a := &App{config: &config.Config{Providers: map[string]json.RawMessage{}}, cache: cache.New(t.TempDir())}
	_, _, err := captureFetchOutput(t, func() error { return a.fetchAll(context.Background(), false) })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("proxy API calls=%d, want one per account type", calls)
	}
}

func TestFetchAllDeduplicatesCodexOnlyAfterSuccessfulLocalFetch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		localError error
		wantErr    bool
	}{
		{name: "local account succeeds"},
		{name: "local failure keeps proxy account", localError: errors.New("fake local fetch error"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
			t.Setenv("OPENTRACKER_CODEX_ACCESS_TOKEN", "fake-local-token")
			t.Setenv("OPENTRACKER_CODEX_ACCOUNT_ID", "shared-id")
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
			t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
			t.Setenv("OPENTRACKER_CLAUDE_ACCESS_TOKEN", "placeholder")
			_ = os.Unsetenv("OPENTRACKER_CLAUDE_ACCESS_TOKEN")
			files := `{"files":[{"auth_index":"same","provider":"codex","chatgpt_account_id":"shared-id"},{"auth_index":"other","provider":"codex","chatgpt_account_id":"other-id"},{"auth_index":"claude","provider":"claude","email":"shared@example.test"}]}`
			calls := 0
			proxyTestServer(t, files, &calls)
			a := &App{config: &config.Config{Providers: map[string]json.RawMessage{}}, cache: cache.New(t.TempDir())}
			localCalls := 0
			out, stderr, err := captureFetchOutput(t, func() error {
				return a.fetchAllWithProvider(context.Background(), false, func(name string, _ *config.Config) (provider.Provider, error) {
					if name != "codex" {
						t.Fatalf("unexpected local provider construction: %s", name)
					}
					return &fakeCodexProvider{err: tc.localError, calls: &localCalls}, nil
				})
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr && !strings.Contains(stderr, "local Codex account") {
				t.Fatalf("local error missing from stderr: %q", stderr)
			}
			decoder := json.NewDecoder(strings.NewReader(out))
			codexCount, claudeCount, matchingProxyCount := 0, 0, 0
			for {
				var results []model.ProviderResult
				decodeErr := decoder.Decode(&results)
				if decodeErr == io.EOF {
					break
				}
				if decodeErr != nil {
					t.Fatalf("decode output: %v, output=%q", decodeErr, out)
				}
				for _, result := range results {
					if result.Provider == "codex" {
						codexCount++
					}
					if result.Provider == "claude" {
						claudeCount++
					}
					if result.Provider == "cliproxyapi" {
						usage := result.Usage.(map[string]any)
						switch usage["accountProvider"] {
						case "codex":
							codexCount++
							if usage["accountId"] == "shared-id" {
								matchingProxyCount++
							}
						case "claude":
							claudeCount++
						}
					}
				}
			}
			if codexCount != 2 || claudeCount != 1 {
				t.Fatalf("Codex=%d Claude=%d, output=%s", codexCount, claudeCount, out)
			}
			if tc.localError == nil && matchingProxyCount != 0 {
				t.Fatalf("matching proxy account duplicated local result: %s", out)
			}
			if tc.localError != nil && matchingProxyCount != 1 {
				t.Fatalf("proxy account was dropped after local failure: %s", out)
			}
			if localCalls != 1 {
				t.Fatalf("local Codex fetch count=%d, want 1", localCalls)
			}
		})
	}
}

func isolatedClaudeProxyApp(t *testing.T, files string, calls *int, localToken string) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	claudeDir := filepath.Join(home, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	t.Setenv("OPENTRACKER_CLIPROXYAPI_KEY", "fake-management-key")
	if localToken == "" {
		t.Setenv("OPENTRACKER_CLAUDE_ACCESS_TOKEN", "placeholder")
		_ = os.Unsetenv("OPENTRACKER_CLAUDE_ACCESS_TOKEN")
	} else {
		t.Setenv("OPENTRACKER_CLAUDE_ACCESS_TOKEN", localToken)
	}
	proxyTestServer(t, files, calls)
	return &App{config: &config.Config{Providers: map[string]json.RawMessage{}}, cache: cache.New(t.TempDir())}
}

func TestClaudeFetchSupportsProxyOnlyAndCombinedAccounts(t *testing.T) {
	t.Run("proxy only", func(t *testing.T) {
		calls := 0
		a := isolatedClaudeProxyApp(t, `{"files":[{"auth_index":"proxy-one","provider":"claude","email":"shared@example.test"}]}`, &calls, "")
		out, stderr, err := captureFetchOutput(t, func() error {
			return a.fetchOneWithProvider(context.Background(), "claude", false, false, func(string, *config.Config) (provider.Provider, error) {
				t.Fatal("local provider should not be created")
				return nil, nil
			})
		})
		if err != nil || stderr != "" {
			t.Fatalf("err=%v stderr=%q", err, stderr)
		}
		var results []model.ProviderResult
		if e := json.Unmarshal([]byte(out), &results); e != nil || len(results) != 1 {
			t.Fatalf("results=%q err=%v", out, e)
		}
		if results[0].Provider != "cliproxyapi" {
			t.Fatalf("provider=%s", results[0].Provider)
		}
		usage := results[0].Usage.(map[string]any)
		if usage["accountProvider"] != "claude" || usage["accountEmail"] != "shared@example.test" {
			t.Fatalf("proxy identity not labeled: %#v", usage)
		}
	})
	t.Run("local plus proxy with duplicate email", func(t *testing.T) {
		calls := 0
		a := isolatedClaudeProxyApp(t, `{"files":[{"auth_index":"proxy-one","provider":"claude","email":"shared@example.test"}]}`, &calls, "fake-local-claude-token")
		localCalls := 0
		out, _, err := captureFetchOutput(t, func() error {
			return a.fetchOneWithProvider(context.Background(), "claude", false, false, func(string, *config.Config) (provider.Provider, error) {
				return &fakeClaudeProvider{identity: "local-usage", calls: &localCalls}, nil
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		var results []model.ProviderResult
		if e := json.Unmarshal([]byte(out), &results); e != nil || len(results) != 2 {
			t.Fatalf("results=%q err=%v", out, e)
		}
		if results[0].Provider != "claude" || results[1].Provider != "cliproxyapi" {
			t.Fatalf("local and proxy accounts collapsed: %+v", results)
		}
	})
}

func TestClaudeLocalFailureKeepsProxyResultAndSignalsError(t *testing.T) {
	calls := 0
	a := isolatedClaudeProxyApp(t, `{"files":[{"auth_index":"proxy-one","provider":"claude","email":"shared@example.test"}]}`, &calls, "fake-local-claude-token")
	localCalls := 0
	out, stderr, err := captureFetchOutput(t, func() error {
		return a.fetchOneWithProvider(context.Background(), "claude", false, false, func(string, *config.Config) (provider.Provider, error) {
			return &fakeClaudeProvider{identity: "", calls: &localCalls, err: errors.New("local fetch failed")}, nil
		})
	})
	if err == nil || !strings.Contains(stderr, "local Claude account") {
		t.Fatalf("error=%v stderr=%q", err, stderr)
	}
	var results []model.ProviderResult
	if e := json.Unmarshal([]byte(out), &results); e != nil || len(results) != 1 || results[0].Provider != "cliproxyapi" {
		t.Fatalf("proxy success missing: %q err=%v", out, e)
	}
}

func TestClaudeInvalidLocalAuthReportsErrorAndKeepsProxyResult(t *testing.T) {
	calls := 0
	a := isolatedClaudeProxyApp(t, `{"files":[{"auth_index":"proxy-one","provider":"claude"}]}`, &calls, "")
	t.Setenv("OPENTRACKER_CLAUDE_ACCESS_TOKEN", "")
	out, stderr, err := captureFetchOutput(t, func() error {
		return a.fetchOneWithProvider(context.Background(), "claude", false, false, func(string, *config.Config) (provider.Provider, error) {
			t.Fatal("invalid local auth must not construct a provider")
			return nil, nil
		})
	})
	if err == nil || !strings.Contains(stderr, "local Claude account") {
		t.Fatalf("err=%v stderr=%q", err, stderr)
	}
	var results []model.ProviderResult
	if e := json.Unmarshal([]byte(out), &results); e != nil || len(results) != 1 || results[0].Provider != "cliproxyapi" {
		t.Fatalf("proxy result missing: %q err=%v", out, e)
	}
}

func TestFetchCLIProxyAPIReturnsBothAccountTypes(t *testing.T) {
	calls := 0
	a := isolatedClaudeProxyApp(t, `{"files":[{"auth_index":"c","provider":"codex"},{"auth_index":"cl","provider":"claude"}]}`, &calls, "")
	out, _, err := captureFetchOutput(t, func() error {
		return a.fetchOneWithProvider(context.Background(), "cliproxyapi", false, false, provider.Get)
	})
	if err != nil {
		t.Fatal(err)
	}
	var results []model.ProviderResult
	if e := json.Unmarshal([]byte(out), &results); e != nil || len(results) != 2 {
		t.Fatalf("results=%q err=%v", out, e)
	}
	seen := map[string]int{}
	for _, result := range results {
		seen[result.Usage.(map[string]any)["accountProvider"].(string)]++
	}
	if seen["codex"] != 1 || seen["claude"] != 1 {
		t.Fatalf("account types=%v", seen)
	}
}

func TestClaudePartialProxyFailureKeepsSuccessAndReportsOrdinal(t *testing.T) {
	calls := 0
	a := isolatedClaudeProxyApp(t, `{"files":[{"auth_index":"good","provider":"claude"},{"name":"secret-credential-name","auth_index":"bad-claude","provider":"claude"}]}`, &calls, "")
	out, stderr, err := captureFetchOutput(t, func() error {
		return a.fetchOneWithProvider(context.Background(), "claude", false, false, provider.Get)
	})
	if err == nil || !strings.Contains(stderr, "claude account #2") || strings.Contains(stderr, "secret-credential-name") {
		t.Fatalf("err=%v stderr=%q", err, stderr)
	}
	var results []model.ProviderResult
	if e := json.Unmarshal([]byte(out), &results); e != nil || len(results) != 1 {
		t.Fatalf("successful Claude result missing: %q err=%v", out, e)
	}
}

func TestCodexFetchFiltersClaudeAndFetchAllPrintsBothOnce(t *testing.T) {
	t.Run("codex filter", func(t *testing.T) {
		calls := 0
		a := isolatedClaudeProxyApp(t, `{"files":[{"auth_index":"c","provider":"codex"},{"auth_index":"cl","provider":"claude"}]}`, &calls, "")
		out, _, err := captureFetchOutput(t, func() error { return a.fetchOneWithProvider(context.Background(), "codex", false, false, provider.Get) })
		if err != nil {
			t.Fatal(err)
		}
		var results []model.ProviderResult
		if e := json.Unmarshal([]byte(out), &results); e != nil || len(results) != 1 {
			t.Fatalf("results=%q err=%v", out, e)
		}
		usage := results[0].Usage.(map[string]any)
		if usage["accountProvider"] != "codex" {
			t.Fatalf("unexpected proxy account: %#v", usage)
		}
	})
	t.Run("fetch all", func(t *testing.T) {
		calls := 0
		a := isolatedClaudeProxyApp(t, `{"files":[{"auth_index":"c","provider":"codex"},{"auth_index":"cl","provider":"claude"}]}`, &calls, "")
		out, _, err := captureFetchOutput(t, func() error { return a.fetchAll(context.Background(), false) })
		if err != nil {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(strings.NewReader(out))
		seen := map[string]int{}
		for decoder.More() {
			var results []model.ProviderResult
			if e := decoder.Decode(&results); e != nil {
				t.Fatal(e)
			}
			for _, result := range results {
				key := result.Provider
				if result.Provider == "cliproxyapi" {
					key = fmt.Sprint(result.Usage.(map[string]any)["accountProvider"])
				}
				seen[key]++
			}
		}
		if seen["codex"] != 1 || seen["claude"] != 1 {
			t.Fatalf("provider results printed other than once: %#v; output %s", seen, out)
		}
	})
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
	err      error
}

func (p *fakeClaudeProvider) Name() string          { return "claude" }
func (p *fakeClaudeProvider) CacheIdentity() string { return p.identity }
func (p *fakeClaudeProvider) Fetch(context.Context) (string, error) {
	(*p.calls)++
	return p.identity, p.err
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
