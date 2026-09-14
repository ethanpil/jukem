// Command jukem is the jukebox appliance service.
//
// Subcommands: serve, healthcheck, reset-password, version.
package main

import (
	"fmt"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usageText = `usage: jukem <command> [options]

commands:
  serve           run the service (--config <file>)
  healthcheck     exit 0 when the local service answers /healthz with 200
  reset-password  set a new login password from the console
  version         print the version
`

func usage() {
	fmt.Fprint(os.Stderr, usageText)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = runServe(os.Args[2:])
	case "healthcheck":
		err = runHealthcheck(os.Args[2:])
	case "reset-password":
		err = runResetPassword(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println("jukem " + version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "jukem: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "jukem:", err)
		os.Exit(1)
	}
}
