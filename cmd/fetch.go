package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"opentracker/internal/app"
)

var (
	force bool
)

var fetchCmd = &cobra.Command{
	Use:   "fetch [provider]",
	Short: "Fetch usage data from a provider",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := "all"
		if len(args) > 0 {
			provider = args[0]
		}

		application, err := app.New()
		if err != nil {
			return fmt.Errorf("cannot initialize app: %w", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		return application.Fetch(ctx, provider, force)
	},
}

func init() {
	fetchCmd.Flags().BoolVar(&force, "force", false, "skip cache and force fresh fetch")
	rootCmd.AddCommand(fetchCmd)
}
