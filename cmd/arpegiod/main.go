// Command arpegiod is the Arpegio music daemon.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"arpegio/internal/app"
	"arpegio/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "arpegiod:", err)
		os.Exit(1)
	}
}

func run() error {
	paths, err := app.DefaultPaths()
	if err != nil {
		return err
	}
	cfgPath := flag.String("config", paths.Config, "path to config.toml")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	log, closeLog, err := app.NewLogger(os.Stderr, paths.Log)
	if err != nil {
		return err
	}
	defer closeLog()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx, cfg, paths, log)
	if err != nil {
		return err
	}
	defer a.Close()
	ls, err := a.Listeners()
	if err != nil {
		return err
	}
	for _, l := range ls {
		log.Info("listening", slog.String("addr", l.Addr().String()))
	}
	return a.Serve(ctx, ls...)
}
