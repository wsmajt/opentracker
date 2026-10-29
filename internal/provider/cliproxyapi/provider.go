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
	EnvURL              = "OPENTRACKER_CLIPROXYAPI_URL"
	EnvKey              = "OPENTRACKER_CLIPROXYAPI_KEY"
	defaultURL          = "http://127.0.0.1:8317"
	usageURL            = "https://chatgpt.com/backend-api/wham/usage"
	resetCreditsURL     = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	claudeUsageURL      = "https://api.anthropic.com/api/oauth/usage"
	maxResponseSize     = 4 << 20
	resetCreditsTimeout = 5 * time.Second
	requestUserAgent    = "codex-tui/0.149.1 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.149.1)"
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
	transport := make([]accountTransport, len(accounts))
	for i, account := range accounts {
		transport[i] = accountTransport(account)
	}
	raw, err := json.Marshal(transport)
	return string(raw), err
}
func (*managementProvider) Parse(raw string) (interface{}, error) {
	var transport []accountTransport
	if err := json.Unmarshal([]byte(raw), &transport); err == nil {
		accounts := make([]Account, len(transport))
		for i, account := range transport {
			accounts[i] = Account(account)
		}
		return accounts, nil
	}
	var accounts []Account
	err := json.Unmarshal([]byte(raw), &accounts)
	return accounts, err
}

type Account struct {
	AccountProvider   string             `json:"accountProvider"`
	ID                string             `json:"accountId,omitempty"`
	AuthIndex         string             `json:"authIndex,omitempty"`
	Email             string             `json:"accountEmail,omitempty"`
	ResetTarget       *codex.ResetTarget `json:"resetTarget,omitempty"`
	Usage             interface{}        `json:"usage"`
	ResetSelectorSafe bool               `json:"-"`
}

type accountTransport struct {
	AccountProvider   string             `json:"accountProvider"`
	ID                string             `json:"accountId,omitempty"`
	AuthIndex         string             `json:"authIndex,omitempty"`
	Email             string             `json:"accountEmail,omitempty"`
	ResetTarget       *codex.ResetTarget `json:"resetTarget,omitempty"`
	Usage             interface{}        `json:"usage"`
	ResetSelectorSafe bool               `json:"resetSelectorSafe,omitempty"`
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
	ResetSelectorSafe     bool           `json:"-"`
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
	markSafeResetSelectors(credentials)
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
			if envelope.Usage.PlanType == "" && envelope.Usage.RateLimit == nil && envelope.Usage.Credits == nil && envelope.Usage.RateLimitResetCredits == nil {
				if e = json.Unmarshal(bodyData, &envelope.Usage); e != nil || (envelope.Usage.PlanType == "" && envelope.Usage.RateLimit == nil && envelope.Usage.Credits == nil && envelope.Usage.RateLimitResetCredits == nil) {
					failures = append(failures, Failure{Account: accountNumber, Provider: kind, Err: fmt.Errorf("invalid Codex usage response")})
					continue
				}
			}
			email := first(envelope.AccountEmail, c.Email)
			id := accountID(c)
			usage := codex.MapUsageResponse(envelope.Usage, email, time.Now())
			if details := fetchResetCredits(ctx, key, idx, id); details != nil {
				if usage.ResetCredits == nil {
					usage.ResetCredits = &codex.ResetCredits{}
				}
				if details.Available != nil {
					usage.ResetCredits.Available = details.Available
				}
				usage.ResetCredits.Credits = details.Credits
			}
			account := Account{AccountProvider: kind, ID: id, AuthIndex: idx, Email: email, Usage: usage, ResetSelectorSafe: c.ResetSelectorSafe}
			if c.ResetSelectorSafe && id != "" {
				account.ResetTarget = &codex.ResetTarget{AccountID: id, AuthIndex: idx}
			}
			accounts = append(accounts, account)
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

func fetchResetCredits(ctx context.Context, key, authIndex, accountID string) *codex.ResetCredits {
	ctx, cancel := context.WithTimeout(ctx, resetCreditsTimeout)
	defer cancel()

	headers := map[string]string{
		"Authorization": "Bearer $TOKEN$",
		"Accept":        "application/json",
		"Content-Type":  "application/json",
		"OpenAI-Beta":   "codex-1",
		"Originator":    "Codex Desktop",
		"User-Agent":    requestUserAgent,
	}
	if accountID != "" {
		headers["Chatgpt-Account-Id"] = accountID
	}
	payload, err := managementRequest(ctx, key, http.MethodPost, "/v8/management/requests/api-call", apiCallRequest{
		AuthIndex: authIndex,
		Method:    http.MethodGet,
		URL:       resetCreditsURL,
		Header:    headers,
	})
	if err != nil {
		return nil
	}
	var result apiCallResponse
	if json.Unmarshal(payload, &result) != nil || result.StatusCode < 200 || result.StatusCode >= 300 || len(result.Body) == 0 {
		return nil
	}
	body := result.Body
	var bodyString string
	if json.Unmarshal(result.Body, &bodyString) == nil {
		body = []byte(bodyString)
	}
	return codex.ParseResetCreditsDetails(body)
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

func markSafeResetSelectors(credentials []credential) {
	indexCounts := make(map[string]int)
	idCounts := make(map[string]int)
	ids := make([]string, len(credentials))
	indexes := make([]string, len(credentials))
	validIndexes := make([]bool, len(credentials))
	validIDs := make([]bool, len(credentials))
	safeKinds := make([]bool, len(credentials))
	credentialIDs := make([][]string, len(credentials))
	for i, c := range credentials {
		indexes[i], validIndexes[i] = safeCredentialIndex(c)
		rowIndexes := map[string]struct{}{}
		for _, index := range []string{strings.TrimSpace(c.AuthIndex), strings.TrimSpace(c.AuthIndexCamel)} {
			if index != "" {
				if _, exists := rowIndexes[index]; exists {
					continue
				}
				rowIndexes[index] = struct{}{}
				indexCounts[index]++
			}
		}
		safeKinds[i] = safeCodexCredential(c)
		if !credentialCouldBeCodex(c) {
			continue
		}
		credentialIDs[i], validIDs[i] = safeCredentialAccountIDs(c)
		if len(credentialIDs[i]) > 0 {
			ids[i] = credentialIDs[i][0]
		}
		seenCredentialIDs := make(map[string]struct{}, len(credentialIDs[i]))
		for _, id := range credentialIDs[i] {
			if _, exists := seenCredentialIDs[id]; exists {
				continue
			}
			seenCredentialIDs[id] = struct{}{}
			idCounts[id]++
		}
	}
	for i := range credentials {
		credentials[i].ResetSelectorSafe = safeKinds[i] && validIndexes[i] && validIDs[i] && ids[i] != "" && indexCounts[indexes[i]] == 1 && idCounts[ids[i]] == 1
	}
}

func safeCredentialIndex(c credential) (string, bool) {
	authIndex := strings.TrimSpace(c.AuthIndex)
	camelIndex := strings.TrimSpace(c.AuthIndexCamel)
	if authIndex != "" && camelIndex != "" && authIndex != camelIndex {
		return "", false
	}
	if authIndex == "" {
		authIndex = camelIndex
	}
	return authIndex, authIndex != ""
}

func safeCodexCredential(c credential) bool {
	kind := ""
	for _, raw := range []string{c.Provider, c.Type} {
		candidate := strings.ToLower(strings.TrimSpace(raw))
		if candidate == "" {
			continue
		}
		if candidate != "codex" && candidate != "claude" {
			return false
		}
		if kind != "" && kind != candidate {
			return false
		}
		kind = candidate
	}
	return kind == "codex" && !c.Disabled && !strings.EqualFold(c.Status, "disabled")
}

func credentialCouldBeCodex(c credential) bool {
	if c.Disabled || strings.EqualFold(c.Status, "disabled") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(c.Provider), "codex") || strings.EqualFold(strings.TrimSpace(c.Type), "codex")
}

func safeCredentialAccountIDs(c credential) ([]string, bool) {
	ids := make([]string, 0, 8)
	valid := true
	add := func(value any) bool {
		if value == nil {
			return true
		}
		s, ok := value.(string)
		if !ok {
			return false
		}
		if strings.TrimSpace(s) != "" {
			ids = append(ids, strings.TrimSpace(s))
		}
		return true
	}
	valid = add(c.ChatGPTAccountID) && valid
	valid = add(c.ChatGPTAccountIDCamel) && valid
	for _, object := range []map[string]any{c.Metadata, c.Attributes} {
		for _, key := range []string{"chatgpt_account_id", "chatgptAccountId"} {
			if !add(object[key]) {
				valid = false
			}
		}
		claimIDs, ok := strictTokenAccountIDs(object["id_token"])
		ids = append(ids, claimIDs...)
		valid = ok && valid
	}
	claimIDs, ok := strictTokenAccountIDs(c.IDToken)
	ids = append(ids, claimIDs...)
	valid = ok && valid
	if len(ids) == 0 {
		return ids, false
	}
	for _, id := range ids[1:] {
		if id != ids[0] {
			valid = false
		}
	}
	return ids, valid
}

func strictTokenAccountIDs(value any) ([]string, bool) {
	if value == nil {
		return nil, true
	}
	var claims map[string]any
	switch v := value.(type) {
	case map[string]any:
		claims = v
	case string:
		parts := strings.Split(v, ".")
		if len(parts) < 2 {
			return nil, false
		}
		raw, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			raw, err = base64.URLEncoding.DecodeString(parts[1])
		}
		if err != nil || json.Unmarshal(raw, &claims) != nil || claims == nil {
			return nil, false
		}
	default:
		return nil, false
	}
	ids := make([]string, 0, 4)
	valid := true
	appendClaims := func(m map[string]any) bool {
		claimsValid := true
		for _, key := range []string{"chatgpt_account_id", "chatgptAccountId"} {
			v, exists := m[key]
			if !exists || v == nil {
				continue
			}
			s, ok := v.(string)
			if !ok {
				claimsValid = false
				continue
			}
			if strings.TrimSpace(s) != "" {
				ids = append(ids, strings.TrimSpace(s))
			}
		}
		return claimsValid
	}
	valid = appendClaims(claims) && valid
	if nested, exists := claims["https://api.openai.com/auth"]; exists {
		auth, ok := nested.(map[string]any)
		if !ok {
			valid = false
		} else {
			valid = appendClaims(auth) && valid
		}
	}
	return ids, valid
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
	ids, valid := safeCredentialAccountIDs(c)
	if valid && len(ids) > 0 {
		return ids[0]
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
