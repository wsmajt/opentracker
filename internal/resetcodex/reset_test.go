package resetcodex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testIndex = "selected-codex-index"
const testAccount = "account-id-not-an-email"

func TestResetPersistsBeforeConsumeAndCleansCooldown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var consumes atomic.Int32
	server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case credentialsPath:
			_, _ = w.Write(credentialsJSON())
		case apiCallPath:
			if consumes.Add(1) != 1 {
				t.Error("more than one consume request")
			}
			var call apiCall
			if json.NewDecoder(r.Body).Decode(&call) != nil {
				t.Error("invalid api-call payload")
			}
			if call.AuthIndex != testIndex || call.Method != http.MethodPost || call.URL != resetURL || call.Header["Chatgpt-Account-Id"] != testAccount {
				t.Errorf("unexpected reset request: %#v", call)
			}
			var redeem map[string]string
			if json.Unmarshal([]byte(call.Data), &redeem) != nil || !validUUID(redeem["redeem_request_id"]) {
				t.Errorf("invalid redeem request id: %q", call.Data)
			}
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"code\":\"reset\",\"windows_reset\":2}"}`))
		case cooldownPath:
			_, _ = w.Write([]byte(`{"status":"ok","auth_index":"selected-codex-index"}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	})
	state := filepath.Join(t.TempDir(), "private-state")
	result, err := Reset(context.Background(), testOptions(server.URL, state, func(string) bool { return true }))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "reset" || result.WindowsReset == nil || *result.WindowsReset != 2 || consumes.Load() != 1 {
		t.Fatalf("result=%+v consumes=%d", result, consumes.Load())
	}
	if !validUUID(result.OperationID) {
		t.Fatalf("invalid operation ID in result: %q", result.OperationID)
	}
	files, err := filepath.Glob(filepath.Join(state, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("journal files=%v err=%v", files, err)
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), testAccount) || strings.Contains(string(b), "Bearer") {
		t.Fatalf("journal leaked account data: %s", b)
	}
	if _, err := Reset(context.Background(), testOptions(server.URL, state, func(string) bool { return true })); err == nil || !strings.Contains(err.Error(), "--new") {
		t.Fatalf("repeat reset was not blocked: %v", err)
	}
}

func TestResetCancellationDoesNotConsume(t *testing.T) {
	var consumes atomic.Int32
	server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == credentialsPath {
			_, _ = w.Write(credentialsJSON())
			return
		}
		if r.URL.Path == apiCallPath {
			consumes.Add(1)
		}
	})
	state := filepath.Join(t.TempDir(), "state")
	_, err := Reset(context.Background(), testOptions(server.URL, state, func(string) bool { return false }))
	if err == nil || consumes.Load() != 0 {
		t.Fatalf("err=%v consumes=%d", err, consumes.Load())
	}
	files, _ := filepath.Glob(filepath.Join(state, "*.json"))
	if len(files) != 0 {
		t.Fatalf("cancel wrote journal: %v", files)
	}
}

func TestResetExpectedAccountIDMatchAllowsReset(t *testing.T) {
	var consumes atomic.Int32
	server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case credentialsPath:
			_, _ = w.Write(credentialsJSON())
		case apiCallPath:
			consumes.Add(1)
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"code\":\"no_credit\"}"}`))
		}
	})
	options := testOptions(server.URL, filepath.Join(t.TempDir(), "state"), func(string) bool { return true })
	options.ExpectedAccountID = testAccount
	if _, err := Reset(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if consumes.Load() != 1 {
		t.Fatalf("consume calls=%d, want 1", consumes.Load())
	}
}

func TestResetExpectedAccountIDMismatchSkipsConfirmationAndConsume(t *testing.T) {
	var consumes atomic.Int32
	confirmed := false
	server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case credentialsPath:
			_, _ = w.Write(credentialsJSON())
		case apiCallPath:
			consumes.Add(1)
		}
	})
	options := testOptions(server.URL, filepath.Join(t.TempDir(), "state"), func(string) bool {
		confirmed = true
		return true
	})
	options.ExpectedAccountID = "different-synthetic-account"
	_, err := Reset(context.Background(), options)
	if err == nil || confirmed || consumes.Load() != 0 {
		t.Fatalf("error=%v confirmed=%v consumes=%d", err, confirmed, consumes.Load())
	}
	if strings.Contains(err.Error(), testAccount) || strings.Contains(err.Error(), options.ExpectedAccountID) {
		t.Fatalf("error disclosed an account ID: %v", err)
	}
}

func TestResetExpectedAccountIDRecheckedAfterConfirmation(t *testing.T) {
	changed := base64.RawURLEncoding.EncodeToString([]byte(`{"chatgpt_account_id":"replacement-synthetic-account"}`))
	var credentialReads, consumes atomic.Int32
	server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case credentialsPath:
			if credentialReads.Add(1) == 1 {
				_, _ = w.Write(credentialsJSON())
				return
			}
			_, _ = w.Write([]byte(`{"files":[{"auth_index":"` + testIndex + `","provider":"codex","id_token":"x.` + changed + `.x"}]}`))
		case apiCallPath:
			consumes.Add(1)
		}
	})
	options := testOptions(server.URL, filepath.Join(t.TempDir(), "state"), func(string) bool { return true })
	options.ExpectedAccountID = testAccount
	_, err := Reset(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "selected account changed") || consumes.Load() != 0 {
		t.Fatalf("error=%v consumes=%d", err, consumes.Load())
	}
}

func TestUncertainConsumeBlocksFurtherAttempts(t *testing.T) {
	var consumes atomic.Int32
	server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case credentialsPath:
			_, _ = w.Write(credentialsJSON())
		case apiCallPath:
			consumes.Add(1)
			http.Error(w, "failure", http.StatusBadGateway)
		}
	})
	state := filepath.Join(t.TempDir(), "state")
	for i := 0; i < 2; i++ {
		_, err := Reset(context.Background(), testOptions(server.URL, state, func(string) bool { return true }))
		if err == nil || !strings.Contains(err.Error(), "DO NOT RETRY") {
			t.Fatalf("attempt %d error=%v", i, err)
		}
	}
	if consumes.Load() != 1 {
		t.Fatalf("consume calls=%d", consumes.Load())
	}
}

func TestUncertainOperationBlocksSameAccountAtAnotherEndpoint(t *testing.T) {
	var consumes atomic.Int32
	first := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == credentialsPath {
			_, _ = w.Write(credentialsJSON())
			return
		}
		if r.URL.Path == apiCallPath {
			consumes.Add(1)
			http.Error(w, "ambiguous", http.StatusBadGateway)
		}
	})
	defer first.Close()
	state := filepath.Join(t.TempDir(), "state")
	_, err := Reset(context.Background(), testOptions(first.URL, state, func(string) bool { return true }))
	if err == nil {
		t.Fatal("first outcome should be unknown")
	}
	second := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == credentialsPath {
			_, _ = w.Write(credentialsJSON())
			return
		}
		if r.URL.Path == apiCallPath {
			consumes.Add(1)
		}
	})
	defer second.Close()
	_, err = Reset(context.Background(), testOptions(second.URL, state, func(string) bool { return true }))
	if err == nil || !strings.Contains(err.Error(), "UNKNOWN, DO NOT RETRY") {
		t.Fatalf("cross-endpoint retry error=%v", err)
	}
	if consumes.Load() != 1 {
		t.Fatalf("consume calls=%d, want 1", consumes.Load())
	}
}

func TestResetLockFailsFast(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	started := make(chan struct{})
	release := make(chan struct{})
	var credentialRequests atomic.Int32
	server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case credentialsPath:
			if credentialRequests.Add(1) == 1 {
				close(started)
				<-release
			}
			_, _ = w.Write(credentialsJSON())
		case apiCallPath:
			http.Error(w, "ambiguous", http.StatusBadGateway)
		}
	})
	defer server.Close()
	firstDone := make(chan error, 1)
	go func() {
		_, err := Reset(context.Background(), testOptions(server.URL, state, func(string) bool { return true }))
		firstDone <- err
	}()
	<-started
	secondDone := make(chan error, 1)
	go func() {
		_, err := Reset(context.Background(), testOptions(server.URL, state, func(string) bool { return true }))
		secondDone <- err
	}()
	var secondErr error
	select {
	case secondErr = <-secondDone:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("concurrent reset waited for the active operation")
	}
	if secondErr == nil || !strings.Contains(secondErr.Error(), "another reset operation is active") {
		close(release)
		t.Fatalf("concurrent lock error=%v", secondErr)
	}
	close(release)
	if err := <-firstDone; err == nil || !strings.Contains(err.Error(), "UNKNOWN, DO NOT RETRY") {
		t.Fatalf("first operation error=%v", err)
	}
}

func TestResetRejectsMalformedAndUppercaseOutcomes(t *testing.T) {
	for _, body := range []string{`{"code":"RESET"}`, `{"code":"reset"`, `{"code":"reset "}`} {
		t.Run(body, func(t *testing.T) {
			var consumes atomic.Int32
			server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case credentialsPath:
					_, _ = w.Write(credentialsJSON())
				case apiCallPath:
					consumes.Add(1)
					encoded, _ := json.Marshal(body)
					_, _ = w.Write([]byte(`{"status_code":200,"body":` + string(encoded) + `}`))
				}
			})
			defer server.Close()
			_, err := Reset(context.Background(), testOptions(server.URL, filepath.Join(t.TempDir(), "state"), func(string) bool { return true }))
			if err == nil || !strings.Contains(err.Error(), "UNKNOWN, DO NOT RETRY") || consumes.Load() != 1 {
				t.Fatalf("error=%v consumes=%d", err, consumes.Load())
			}
		})
	}
}

func TestCooldownFailureNeverRepeatsConsume(t *testing.T) {
	var consumes atomic.Int32
	server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case credentialsPath:
			_, _ = w.Write(credentialsJSON())
		case apiCallPath:
			consumes.Add(1)
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"code\":\"already_redeemed\"}"}`))
		case cooldownPath:
			_, _ = w.Write([]byte(`{"auth_index":"different-index"}`))
		}
	})
	state := filepath.Join(t.TempDir(), "state")
	_, err := Reset(context.Background(), testOptions(server.URL, state, func(string) bool { return true }))
	if err == nil || !strings.Contains(err.Error(), "reset outcome already_redeemed; DO NOT retry consumption") {
		t.Fatalf("error=%v", err)
	}
	_, err = Reset(context.Background(), Options{BaseURL: server.URL, Key: "fake-key", StateDir: state, AuthIndex: testIndex, Confirm: func(string) bool { return true }, NewOperation: "00000000-0000-4000-8000-000000000001"})
	if err == nil || consumes.Load() != 1 {
		t.Fatalf("repeat err=%v consumes=%d", err, consumes.Load())
	}
	files, _ := filepath.Glob(filepath.Join(state, "*.json"))
	if len(files) != 1 {
		t.Fatalf("journal files=%v", files)
	}
	data, readErr := os.ReadFile(files[0])
	if readErr != nil || !strings.Contains(string(data), `"outcome":"already_redeemed"`) || !strings.Contains(string(data), `"cleanup":"failed"`) {
		t.Fatalf("cleanup outcome was not persisted: %s, err=%v", data, readErr)
	}
}

func TestCooldownRequiresOfficialStatusAndExactAuthIndex(t *testing.T) {
	for _, body := range []string{
		`{"status":"ok","auth_index":"different-index"}`,
		`{"status":"error","auth_index":"selected-codex-index"}`,
		`{"status":"ok","authIndex":"selected-codex-index"}`,
		`{"auth_index":"selected-codex-index"}`,
	} {
		t.Run(body, func(t *testing.T) {
			var consumes atomic.Int32
			server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case credentialsPath:
					_, _ = w.Write(credentialsJSON())
				case apiCallPath:
					consumes.Add(1)
					_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"code\":\"reset\"}"}`))
				case cooldownPath:
					_, _ = w.Write([]byte(body))
				}
			})
			defer server.Close()
			_, err := Reset(context.Background(), testOptions(server.URL, filepath.Join(t.TempDir(), "state"), func(string) bool { return true }))
			if err == nil || !strings.Contains(err.Error(), "reset outcome reset; DO NOT retry consumption") || consumes.Load() != 1 {
				t.Fatalf("error=%v consumes=%d", err, consumes.Load())
			}
		})
	}
}

func TestIdentitySwitchAfterConsumeBlocksCooldownAndOtherAccount(t *testing.T) {
	changed := base64.RawURLEncoding.EncodeToString([]byte(`{"chatgpt_account_id":"replacement-account"}`))
	var credentialReads, consumes, cooldowns atomic.Int32
	server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case credentialsPath:
			if credentialReads.Add(1) <= 2 {
				_, _ = w.Write(credentialsJSON())
			} else {
				_, _ = w.Write([]byte(`{"files":[{"auth_index":"` + testIndex + `","provider":"codex","id_token":"x.` + changed + `.x"}]}`))
			}
		case apiCallPath:
			consumes.Add(1)
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"code\":\"reset\"}"}`))
		case cooldownPath:
			cooldowns.Add(1)
		}
	})
	defer server.Close()
	state := filepath.Join(t.TempDir(), "state")
	_, err := Reset(context.Background(), testOptions(server.URL, state, func(string) bool { return true }))
	if err == nil || !strings.Contains(err.Error(), "account identity changed") || consumes.Load() != 1 || cooldowns.Load() != 0 {
		t.Fatalf("err=%v consumes=%d cooldowns=%d", err, consumes.Load(), cooldowns.Load())
	}
	files, _ := filepath.Glob(filepath.Join(state, "*.json"))
	if len(files) != 1 {
		t.Fatalf("journal files=%v", files)
	}
	data, err := os.ReadFile(files[0])
	if err != nil || !strings.Contains(string(data), `"cleanup":"failed"`) || !strings.Contains(string(data), `"outcome":"reset"`) {
		t.Fatalf("unresolved state not persisted: %s, err=%v", data, err)
	}
	_, err = Reset(context.Background(), testOptions(server.URL, state, func(string) bool { return true }))
	if err == nil || !strings.Contains(err.Error(), "auth index is unresolved") || consumes.Load() != 1 {
		t.Fatalf("different account was not blocked: err=%v consumes=%d", err, consumes.Load())
	}
}

func TestCredentialLookupFailureAfterConsumeBlocksCooldown(t *testing.T) {
	var credentialReads, consumes, cooldowns atomic.Int32
	server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case credentialsPath:
			if credentialReads.Add(1) == 3 {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write(credentialsJSON())
		case apiCallPath:
			consumes.Add(1)
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"code\":\"already_redeemed\"}"}`))
		case cooldownPath:
			cooldowns.Add(1)
		}
	})
	defer server.Close()
	state := filepath.Join(t.TempDir(), "state")
	_, err := Reset(context.Background(), testOptions(server.URL, state, func(string) bool { return true }))
	if err == nil || !strings.Contains(err.Error(), "account identity changed or could not be verified") || consumes.Load() != 1 || cooldowns.Load() != 0 {
		t.Fatalf("err=%v consumes=%d cooldowns=%d", err, consumes.Load(), cooldowns.Load())
	}
	_, err = Reset(context.Background(), testOptions(server.URL, state, func(string) bool { return true }))
	if err == nil || !strings.Contains(err.Error(), "auth index is unresolved") || consumes.Load() != 1 {
		t.Fatalf("unresolved lookup failure was not blocked: err=%v consumes=%d", err, consumes.Load())
	}
}

func TestUnknownJournalVersionsAndFieldsFailClosed(t *testing.T) {
	for _, invalid := range []string{
		`{"version":7,"endpoint_hash":"` + hashString("endpoint") + `","account_hash":"` + hashString(testAccount) + `","auth_index":"` + testIndex + `","operation":"00000000-0000-4000-8000-000000000001","phase":"done","outcome":"reset","cleanup":"done"}`,
		`{"version":1,"endpoint_hash":"` + hashString("endpoint") + `","account_hash":"` + hashString(testAccount) + `","auth_index":"` + testIndex + `","operation":"00000000-0000-4000-8000-000000000001","phase":"waiting","outcome":"reset","cleanup":"done"}`,
		`{"version":1,"endpoint_hash":"` + hashString("endpoint") + `","account_hash":"` + hashString(testAccount) + `","auth_index":"` + testIndex + `","operation":"00000000-0000-4000-8000-000000000001","phase":"done","outcome":"success","cleanup":"done"}`,
	} {
		t.Run(invalid, func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "state")
			if err := os.MkdirAll(state, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(state, hashString(testAccount)+".json")
			if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
				t.Fatal(err)
			}
			var consumes atomic.Int32
			server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == credentialsPath {
					_, _ = w.Write(credentialsJSON())
				}
				if r.URL.Path == apiCallPath {
					consumes.Add(1)
				}
			})
			defer server.Close()
			_, err := Reset(context.Background(), testOptions(server.URL, state, func(string) bool { return true }))
			if err == nil || !strings.Contains(err.Error(), "corrupt") || consumes.Load() != 0 {
				t.Fatalf("invalid journal accepted: err=%v consumes=%d", err, consumes.Load())
			}
		})
	}
}

func TestResetRejectsDuplicateAuthIndexAndSymlinkState(t *testing.T) {
	server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == credentialsPath {
			_, _ = w.Write(duplicateCredentialsJSON())
		}
	})
	state := filepath.Join(t.TempDir(), "state")
	_, err := Reset(context.Background(), testOptions(server.URL, state, func(string) bool { return true }))
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("duplicate error=%v", err)
	}
	other := t.TempDir()
	link := filepath.Join(other, "linked")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	_, err = Reset(context.Background(), testOptions(server.URL, link, func(string) bool { return true }))
	if err == nil {
		t.Fatal("symlink state directory accepted")
	}
}

func TestResetRejectsConflictingCredentialAccountIDs(t *testing.T) {
	claim := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"claim-account"}}`))
	credentials := []byte(`{"files":[{"auth_index":"` + testIndex + `","provider":"codex","chatgpt_account_id":"different-account","id_token":"x.` + claim + `.x"}]}`)
	var consumes atomic.Int32
	server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case credentialsPath:
			_, _ = w.Write(credentials)
		case apiCallPath:
			consumes.Add(1)
		}
	})
	defer server.Close()
	_, err := Reset(context.Background(), testOptions(server.URL, filepath.Join(t.TempDir(), "state"), func(string) bool { return true }))
	if err == nil || !strings.Contains(err.Error(), "conflicting account IDs") || consumes.Load() != 0 {
		t.Fatalf("error=%v consumes=%d", err, consumes.Load())
	}
}

func TestResetResolvesCredentialAccountIDMetadata(t *testing.T) {
	for _, fields := range []string{
		`"chatgpt_account_id":"` + testAccount + `"`,
		`"metadata":{"chatgptAccountId":"` + testAccount + `"}`,
		`"attributes":{"chatgpt_account_id":"` + testAccount + `"}`,
	} {
		t.Run(fields, func(t *testing.T) {
			data := []byte(`{"files":[{"auth_index":"` + testIndex + `","provider":"codex",` + fields + `}]}`)
			server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == credentialsPath {
					_, _ = w.Write(data)
				}
			})
			defer server.Close()
			selected, err := fetchSelected(context.Background(), http.DefaultClient, server.URL, "fake-key", testIndex)
			if err != nil || selected.accountID != testAccount {
				t.Fatalf("selected=%+v err=%v", selected, err)
			}
		})
	}
}

func TestResetValidatesWindowsResetCount(t *testing.T) {
	for _, count := range []string{"-1", "1.5", `"1"`, "null"} {
		t.Run(count, func(t *testing.T) {
			var consumes atomic.Int32
			server := newResetServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case credentialsPath:
					_, _ = w.Write(credentialsJSON())
				case apiCallPath:
					consumes.Add(1)
					_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"code\":\"reset\",\"windows_reset\":` + count + `}"}`))
				case cooldownPath:
					_, _ = w.Write([]byte(`{"status":"ok","auth_index":"selected-codex-index"}`))
				}
			})
			defer server.Close()
			_, err := Reset(context.Background(), testOptions(server.URL, filepath.Join(t.TempDir(), "state"), func(string) bool { return true }))
			if err == nil || !strings.Contains(err.Error(), "UNKNOWN, DO NOT RETRY") {
				t.Fatalf("invalid windows_reset accepted, error=%v", err)
			}
			if consumes.Load() != 1 {
				t.Fatalf("consume calls=%d", consumes.Load())
			}
		})
	}
}

func testOptions(base, state string, confirm func(string) bool) Options {
	return Options{BaseURL: base, Key: "fake-key", StateDir: state, AuthIndex: testIndex, Confirm: confirm}
}
func newResetServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-key" {
			t.Error("missing fake management key")
		}
		h(w, r)
	}))
}
func credentialsJSON() []byte {
	claim := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + testAccount + `"}}`))
	token := "x." + claim + ".x"
	b, _ := json.Marshal(map[string]any{"files": []any{map[string]any{"auth_index": testIndex, "provider": "codex", "id_token": token}}})
	return b
}
func duplicateCredentialsJSON() []byte {
	var v map[string]any
	_ = json.Unmarshal(credentialsJSON(), &v)
	files := v["files"].([]any)
	v["files"] = append(files, files[0])
	b, _ := json.Marshal(v)
	return b
}
