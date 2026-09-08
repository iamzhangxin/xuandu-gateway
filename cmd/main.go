package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/iamzhangxin/xuandu-gateway/internal/app"
	"github.com/iamzhangxin/xuandu-gateway/internal/config"
)

func main() {
	path := flag.String("config", "config/example.yaml", "bootstrap configuration")
	flag.Parse()
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	c, e := config.Load(*path)
	if e != nil {
		slog.Error("invalid configuration", "error", e)
		os.Exit(1)
	}
	a, e := app.Initialize(c)
	if e != nil {
		slog.Error("initialization failed")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if e = a.Run(ctx); e != nil {
		slog.Error("gateway stopped", "error", e)
		os.Exit(1)
	}
}
