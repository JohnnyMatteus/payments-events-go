package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/grpcapi"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/config"
)

// balanceGRPC monta o gRPC interno de saldo, ou nil se GRPC_ADDR está vazio.
// Sem autenticação configurada o processo não sobe: a API de saldo nunca fica
// aberta.
func balanceGRPC(cfg config.Config, pool *pgxpool.Pool, auth identityProvider, logger *slog.Logger) (*grpc.Server, error) {
	if cfg.GRPCAddr == "" {
		return nil, nil //nolint:nilnil // nil significa "gRPC desligado"
	}
	if auth == nil {
		return nil, errors.New("GRPC_ADDR exige autenticação: configure OIDC_ISSUER_URL (ou OPERATOR_TOKENS em dev)")
	}
	var creds credentials.TransportCredentials
	if cfg.GRPCTLSCertFile != "" {
		var err error
		if creds, err = credentials.NewServerTLSFromFile(cfg.GRPCTLSCertFile, cfg.GRPCTLSKeyFile); err != nil {
			return nil, fmt.Errorf("grpc tls: %w", err)
		}
	} else {
		logger.Warn("gRPC sem TLS: o token trafega em texto claro (só aceitável em dev)")
	}
	return grpcapi.New(auth, app.NewBalanceService(postgres.NewOperationsReader(pool)), creds, logger), nil
}

// serveGRPC roda o servidor até o contexto acabar e encerra com GracefulStop,
// forçando Stop se passar do prazo.
func serveGRPC(ctx context.Context, srv *grpc.Server, addr string, timeout time.Duration, logger *slog.Logger) error {
	lis, err := net.Listen("tcp", addr) //nolint:noctx // listener do servidor, sem requisição associada
	if err != nil {
		return fmt.Errorf("grpc listen %s: %w", addr, err)
	}
	errCh := make(chan error, 1)
	go func() {
		logger.InfoContext(ctx, "grpc server listening", slog.String("addr", addr))
		errCh <- srv.Serve(lis)
	}()
	select {
	case err := <-errCh:
		return fmt.Errorf("grpc server %s: %w", addr, err)
	case <-ctx.Done():
	}
	done := make(chan struct{})
	go func() { srv.GracefulStop(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		srv.Stop()
	}
	return nil
}
