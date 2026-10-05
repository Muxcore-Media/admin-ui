// Command module is the admin-ui entry point (built by core's spool resolver
// and by the Makefile/Dockerfile).
package main

import adminui "github.com/Muxcore-Media/admin-ui"

var version = "dev"

func main() { adminui.Main(version) }
