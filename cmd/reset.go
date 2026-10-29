package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"opentracker/internal/config"
	"opentracker/internal/provider/cliproxyapi"
	"opentracker/internal/resetcodex"
)

var (
	resetAccount string
	resetNew     string
	resetInput   io.Reader   = os.Stdin
	resetTTY                 = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	resetResolve keyResolver = resolveCodexResetCredentials
)

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Run an explicit usage reset action",
}

var resetCodexCmd = &cobra.Command{
	Use:   "codex",
	Short: "Reset Codex usage credits through CLIProxyAPI",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runCodexReset(cmd, resetInput, cmd.ErrOrStderr(), cmd.OutOrStdout(), resetResolve, resetTTY)
	},
}

type keyResolver func() (key, baseURL, stateDir string, err error)

func resolveCodexResetCredentials() (string, string, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", "", "", errors.New("cannot load CLIProxyAPI configuration")
	}
	key, configured, err := cliproxyapi.ResolveManagementKey(cfg, cliproxyapi.NewCredentialStore())
	if err != nil {
		return "", "", "", errors.New("cannot resolve CLIProxyAPI management key")
	}
	if !configured {
		return "", "", "", fmt.Errorf("%s is not configured; run 'opentracker login cliproxyapi'", cliproxyapi.EnvKey)
	}
	base := strings.TrimSpace(os.Getenv(cliproxyapi.EnvURL))
	if base == "" {
		base = "http://127.0.0.1:8317"
	}
	stateDir, err := os.UserConfigDir()
	if err != nil {
		return "", "", "", errors.New("cannot locate private reset state directory")
	}
	return key, base, filepath.Join(stateDir, "opentracker", "reset"), nil
}

func runCodexReset(cmd *cobra.Command, input io.Reader, prompt, output io.Writer, resolve keyResolver, isTerminal func() bool) error {
	if strings.TrimSpace(resetAccount) == "" {
		return errors.New("--account is required")
	}
	if isTerminal == nil || !isTerminal() {
		return errors.New("reset requires an interactive terminal; no request was sent")
	}
	key, base, stateDir, err := resolve()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 45*time.Second)
	defer cancel()
	result, err := resetcodex.Reset(ctx, resetcodex.Options{
		BaseURL: base, Key: key, StateDir: stateDir, AuthIndex: resetAccount, NewOperation: resetNew,
		ConfirmDetails: func(accountID string, previous *resetcodex.PreviousOperation) bool {
			if _, writeErr := fmt.Fprintln(prompt, "CLIProxyAPI Codex reset. Direct/local Codex reset is not supported."); writeErr != nil {
				return false
			}
			if _, writeErr := fmt.Fprintf(prompt, "Selected account fingerprint (SHA-256): %s\nVerify this is the intended account. ", resetcodex.AccountFingerprint(accountID)); writeErr != nil {
				return false
			}
			if previous != nil {
				if _, writeErr := fmt.Fprintf(prompt, "Prior operation %s had outcome %s. ", previous.ID, previous.Outcome); writeErr != nil {
					return false
				}
			}
			if _, writeErr := fmt.Fprint(prompt, "This consumes one reset. No automatic retries are made. Type RESET to continue: "); writeErr != nil {
				return false
			}
			line, readErr := bufio.NewReader(io.LimitReader(input, 32)).ReadString('\n')
			return readErr == nil && len(line) <= 8 && strings.TrimSpace(line) == "RESET"
		},
	})
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return errors.New("cannot encode reset result")
	}
	_, err = fmt.Fprintln(output, string(encoded))
	if err != nil {
		return errors.New("cannot write reset result")
	}
	return nil
}

func init() {
	resetCodexCmd.Flags().StringVar(&resetAccount, "account", "", "CLIProxyAPI auth index for the Codex account")
	resetCodexCmd.Flags().StringVar(&resetNew, "new", "", "completed operation ID to confirm a separate reset")
	resetCmd.AddCommand(resetCodexCmd)
	rootCmd.AddCommand(resetCmd)
}
