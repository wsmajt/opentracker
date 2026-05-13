package main

import (
	"opentracker/cmd"

	// Register opencode providers.
	_ "opentracker/internal/provider/opencodego"
)

var version = "dev"

func main() {
	cmd.SetVersion(version)
	cmd.Execute()
}
