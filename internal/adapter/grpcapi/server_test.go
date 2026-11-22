package grpcapi_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/grpcapi"
	balancev1 "github.com/JohnnyMatteus/payments-events-go/internal/adapter/grpcapi/balance/v1"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/httpapi"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
)

type stubReader struct{ calls int }

func (s *stubReader) PaymentBalance(_ context.Context, id string) (app.PaymentBalance, error) {
	s.calls++
	if id == "pay_1" {
		return app.PaymentBalance{PaymentID: "pay_1", MerchantID: "m1", Currency: "BRL", CapturedCents: 15000,
			RefundedCents: 2500, LedgerNetCents: 12500, UpdatedAt: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}, nil
	}
	return app.PaymentBalance{}, app.ErrNotFound
}

func (s *stubReader) MerchantBalances(context.Context) ([]app.MerchantBalance, error) {
	s.calls++
	return []app.MerchantBalance{{MerchantID: "m1", Currency: "BRL", Payments: 1, CapturedCents: 15000}}, nil
}

func (s *stubReader) PaymentBalances(_ context.Context, _, _ string, _ int) (app.Page[app.PaymentBalance], error) {
	s.calls++
	return app.Page[app.PaymentBalance]{Items: []app.PaymentBalance{{PaymentID: "pay_1"}}, NextCursor: "pay_1"}, nil
}

func start(t *testing.T) (balancev1.BalanceServiceClient, healthpb.HealthClient, *stubReader) {
	t.Helper()
	auth, err := httpapi.NewOperatorAuth([]string{"ana:operador:token-da-ana", "carla:leitor:token-da-carla"})
	if err != nil {
		t.Fatal(err)
	}
	reader := &stubReader{}
	srv := grpcapi.New(auth, app.NewBalanceService(reader), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.DialContext(context.Background()) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return balancev1.NewBalanceServiceClient(conn), healthpb.NewHealthClient(conn), reader
}

func withToken(token string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token)
}

// Guardrail pelo ponto de entrada: sem credencial válida o saldo nunca é lido.
func TestEveryCallRequiresAValidToken(t *testing.T) {
	client, _, reader := start(t)
	calls := map[string]func(ctx context.Context) error{
		"GetPaymentBalance": func(ctx context.Context) error {
			_, err := client.GetPaymentBalance(ctx, &balancev1.GetPaymentBalanceRequest{PaymentId: "pay_1"})
			return err
		},
		"ListMerchantBalances": func(ctx context.Context) error {
			_, err := client.ListMerchantBalances(ctx, &balancev1.ListMerchantBalancesRequest{})
			return err
		},
		"ListPaymentBalances": func(ctx context.Context) error {
			_, err := client.ListPaymentBalances(ctx, &balancev1.ListPaymentBalancesRequest{})
			return err
		},
	}
	contexts := map[string]context.Context{
		"sem metadata":   context.Background(),
		"token errado":   withToken("token-que-nao-existe"),
		"esquema errado": metadata.AppendToOutgoingContext(context.Background(), "authorization", "Basic abc"),
		"token vazio":    withToken(""),
	}
	for method, call := range calls {
		for name, ctx := range contexts {
			if got := status.Code(call(ctx)); got != codes.Unauthenticated {
				t.Errorf("%s (%s) = %v, want Unauthenticated", method, name, got)
			}
		}
	}
	if reader.calls != 0 {
		t.Fatalf("o leitor foi chamado %d vezes sem autenticação", reader.calls)
	}
}

func TestReaderAndOperatorCanQuery(t *testing.T) {
	client, _, _ := start(t)
	for _, token := range []string{"token-da-carla", "token-da-ana"} {
		got, err := client.GetPaymentBalance(withToken(token), &balancev1.GetPaymentBalanceRequest{PaymentId: "pay_1"})
		if err != nil || got.GetCapturedCents() != 15000 || got.GetRefundedCents() != 2500 || got.GetLedgerNetCents() != 12500 ||
			got.GetUpdatedAt() != "2026-11-20T12:00:00Z" {
			t.Fatalf("GetPaymentBalance(%s) = %v, %v", token, got, err)
		}
	}
	m, err := client.ListMerchantBalances(withToken("token-da-carla"), &balancev1.ListMerchantBalancesRequest{})
	if err != nil || len(m.GetItems()) != 1 || m.GetItems()[0].GetCapturedCents() != 15000 {
		t.Fatalf("ListMerchantBalances = %v, %v", m, err)
	}
	p, err := client.ListPaymentBalances(withToken("token-da-carla"), &balancev1.ListPaymentBalancesRequest{PageSize: 1})
	if err != nil || len(p.GetItems()) != 1 || p.GetNextPageToken() != "pay_1" {
		t.Fatalf("ListPaymentBalances = %v, %v", p, err)
	}
}

func TestErrorsMapToGRPCCodes(t *testing.T) {
	client, _, _ := start(t)
	if _, err := client.GetPaymentBalance(withToken("token-da-carla"), &balancev1.GetPaymentBalanceRequest{PaymentId: "nope"}); status.Code(err) != codes.NotFound {
		t.Errorf("unknown payment = %v, want NotFound", err)
	}
	if _, err := client.GetPaymentBalance(withToken("token-da-carla"), &balancev1.GetPaymentBalanceRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty payment id = %v, want InvalidArgument", err)
	}
	if _, err := client.ListPaymentBalances(withToken("token-da-carla"), &balancev1.ListPaymentBalancesRequest{PageSize: -1}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("negative page size = %v, want InvalidArgument", err)
	}
}

func TestHealthCheckNeedsNoToken(t *testing.T) {
	_, health, _ := start(t)
	resp, err := health.Check(context.Background(), &healthpb.HealthCheckRequest{})
	if err != nil || resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health = %v, %v", resp, err)
	}
}
