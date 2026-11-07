package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	path := flag.String("config.path", "example.yaml", "Path to the configuration file")
	address := flag.String("web.listen-address", ":9430", "HTTP listen address")
	refresh := flag.Duration("config.refresh", time.Second, "Configuration polling interval (must be positive)")
	flag.Parse()
	if *refresh <= 0 || *path == "" || *address == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "config.refresh must be positive, config.path and web.listen-address must not be empty, and positional arguments are not supported")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := serve(ctx, *path, *address, *refresh); err != nil {
		slog.Error("Configuration service stopped", "error", err)
		os.Exit(1)
	}
}

func serve(ctx context.Context, path, address string, refresh time.Duration) error {
	service, err := newConfigService(path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		service.watch(ctx, refresh)
	}()
	server := &http.Server{
		Addr:              address,
		Handler:           service.handler(),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       time.Minute,
	}
	serverDone := make(chan error, 1)
	go func() {
		slog.Info("Serving ping_exporter configurations", "address", address, "config", path)
		serverDone <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = server.Shutdown(shutdownCtx)
		shutdownCancel()
		if err != nil {
			server.Close()
		}
		<-serverDone
	case err = <-serverDone:
		cancel()
	}
	<-watchDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
