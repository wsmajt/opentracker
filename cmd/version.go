package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	Run: func(cmd *cobra.Command, args []string) {
		if version == "" {
			version = "dev"
		}
		fmt.Println("opentracker", version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
