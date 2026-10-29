package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"opentracker/internal/resetcodex"
)

func TestResetCommandRejectsNonTerminalWithoutResolvingKey(t *testing.T) {
	resetAccount = "fake-index"
	resolved := false
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	err := runCodexReset(cmd, strings.NewReader("RESET\n"), &strings.Builder{}, &strings.Builder{}, func() (string, string, string, error) {
		resolved = true
		return "fake", "http://127.0.0.1:8317", t.TempDir(), nil
	}, func() bool { return false })
	if err == nil || !strings.Contains(err.Error(), "interactive terminal") || resolved {
		t.Fatalf("error=%v resolved=%v", err, resolved)
	}
}

func TestResetCommandRequiresTypedConfirmation(t *testing.T) {
	resetAccount = "fake-index"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v8/management/credentials" {
			t.Errorf("unexpected pre-confirmation request %s", r.URL.Path)
		}
		claim := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"fake-account"}}`))
		_, _ = fmt.Fprintf(w, `{"files":[{"auth_index":"fake-index","provider":"codex","id_token":"x.%s.x"}]}`, claim)
	}))
	defer server.Close()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var output, prompt strings.Builder
	err := runCodexReset(cmd, strings.NewReader("no\n"), &prompt, &output, func() (string, string, string, error) {
		return "fake-key", server.URL, filepath.Join(t.TempDir(), "state"), nil
	}, func() bool { return true })
	if err == nil || requests != 1 || output.Len() != 0 {
		t.Fatalf("error=%v requests=%d output=%q", err, requests, output.String())
	}
	if !strings.Contains(prompt.String(), "Direct/local Codex reset is not supported") {
		t.Fatalf("prompt=%q", prompt.String())
	}
}

func TestResetCommandPrintsJSONAfterConfirmedFakeReset(t *testing.T) {
	resetAccount = "fake-index"
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/v8/management/credentials":
			claim := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"fake-account"}}`))
			_, _ = fmt.Fprintf(w, `{"files":[{"auth_index":"fake-index","provider":"codex","id_token":"x.%s.x"}]}`, claim)
		case "/v8/management/requests/api-call":
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"code\":\"already_redeemed\",\"windows_reset\":0}"}`))
		case "/v8/management/routing/cooldown/reset":
			_, _ = w.Write([]byte(`{"status":"ok","auth_index":"fake-index","models":[]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var output strings.Builder
	var prompt strings.Builder
	err := runCodexReset(cmd, strings.NewReader("RESET\n"), &prompt, &output, func() (string, string, string, error) {
		return "fake-key", server.URL, filepath.Join(t.TempDir(), "state"), nil
	}, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Status       string `json:"status"`
		WindowsReset *int   `json:"windows_reset"`
	}
	if json.Unmarshal([]byte(output.String()), &got) != nil || got.Status != "already_redeemed" || got.WindowsReset == nil || *got.WindowsReset != 0 {
		t.Fatalf("output=%q", output.String())
	}
	if requests != 5 {
		t.Fatalf("management requests=%d, want pre-confirmation lookup, consume, identity revalidation, and cleanup", requests)
	}
	if strings.Contains(prompt.String(), "fake-account") || !strings.Contains(prompt.String(), resetcodex.AccountFingerprint("fake-account")) || !strings.Contains(prompt.String(), "This consumes one reset") {
		t.Fatalf("unsafe or incomplete prompt: %q", prompt.String())
	}
}

func TestRootResetCodexHelpUsesNestedCommand(t *testing.T) {
	oldResolve, oldInput, oldTTY := resetResolve, resetInput, resetTTY
	defer func() { resetResolve, resetInput, resetTTY = oldResolve, oldInput, oldTTY }()
	resolved := false
	resetResolve = func() (string, string, string, error) {
		resolved = true
		return "", "", "", nil
	}
	rootCmd.SetArgs([]string{"reset", "codex", "--help"})
	var stdout, stderr strings.Builder
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	resetCodexCmd.SetOut(&stdout)
	resetCodexCmd.SetErr(&stderr)
	err := rootCmd.Execute()
	rootCmd.SetArgs(nil)
	_ = resetCodexCmd.Flags().Set("help", "false")
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	resetCodexCmd.SetOut(nil)
	resetCodexCmd.SetErr(nil)
	if err != nil || resolved || !strings.Contains(stdout.String(), "Usage:") || !strings.Contains(stdout.String(), "reset codex") {
		t.Fatalf("err=%v resolved=%v stdout=%q stderr=%q", err, resolved, stdout.String(), stderr.String())
	}
}

func TestRootResetCodexRunsWithActualArgsAndFakeServer(t *testing.T) {
	oldResolve, oldInput, oldTTY := resetResolve, resetInput, resetTTY
	defer func() { resetResolve, resetInput, resetTTY = oldResolve, oldInput, oldTTY }()
	resetInput = strings.NewReader("RESET\n")
	resetTTY = func() bool { return true }
	stateDir := filepath.Join(t.TempDir(), "state")
	var consumes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v8/management/credentials":
			claim := base64.RawURLEncoding.EncodeToString([]byte(`{"chatgpt_account_id":"fake-account"}`))
			_, _ = fmt.Fprintf(w, `{"files":[{"auth_index":"cli-index","provider":"codex","id_token":"x.%s.x"}]}`, claim)
		case "/v8/management/requests/api-call":
			consumes++
			_, _ = w.Write([]byte(`{"status_code":200,"body":"{\"code\":\"no_credit\",\"windows_reset\":0}"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	resetResolve = func() (string, string, string, error) {
		return "fake-key", server.URL, stateDir, nil
	}
	rootCmd.SetArgs([]string{"reset", "codex", "--account", "cli-index"})
	var stdout, stderr strings.Builder
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	resetCodexCmd.SetOut(&stdout)
	resetCodexCmd.SetErr(&stderr)
	err := rootCmd.Execute()
	rootCmd.SetArgs(nil)
	_ = resetCodexCmd.Flags().Set("help", "false")
	_ = resetCodexCmd.Flags().Set("account", "")
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	resetCodexCmd.SetOut(nil)
	resetCodexCmd.SetErr(nil)
	if err != nil {
		t.Fatalf("execute: %v, stderr=%q", err, stderr.String())
	}
	var got struct {
		Status       string `json:"status"`
		WindowsReset *int   `json:"windows_reset"`
		OperationID  string `json:"operation_id"`
	}
	if json.Unmarshal([]byte(stdout.String()), &got) != nil || got.Status != "no_credit" || got.WindowsReset == nil || *got.WindowsReset != 0 || got.OperationID == "" {
		t.Fatalf("stdout=%q", stdout.String())
	}
	firstOperation := got.OperationID
	resetInput = strings.NewReader("RESET\n")
	rootCmd.SetArgs([]string{"reset", "codex", "--account", "cli-index", "--new", firstOperation})
	stdout.Reset()
	stderr.Reset()
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	resetCodexCmd.SetOut(&stdout)
	resetCodexCmd.SetErr(&stderr)
	err = rootCmd.Execute()
	rootCmd.SetArgs(nil)
	_ = resetCodexCmd.Flags().Set("account", "")
	_ = resetCodexCmd.Flags().Set("new", "")
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	resetCodexCmd.SetOut(nil)
	resetCodexCmd.SetErr(nil)
	if err != nil {
		t.Fatalf("second explicit reset: %v", err)
	}
	if consumes != 2 || !strings.Contains(stderr.String(), "Prior operation "+firstOperation+" had outcome no_credit") {
		t.Fatalf("second reset did not show prior outcome: consumes=%d stderr=%q", consumes, stderr.String())
	}
}
