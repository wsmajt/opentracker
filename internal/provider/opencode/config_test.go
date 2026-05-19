package opencode

import (
	"encoding/json"
	"testing"
)

func TestParseConfig_Valid(t *testing.T) {
	raw := json.RawMessage(`{"workspace":"wrk_12345"}`)
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Workspace != "wrk_12345" {
		t.Errorf("workspace = %q, want wrk_12345", cfg.Workspace)
	}
}

func TestParseConfig_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`{bad json`)
	_, err := ParseConfig(raw)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseConfig_Empty(t *testing.T) {
	raw := json.RawMessage(`{}`)
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Workspace != "" {
		t.Errorf("workspace = %q, want empty string", cfg.Workspace)
	}
}
