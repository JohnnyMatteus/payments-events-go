// Comando payments reúne os processos do serviço num único binário:
//
//	payments migrate     aplica as migrações do banco
//	payments api         recebe webhooks do provedor
//	payments relay       publica o outbox no RabbitMQ
//	payments consumer    consumidor de exemplo (projeção de saldos)
//	payments reconcile   concilia o extrato CSV com o ledger
//	payments healthcheck verifica um endpoint HTTP (healthcheck do contêiner)
//
// A configuração vem de variáveis de ambiente (ver .env.example).
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/config"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/observability"
)

// version é preenchida no build com -ldflags "-X main.version=...".
var version = "dev"

// Códigos de saída.
const (
	exitOK         = 0
	exitError      = 1
	exitUsage      = 2
	exitDivergence = 3
)

const usage = `uso: payments <comando> [flags]

comandos:
  migrate     aplica as migrações do banco
  api         recebe webhooks do provedor (HTTP_ADDR)
  relay       publica o outbox no RabbitMQ
  consumer    consumidor de exemplo: projeção de saldos por pagamento
  reconcile   concilia o extrato CSV com o ledger (payments reconcile -h)
  healthcheck <url>  sai com 0 se a URL responder 2xx (imagem sem shell nem curl)
  version     mostra a versão
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run executa um subcomando e devolve o código de saída. O contexto é
// cancelado por SIGINT/SIGTERM, o que dispara o desligamento gracioso.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	command := args[0]
	switch command {
	case "version":
		fmt.Fprintln(stdout, version)
		return exitOK
	case "healthcheck":
		return runHealthcheck(ctx, args[1:], stderr)
	}

	cfg, err := config.Load(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "configuração inválida: %v\n", err)
		return exitUsage
	}
	// O relatório da conciliação vai para stdout; os logs, para stderr.
	logOut := stdout
	if command == "reconcile" {
		logOut = stderr
	}
	logger := observability.NewLogger(logOut, cfg.LogLevel, "payments-"+command)

	switch command {
	case "migrate":
		err = runMigrate(ctx, cfg, logger)
	case "api":
		err = runAPI(ctx, cfg, logger)
	case "relay":
		err = runRelay(ctx, cfg, logger)
	case "consumer":
		err = runConsumer(ctx, cfg, logger)
	case "reconcile":
		return runReconcile(ctx, cfg, logger, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "comando desconhecido %q\n\n%s", command, usage)
		return exitUsage
	}
	if err != nil {
		logger.Error("command failed", slog.Any("error", err))
		return exitError
	}
	return exitOK
}

func runMigrate(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if err := cfg.Require("DATABASE_URL"); err != nil {
		return err
	}
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	applied, err := postgres.Migrate(ctx, pool)
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "migrations applied", slog.Any("versions", applied))
	return nil
}

func runHealthcheck(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "uso: payments healthcheck <url>")
		return exitUsage
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// A URL vem do healthcheck do próprio contêiner, não de entrada externa.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, args[0], nil) //nolint:gosec // ver comentário acima
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return exitUsage
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // URL local do healthcheck
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return exitError
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		fmt.Fprintf(stderr, "healthcheck: status %d\n", resp.StatusCode)
		return exitError
	}
	return exitOK
}
