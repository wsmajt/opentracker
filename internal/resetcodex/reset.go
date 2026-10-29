// Package resetcodex implements the explicit CLIProxyAPI Codex reset flow.
package resetcodex

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	credentialsPath = "/v8/management/credentials"
	apiCallPath     = "/v8/management/requests/api-call"
	cooldownPath    = "/v8/management/routing/cooldown/reset"
	resetURL        = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"
	maxResponse     = 4 << 20
)

// Options contains credentials and paths supplied by the CLI. No credential
// discovery happens in this package, so tests can use an isolated fake server.
type Options struct {
	BaseURL           string
	Key               string
	StateDir          string
	Client            *http.Client
	AuthIndex         string
	ExpectedAccountID string
	Confirm           func(accountID string) bool
	ConfirmDetails    func(accountID string, previous *PreviousOperation) bool
	NewOperation      string
}

// Result reports the upstream outcome without including account identifiers.
type Result struct {
	Status       string `json:"status"`
	WindowsReset *int   `json:"windows_reset,omitempty"`
	OperationID  string `json:"operation_id"`
}

// PreviousOperation identifies the completed action that --new must name.
type PreviousOperation struct {
	ID      string
	Outcome string
}

type credentialList struct {
	Files json.RawMessage `json:"files"`
}
type credential struct {
	AuthIndex             string         `json:"auth_index"`
	AuthIndexCamel        string         `json:"authIndex"`
	Provider              string         `json:"provider"`
	Type                  string         `json:"type"`
	Disabled              bool           `json:"disabled"`
	Status                string         `json:"status"`
	IDToken               any            `json:"id_token"`
	ChatGPTAccountID      string         `json:"chatgpt_account_id"`
	ChatGPTAccountIDCamel string         `json:"chatgptAccountId"`
	Metadata              map[string]any `json:"metadata"`
	Attributes            map[string]any `json:"attributes"`
}
type apiCall struct {
	AuthIndex string            `json:"authIndex"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Header    map[string]string `json:"header"`
	Data      string            `json:"data"`
}
type apiResult struct {
	StatusCode int             `json:"status_code"`
	Body       json.RawMessage `json:"body"`
}
type resetResponse struct {
	Code         string          `json:"code"`
	WindowsReset json.RawMessage `json:"windows_reset"`
}
type journal struct {
	Version      int    `json:"version"`
	EndpointHash string `json:"endpoint_hash"`
	AccountHash  string `json:"account_hash"`
	AuthIndex    string `json:"auth_index"`
	Operation    string `json:"operation"`
	Phase        string `json:"phase"`
	Outcome      string `json:"outcome,omitempty"`
	Cleanup      string `json:"cleanup,omitempty"`
	WindowsReset *int   `json:"windows_reset,omitempty"`
}

// Reset performs one confirmed reset attempt. Uncertain consume outcomes remain
// pending and block all later attempts for that account.
func Reset(ctx context.Context, o Options) (result Result, retErr error) {
	if strings.TrimSpace(o.AuthIndex) == "" || strings.TrimSpace(o.Key) == "" || strings.TrimSpace(o.StateDir) == "" {
		return Result{}, errors.New("reset configuration is incomplete")
	}
	if o.NewOperation != "" && !validUUID(o.NewOperation) {
		return Result{}, errors.New("--new must name a completed operation ID")
	}
	base, err := validateBase(o.BaseURL)
	if err != nil {
		return Result{}, err
	}
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	} else {
		copy := *client
		client = &copy
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if err := secureDir(o.StateDir); err != nil {
		return Result{}, err
	}
	lock, err := acquireLock(o.StateDir)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if err := releaseLock(lock); retErr == nil && err != nil {
			retErr = errors.New("cannot release reset state lock")
		}
	}()

	selected, err := fetchSelected(ctx, client, base, o.Key, o.AuthIndex)
	if err != nil {
		return Result{}, err
	}
	if o.ExpectedAccountID != "" && selected.accountID != o.ExpectedAccountID {
		return Result{}, errors.New("selected account does not match the expected account; no reset was sent")
	}
	endpointHash := hashString(base)
	accountHash := hashString(selected.accountID)
	if err := unresolvedIndexOperation(o.StateDir, endpointHash, selected.authIndex); err != nil {
		return Result{}, err
	}
	statePath := filepath.Join(o.StateDir, accountHash+".json")
	previous, exists, err := loadJournal(statePath)
	if err != nil {
		return Result{}, err
	}
	var prior *PreviousOperation
	if exists {
		if previous.AccountHash != accountHash {
			return Result{}, errors.New("reset state identity mismatch; manual intervention required")
		}
		if previous.Phase != "done" || previous.Cleanup != "done" {
			return Result{}, errors.New("a prior reset is unresolved for this account; UNKNOWN, DO NOT RETRY; manual intervention required")
		}
		if previous.EndpointHash != endpointHash {
			return Result{}, errors.New("reset state belongs to a different CLIProxyAPI endpoint; manual intervention required")
		}
		if o.NewOperation == "" || o.NewOperation != previous.Operation {
			return Result{}, errors.New("a reset was already completed; pass --new with its operation ID to confirm a separate reset")
		}
		prior = &PreviousOperation{ID: previous.Operation, Outcome: previous.Outcome}
	} else if o.NewOperation != "" {
		return Result{}, errors.New("--new does not match a completed reset for this account")
	}
	confirmed := false
	if o.ConfirmDetails != nil {
		confirmed = o.ConfirmDetails(selected.accountID, prior)
	} else if o.Confirm != nil {
		confirmed = o.Confirm(selected.accountID)
	}
	if !confirmed {
		return Result{}, errors.New("reset cancelled")
	}

	// Re-resolve the exact enabled account after confirmation, before any write.
	selectedAgain, err := fetchSelected(ctx, client, base, o.Key, o.AuthIndex)
	if err != nil {
		return Result{}, errors.New("cannot revalidate the selected account; no reset was sent")
	}
	if selectedAgain.accountID != selected.accountID || selectedAgain.authIndex != selected.authIndex {
		return Result{}, errors.New("selected account changed; no reset was sent")
	}
	if o.ExpectedAccountID != "" && selectedAgain.accountID != o.ExpectedAccountID {
		return Result{}, errors.New("selected account does not match the expected account; no reset was sent")
	}

	var rawUUID [16]byte
	if _, err := rand.Read(rawUUID[:]); err != nil {
		return Result{}, errors.New("cannot create reset request identifier")
	}
	// RFC 4122 version 4 representation.
	rawUUID[6] = rawUUID[6]&0x0f | 0x40
	rawUUID[8] = rawUUID[8]&0x3f | 0x80
	requestID := formatUUID(rawUUID)
	entry := journal{Version: 1, EndpointHash: endpointHash, AccountHash: accountHash, AuthIndex: selected.authIndex, Operation: requestID, Phase: "may_have_sent"}
	if err := saveJournal(statePath, entry); err != nil {
		return Result{}, errors.New("cannot persist reset safety state; no reset was sent")
	}

	payload := apiCall{AuthIndex: o.AuthIndex, Method: http.MethodPost, URL: resetURL, Header: map[string]string{
		"Authorization": "Bearer $TOKEN$", "Content-Type": "application/json", "Chatgpt-Account-Id": selected.accountID,
	}, Data: `{"redeem_request_id":"` + requestID + `"}`}
	outer, err := managementRequest(ctx, client, base, o.Key, http.MethodPost, apiCallPath, payload)
	if err != nil {
		return Result{}, errors.New("reset outcome is UNKNOWN, DO NOT RETRY; manual intervention required")
	}
	var callResult apiResult
	if json.Unmarshal(outer, &callResult) != nil || callResult.StatusCode < 200 || callResult.StatusCode >= 300 {
		return Result{}, errors.New("reset outcome is UNKNOWN, DO NOT RETRY; manual intervention required")
	}
	body, ok := decodeBody(callResult.Body)
	if !ok {
		return Result{}, errors.New("reset outcome is UNKNOWN, DO NOT RETRY; manual intervention required")
	}
	var outcome resetResponse
	if json.Unmarshal(body, &outcome) != nil || !knownOutcome(outcome.Code) {
		return Result{}, errors.New("reset outcome is UNKNOWN, DO NOT RETRY; manual intervention required")
	}
	var windowsReset *int
	if len(outcome.WindowsReset) != 0 {
		var count int
		if bytes.Equal(bytes.TrimSpace(outcome.WindowsReset), []byte("null")) || json.Unmarshal(outcome.WindowsReset, &count) != nil || count < 0 {
			return Result{}, errors.New("reset outcome is UNKNOWN, DO NOT RETRY; manual intervention required")
		}
		windowsReset = &count
	}
	entry.Phase, entry.Outcome, entry.Cleanup, entry.WindowsReset = "done", outcome.Code, "pending", windowsReset
	if outcome.Code == "no_credit" || outcome.Code == "nothing_to_reset" {
		entry.Cleanup = "done"
		if err := saveJournal(statePath, entry); err != nil {
			return Result{}, errors.New("reset outcome could not be persisted; DO NOT RETRY")
		}
		return Result{Status: outcome.Code, WindowsReset: windowsReset, OperationID: requestID}, nil
	}
	if err := saveJournal(statePath, entry); err != nil {
		return Result{}, errors.New("reset response was definitive, but its result could not be persisted; DO NOT RETRY")
	}

	latest, err := fetchSelected(ctx, client, base, o.Key, o.AuthIndex)
	if err != nil || latest.accountID != selected.accountID || latest.authIndex != selected.authIndex {
		entry.Cleanup = "failed"
		if persistErr := saveJournal(statePath, entry); persistErr != nil {
			return Result{}, fmt.Errorf("reset outcome %s; account identity changed or could not be verified, cooldown cleanup was not sent; DO NOT retry consumption; journal update failed", outcome.Code)
		}
		return Result{}, fmt.Errorf("reset outcome %s; account identity changed or could not be verified, cooldown cleanup was not sent; DO NOT retry consumption; manual intervention required", outcome.Code)
	}
	cleanup, err := managementRequest(ctx, client, base, o.Key, http.MethodPost, cooldownPath, map[string]string{"auth_index": selected.authIndex})
	if err == nil && cooldownMatches(cleanup, o.AuthIndex) {
		entry.Cleanup = "done"
		if saveJournal(statePath, entry) != nil {
			return Result{}, fmt.Errorf("reset outcome %s; DO NOT retry consumption; cooldown cleanup succeeded but could not be recorded", outcome.Code)
		}
	} else {
		entry.Cleanup = "failed"
		if persistErr := saveJournal(statePath, entry); persistErr != nil {
			return Result{}, fmt.Errorf("reset outcome %s; DO NOT retry consumption; cooldown cleanup failed and its status could not be recorded", outcome.Code)
		}
		return Result{}, fmt.Errorf("reset outcome %s; DO NOT retry consumption; cooldown cleanup failed, manual intervention required", outcome.Code)
	}
	return Result{Status: outcome.Code, WindowsReset: windowsReset, OperationID: requestID}, nil
}

type selectedCredential struct {
	accountID string
	authIndex string
}

func fetchSelected(ctx context.Context, client *http.Client, base, key, index string) (selectedCredential, error) {
	data, err := managementRequest(ctx, client, base, key, http.MethodGet, credentialsPath, nil)
	if err != nil {
		return selectedCredential{}, errors.New("cannot read CLIProxyAPI credentials")
	}
	var list credentialList
	if json.Unmarshal(data, &list) != nil || len(list.Files) == 0 || string(list.Files) == "null" {
		return selectedCredential{}, errors.New("invalid CLIProxyAPI credentials response")
	}
	var entries []credential
	if json.Unmarshal(list.Files, &entries) != nil {
		var wrapped struct {
			Files []credential `json:"files"`
		}
		if json.Unmarshal(list.Files, &wrapped) != nil {
			return selectedCredential{}, errors.New("invalid CLIProxyAPI credentials response")
		}
		entries = wrapped.Files
	}
	found := 0
	var account string
	for _, c := range entries {
		authIndex := strings.TrimSpace(c.AuthIndex)
		camelIndex := strings.TrimSpace(c.AuthIndexCamel)
		if authIndex != "" && camelIndex != "" && authIndex != camelIndex {
			return selectedCredential{}, errors.New("selected credential has conflicting auth indexes")
		}
		idx := authIndex
		if idx == "" {
			idx = camelIndex
		}
		if idx != index {
			continue
		}
		kind := ""
		for _, candidate := range []string{c.Provider, c.Type} {
			candidate = strings.ToLower(strings.TrimSpace(candidate))
			if candidate == "codex" || candidate == "claude" {
				if kind != "" && kind != candidate {
					return selectedCredential{}, errors.New("selected credential provider types conflict")
				}
				kind = candidate
			}
		}
		if kind != "codex" || c.Disabled || strings.EqualFold(c.Status, "disabled") {
			return selectedCredential{}, errors.New("selected credential is not an enabled Codex account")
		}
		found++
		var idErr error
		account, idErr = accountID(c)
		if idErr != nil {
			return selectedCredential{}, idErr
		}
	}
	if found != 1 {
		return selectedCredential{}, errors.New("auth index must resolve to exactly one Codex account")
	}
	if account == "" {
		return selectedCredential{}, errors.New("selected Codex account has no reliable account ID")
	}
	return selectedCredential{accountID: account, authIndex: index}, nil
}

func accountID(c credential) (string, error) {
	ids := make([]string, 0, 6)
	add := func(value any) {
		if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
			ids = append(ids, strings.TrimSpace(s))
		}
	}
	add(c.ChatGPTAccountID)
	add(c.ChatGPTAccountIDCamel)
	for _, object := range []map[string]any{c.Metadata, c.Attributes} {
		add(object["chatgpt_account_id"])
		add(object["chatgptAccountId"])
		ids = append(ids, tokenAccountIDs(object["id_token"])...)
	}
	ids = append(ids, tokenAccountIDs(c.IDToken)...)
	if len(ids) == 0 {
		return "", nil
	}
	selected := ids[0]
	for _, id := range ids[1:] {
		if id != selected {
			return "", errors.New("selected Codex credential has conflicting account IDs")
		}
	}
	return selected, nil
}

func tokenAccountIDs(value any) []string {
	ids := claimAccountIDs(value)
	if token, ok := value.(string); ok {
		parts := strings.Split(token, ".")
		if len(parts) >= 2 {
			decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				decoded, err = base64.URLEncoding.DecodeString(parts[1])
			}
			if err == nil {
				var claims map[string]any
				if json.Unmarshal(decoded, &claims) == nil {
					ids = append(ids, claimAccountIDs(claims)...)
				}
			}
		}
	}
	return ids
}

func claimAccountIDs(v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	ids := make([]string, 0, 2)
	appendID := func(claims map[string]any) {
		for _, key := range []string{"chatgpt_account_id", "chatgptAccountId"} {
			if s, ok := claims[key].(string); ok && strings.TrimSpace(s) != "" {
				ids = append(ids, strings.TrimSpace(s))
			}
		}
	}
	appendID(m)
	if n, ok := m["https://api.openai.com/auth"].(map[string]any); ok {
		appendID(n)
	}
	return ids
}

func managementRequest(ctx context.Context, c *http.Client, base, key, method, path string, body any) ([]byte, error) {
	var r io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return nil, e
		}
		r = strings.NewReader(string(b))
	}
	req, e := http.NewRequestWithContext(ctx, method, base+path, r)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.Do(req)
	if e != nil {
		return nil, e
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	closeErr := resp.Body.Close()
	if e == nil && closeErr != nil {
		e = closeErr
	}
	if e != nil || len(b) > maxResponse || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("management request failed")
	}
	return b, nil
}
func decodeBody(v json.RawMessage) ([]byte, bool) {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return []byte(s), true
	}
	if len(v) > 0 && v[0] == '{' {
		return v, true
	}
	return nil, false
}
func knownOutcome(code string) bool {
	switch code {
	case "reset", "already_redeemed", "no_credit", "nothing_to_reset":
		return true
	}
	return false
}
func cooldownMatches(data []byte, index string) bool {
	var v struct {
		Status    string `json:"status"`
		AuthIndex string `json:"auth_index"`
	}
	if json.Unmarshal(data, &v) != nil {
		return false
	}
	return v.Status == "ok" && v.AuthIndex == index
}
func validateBase(raw string) (string, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("invalid CLIProxyAPI URL")
	}
	h := u.Hostname()
	ip := net.ParseIP(h)
	loop := strings.EqualFold(h, "localhost") || (ip != nil && ip.IsLoopback())
	if (u.Scheme != "http" && u.Scheme != "https") || (!loop && u.Scheme != "https") {
		return "", errors.New("CLIProxyAPI URL must use HTTP on loopback or HTTPS remotely")
	}
	return strings.TrimRight(raw, "/"), nil
}
func hashString(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// AccountFingerprint identifies an account in prompts without displaying its
// account ID or email.
func AccountFingerprint(accountID string) string { return hashString(accountID) }

func validJournal(entry journal) bool {
	if entry.Version != 1 || !isHash(entry.EndpointHash) || !isHash(entry.AccountHash) || entry.AuthIndex == "" || !validUUID(entry.Operation) {
		return false
	}
	if entry.WindowsReset != nil && *entry.WindowsReset < 0 {
		return false
	}
	switch entry.Phase {
	case "may_have_sent":
		return entry.Outcome == "" && entry.Cleanup == "" && entry.WindowsReset == nil
	case "done":
		if !knownOutcome(entry.Outcome) {
			return false
		}
		switch entry.Cleanup {
		case "pending", "done", "failed":
		default:
			return false
		}
		if (entry.Outcome == "no_credit" || entry.Outcome == "nothing_to_reset") && entry.Cleanup != "done" {
			return false
		}
		return true
	default:
		return false
	}
}

func isHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, char := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if char != '-' {
				return false
			}
		} else if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func unresolvedIndexOperation(stateDir, endpointHash, authIndex string) error {
	files, err := filepath.Glob(filepath.Join(stateDir, "*.json"))
	if err != nil {
		return errors.New("cannot inspect reset safety state")
	}
	for _, path := range files {
		entry, _, err := loadJournal(path)
		if err != nil {
			return errors.New("reset state is corrupt; manual intervention required")
		}
		if entry.EndpointHash == endpointHash && entry.AuthIndex == authIndex && (entry.Phase != "done" || entry.Cleanup != "done") {
			return errors.New("a prior operation for this auth index is unresolved; UNKNOWN, DO NOT RETRY; manual intervention required")
		}
	}
	return nil
}

func formatUUID(b [16]byte) string {
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func secureDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return errors.New("cannot create private reset state directory")
	}
	i, err := os.Lstat(path)
	if err != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return errors.New("reset state path must be a real directory")
	}
	if i.Mode().Perm()&0077 != 0 {
		return errors.New("reset state directory permissions must be 0700")
	}
	if err := syncStateParents(path); err != nil {
		return errors.New("cannot durably sync reset state directories")
	}
	return nil
}
