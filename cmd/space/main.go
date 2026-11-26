//go:build darwin

// Command space shows what takes up a Mac's disk and cleans it safely.
package main

import (
	"os"

	"github.com/romus/space/internal/cli"
)

// Set by the linker: -ldflags "-X main.version=1.2.3".
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	os.Exit(cli.Execute(cli.Build{Version: version, Commit: commit, Date: date}))
}
