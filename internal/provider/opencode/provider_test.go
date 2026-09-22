package opencode

import "testing"

func TestContains(t *testing.T) {
	if !contains("hello world", "world") {
		t.Error("expected contains to return true for substring")
	}
	if contains("hello world", "foo") {
		t.Error("expected contains to return false for missing substring")
	}
	if contains("", "") {
		t.Error("this implementation returns false for empty strings")
	}
	if contains("abc", "") {
		t.Error("expected non-empty string not to 'contain' empty substring in this implementation")
	}
}

func TestContainsAny(t *testing.T) {
	if !containsAny("hello world", []string{"foo", "world"}) {
		t.Error("expected containsAny to return true when one substring matches")
	}
	if containsAny("hello world", []string{"foo", "bar"}) {
		t.Error("expected containsAny to return false when no substring matches")
	}
}

func TestIsValidResponse(t *testing.T) {
	if !isValidResponse(`<div data-slot="usage-item">Rolling usage</div>`) {
		t.Error("expected valid response for HTML with usage-item")
	}
	if !isValidResponse(`{"rollingUsage": 5}`) {
		t.Error("expected valid response for JSON with rollingUsage")
	}
	if !isValidResponse(`{"id": "wrk_123"}`) {
		t.Error("expected valid response for JSON with wrk_ prefix")
	}
	if !isValidResponse(`{"access": {"meters": {"fiveHour": {}}}}`) {
		t.Error("expected valid response for console Go status JSON")
	}
	if !isValidResponse(`{"billingMode": "prepaid", "balanceMicroCents": "0"}`) {
		t.Error("expected valid response for console billing status JSON")
	}
	if isValidResponse(`random text without markers`) {
		t.Error("expected invalid response for random text")
	}
	if isValidResponse("") {
		t.Error("expected invalid response for empty string")
	}
}

func TestParseDispatch(t *testing.T) {
	goPlan := &OpenCodeProvider{plan: "go"}
	zenPlan := &OpenCodeProvider{plan: "zen"}

	usage, err := goPlan.Parse(`{"access": {"endsAt": null, "meters": {"fiveHour": {}, "week": {}, "month": {}}}}`)
	if err != nil {
		t.Fatalf("go plan JSON parse failed: %v", err)
	}
	if _, ok := usage.(GoUsage); !ok {
		t.Errorf("go plan JSON returned %T, want GoUsage", usage)
	}

	billing, err := zenPlan.Parse(`{"billingMode": "prepaid", "mode": "pay-as-you-go", "balanceMicroCents": "0", "availableMicroCents": "0"}`)
	if err != nil {
		t.Fatalf("zen plan JSON parse failed: %v", err)
	}
	if _, ok := billing.(ZenBilling); !ok {
		t.Errorf("zen plan JSON returned %T, want ZenBilling", billing)
	}

	html, err := goPlan.Parse(`<div data-slot="usage-item"><span data-slot="usage-label">Rolling usage</span><div data-slot="progress-bar" style="width:1%"></div></div>`)
	if err != nil {
		t.Fatalf("legacy HTML parse failed: %v", err)
	}
	if _, ok := html.(GoUsage); !ok {
		t.Errorf("legacy HTML returned %T, want GoUsage", html)
	}
}
