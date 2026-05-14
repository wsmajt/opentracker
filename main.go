package main

import (
	"opentracker/cmd"

	// Register opencode providers.
	_ "opentracker/internal/provider/opencode"
)

var version = "dev"

func main() {
	cmd.SetVersion(version)
	cmd.Execute()
}
