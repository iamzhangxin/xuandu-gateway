package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/cloudwego/kitex/pkg/klog"

	"github.com/iamzhangxin/xuandu-gateway/internal/app"
	"github.com/iamzhangxin/xuandu-gateway/internal/config"
	"github.com/iamzhangxin/xuandu-gateway/internal/logging"
)

func main() { os.Exit(run()) }

func run() int {
	path := flag.String("config", "config/example.yaml", "bootstrap configuration")
	flag.Parse()
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	dir := os.Getenv("XUANDU_LOG_DIR")
	if dir == "" {
		dir = logging.DefaultDir
	}
	file, err := logging.Open(dir)
	if err != nil {
		slog.Error("cannot open log directory", "directory", dir, "error", err)
		return 1
	}
	defer file.Close()
	output := logging.Tee(os.Stdout, file, os.Stderr)
	slog.SetDefault(slog.New(slog.NewJSONHandler(output, nil)))
	hlog.SetOutput(output)
	klog.SetOutput(output)
	c, e := config.Load(*path)
	if e != nil {
		slog.Error("invalid configuration", "error", e)
		return 1
	}
	a, e := app.Initialize(c)
	if e != nil {
		slog.Error("initialization failed")
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if e = a.Run(ctx); e != nil {
		slog.Error("gateway stopped", "error", e)
		return 1
	}
	return 0
}
