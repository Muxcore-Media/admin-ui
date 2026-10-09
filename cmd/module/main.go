// Command module is the admin-ui entry point (built by core's spool resolver
// and by the Makefile/Dockerfile).
//
// `admin-ui parental-seed ...` runs the separate admin-bearer seed helper
// (internal/parentalseed, ADR-0033 §4) instead of the daemon: it is dispatched
// before any daemon configuration, enrollment or listener, so every image and
// host artifact that ships admin-ui also ships the helper at the same version.
package main

import (
	"os"

	adminui "github.com/Muxcore-Media/admin-ui"
	"github.com/Muxcore-Media/admin-ui/internal/parentalseed"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == parentalseed.Command {
		os.Exit(parentalseed.Main(os.Args[2:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
	}
	adminui.Main(version)
}
