package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"

	"opentracker/internal/browsercookies"
)

var verbose bool

var loginCmd = &cobra.Command{
	Use:   "login <provider>",
	Short: "Open the login page for a provider",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := args[0]

		var url string
		switch provider {
		case "opencode":
			url = "https://opencode.ai/go"
		default:
			return fmt.Errorf("unknown provider: %s", provider)
		}

		fmt.Printf("Open this URL to log in: %s\n", url)

		switch runtime.GOOS {
		case "linux":
			exec.Command("xdg-open", url).Start()
		case "darwin":
			exec.Command("open", url).Start()
		case "windows":
			exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
		}

		fmt.Println()
		fmt.Println("After logging in, press Enter to automatically import cookies...")
		bufio.NewReader(os.Stdin).ReadBytes('\n')

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

		if err := browsercookies.SaveOpenCodeCookies(cookies); err != nil {
			return fmt.Errorf("failed to save cookies: %w", err)
		}

		fmt.Printf("Successfully imported %d cookies from %s\n", len(cookies), source)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "show detailed browser scanning output")
}
