//go:build integration

// Package testinfra sobe dependências reais (PostgreSQL e RabbitMQ) com
// testcontainers-go para os testes de integração. Só compila com a build tag
// "integration"; sem Docker disponível, os testes são pulados.
package testinfra

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcrabbitmq "github.com/testcontainers/testcontainers-go/modules/rabbitmq"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
)

// Imagens fixadas para testes reprodutíveis; as mesmas do docker-compose.
const (
	PostgresImage = "postgres:17.6-alpine"
	RabbitMQImage = "rabbitmq:4.1.4-management-alpine"
)

// Run executa os testes do pacote, ou os pula com código 0 quando não há
// Docker. Use em TestMain.
func Run(m *testing.M, setup func(ctx context.Context) (teardown func(), err error)) int {
	if !dockerAvailable() {
		fmt.Println("testinfra: Docker indisponível, testes de integração pulados")
		return 0
	}
	teardown, err := setup(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "testinfra: setup failed: %v\n", err)
		return 1
	}
	defer teardown()
	return m.Run()
}

func dockerAvailable() (ok bool) {
	defer func() {
		// O provider do testcontainers entra em pânico quando não acha Docker.
		if recover() != nil {
			ok = false
		}
	}()
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return false
	}
	defer provider.Close()
	return provider.Health(context.Background()) == nil
}

// StartPostgres sobe um PostgreSQL, aplica as migrações e devolve o pool.
func StartPostgres(ctx context.Context) (*pgxpool.Pool, func(), error) {
	ctr, err := tcpostgres.Run(ctx, PostgresImage,
		tcpostgres.WithDatabase("payments"),
		tcpostgres.WithUsername("payments"),
		tcpostgres.WithPassword("dev-only-change-me"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start postgres: %w", err)
	}
	terminate := func() { _ = testcontainers.TerminateContainer(ctr) }
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		terminate()
		return nil, nil, err
	}
	pool, err := postgres.Connect(ctx, dsn)
	if err != nil {
		terminate()
		return nil, nil, err
	}
	if _, err := postgres.Migrate(ctx, pool); err != nil {
		pool.Close()
		terminate()
		return nil, nil, err
	}
	return pool, func() { pool.Close(); terminate() }, nil
}

// StartRabbitMQ sobe um RabbitMQ e devolve a URL AMQP.
func StartRabbitMQ(ctx context.Context) (string, func(), error) {
	ctr, err := tcrabbitmq.Run(ctx, RabbitMQImage)
	if err != nil {
		return "", nil, fmt.Errorf("start rabbitmq: %w", err)
	}
	terminate := func() { _ = testcontainers.TerminateContainer(ctr) }
	url, err := ctr.AmqpURL(ctx)
	if err != nil {
		terminate()
		return "", nil, err
	}
	return url, terminate, nil
}

// ResetDatabase apaga os dados entre testes, preservando o schema.
func ResetDatabase(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `TRUNCATE processed_events, ledger_entries, outbox_messages,
		consumed_messages, payment_balances RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("reset database: %v", err)
	}
}
