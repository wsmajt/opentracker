package cmd

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

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
		fmt.Println("After logging in, export your cookies (Netscape format) to:")
		fmt.Printf("  ~/.config/opentracker/%s-cookies.txt\n", provider)
		fmt.Println()
		fmt.Println("You can use browser extensions like 'Export Cookies' for Firefox/Chrome.")

		return nil
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
}
