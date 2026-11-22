// Package grpcapi expõe a consulta interna de saldo por gRPC. Toda chamada
// (exceto a verificação de saúde) exige um access token no metadata
// "authorization"; o papel é decidido pelo caso de uso (app.Authorize).
package grpcapi

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	balancev1 "github.com/JohnnyMatteus/payments-events-go/internal/adapter/grpcapi/balance/v1"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

// TokenAuthenticator identifica o chamador a partir de um token de portador.
// É a mesma porta de entrada que a API HTTP usa (OIDC ou token de dev).
type TokenAuthenticator interface {
	AuthenticateBearer(ctx context.Context, token string) (app.Actor, error)
}

// Server implementa balancev1.BalanceServiceServer.
type Server struct {
	balancev1.UnimplementedBalanceServiceServer
	svc    *app.BalanceService
	logger *slog.Logger
}

// New monta o servidor gRPC. creds nil só é aceitável em dev (texto claro).
func New(auth TokenAuthenticator, svc *app.BalanceService, creds credentials.TransportCredentials, logger *slog.Logger) *grpc.Server {
	opts := []grpc.ServerOption{grpc.UnaryInterceptor(authInterceptor(auth))}
	if creds != nil {
		opts = append(opts, grpc.Creds(creds))
	}
	srv := grpc.NewServer(opts...)
	balancev1.RegisterBalanceServiceServer(srv, &Server{svc: svc, logger: logger})
	healthpb.RegisterHealthServer(srv, health.NewServer())
	return srv
}

const healthPrefix = "/grpc.health.v1.Health/"

// authInterceptor exige o token em toda chamada, menos na verificação de saúde
// (probe do orquestrador). Falha fechado: qualquer dúvida é UNAUTHENTICATED.
func authInterceptor(auth TokenAuthenticator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if strings.HasPrefix(info.FullMethod, healthPrefix) {
			return next(ctx, req)
		}
		md, _ := metadata.FromIncomingContext(ctx)
		values := md.Get("authorization")
		if len(values) != 1 {
			return nil, status.Error(codes.Unauthenticated, "informe o token no metadata authorization")
		}
		token, ok := strings.CutPrefix(values[0], "Bearer ")
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "use authorization: Bearer <token>")
		}
		actor, err := auth.AuthenticateBearer(ctx, token)
		if err != nil {
			return nil, toStatus(err)
		}
		return next(app.WithActor(ctx, actor), req)
	}
}

func toStatus(err error) error {
	switch {
	case errors.Is(err, app.ErrUnauthenticated):
		return status.Error(codes.Unauthenticated, "token inválido")
	case errors.Is(err, app.ErrForbidden):
		return status.Error(codes.PermissionDenied, "a identidade não tem papel neste serviço")
	case errors.Is(err, app.ErrNotFound):
		return status.Error(codes.NotFound, "registro não encontrado")
	case errors.Is(err, app.ErrInvalidInput):
		return status.Error(codes.InvalidArgument, "parâmetro inválido")
	default:
		return status.Error(codes.Internal, "erro interno")
	}
}

func (s *Server) fail(ctx context.Context, method string, err error) error {
	st := toStatus(err)
	if status.Code(st) == codes.Internal {
		s.logger.ErrorContext(ctx, "grpc request failed", slog.String("method", method), slog.Any("error", err))
	}
	return st
}

// GetPaymentBalance devolve o saldo de um pagamento.
func (s *Server) GetPaymentBalance(ctx context.Context, req *balancev1.GetPaymentBalanceRequest) (*balancev1.PaymentBalance, error) {
	b, err := s.svc.PaymentBalance(ctx, req.GetPaymentId())
	if err != nil {
		return nil, s.fail(ctx, "GetPaymentBalance", err)
	}
	return paymentOf(b), nil
}

// ListMerchantBalances devolve o saldo por lojista.
func (s *Server) ListMerchantBalances(ctx context.Context, _ *balancev1.ListMerchantBalancesRequest) (*balancev1.ListMerchantBalancesResponse, error) {
	items, err := s.svc.MerchantBalances(ctx)
	if err != nil {
		return nil, s.fail(ctx, "ListMerchantBalances", err)
	}
	out := &balancev1.ListMerchantBalancesResponse{}
	for _, m := range items {
		out.Items = append(out.Items, &balancev1.MerchantBalance{
			MerchantId: m.MerchantID, Currency: m.Currency, Payments: int64(m.Payments),
			CapturedCents: m.CapturedCents, RefundedCents: m.RefundedCents, AdjustmentCents: m.AdjustmentCents,
			LedgerNetCents: m.LedgerNetCents, ProjectedNetCents: m.ProjectedNetCents,
			UnprojectedMessages: int64(m.UnprojectedMessages),
		})
	}
	return out, nil
}

// ListPaymentBalances lista os saldos por pagamento.
func (s *Server) ListPaymentBalances(ctx context.Context, req *balancev1.ListPaymentBalancesRequest) (*balancev1.ListPaymentBalancesResponse, error) {
	page, err := s.svc.PaymentBalances(ctx, req.GetMerchantId(), req.GetPageToken(), int(req.GetPageSize()))
	if err != nil {
		return nil, s.fail(ctx, "ListPaymentBalances", err)
	}
	out := &balancev1.ListPaymentBalancesResponse{NextPageToken: page.NextCursor}
	for _, b := range page.Items {
		out.Items = append(out.Items, paymentOf(b))
	}
	return out, nil
}

func paymentOf(b app.PaymentBalance) *balancev1.PaymentBalance {
	return &balancev1.PaymentBalance{
		PaymentId: b.PaymentID, MerchantId: b.MerchantID, Currency: b.Currency,
		CapturedCents: b.CapturedCents, RefundedCents: b.RefundedCents, LedgerNetCents: b.LedgerNetCents,
		UpdatedAt: b.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}
