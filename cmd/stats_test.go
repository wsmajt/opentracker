package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatsOpenCodeHelpAndJSONOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	raw := fmt.Sprintf(`{"range":{"from":%d,"to":%d},"sessions":3,"tokens":{"input":10,"output":20,"reasoning":30,"cache":{"read":40,"write":50}}}`,
		start.UnixMilli(), now.UnixMilli())
	binary := filepath.Join(dir, "opencode")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s' '"+raw+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	if statsOpenCodeCmd.Parent() != statsCmd {
		t.Fatal("stats opencode command is not registered under stats")
	}
	if !strings.Contains(statsOpenCodeCmd.UsageString(), "--force") {
		t.Fatalf("stats usage lacks --force: %s", statsOpenCodeCmd.UsageString())
	}

	var output bytes.Buffer
	statsOpenCodeCmd.SetOut(&output)
	statsOpenCodeCmd.SetContext(context.Background())
	if err := statsOpenCodeCmd.RunE(statsOpenCodeCmd, nil); err != nil {
		t.Fatal(err)
	}
	var contract map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract) != 6 {
		t.Fatalf("JSON contract has keys %v", contract)
	}
	var updatedAt string
	if err := json.Unmarshal(contract["updatedAt"], &updatedAt); err != nil {
		t.Fatal(err)
	}
	updated, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil || updated.Location() != time.UTC {
		t.Fatalf("updatedAt = %q, parse error %v", updatedAt, err)
	}
	want := fmt.Sprintf(`{
  "source": "opencode-local",
  "period": "today",
  "totalTokens": 150,
  "sessions": 3,
  "updatedAt": %q,
  "range": {
    "from": %d,
    "to": %d
  }
}`+"\n", updatedAt, start.UnixMilli(), now.UnixMilli())
	if output.String() != want {
		t.Fatalf("stats JSON = %s, want %s", output.String(), want)
	}
	var rangeFields map[string]json.RawMessage
	if err := json.Unmarshal(contract["range"], &rangeFields); err != nil || len(rangeFields) != 2 {
		t.Fatalf("range contract = %s, %v", contract["range"], err)
	}
}
