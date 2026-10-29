package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchReadsResetCreditDetailsWithoutBlockingUsage(t *testing.T) {
	for _, tc := range []struct {
		name, details string
		status        int
		wantAvailable int64
		wantCredits   int
		wantJSON      string
	}{
		{name: "details", details: `{"available_count":5,"credits":[{"reset_type":"codex_rate_limits","status":"available","expires_at":"2026-01-02T03:04:05Z"},{"reset_type":"codex_rate_limits","status":"available","expires_at":null},{"reset_type":"codex_rate_limits","status":"available"},{"reset_type":"codex_rate_limits","status":"available","expires_at":"invalid"}]}`, status: http.StatusOK, wantAvailable: 5, wantCredits: 4, wantJSON: `"credits":[{"expiresAt":"2026-01-02T03:04:05Z"},{"expiresAt":null},{},{}]`},
		{name: "known empty list", details: `{"available_count":0,"credits":[]}`, status: http.StatusOK, wantAvailable: 0, wantJSON: `"credits":[]`},
		{name: "rate limited details", details: `private-marker`, status: http.StatusTooManyRequests, wantAvailable: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
			t.Setenv(envAccessToken, "fake-local-token")
			t.Setenv(envAccountID, "fake-account")
			calls := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls[r.URL.Path]++
				if r.Header.Get("Authorization") != "Bearer fake-local-token" || r.Header.Get("ChatGPT-Account-Id") != "fake-account" {
					t.Errorf("unexpected auth headers: %v", r.Header)
				}
				switch r.URL.Path {
				case "/usage":
					_, _ = fmt.Fprint(w, `{"plan_type":"plus","rate_limit_reset_credits":{"available_count":3,"applicable_available_count":1}}`)
				case "/details":
					w.WriteHeader(tc.status)
					_, _ = fmt.Fprint(w, tc.details)
				default:
					t.Errorf("unexpected read endpoint %q", r.URL.Path)
				}
			}))
			defer server.Close()
			provider := &CodexProvider{
				client:               server.Client(),
				usageEndpoint:        server.URL + "/usage",
				resetCreditsEndpoint: server.URL + "/details",
			}
			data, err := provider.Fetch(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := provider.Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			usage := parsed.(UsageSnapshot)
			if calls["/usage"] != 1 || calls["/details"] != 1 {
				t.Fatalf("GET counts = %#v", calls)
			}
			if usage.ResetCredits == nil || usage.ResetCredits.Available == nil || *usage.ResetCredits.Available != tc.wantAvailable || usage.ResetCredits.Applicable == nil || *usage.ResetCredits.Applicable != 1 {
				t.Fatalf("usage count was not preserved: %#v", usage.ResetCredits)
			}
			if usage.AccountIDInternal != "fake-account" {
				t.Fatalf("fetched account ID=%q, want fake-account", usage.AccountIDInternal)
			}
			if len(usage.ResetCredits.Credits) != tc.wantCredits {
				t.Fatalf("credits=%#v, want %d entries", usage.ResetCredits.Credits, tc.wantCredits)
			}
			encoded, _ := json.Marshal(usage)
			if tc.wantJSON != "" && !strings.Contains(string(encoded), tc.wantJSON) {
				t.Fatalf("round-tripped local JSON lacks %s: %s", tc.wantJSON, encoded)
			}
			if strings.Contains(string(encoded), "fake-local-token") || strings.Contains(string(encoded), "private-marker") || strings.Contains(string(encoded), "fake-account") {
				t.Fatalf("sensitive data leaked: %s", encoded)
			}
		})
	}
}
