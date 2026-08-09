package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JonasBorgesLM/task-api/config"
	"github.com/JonasBorgesLM/task-api/task"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

// run wires dependencies, starts the HTTP server, and handles graceful shutdown.
// It returns a non-nil error only for unexpected failures; a clean shutdown returns nil.
func run(logger *slog.Logger) error {
	// Load configuration from environment variables.
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Dependency wiring: Repository → Service → Handler.
	repo := task.NewMemoryRepository()
	svc := task.NewService(repo)
	handler := task.NewHandler(svc, logger)

	// Route registration.
	mux := http.NewServeMux()
	registerHealthRoute(mux, logger)
	handler.RegisterRoutes(mux)

	// HTTP server with explicit timeouts.
	srv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// serverErr receives the result of ListenAndServe from its goroutine.
	serverErr := make(chan error, 1)

	go func() {
		logger.Info("server started", "addr", cfg.Addr())
		serverErr <- srv.ListenAndServe()
	}()

	// Block until a termination signal or a server error is received.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	select {
	case err := <-serverErr:
		// ListenAndServe returned before any signal — unexpected startup failure.
		return err

	case sig := <-quit:
		logger.Info("shutdown initiated", "signal", sig.String())
	}

	// Give in-flight requests time to complete.
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", "error", err)
		return err
	}

	// Drain the serverErr channel: after Shutdown returns, ListenAndServe
	// will return http.ErrServerClosed, which is the expected outcome.
	if err := <-serverErr; !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	logger.Info("server stopped cleanly")
	return nil
}
