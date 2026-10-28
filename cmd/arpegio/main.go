// Command arpegio is the command-line client for arpegiod.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"arpegio/internal/cli"
)

func main() {
	addr := os.Getenv("ARPEGIO_ADDR")
	if addr == "" {
		addr = "http://127.0.0.1:7600"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := cli.New(addr, os.Stdout).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "arpegio:", err)
		os.Exit(1)
	}
}
