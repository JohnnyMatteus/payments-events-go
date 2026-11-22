package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/csvstatement"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/objectstore"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/postgres"
	"github.com/JohnnyMatteus/payments-events-go/internal/adapter/reportout"
	"github.com/JohnnyMatteus/payments-events-go/internal/app"
	"github.com/JohnnyMatteus/payments-events-go/internal/domain"
	"github.com/JohnnyMatteus/payments-events-go/internal/platform/config"
)

type reconcileOptions struct {
	statement        string
	period           domain.Period
	format           string
	failOnDivergence bool
	daily            bool
}

// parseReconcileFlags lê as flags; now define o "ontem" do modo -daily.
func parseReconcileFlags(args []string, stderr io.Writer, now time.Time) (reconcileOptions, error) {
	fs := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opts reconcileOptions
	var from, to string
	fs.StringVar(&opts.statement, "statement", "", "extrato CSV do provedor: s3://bucket/chave ou caminho local (obrigatório sem -daily)")
	fs.StringVar(&from, "from", "", "primeiro dia do período, AAAA-MM-DD em UTC (obrigatório)")
	fs.StringVar(&to, "to", "", "último dia do período, inclusivo (padrão: igual a -from)")
	fs.StringVar(&opts.format, "format", "text", "formato do relatório: text ou json")
	fs.BoolVar(&opts.failOnDivergence, "fail-on-divergence", false, "sai com código 3 se houver divergência")
	fs.BoolVar(&opts.daily, "daily", false, "concilia o dia anterior (UTC) com o extrato do bucket e grava a execução; para o cron")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if opts.daily {
		if opts.statement != "" || from != "" || to != "" {
			return opts, errors.New("-daily não combina com -statement, -from e -to")
		}
		yesterday := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
		opts.period = domain.Period{From: yesterday, To: yesterday.AddDate(0, 0, 1)}
		return opts, nil
	}
	if opts.statement == "" || from == "" {
		return opts, errors.New("-statement e -from são obrigatórios")
	}
	if to == "" {
		to = from
	}
	start, err := time.Parse(time.DateOnly, from)
	if err != nil {
		return opts, fmt.Errorf("-from inválido: %w", err)
	}
	end, err := time.Parse(time.DateOnly, to)
	if err != nil {
		return opts, fmt.Errorf("-to inválido: %w", err)
	}
	if end.Before(start) {
		return opts, errors.New("-to não pode ser anterior a -from")
	}
	if opts.format != "text" && opts.format != "json" {
		return opts, fmt.Errorf("-format deve ser text ou json, recebido %q", opts.format)
	}
	opts.period = domain.Period{From: start, To: end.AddDate(0, 0, 1)}
	return opts, nil
}

func runReconcile(ctx context.Context, cfg config.Config, logger *slog.Logger,
	args []string, stdout, stderr io.Writer) int {
	opts, err := parseReconcileFlags(args, stderr, time.Now())
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		fmt.Fprintf(stderr, "reconcile: %v\n", err)
		return exitUsage
	}
	if err := cfg.Require("DATABASE_URL"); err != nil {
		fmt.Fprintf(stderr, "reconcile: %v\n", err)
		return exitUsage
	}

	if opts.daily {
		return runDaily(ctx, cfg, logger, opts, stdout, stderr)
	}
	report, err := reconcile(ctx, cfg, logger, opts)
	if err != nil {
		logger.ErrorContext(ctx, "reconciliation failed", slog.Any("error", err))
		return exitError
	}

	write := reportout.WriteText
	if opts.format == "json" {
		write = reportout.WriteJSON
	}
	if err := write(stdout, report); err != nil {
		logger.ErrorContext(ctx, "write report failed", slog.Any("error", err))
		return exitError
	}

	counts := report.CountByKind()
	logger.InfoContext(ctx, "reconciliation finished",
		slog.Int("matched", report.Matched),
		slog.Int("missing_in_ledger", counts[domain.MissingInLedger]),
		slog.Int("missing_in_statement", counts[domain.MissingInStatement]),
		slog.Int("amount_mismatch", counts[domain.AmountMismatch]),
		slog.Int("duplicate_in_statement", counts[domain.DuplicateInStatement]))
	if opts.failOnDivergence && report.HasDivergences() {
		return exitDivergence
	}
	return exitOK
}

// runDaily concilia o extrato do dia anterior pelo mesmo executor do painel e
// do S3, então a execução aparece no painel com a origem "cli".
func runDaily(ctx context.Context, cfg config.Config, logger *slog.Logger, opts reconcileOptions,
	stdout, stderr io.Writer) int {
	opener, err := objectstore.New(ctx, objectstore.S3Config{
		Endpoint: cfg.S3Endpoint, Region: cfg.AWSRegion, UsePathStyle: cfg.S3UsePathStyle,
	})
	if err != nil {
		logger.ErrorContext(ctx, "daily reconciliation failed", slog.Any("error", err))
		return exitError
	}
	pool, err := openPool(ctx, cfg, logger)
	if err != nil {
		logger.ErrorContext(ctx, "daily reconciliation failed", slog.Any("error", err))
		return exitError
	}
	defer pool.Close()
	reconciler := app.NewReconcileService(csvstatement.NewReader(opener), postgres.NewLedgerReader(pool))
	runner := app.NewReconciliationRunner(opener.Statements(cfg.StatementsBucket), reconciler,
		postgres.NewReconciliationStore(pool), logger)
	summary, err := runner.Run(ctx, opts.period.From, app.TriggerCLI, "cron")
	if err != nil {
		fmt.Fprintf(stderr, "reconcile --daily %s: %v\n", opts.period.From.Format(time.DateOnly), err)
		return exitError
	}
	divergences := 0
	for _, n := range summary.Counts {
		divergences += n
	}
	fmt.Fprintf(stdout, "conciliação de %s: %d casadas, %d divergências (execução %d)\n",
		opts.period.From.Format(time.DateOnly), summary.Matched, divergences, summary.ID)
	if opts.failOnDivergence && divergences > 0 {
		return exitDivergence
	}
	return exitOK
}

func reconcile(ctx context.Context, cfg config.Config, logger *slog.Logger, opts reconcileOptions) (domain.Report, error) {
	opener, err := objectstore.New(ctx, objectstore.S3Config{
		Endpoint: cfg.S3Endpoint, Region: cfg.AWSRegion, UsePathStyle: cfg.S3UsePathStyle,
	})
	if err != nil {
		return domain.Report{}, err
	}
	pool, err := openPool(ctx, cfg, logger)
	if err != nil {
		return domain.Report{}, err
	}
	defer pool.Close()
	svc := app.NewReconcileService(csvstatement.NewReader(opener), postgres.NewLedgerReader(pool))
	return svc.Reconcile(ctx, opts.period, opts.statement)
}
