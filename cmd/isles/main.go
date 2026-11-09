package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Mythicove/mythic-isles/internal/isles"
)

func main() {
	app, err := isles.Open(os.Getenv("ISLES_DATA_DIR"), os.Getenv("ISLES_ADMIN_TOKEN"), os.Getenv("ISLES_SECRET_KEY"))
	if err != nil {
		slog.Error("initialize", "error", err)
		os.Exit(1)
	}
	addr := os.Getenv("ISLES_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:8095"
	}
	srv := &http.Server{Addr: addr, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	slog.Info("listening", "address", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("serve", "error", err)
		os.Exit(1)
	}
}
