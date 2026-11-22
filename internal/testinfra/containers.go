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
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tclocalstack "github.com/testcontainers/testcontainers-go/modules/localstack"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcrabbitmq "github.com/testcontainers/testcontainers-go/modules/rabbitmq"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
)

// Imagens fixadas para testes reprodutíveis; as mesmas do docker-compose.
const (
	PostgresImage   = "postgres:17.6-alpine"
	RabbitMQImage   = "rabbitmq:4.1.4-management-alpine"
	LocalStackImage = "localstack/localstack:4.5.0"
	RedisImage      = "redis:7.4.6-alpine"
)

// startupTimeout é generoso porque o Docker local costuma estar dividido com
// outros ambientes; no CI os contêineres sobem em segundos.
const startupTimeout = 3 * time.Minute

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

// Postgres é um banco de teste já migrado.
type Postgres struct {
	Pool *pgxpool.Pool
	DSN  string
}

// StartPostgres sobe um PostgreSQL, aplica as migrações e devolve pool e DSN.
func StartPostgres(ctx context.Context) (*Postgres, func(), error) {
	ctr, err := tcpostgres.Run(ctx, PostgresImage,
		tcpostgres.WithDatabase("payments"),
		tcpostgres.WithUsername("payments"),
		tcpostgres.WithPassword("dev-only-change-me"),
		testcontainers.WithWaitStrategyAndDeadline(startupTimeout,
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(startupTimeout),
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(startupTimeout),
		),
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
	return &Postgres{Pool: pool, DSN: dsn}, func() { pool.Close(); terminate() }, nil
}

// StartRabbitMQ sobe um RabbitMQ e devolve a URL AMQP.
func StartRabbitMQ(ctx context.Context) (string, func(), error) {
	ctr, err := tcrabbitmq.Run(ctx, RabbitMQImage,
		testcontainers.WithWaitStrategyAndDeadline(startupTimeout,
			wait.ForLog(".*Server startup complete.*").AsRegexp().WithStartupTimeout(startupTimeout),
		),
	)
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

// StartLocalStack sobe o LocalStack com S3 e SQS e devolve o endpoint HTTP.
// As credenciais de teste ("test"/"test") são exportadas para o SDK da AWS.
func StartLocalStack(ctx context.Context) (string, func(), error) {
	ctr, err := tclocalstack.Run(ctx, LocalStackImage,
		testcontainers.WithEnv(map[string]string{"SERVICES": "s3,sqs"}),
		testcontainers.WithWaitStrategyAndDeadline(startupTimeout,
			wait.ForHTTP("/_localstack/health").WithPort("4566/tcp").WithStartupTimeout(startupTimeout),
		),
	)
	if err != nil {
		return "", nil, fmt.Errorf("start localstack: %w", err)
	}
	terminate := func() { _ = testcontainers.TerminateContainer(ctr) }
	host, err := ctr.Host(ctx)
	if err != nil {
		terminate()
		return "", nil, err
	}
	port, err := ctr.MappedPort(ctx, "4566/tcp")
	if err != nil {
		terminate()
		return "", nil, err
	}
	for k, v := range map[string]string{"AWS_ACCESS_KEY_ID": "test", "AWS_SECRET_ACCESS_KEY": "test", "AWS_REGION": "us-east-1"} {
		if err := os.Setenv(k, v); err != nil {
			terminate()
			return "", nil, err
		}
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port()), terminate, nil
}

// ResetDatabase apaga os dados entre testes, preservando o schema.
func ResetDatabase(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `TRUNCATE processed_events, ledger_entries, outbox_messages,
		consumed_messages, payment_balances, dead_letters, reconciliation_runs, reconciliation_items,
		divergence_cases, ledger_adjustments, statement_object_claims RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("reset database: %v", err)
	}
}
