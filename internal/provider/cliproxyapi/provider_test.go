package cliproxyapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"opentracker/internal/config"
	"opentracker/internal/provider/claude"
	"opentracker/internal/provider/codex"
)

func isolateProxyProviderEnvironment(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	t.Setenv(EnvKey, "")
	t.Setenv(EnvURL, "")
}

func TestFetchCodexCredentials(t *testing.T) {
	isolateProxyProviderEnvironment(t)
	idToken := map[string]any{"chatgpt_account_id": "acct-1"}
	var apiCall map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer management-secret" {
			t.Errorf("management authorization missing")
		}
		switch r.URL.Path {
		case "/v8/management/credentials":
			credentials, _ := json.Marshal(map[string]any{"files": []any{
				map[string]any{"name": "codex.json", "auth_index": "idx-1", "type": "codex", "email": "same@example.test", "metadata": map[string]any{"id_token": idToken}},
				map[string]any{"name": "codex-two.json", "authIndex": "idx-2", "provider": "codex", "email": "same@example.test", "id_token": map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-2"}}},
				map[string]any{"name": "off.json", "auth_index": "idx-3", "provider": "claude"},
			}})
			_, _ = w.Write(credentials)
		case "/v8/management/requests/api-call":
			if err := json.NewDecoder(r.Body).Decode(&apiCall); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"plan_type\":\"plus\",\"rate_limit\":{\"primary_window\":{\"used_percent\":25}}}"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv(EnvURL, server.URL)
	t.Setenv(EnvKey, "management-secret")
	accounts, failures, err := FetchCodexWithConfig(context.Background(), &config.Config{Providers: map[string]json.RawMessage{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 || len(accounts) != 2 {
		t.Fatalf("accounts=%d failures=%d", len(accounts), len(failures))
	}
	usage, ok := accounts[0].Usage.(codex.UsageSnapshot)
	if accounts[0].ID != "acct-1" || accounts[0].Email != "same@example.test" || !ok || usage.Plan != "plus" || accounts[1].ID != "acct-2" {
		t.Fatalf("unexpected account: %+v", accounts[0])
	}
	if apiCall["method"] != "GET" || apiCall["url"] != usageURL || apiCall["authIndex"] != "idx-2" {
		t.Fatalf("unexpected api-call request: %#v", apiCall)
	}
	header := apiCall["header"].(map[string]any)
	if header["Authorization"] != "Bearer $TOKEN$" || header["Content-Type"] != "application/json" || header["User-Agent"] != requestUserAgent || header["Chatgpt-Account-Id"] != "acct-2" {
		t.Fatalf("unexpected headers: %#v", header)
	}
	serialized, _ := json.Marshal(accounts)
	if strings.Contains(string(serialized), "management-secret") || strings.Contains(string(serialized), "chatgpt_account_id\":\"acct-1\"") {
		t.Fatal("credential leaked into account result")
	}
}

func TestFetchMixedCodexAndClaudeCredentials(t *testing.T) {
	isolateProxyProviderEnvironment(t)
	var calls []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v8/management/credentials" {
			_, _ = w.Write([]byte(`{"files":[{"auth_index":"codex-1","provider":"codex"},{"auth_index":"claude-1","type":"claude","email":"same@example.test"},{"auth_index":"disabled","provider":"claude","disabled":true},{"auth_index":"ignored","provider":"gemini"}]}`))
			return
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		calls = append(calls, request)
		if request["url"] == claudeUsageURL {
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"five_hour\":{\"utilization\":42}}"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"plan_type\":\"plus\"}"}`))
	}))
	defer server.Close()
	t.Setenv(EnvURL, server.URL)
	t.Setenv(EnvKey, "management-test-key")
	accounts, failures, err := FetchWithConfig(t.Context(), &config.Config{Providers: map[string]json.RawMessage{}})
	if err != nil || len(failures) != 0 || len(accounts) != 2 {
		t.Fatalf("accounts=%d failures=%v err=%v", len(accounts), failures, err)
	}
	if accounts[0].AccountProvider != "claude" || accounts[0].Email != "same@example.test" || accounts[1].AccountProvider != "codex" {
		t.Fatalf("unexpected mixed results: %+v", accounts)
	}
	claudeUsage, ok := accounts[0].Usage.(claude.UsageSnapshot)
	if !ok || claudeUsage.FiveHour == nil || claudeUsage.FiveHour.UsedPercent != 42 {
		t.Fatalf("Claude parser result = %#v", accounts[0].Usage)
	}
	if len(calls) != 2 {
		t.Fatalf("api-call count=%d want 2", len(calls))
	}
	claudeCall := calls[1]
	if claudeCall["url"] != claudeUsageURL || claudeCall["authIndex"] != "claude-1" {
		t.Fatalf("unexpected Claude request: %#v", claudeCall)
	}
	headers := claudeCall["header"].(map[string]any)
	if headers["Authorization"] != "Bearer $TOKEN$" || headers["anthropic-beta"] != "oauth-2025-04-20" || headers["Accept"] != "application/json" || headers["Content-Type"] != "application/json" || headers["User-Agent"] != "claude-cli/2.1.280 (external, cli)" {
		t.Fatalf("unexpected Claude headers: %#v", headers)
	}
}

func TestClaudeFilterAndRateLimitFailure(t *testing.T) {
	isolateProxyProviderEnvironment(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v8/management/credentials" {
			_, _ = w.Write([]byte(`{"files":[{"auth_index":"codex","provider":"codex"},{"auth_index":"claude","provider":"claude"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"status_code":429,"body":"private error body"}`))
	}))
	defer server.Close()
	t.Setenv(EnvURL, server.URL)
	t.Setenv(EnvKey, "management-test-key")
	accounts, failures, err := FetchClaudeWithConfig(t.Context(), &config.Config{Providers: map[string]json.RawMessage{}})
	if err != nil || len(accounts) != 0 || len(failures) != 1 || failures[0].Provider != "claude" {
		t.Fatalf("accounts=%v failures=%v err=%v", accounts, failures, err)
	}
	if !strings.Contains(failures[0].Err.Error(), "HTTP 429") || !strings.Contains(failures[0].Err.Error(), "retry later") || strings.Contains(failures[0].Err.Error(), "private") {
		t.Fatalf("unsafe/incomplete rate-limit error: %v", failures[0].Err)
	}
}

func TestClaudeUsageWithoutQuotaWindowsFails(t *testing.T) {
	isolateProxyProviderEnvironment(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v8/management/credentials" {
			_, _ = w.Write([]byte(`{"files":[{"auth_index":"claude-empty","provider":"claude"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"status_code":200,"body":"{}"}`))
	}))
	defer server.Close()
	t.Setenv(EnvURL, server.URL)
	t.Setenv(EnvKey, "management-test-key")
	accounts, failures, err := FetchClaudeWithConfig(context.Background(), &config.Config{Providers: map[string]json.RawMessage{}})
	if err != nil || len(accounts) != 0 || len(failures) != 1 || !strings.Contains(failures[0].Err.Error(), "no quota windows") {
		t.Fatalf("accounts=%v failures=%v err=%v", accounts, failures, err)
	}
}

func TestEndpointRejectsUnsafeURLs(t *testing.T) {
	isolateProxyProviderEnvironment(t)
	for _, raw := range []string{"http://example.test:8317", "http://127.0.0.1:8317@evil.test", "http://127.0.0.1:8317?x=1"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv(EnvURL, raw)
			if _, err := endpoint(); err == nil {
				t.Fatal("expected URL rejection")
			}
		})
	}
}

func TestFetchRejectsMalformedListsPartialFailuresAndRedirects(t *testing.T) {
	isolateProxyProviderEnvironment(t)
	for _, tc := range []struct{ name, list, status, want string }{
		{"missing-files", `{}`, ``, "no files list"},
		{"null-files", `{"files":null}`, ``, "no files list"},
		{"bad-files", `{"files":{}}`, ``, "invalid files list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.list)) }))
			defer s.Close()
			t.Setenv(EnvURL, s.URL)
			t.Setenv(EnvKey, "secret")
			_, _, err := Fetch(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want %q", err, tc.want)
			}
		})
	}
	t.Run("partial", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v8/management/credentials" {
				_, _ = w.Write([]byte(`{"files":[{"auth_index":"good","provider":"codex"},{"auth_index":"bad","provider":"codex"}]}`))
				return
			}
			var request map[string]any
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request["authIndex"] == "bad" {
				_, _ = w.Write([]byte(`{"status_code":403,"body":"sensitive upstream error"}`))
				return
			}
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"plan_type\":\"plus\"}"}`))
		}))
		defer s.Close()
		t.Setenv(EnvURL, s.URL)
		t.Setenv(EnvKey, "secret")
		accounts, failures, err := Fetch(context.Background())
		if err != nil || len(accounts) != 1 || len(failures) != 1 || failures[0].Account != 2 {
			t.Fatalf("accounts=%d failures=%d err=%v", len(accounts), len(failures), err)
		}
		if strings.Contains(failures[0].Err.Error(), "sensitive") {
			t.Fatal("upstream response leaked")
		}
	})
	t.Run("malformed usage body", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v8/management/credentials" {
				_, _ = w.Write([]byte(`{"files":[{"auth_index":"one","provider":"codex"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"status_code":200,"body":"not-json"}`))
		}))
		defer s.Close()
		t.Setenv(EnvURL, s.URL)
		t.Setenv(EnvKey, "secret")
		accounts, failures, err := Fetch(context.Background())
		if err != nil || len(accounts) != 0 || len(failures) != 1 || !strings.Contains(failures[0].Err.Error(), "invalid Codex usage") {
			t.Fatalf("accounts=%d failures=%v err=%v", len(accounts), failures, err)
		}
	})
	t.Run("oversized response", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", maxResponseSize+1)))
		}))
		defer s.Close()
		t.Setenv(EnvURL, s.URL)
		t.Setenv(EnvKey, "secret")
		_, _, err := Fetch(context.Background())
		if err == nil || !strings.Contains(err.Error(), "size limit") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("redirect", func(t *testing.T) {
		called := false
		target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
		defer target.Close()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
		defer server.Close()
		t.Setenv(EnvURL, server.URL)
		t.Setenv(EnvKey, "secret")
		_, _, err := Fetch(context.Background())
		if err == nil || called {
			t.Fatalf("redirect followed or not rejected: called=%v err=%v", called, err)
		}
	})
}

func TestAccountIDUsesOnlyAuthoritativeClaims(t *testing.T) {
	isolateProxyProviderEnvironment(t)
	if got := accountID(credential{IDToken: map[string]any{"chatgpt_account_id": "acct-object"}}); got != "acct-object" {
		t.Fatalf("got %q", got)
	}
	if got := accountID(credential{Email: "acct-object"}); got != "" {
		t.Fatalf("email used as account ID: %q", got)
	}
	if got := accountID(credential{IDToken: map[string]any{"email": "acct-object"}}); got != "" {
		t.Fatalf("email claim used as account ID: %q", got)
	}
}
