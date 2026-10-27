package cliproxyapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"opentracker/internal/config"
	"opentracker/internal/provider"
	"opentracker/internal/provider/claude"
	"opentracker/internal/provider/codex"
)

const (
	EnvURL           = "OPENTRACKER_CLIPROXYAPI_URL"
	EnvKey           = "OPENTRACKER_CLIPROXYAPI_KEY"
	defaultURL       = "http://127.0.0.1:8317"
	usageURL         = "https://chatgpt.com/backend-api/wham/usage"
	claudeUsageURL   = "https://api.anthropic.com/api/oauth/usage"
	maxResponseSize  = 4 << 20
	requestUserAgent = "codex-tui/0.149.1 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.149.1)"
)

func init() {
	provider.Register("cliproxyapi", func(*config.Config) (provider.Provider, error) { return &managementProvider{}, nil })
}

type managementProvider struct{}

func (*managementProvider) Name() string { return "cliproxyapi" }
func (*managementProvider) Fetch(ctx context.Context) (string, error) {
	accounts, failures, err := Fetch(ctx)
	if err != nil {
		return "", err
	}
	if len(failures) > 0 {
		return "", fmt.Errorf("one or more CLIProxyAPI accounts failed")
	}
	raw, err := json.Marshal(accounts)
	return string(raw), err
}
func (*managementProvider) Parse(raw string) (interface{}, error) {
	var accounts []Account
	err := json.Unmarshal([]byte(raw), &accounts)
	return accounts, err
}

type Account struct {
	AccountProvider string      `json:"accountProvider"`
	ID              string      `json:"accountId,omitempty"`
	AuthIndex       string      `json:"authIndex,omitempty"`
	Email           string      `json:"accountEmail,omitempty"`
	Usage           interface{} `json:"usage"`
}

type Failure struct {
	Account  int
	Provider string
	Err      error
}

type credentialList struct {
	Files json.RawMessage `json:"files"`
}
type credential struct {
	Name                  string         `json:"name"`
	AuthIndex             string         `json:"auth_index"`
	AuthIndexCamel        string         `json:"authIndex"`
	Provider              string         `json:"provider"`
	Type                  string         `json:"type"`
	Email                 string         `json:"email"`
	Disabled              bool           `json:"disabled"`
	Status                string         `json:"status"`
	Metadata              map[string]any `json:"metadata"`
	Attributes            map[string]any `json:"attributes"`
	IDToken               any            `json:"id_token"`
	ChatGPTAccountID      string         `json:"chatgpt_account_id"`
	ChatGPTAccountIDCamel string         `json:"chatgptAccountId"`
}

type apiCallRequest struct {
	AuthIndex string            `json:"authIndex"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Header    map[string]string `json:"header"`
}
type apiCallResponse struct {
	StatusCode int             `json:"status_code"`
	Body       json.RawMessage `json:"body"`
}

func Fetch(ctx context.Context) ([]Account, []Failure, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("cannot load CLIProxyAPI configuration")
	}
	return FetchWithConfig(ctx, cfg)
}

func FetchWithConfig(ctx context.Context, cfg *config.Config) ([]Account, []Failure, error) {
	return FetchWithConfigAndStore(ctx, cfg, NewCredentialStore())
}

func FetchCodexWithConfig(ctx context.Context, cfg *config.Config) ([]Account, []Failure, error) {
	return fetchConfigured(ctx, cfg, NewCredentialStore(), "codex")
}

func FetchClaudeWithConfig(ctx context.Context, cfg *config.Config) ([]Account, []Failure, error) {
	return fetchConfigured(ctx, cfg, NewCredentialStore(), "claude")
}

func FetchWithConfigAndStore(ctx context.Context, cfg *config.Config, store CredentialStore) ([]Account, []Failure, error) {
	return fetchConfigured(ctx, cfg, store, "")
}

func fetchConfigured(ctx context.Context, cfg *config.Config, store CredentialStore, only string) ([]Account, []Failure, error) {
	key, configured, err := ResolveManagementKey(cfg, store)
	if err != nil {
		return nil, nil, err
	}
	if !configured {
		return nil, nil, fmt.Errorf("%s is not set; run 'opentracker login cliproxyapi' or set the environment variable", EnvKey)
	}
	return fetchWithKey(ctx, key, only)
}

func VerifyManagementKey(ctx context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("CLIProxyAPI key cannot be empty")
	}
	data, err := managementRequest(ctx, key, http.MethodGet, "/v8/management/credentials", nil)
	if err != nil {
		return err
	}
	if _, err := parseCredentials(data); err != nil {
		return err
	}
	return nil
}

func fetchWithKey(ctx context.Context, key, only string) ([]Account, []Failure, error) {
	data, err := managementRequest(ctx, key, http.MethodGet, "/v8/management/credentials", nil)
	if err != nil {
		return nil, nil, err
	}
	credentials, err := parseCredentials(data)
	if err != nil {
		return nil, nil, err
	}
	accounts := make([]Account, 0)
	failures := make([]Failure, 0)
	for i, c := range credentials {
		accountNumber := i + 1
		kind := credentialKind(c)
		idx := strings.TrimSpace(c.AuthIndex)
		if idx == "" {
			idx = strings.TrimSpace(c.AuthIndexCamel)
		}
		if kind == "" || (only != "" && kind != only) || c.Disabled || strings.EqualFold(c.Status, "disabled") {
			continue
		}
		if idx == "" {
			failures = append(failures, Failure{Account: accountNumber, Provider: kind, Err: fmt.Errorf("credential has no auth index")})
			continue
		}
		headers, target := requestDetails(kind, c)
		payload, e := managementRequest(ctx, key, http.MethodPost, "/v8/management/requests/api-call", apiCallRequest{AuthIndex: idx, Method: http.MethodGet, URL: target, Header: headers})
		if e != nil {
			failures = append(failures, Failure{Account: accountNumber, Provider: kind, Err: e})
			continue
		}
		var result apiCallResponse
		if e = json.Unmarshal(payload, &result); e != nil || result.StatusCode < 200 || result.StatusCode >= 300 || len(result.Body) == 0 {
			message := fmt.Sprintf("%s usage request failed", kind)
			if result.StatusCode == http.StatusTooManyRequests {
				message = fmt.Sprintf("%s usage request was rate limited by CLIProxyAPI (HTTP 429); retry later", strings.ToUpper(kind[:1])+kind[1:])
			}
			failures = append(failures, Failure{Account: accountNumber, Provider: kind, Err: fmt.Errorf("%s", message)})
			continue
		}
		bodyData := result.Body
		var bodyString string
		if json.Unmarshal(result.Body, &bodyString) == nil {
			bodyData = []byte(bodyString)
		}
		var envelope struct {
			Usage        codex.UsageResponse `json:"usageResponse"`
			AccountEmail string              `json:"accountEmail"`
		}
		if kind == "codex" {
			if e = json.Unmarshal(bodyData, &envelope); e != nil {
				failures = append(failures, Failure{Account: accountNumber, Provider: kind, Err: fmt.Errorf("invalid Codex usage response")})
				continue
			}
			if envelope.Usage.PlanType == "" && envelope.Usage.RateLimit == nil && envelope.Usage.Credits == nil {
				if e = json.Unmarshal(bodyData, &envelope.Usage); e != nil || (envelope.Usage.PlanType == "" && envelope.Usage.RateLimit == nil && envelope.Usage.Credits == nil) {
					failures = append(failures, Failure{Account: accountNumber, Provider: kind, Err: fmt.Errorf("invalid Codex usage response")})
					continue
				}
			}
			email := first(envelope.AccountEmail, c.Email)
			accounts = append(accounts, Account{AccountProvider: kind, ID: accountID(c), AuthIndex: idx, Email: email, Usage: codex.MapUsageResponse(envelope.Usage, email, time.Now())})
		} else {
			usage, parseErr := claude.ParseUsage(string(bodyData))
			if parseErr != nil || (usage.FiveHour == nil && usage.SevenDay == nil && usage.Sonnet == nil && usage.Opus == nil) {
				failures = append(failures, Failure{Account: accountNumber, Provider: kind, Err: fmt.Errorf("claude usage response has no quota windows")})
				continue
			}
			accounts = append(accounts, Account{AccountProvider: kind, AuthIndex: idx, Email: c.Email, Usage: usage})
		}
	}
	sort.SliceStable(accounts, func(i, j int) bool {
		if accounts[i].AccountProvider != accounts[j].AccountProvider {
			return accounts[i].AccountProvider < accounts[j].AccountProvider
		}
		if accounts[i].AuthIndex != accounts[j].AuthIndex {
			return accounts[i].AuthIndex < accounts[j].AuthIndex
		}
		return accounts[i].Email < accounts[j].Email
	})
	unique := accounts[:0]
	seenIDs := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		if account.AccountProvider == "codex" && account.ID != "" {
			if _, exists := seenIDs[account.ID]; exists {
				continue
			}
			seenIDs[account.ID] = struct{}{}
		}
		unique = append(unique, account)
	}
	return unique, failures, nil
}

func parseCredentials(data []byte) ([]credential, error) {
	var list credentialList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("cannot decode CLIProxyAPI credentials")
	}
	if len(list.Files) == 0 || string(list.Files) == "null" {
		return nil, fmt.Errorf("CLIProxyAPI credentials response has no files list")
	}
	var credentials []credential
	if err := json.Unmarshal(list.Files, &credentials); err != nil || credentials == nil {
		return nil, fmt.Errorf("CLIProxyAPI credentials response has an invalid files list")
	}
	return credentials, nil
}

func credentialKind(c credential) string {
	for _, raw := range []string{c.Provider, c.Type} {
		kind := strings.ToLower(strings.TrimSpace(raw))
		if kind == "codex" || kind == "claude" {
			return kind
		}
	}
	return ""
}

func requestDetails(kind string, c credential) (map[string]string, string) {
	if kind == "claude" {
		return map[string]string{
			"Authorization":  "Bearer $TOKEN$",
			"anthropic-beta": "oauth-2025-04-20",
			"Accept":         "application/json",
			"Content-Type":   "application/json",
			"User-Agent":     "claude-cli/2.1.280 (external, cli)",
		}, claudeUsageURL
	}
	headers := map[string]string{"Authorization": "Bearer $TOKEN$", "Content-Type": "application/json", "Accept": "application/json", "User-Agent": requestUserAgent}
	if id := accountID(c); id != "" {
		headers["Chatgpt-Account-Id"] = id
	}
	return headers, usageURL
}

func managementRequest(ctx context.Context, key, method, path string, data any) ([]byte, error) {
	base, err := endpoint()
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if data != nil {
		raw, e := json.Marshal(data)
		if e != nil {
			return nil, fmt.Errorf("cannot encode CLIProxyAPI request")
		}
		body = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return nil, fmt.Errorf("cannot create CLIProxyAPI request")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to CLIProxyAPI failed")
	}
	defer func() { _ = resp.Body.Close() }()
	result, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read CLIProxyAPI response")
	}
	if len(result) > maxResponseSize {
		return nil, fmt.Errorf("CLIProxyAPI response exceeds size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, fmt.Errorf("CLIProxyAPI rate limited the management request (HTTP 429); retry later")
		}
		return nil, fmt.Errorf("CLIProxyAPI returned HTTP %d", resp.StatusCode)
	}
	return result, nil
}

func endpoint() (string, error) {
	raw := strings.TrimSpace(os.Getenv(EnvURL))
	if raw == "" {
		raw = defaultURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid CLIProxyAPI URL")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	loopback := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
	if (u.Scheme != "http" && u.Scheme != "https") || (!loopback && u.Scheme != "https") {
		return "", fmt.Errorf("CLIProxyAPI URL must use HTTP on loopback or HTTPS remotely")
	}
	return strings.TrimRight(raw, "/"), nil
}

func accountID(c credential) string {
	for _, v := range []any{c.ChatGPTAccountID, c.ChatGPTAccountIDCamel, c.Metadata["chatgpt_account_id"], c.Metadata["chatgptAccountId"], c.Attributes["chatgpt_account_id"], c.Attributes["chatgptAccountId"]} {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	for _, v := range []any{c.IDToken, c.Metadata["id_token"], c.Attributes["id_token"]} {
		if id := accountIDFromClaims(v); id != "" {
			return id
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		parts := strings.Split(s, ".")
		if len(parts) < 2 {
			continue
		}
		raw, e := base64.RawURLEncoding.DecodeString(parts[1])
		if e != nil {
			raw, e = base64.URLEncoding.DecodeString(parts[1])
		}
		if e != nil {
			continue
		}
		var claims map[string]any
		if json.Unmarshal(raw, &claims) == nil {
			if id := accountIDFromClaims(claims); id != "" {
				return id
			}
		}
	}
	return ""
}

func accountIDFromClaims(value any) string {
	claims, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	if nested, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		claims = nested
	}
	if id, ok := claims["chatgpt_account_id"].(string); ok {
		return strings.TrimSpace(id)
	}
	if id, ok := claims["chatgptAccountId"].(string); ok {
		return strings.TrimSpace(id)
	}
	return ""
}
func first(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
