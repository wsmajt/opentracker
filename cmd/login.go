package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"opentracker/internal/app"
	"opentracker/internal/browsercookies"
	"opentracker/internal/cache"
	"opentracker/internal/config"
	"opentracker/internal/provider/opencode"
)

var verbose bool

var loginCmd = &cobra.Command{
	Use:   "login [provider]",
	Short: "Open the login page for a provider",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			printProviderList("login")
			return nil
		}
		provider := args[0]

		var url string
		switch provider {
		case "codex":
			return loginCodex()
		case "opencode":
			url = "https://opencode.ai/console/login"
		default:
			return fmt.Errorf("unknown provider: %s", provider)
		}

		fmt.Printf("Open this URL to log in: %s\n", url)

		switch runtime.GOOS {
		case "linux":
			_ = exec.Command("xdg-open", url).Start()
		case "darwin":
			_ = exec.Command("open", url).Start()
		case "windows":
			_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
		}

		fmt.Println()
		fmt.Println("After logging in, press Enter to automatically import cookies...")
		input := bufio.NewReader(os.Stdin)
		_, _ = input.ReadBytes('\n')

		var logger func(string)
		if verbose {
			logger = func(msg string) {
				fmt.Println(msg)
			}
		}

		cookies, source, err := browsercookies.ImportOpenCode(context.Background(), logger)
		if err != nil {
			return fmt.Errorf("automatic OpenCode browser import failed: %w; sign in to opencode.ai in a supported browser, then retry 'opentracker login opencode'", err)
		}

		// Only an authenticated session that independently identifies its
		// workspace may replace the currently selected account.
		workspaceID, err := loginOpenCodeWithWorkspaceList(cookies, opencode.NewCredentialStore(), opencode.ListWorkspaceIDsFromCookies, input)
		if err != nil {
			return err
		}
		fmt.Printf("Imported %d cookies from %s; workspace saved: %s\n", len(cookies), source, workspaceID)

		return nil
	},
}

func loginOpenCodeWithWorkspaceList(cookies []*http.Cookie, store opencode.CredentialStore, listWorkspaces func([]*http.Cookie) ([]string, error), choiceReader io.Reader) (string, error) {
	workspaceIDs, err := listWorkspaces(cookies)
	if err != nil {
		return "", fmt.Errorf("cannot verify OpenCode workspaces from the imported console session: %w; sign in to the correct account in your browser and retry; the existing account was not changed", err)
	}
	if len(workspaceIDs) == 0 {
		return "", fmt.Errorf("no OpenCode workspaces were returned for the imported console session; sign in to an account with an available workspace and retry; the existing account was not changed")
	}

	workspaceID := ""
	if len(workspaceIDs) == 1 {
		workspaceID = workspaceIDs[0]
	} else {
		fmt.Println("Choose an OpenCode workspace:")
		for i, id := range workspaceIDs {
			fmt.Printf("  %d) %s\n", i+1, id)
		}
		fmt.Print("Workspace number: ")
		if choiceReader == nil {
			return "", fmt.Errorf("workspace selection input is unavailable; the existing account was not changed")
		}
		choice, readErr := bufio.NewReader(choiceReader).ReadString('\n')
		if readErr != nil {
			return "", fmt.Errorf("workspace selection was not completed; the existing account was not changed: %w", readErr)
		}
		index, parseErr := strconv.Atoi(strings.TrimSpace(choice))
		if parseErr != nil || index < 1 || index > len(workspaceIDs) {
			return "", fmt.Errorf("invalid workspace selection; enter a number from 1 to %d; the existing account was not changed", len(workspaceIDs))
		}
		workspaceID = workspaceIDs[index-1]
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if !validSelectedWorkspaceID(workspaceID) {
		return "", fmt.Errorf("OpenCode returned an invalid workspace ID; sign in to the correct account and retry; the existing account was not changed")
	}
	if err := completeOpenCodeLoginWithStore(cookies, workspaceID, store); err != nil {
		return "", err
	}
	return workspaceID, nil
}

func validSelectedWorkspaceID(id string) bool {
	for _, prefix := range []string{"wrk_", "org_"} {
		if strings.HasPrefix(id, prefix) {
			return len(id) > len(prefix)
		}
	}
	return false
}

func completeOpenCodeLoginWithStore(cookies []*http.Cookie, workspaceID string, store opencode.CredentialStore) error {
	return completeOpenCodeLoginWithStoreAndUpdate(cookies, workspaceID, store, func(cfg *config.Config, name string, raw json.RawMessage) error {
		return cfg.UpdateProvider(name, raw)
	})
}

func completeOpenCodeLoginWithStoreAndUpdate(cookies []*http.Cookie, workspaceID string, store opencode.CredentialStore, updateProvider func(*config.Config, string, json.RawMessage) error) error {
	id, err := opencode.SaveCredential(store, workspaceID, cookies)
	if err != nil {
		return fmt.Errorf("cannot save OpenCode credentials to the system keyring (check that a keyring is available and unlocked): %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	return config.WithCredentialLock(func() error {
		// Always re-read inside the cross-process lock so a concurrent login's
		// selector is the one whose keyring entry may eventually be retired.
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("cannot load config: %w", err)
		}
		previousWorkspace, previousID := "", ""
		if previousRaw, ok := cfg.Providers["opencode"]; ok {
			var previous struct {
				Workspace    string `json:"workspace"`
				CredentialID string `json:"credentialId"`
			}
			if json.Unmarshal(previousRaw, &previous) == nil {
				previousWorkspace = previous.Workspace
				previousID = previous.CredentialID
			}
		}
		raw, err := json.Marshal(struct {
			Workspace    string `json:"workspace"`
			CredentialID string `json:"credentialId"`
		}{Workspace: workspaceID, CredentialID: id})
		if err != nil {
			return err
		}

		updateErr := updateProvider(cfg, "opencode", raw)
		active, loadErr := config.Load()
		verified := false
		if loadErr == nil {
			var selected struct {
				Workspace    string `json:"workspace"`
				CredentialID string `json:"credentialId"`
			}
			if activeRaw, ok := active.Providers["opencode"]; ok && json.Unmarshal(activeRaw, &selected) == nil && selected.Workspace == workspaceID && selected.CredentialID == id {
				if _, credentialErr := opencode.LoadCredential(store, id, workspaceID); credentialErr == nil {
					verified = true
				}
			}
		}
		if !verified {
			if updateErr != nil {
				return fmt.Errorf("cannot save OpenCode credential selector: %w", updateErr)
			}
			if loadErr != nil {
				return fmt.Errorf("cannot verify saved OpenCode credential selector: %w", loadErr)
			}
			return fmt.Errorf("saved OpenCode credential selector or keyring record could not be verified")
		}
		if updateErr != nil {
			fmt.Fprintf(os.Stderr, "warning: config save reported an error, but the new OpenCode selector and credential were verified; continuing cleanup: %v\n", updateErr)
		}

		cookieFile := filepath.Join(home, ".config", "opentracker", "opencode-cookies.txt")
		if err := os.Remove(cookieFile); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "warning: cannot remove legacy plaintext OpenCode cookies: %v\n", err)
		}

		// The legacy workspace file is no longer read. Clear both plan caches once
		// the new keyring record and selector have both been confirmed.
		legacy := filepath.Join(home, ".config", "opentracker", "opencode-workspace.txt")
		if err := os.Remove(legacy); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "warning: cannot remove legacy workspace cache: %v\n", err)
		}
		usageCache := cache.New(filepath.Join(home, ".cache", "opentracker"))
		for _, name := range []string{"opencode-go", "opencode-zen"} {
			keys := []string{name}
			if previousWorkspace != "" && previousID != "" {
				keys = append(keys, app.OpenCodeCacheKey(name, previousWorkspace, previousID))
			}
			for _, key := range keys {
				if err := usageCache.Invalidate(key); err != nil {
					fmt.Fprintf(os.Stderr, "warning: cannot clear %s cache; use fetch --force: %v\n", name, err)
				}
			}
		}
		if previousID != "" && previousID != id {
			if err := store.Delete(previousID); err != nil {
				fmt.Fprintf(os.Stderr, "warning: OpenCode account switched, but old keyring credential could not be removed: %v\n", err)
			}
		}
		return nil
	})
}

func loginCodex() error {
	cmd := exec.Command("codex", "login")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("codex login failed: %w", err)
	}
	fmt.Println("Codex login complete. You can now run: opentracker fetch codex")
	return nil
}

func init() {
	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "show detailed browser scanning output")
}
