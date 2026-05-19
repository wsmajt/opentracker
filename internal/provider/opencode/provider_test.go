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
	if isValidResponse(`random text without markers`) {
		t.Error("expected invalid response for random text")
	}
	if isValidResponse("") {
		t.Error("expected invalid response for empty string")
	}
}
