// Command gw is the infrastructure-facing Gateway. On startup it launches every
// configured target via the Runtime and begins readiness polling; it serves the
// target list and per-target attach WebSocket; on SIGINT/SIGTERM it stops the
// whole fleet.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/abyss0-dev/web-terminal/internal/gw"
	"github.com/abyss0-dev/web-terminal/internal/runtime"
)

func main() {
	configPath := flag.String("config", "config.json", "path to GW configuration")
	addr := flag.String("addr", ":8081", "GW listen address")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := runtime.LoadConfig(*configPath)
	if err != nil {
		logger.Error("load config", slog.String("error", err.Error()))
		os.Exit(1)
	}

	opts := runtime.Options{}
	if !runtime.KVMAvailable() {
		// TCG emulation boots far slower than KVM, so the default 60s readiness
		// budget is too tight; extend it and make the cause visible.
		opts.ReadyTimeout = 5 * time.Minute
		logger.Warn("KVM unavailable: using slow TCG emulation; extending readiness timeout to 5m",
			slog.String("hint", "add user to the kvm group or grant /dev/kvm access for fast boots"))
	}

	rt := runtime.NewQEMU(cfg, opts)
	logger.Info("launching fleet", slog.Int("targets", len(cfg.Targets)), slog.Bool("kvm", runtime.KVMAvailable()))
	if err := rt.EnsureStarted(); err != nil {
		logger.Error("ensure started", slog.String("error", err.Error()))
		os.Exit(1)
	}

	srv := &http.Server{Addr: *addr, Handler: gw.NewServer(rt).Handler()}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("gateway listening", slog.String("addr", *addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("serve", slog.String("error", err.Error()))
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown", slog.String("error", err.Error()))
	}
	if err := rt.Shutdown(); err != nil {
		logger.Error("runtime shutdown", slog.String("error", err.Error()))
	}
	logger.Info("fleet stopped")
}
