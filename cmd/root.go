package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var (
	debug   bool
	version string
)

var rootCmd = &cobra.Command{
	Use:   "opentracker",
	Short: "CLI for tracking AI provider usage limits",
}

// SetVersion sets the application version.
func SetVersion(v string) {
	version = v
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&debug, "debug", false, "enable debug output")
}
