package opencode

import "testing"

func TestIsValidWorkspaceID(t *testing.T) {
	tests := []struct {
		id    string
		valid bool
	}{
		{"wrk_123", true},
		{"wrk_", false},
		{"", false},
		{"abc_123", false},
		{"wrk", false},
		{"wrk_1234567890abcdef", true},
	}

	for _, tt := range tests {
		got := isValidWorkspaceID(tt.id)
		if got != tt.valid {
			t.Errorf("isValidWorkspaceID(%q) = %v, want %v", tt.id, got, tt.valid)
		}
	}
}

func TestExtractWorkspaceID_FromJS(t *testing.T) {
	text := `somePrefix id: "wrk_abc123", otherField`
	got := extractWorkspaceID(text)
	if got != "wrk_abc123" {
		t.Errorf("extractWorkspaceID = %q, want wrk_abc123", got)
	}
}

func TestExtractWorkspaceID_FromPath(t *testing.T) {
	text := `href="/workspace/wrk_xyz999/settings"`
	got := extractWorkspaceID(text)
	if got != "wrk_xyz999" {
		t.Errorf("extractWorkspaceID = %q, want wrk_xyz999", got)
	}
}

func TestExtractWorkspaceID_NotFound(t *testing.T) {
	text := `no workspace id here`
	got := extractWorkspaceID(text)
	if got != "" {
		t.Errorf("extractWorkspaceID = %q, want empty string", got)
	}
}
