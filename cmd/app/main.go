// Command app is the whole service: `app serve` (the default) and `app migrate`.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/oldsj/rom-request/internal/config"
	"github.com/oldsj/rom-request/internal/db"
	"github.com/oldsj/rom-request/internal/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		slog.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	switch cmd {
	case "migrate":
		return db.Migrate(ctx, pool, log)
	case "serve":
		if cfg.MigrateOnStart {
			if err := db.Migrate(ctx, pool, log); err != nil {
				return err
			}
		}
		srv, err := server.New(cfg, log, pool)
		if err != nil {
			return err
		}
		h, err := srv.Handler()
		if err != nil {
			return err
		}
		go srv.SweepSessions(ctx)
		if w := srv.Worker(); w != nil {
			go w.Run(ctx, cfg.WorkerInterval)
		} else {
			log.Info("downloads not configured: no request worker")
		}
		return server.Serve(ctx, log, cfg.ListenAddr, h, srv.Drain)
	default:
		return fmt.Errorf("unknown command %q (want serve or migrate)", cmd)
	}
}
