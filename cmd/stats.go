package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"opentracker/internal/cache"
	"opentracker/internal/localstats"
)

var statsForce bool

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show local usage statistics",
}

var statsOpenCodeCmd = &cobra.Command{
	Use:   "opencode",
	Short: "Show today's local OpenCode token and session totals",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot get home directory: %w", err)
		}
		client := localstats.Client{
			Cache:    cache.New(filepath.Join(home, ".cache", "opentracker")),
			Identity: localIdentity(home),
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
		defer cancel()
		result, err := client.Today(ctx, statsForce)
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	},
}

func localIdentity(home string) string {
	return home + "\x00" + os.Getenv("XDG_DATA_HOME") + "\x00" + os.Getenv("OPENCODE_DATA_DIR") + "\x00" + os.Getenv("OPENCODE_CONFIG_DIR")
}

func init() {
	statsOpenCodeCmd.Flags().BoolVar(&statsForce, "force", false, "skip cache and run OpenCode stats")
	statsCmd.AddCommand(statsOpenCodeCmd)
	rootCmd.AddCommand(statsCmd)
}
