package main

import (
	"fmt"
	"os"
)

// version is stamped by GoReleaser via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "serve":
		if err := runServe(version); err != nil {
			fmt.Fprintln(os.Stderr, "serve:", err)
			os.Exit(1)
		}
	case "stdio":
		fmt.Fprintln(os.Stderr, "stdio: not implemented yet")
		os.Exit(1)
	case "status":
		fmt.Fprintln(os.Stderr, "status: not implemented yet")
		os.Exit(1)
	case "stop":
		fmt.Fprintln(os.Stderr, "stop: not implemented yet")
		os.Exit(1)
	case "--version", "version":
		fmt.Println("whatsapp-mcp", version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: whatsapp-mcp <command>

commands:
  serve    run the WhatsApp daemon (session + local HTTP API)
  stdio    run the MCP stdio proxy (spawned by MCP clients; auto-starts serve)
  status   show daemon/connection status
  stop     stop the daemon
  version  print version`)
}
