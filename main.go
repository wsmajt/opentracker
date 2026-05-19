package main

import (
	"opentracker/cmd"

	// Register providers.
	_ "opentracker/internal/provider/codex"
	_ "opentracker/internal/provider/opencode"
)

var version = "dev"

func main() {
	cmd.SetVersion(version)
	cmd.SetupProviderHelp()
	cmd.Execute()
}
