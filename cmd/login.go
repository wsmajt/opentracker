package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

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
		_, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')

		var logger func(string)
		if verbose {
			logger = func(msg string) {
				fmt.Println(msg)
			}
		}

		cookies, source, err := browsercookies.ImportOpenCode(context.Background(), logger)
		if err != nil {
			fmt.Printf("Automatic import failed: %v\n", err)
			fmt.Println()
			fmt.Println("Please export your cookies manually (Netscape format) to:")
			fmt.Printf("  ~/.config/opentracker/%s-cookies.txt\n", provider)
			fmt.Println("You can use browser extensions like 'Export Cookies' for Firefox/Chrome.")
			return nil
		}

		// Verify the imported session before replacing the previous account's
		// cookies or workspace. The legacy workspace cache must not be consulted.
		workspaceID, err := opencode.DetectWorkspaceIDFromCookies(cookies)
		if err != nil {
			fmt.Printf("Could not auto-detect workspace: %v\n", err)
			fmt.Print("Workspace ID (or press Enter to skip): ")
			workspaceID, _ = bufio.NewReader(os.Stdin).ReadString('\n')
			workspaceID = strings.TrimSpace(workspaceID)
		}

		if workspaceID == "" {
			fmt.Println("Login skipped; existing OpenCode account was not changed.")
			return nil
		}
		if !strings.HasPrefix(workspaceID, "wrk_") || len(workspaceID) <= 4 {
			return fmt.Errorf("invalid OpenCode workspace ID %q", workspaceID)
		}
		if err := completeOpenCodeLogin(cookies, workspaceID); err != nil {
			return err
		}
		fmt.Printf("Imported %d cookies from %s; workspace saved: %s\n", len(cookies), source, workspaceID)

		return nil
	},
}

func completeOpenCodeLogin(cookies []*http.Cookie, workspaceID string) error {
	return completeOpenCodeLoginWithSave(cookies, workspaceID, browsercookies.SaveOpenCodeCookies)
}

func completeOpenCodeLoginWithSave(cookies []*http.Cookie, workspaceID string, saveCookies func([]*http.Cookie) error) error {
	// Load config before replacing cookies; a corrupt config must not leave
	// the newly imported session paired with the previous workspace.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("cannot load config: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	raw, err := json.Marshal(opencode.OpenCodeConfig{Workspace: workspaceID})
	if err != nil {
		return err
	}
	cookieFile := filepath.Join(home, ".config", "opentracker", "opencode-cookies.txt")
	if err := browsercookies.SecureOpenCodeCookieFile(cookieFile); err != nil {
		return fmt.Errorf("cannot secure OpenCode cookies: %w", err)
	}
	previous, hadPrevious := cfg.Providers["opencode"]
	if err := cfg.UpdateProvider("opencode", raw); err != nil {
		return fmt.Errorf("cannot save OpenCode workspace: %w", err)
	}
	if err := saveCookies(cookies); err != nil {
		if hadPrevious {
			cfg.Providers["opencode"] = previous
		} else {
			delete(cfg.Providers, "opencode")
		}
		if rollbackErr := cfg.Save(); rollbackErr != nil {
			return fmt.Errorf("failed to save cookies: %w (workspace rollback failed: %v)", err, rollbackErr)
		}
		return fmt.Errorf("failed to save cookies; previous workspace restored: %w", err)
	}

	// The legacy workspace file is no longer read; remove it so it cannot be
	// mistaken for the current account. Clear both plan caches immediately.
	legacy := filepath.Join(home, ".config", "opentracker", "opencode-workspace.txt")
	if err := os.Remove(legacy); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warning: cannot remove legacy workspace cache: %v\n", err)
	}
	usageCache := cache.New(filepath.Join(home, ".cache", "opentracker"))
	for _, name := range []string{"opencode-go", "opencode-zen"} {
		if err := usageCache.Invalidate(name); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cannot clear %s cache; use fetch --force: %v\n", name, err)
		}
	}
	return nil
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
