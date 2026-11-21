package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/platform/config"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/observability"
)

// newServer cria um http.Server com timeouts explícitos (proteção contra
// clientes lentos, como no ataque slowloris).
func newServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// serve roda o servidor até o contexto ser cancelado e faz shutdown gracioso.
func serve(ctx context.Context, srv *http.Server, timeout time.Duration, logger *slog.Logger) error {
	errCh := make(chan error, 1)
	go func() {
		logger.InfoContext(ctx, "http server listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
		return fmt.Errorf("http server %s: %w", srv.Addr, err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown %s: %w", srv.Addr, err)
	}
	return nil
}

// setupTelemetry inicializa a observabilidade e devolve a função de
// encerramento, que exporta spans e métricas pendentes.
func setupTelemetry(ctx context.Context, cfg config.Config, service string,
	logger *slog.Logger) (*observability.Telemetry, func(), error) {
	tel, err := observability.Setup(ctx, service, version)
	if err != nil {
		return nil, nil, err
	}
	return tel, func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
		defer cancel()
		if err := tel.Shutdown(shutdownCtx); err != nil {
			logger.Warn("telemetry shutdown failed", slog.Any("error", err))
		}
	}, nil
}
