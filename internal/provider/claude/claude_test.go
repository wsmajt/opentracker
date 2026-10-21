package claude

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadAuthPrecedenceAndValidation(t *testing.T) {
	tempAuthEnvironment(t)
	dir := t.TempDir()
	writeCredentials(t, filepath.Join(dir, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"file-token","scopes":["user:profile"],"subscriptionType":"pro"}}`)
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	t.Setenv(accessTokenEnv, "")
	if _, err := loadAuth(); err == nil {
		t.Fatal("empty explicit token should fail without file fallback")
	}
	t.Setenv(accessTokenEnv, "env-token")
	a, err := loadAuth()
	if err != nil || a.token != "env-token" {
		t.Fatalf("load env auth: auth=%q err=%v", a.token, err)
	}
	_ = os.Unsetenv(accessTokenEnv)
	a, err = loadAuth()
	if err != nil || a.token != "file-token" || a.plan != "pro" {
		t.Fatalf("load file auth: %#v, %v", a, err)
	}
}

func TestLoadAuthRejectsExpiredAndInsufficientScope(t *testing.T) {
	tempAuthEnvironment(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	_ = os.Unsetenv(accessTokenEnv)
	path := filepath.Join(dir, ".credentials.json")
	writeCredentials(t, path, `{"claudeAiOauth":{"accessToken":"secret","expiresAt":1,"scopes":["user:profile"]}}`)
	if _, err := loadAuth(); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expiry error, got %v", err)
	}
	writeCredentials(t, path, `{"claudeAiOauth":{"accessToken":"secret","scopes":["user:inference"]}}`)
	if _, err := loadAuth(); err == nil || !strings.Contains(err.Error(), "user:profile") {
		t.Fatalf("expected scope error, got %v", err)
	}
}

func TestParseUsageWindowsAndNulls(t *testing.T) {
	input := `{"five_hour":{"utilization":17.5,"resets_at":"2026-10-21T12:00:00Z"},"seven_day":{"utilization":null},"seven_day_sonnet":{"utilization":40},"seven_day_opus":{"utilization":65},"new_field":true}`
	snapshot, err := parseUsage(input, "pro", time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.FiveHour == nil || snapshot.FiveHour.UsedPercent != 17.5 || snapshot.FiveHour.WindowSeconds != 18000 {
		t.Fatalf("five-hour window: %#v", snapshot.FiveHour)
	}
	if snapshot.SevenDay != nil || snapshot.Sonnet == nil || snapshot.Opus == nil {
		t.Fatalf("window mapping: %#v", snapshot)
	}
	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(string(encoded), `"sevenDay"`) {
		t.Fatalf("missing window was emitted: %s", encoded)
	}
	if _, err := parseUsage(`{"five_hour":{"utilization":"bad"}}`, "", time.Now()); err == nil {
		t.Fatal("malformed numeric utilization should fail")
	}
	for _, invalid := range []string{"null", `[]`, `"text"`} {
		if _, err := parseUsage(invalid, "", time.Now()); err == nil {
			t.Errorf("accepted non-object JSON %s", invalid)
		}
	}
}

func TestFetchHeadersErrorsAndCooldownIsolation(t *testing.T) {
	t.Setenv(accessTokenEnv, "claude-secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer claude-secret-token" || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected request headers: %#v", r.Header)
		}
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("private response detail"))
	}))
	defer server.Close()
	a, _ := loadAuth()
	p := &ClaudeProvider{client: server.Client(), url: server.URL, cooldownDir: t.TempDir(), auth: a}
	_, err := p.Fetch(context.Background())
	if err == nil || strings.Contains(err.Error(), "private response") || strings.Contains(err.Error(), a.token) {
		t.Fatalf("unsafe or missing rate-limit error: %v", err)
	}
	if err := p.checkCooldown(a.key, time.Now()); err == nil {
		t.Fatal("cooldown was not persisted")
	}
	if err := p.checkCooldown("another-account", time.Now()); err != nil {
		t.Fatalf("cooldown leaked to another credential: %v", err)
	}
	if err := p.checkCooldown(a.key, time.Now().Add(2*time.Minute)); err != nil {
		t.Fatalf("expired cooldown remains active: %v", err)
	}
}

func TestRedirectDoesNotForwardOAuthToken(t *testing.T) {
	tempAuthEnvironment(t)
	t.Setenv(accessTokenEnv, "redirect-secret")
	var received bool
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { received = r.Header.Get("Authorization") != "" }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", other.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirect.Close()
	a, _ := loadAuth()
	p := &ClaudeProvider{client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, url: redirect.URL, cooldownDir: t.TempDir(), auth: a}
	_, err := p.Fetch(context.Background())
	if err == nil {
		t.Fatal("redirect response should fail")
	}
	if received {
		t.Fatal("OAuth token was forwarded to redirect target")
	}
}

func TestCredentialPathPrecedenceAndInvalidFiles(t *testing.T) {
	tempAuthEnvironment(t)
	configDir, secureDir := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", secureDir)
	writeCredentials(t, filepath.Join(configDir, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"config"}}`)
	writeCredentials(t, filepath.Join(secureDir, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"secure"}}`)
	a, err := loadAuth()
	if err != nil || a.token != "secure" {
		t.Fatalf("secure-storage path did not take precedence: %#v, %v", a, err)
	}
	writeCredentials(t, filepath.Join(secureDir, ".credentials.json"), `not-json`)
	if _, err := loadAuth(); err == nil || !strings.Contains(err.Error(), "cannot parse") {
		t.Fatalf("expected malformed file error, got %v", err)
	}
	writeCredentials(t, filepath.Join(secureDir, ".credentials.json"), `{"mcp":{}}`)
	if _, err := loadAuth(); err == nil || !strings.Contains(err.Error(), "no OAuth") {
		t.Fatalf("expected MCP-only file rejection, got %v", err)
	}
	if err := os.Remove(filepath.Join(secureDir, ".credentials.json")); err != nil {
		t.Fatal(err)
	}
	if a, err := loadAuth(); err != nil || a.token != "" {
		t.Fatalf("missing credentials should be unconfigured: %#v, %v", a, err)
	}
}

func TestDefaultCredentialPathUsesTemporaryHome(t *testing.T) {
	tempAuthEnvironment(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	writeCredentials(t, filepath.Join(home, ".claude", ".credentials.json"), `{"claudeAiOauth":{"accessToken":"home-token"}}`)
	path, err := credentialsPath()
	if err != nil || path != filepath.Join(home, ".claude", ".credentials.json") {
		t.Fatalf("default path = %q, %v", path, err)
	}
	a, err := loadAuth()
	if err != nil || a.token != "home-token" {
		t.Fatalf("default auth = %#v, %v", a, err)
	}
}

func TestProviderKeepsAuthSnapshotAcrossFetchAndParse(t *testing.T) {
	tempAuthEnvironment(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	writeCredentials(t, filepath.Join(dir, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"first-token","subscriptionType":"first-plan"}}`)
	a, err := loadAuth()
	if err != nil {
		t.Fatal(err)
	}
	var gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":5}}`))
	}))
	defer server.Close()
	p := &ClaudeProvider{client: server.Client(), url: server.URL, cooldownDir: t.TempDir(), auth: a}
	writeCredentials(t, filepath.Join(dir, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"second-token","subscriptionType":"second-plan"}}`)
	body, err := p.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := p.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	if gotToken != "first-token" || p.CacheIdentity() != a.key {
		t.Fatalf("provider identity changed: token=%q identity=%q", gotToken, p.CacheIdentity())
	}
	if parsed.(UsageSnapshot).Plan != "first-plan" {
		t.Fatalf("parse used new plan metadata: %#v", parsed)
	}
}

func TestRetryAtDateAndDefault(t *testing.T) {
	now := time.Now().UTC()
	want := now.Add(10 * time.Minute).Truncate(time.Second)
	got := retryAt(want.Format(http.TimeFormat), now)
	if !got.Equal(want) {
		t.Fatalf("date Retry-After = %s, want %s", got, want)
	}
	if got := retryAt("invalid", now); !got.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("default Retry-After = %s", got)
	}
}

func TestFetchStatusErrorsDoNotExposeResponse(t *testing.T) {
	tempAuthEnvironment(t)
	t.Setenv(accessTokenEnv, "status-secret")
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("response-secret"))
			}))
			defer server.Close()
			a, _ := loadAuth()
			p := &ClaudeProvider{client: server.Client(), url: server.URL, cooldownDir: t.TempDir(), auth: a}
			_, err := p.Fetch(context.Background())
			if err == nil || strings.Contains(err.Error(), "response-secret") || strings.Contains(err.Error(), "status-secret") {
				t.Fatalf("unsafe status error: %v", err)
			}
		})
	}
}

func TestFetchCanceledContext(t *testing.T) {
	tempAuthEnvironment(t)
	t.Setenv(accessTokenEnv, "cancel-secret")
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	a, _ := loadAuth()
	p := &ClaudeProvider{client: server.Client(), url: server.URL, cooldownDir: t.TempDir(), auth: a}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := p.Fetch(ctx); done <- err }()
	<-started
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled request succeeded")
	}
}

func tempAuthEnvironment(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "config"))
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	t.Setenv(accessTokenEnv, "placeholder")
	if err := os.Unsetenv(accessTokenEnv); err != nil {
		t.Fatal(err)
	}
}

func writeCredentials(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}
