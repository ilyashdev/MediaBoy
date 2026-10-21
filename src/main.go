package main

import (
	"os"
	"strings"

	"MediaBoy/internal/cli"
	"MediaBoy/internal/ui"
)

func main() {
	// Arguments run the command line; macOS passes -psn_… to apps started
	// from Finder on old systems, which still means the GUI.
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-psn_") {
		os.Exit(cli.Run(os.Args[1:]))
	}
	ui.Run()
}
