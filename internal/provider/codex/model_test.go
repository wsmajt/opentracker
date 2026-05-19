package codex

import (
	"encoding/json"
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
