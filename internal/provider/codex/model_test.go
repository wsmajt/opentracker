package codex

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestUsageResponseDecodeAndMap(t *testing.T) {
	data := []byte(`{
		"plan_type": "plus",
		"rate_limit": {
			"primary_window": {
				"used_percent": 42,
				"reset_at": 1760000000,
				"limit_window_seconds": 18000
			},
			"secondary_window": {
				"used_percent": 10,
				"reset_at": "1760500000",
				"limit_window_seconds": 604800
			}
		},
		"credits": {
			"has_credits": true,
			"unlimited": false,
			"balance": "123.45"
		}
	}`)

	var api UsageResponse
	if err := json.Unmarshal(data, &api); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	snapshot := mapUsageResponse(api, "user@example.com", time.Unix(100, 0))
	if snapshot.AccountEmail != "user@example.com" {
		t.Fatalf("AccountEmail = %q", snapshot.AccountEmail)
	}
	if snapshot.Plan != "plus" {
		t.Fatalf("Plan = %q, want plus", snapshot.Plan)
	}
	if snapshot.Primary == nil || snapshot.Primary.UsedPercent != 42 || snapshot.Primary.RemainingPercent != 58 {
		t.Fatalf("Primary = %#v, want 42 used / 58 remaining", snapshot.Primary)
	}
	if snapshot.Primary.WindowSeconds == nil || *snapshot.Primary.WindowSeconds != 18000 {
		t.Fatalf("Primary.WindowSeconds = %#v, want 18000", snapshot.Primary.WindowSeconds)
	}
	if snapshot.Primary.ResetsAt == nil || *snapshot.Primary.ResetsAt != "2025-10-09T08:53:20Z" {
		t.Fatalf("Primary.ResetsAt = %#v", snapshot.Primary.ResetsAt)
	}
	if snapshot.Secondary == nil || snapshot.Secondary.UsedPercent != 10 {
		t.Fatalf("Secondary = %#v, want used 10", snapshot.Secondary)
	}
	if snapshot.CreditsRemaining == nil || *snapshot.CreditsRemaining != 123.45 {
		t.Fatalf("CreditsRemaining = %#v, want 123.45", snapshot.CreditsRemaining)
	}
	if snapshot.UpdatedAt != "1970-01-01T00:01:40Z" {
		t.Fatalf("UpdatedAt = %q", snapshot.UpdatedAt)
	}
}

func TestUsageResponseRoundTrip(t *testing.T) {
	data := []byte(`{"usageResponse":{"rate_limit":{"primary_window":{"used_percent":42,"reset_at":1760000000,"limit_window_seconds":18000}}}}`)

	var envelope fetchEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var decoded fetchEnvelope
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("round-trip json.Unmarshal() error = %v; raw=%s", err, raw)
	}
	if decoded.UsageResponse.RateLimit.PrimaryWindow.UsedPercent.Value != 42 {
		t.Fatalf("UsedPercent = %v, want 42", decoded.UsageResponse.RateLimit.PrimaryWindow.UsedPercent.Value)
	}
}

func TestMapUsageResponseAllowsPartialCredits(t *testing.T) {
	var api UsageResponse
	if err := json.Unmarshal([]byte(`{"credits":{"balance":5}}`), &api); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	snapshot := mapUsageResponse(api, "", time.Unix(0, 0))
	if snapshot.Primary != nil || snapshot.Secondary != nil {
		t.Fatalf("expected no rate windows, got primary=%#v secondary=%#v", snapshot.Primary, snapshot.Secondary)
	}
	if snapshot.CreditsRemaining == nil || *snapshot.CreditsRemaining != 5 {
		t.Fatalf("CreditsRemaining = %#v, want 5", snapshot.CreditsRemaining)
	}
}

func TestUsageResponseMapsResetCredits(t *testing.T) {
	tests := []struct {
		name             string
		body             string
		wantAvailable    *int64
		wantApplicable   *int64
		wantResetCredits bool
		wantError        bool
	}{
		{name: "absent", body: `{}`},
		{name: "null", body: `{"rate_limit_reset_credits":null}`},
		{name: "zero", body: `{"rate_limit_reset_credits":{"available_count":0,"applicable_available_count":0}}`, wantAvailable: int64Pointer(0), wantApplicable: int64Pointer(0), wantResetCredits: true},
		{name: "integers", body: `{"rate_limit_reset_credits":{"available_count":2,"applicable_available_count":1}}`, wantAvailable: int64Pointer(2), wantApplicable: int64Pointer(1), wantResetCredits: true},
		{name: "numeric strings", body: `{"rate_limit_reset_credits":{"available_count":"3","applicable_available_count":"2"}}`, wantAvailable: int64Pointer(3), wantApplicable: int64Pointer(2), wantResetCredits: true},
		{name: "partial and unknown fields", body: `{"rate_limit_reset_credits":{"available_count":4,"future_field":7}}`, wantAvailable: int64Pointer(4), wantResetCredits: true},
		{name: "negative values are omitted", body: `{"rate_limit_reset_credits":{"available_count":-1,"applicable_available_count":-2}}`},
		{name: "malformed value", body: `{"rate_limit_reset_credits":{"available_count":"many"}}`, wantError: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var api UsageResponse
			err := json.Unmarshal([]byte(tc.body), &api)
			if tc.wantError {
				if err == nil {
					t.Fatal("json.Unmarshal() succeeded, want malformed reset-credit error")
				}
				return
			}
			if err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			snapshot := mapUsageResponse(api, "", time.Unix(0, 0))
			if (snapshot.ResetCredits != nil) != tc.wantResetCredits {
				t.Fatalf("ResetCredits = %#v, want present=%v", snapshot.ResetCredits, tc.wantResetCredits)
			}
			if !tc.wantResetCredits {
				return
			}
			if !equalInt64Pointer(snapshot.ResetCredits.Available, tc.wantAvailable) || !equalInt64Pointer(snapshot.ResetCredits.Applicable, tc.wantApplicable) {
				t.Fatalf("ResetCredits = %#v, want available=%v applicable=%v", snapshot.ResetCredits, tc.wantAvailable, tc.wantApplicable)
			}
		})
	}
}

func TestParseResetCreditsDetails(t *testing.T) {
	got := ParseResetCreditsDetails([]byte(`{"available_count":4,"credits":[{"id":"private-id","reset_type":"codex_rate_limits","status":"available","granted_at":"2026-01-01T00:00:00Z","expires_at":"2026-02-01T03:00:00+03:00"},{"reset_type":"codex_rate_limits","status":"available","expires_at":null},{"reset_type":"codex_rate_limits","status":"available"},{"reset_type":"other","status":"available","expires_at":"2026-01-01T00:00:00Z"},{"reset_type":"codex_rate_limits","status":"used"}]}`))
	if got == nil || got.Available == nil || *got.Available != 4 || len(got.Credits) != 3 {
		t.Fatalf("parsed details = %#v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"available":4,"credits":[{"expiresAt":"2026-02-01T00:00:00Z"},{"expiresAt":null},{}]}`
	if string(encoded) != want {
		t.Fatalf("encoded details = %s, want %s", encoded, want)
	}
	if strings.Contains(string(encoded), "private-id") || strings.Contains(string(encoded), "granted_at") {
		t.Fatalf("private upstream fields leaked: %s", encoded)
	}
}

func TestParseResetCreditsPreservesKnownEmptyList(t *testing.T) {
	got := ParseResetCreditsDetails([]byte(`{"available_count":0,"credits":[]}`))
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"available":0,"credits":[]}` {
		t.Fatalf("encoded details = %s", encoded)
	}
}

func TestParseResetCreditsRejectsNegativeDetailsCount(t *testing.T) {
	got := ParseResetCreditsDetails([]byte(`{"available_count":-4,"credits":[]}`))
	if got == nil || got.Available != nil || got.Credits == nil || len(got.Credits) != 0 {
		t.Fatalf("parsed details = %#v, want no count and a known empty list", got)
	}
}

func int64Pointer(value int64) *int64 { return &value }

func equalInt64Pointer(left, right *int64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func TestMapWindowClampsPercent(t *testing.T) {
	under := mapWindow(&APIWindow{UsedPercent: FlexibleFloat{Value: -10, Set: true}})
	if under.UsedPercent != 0 || under.RemainingPercent != 100 {
		t.Fatalf("under = %#v, want 0/100", under)
	}

	over := mapWindow(&APIWindow{UsedPercent: FlexibleFloat{Value: 150, Set: true}})
	if over.UsedPercent != 100 || over.RemainingPercent != 0 {
		t.Fatalf("over = %#v, want 100/0", over)
	}
}

func TestMapWindowOmitsMissingUsedPercent(t *testing.T) {
	if got := mapWindow(&APIWindow{}); got != nil {
		t.Fatalf("mapWindow() = %#v, want nil", got)
	}
}
