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
}

func parseReconcileFlags(args []string, stderr io.Writer) (reconcileOptions, error) {
	fs := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opts reconcileOptions
	var from, to string
	fs.StringVar(&opts.statement, "statement", "", "extrato CSV do provedor: s3://bucket/chave ou caminho local (obrigatório)")
	fs.StringVar(&from, "from", "", "primeiro dia do período, AAAA-MM-DD em UTC (obrigatório)")
	fs.StringVar(&to, "to", "", "último dia do período, inclusivo (padrão: igual a -from)")
	fs.StringVar(&opts.format, "format", "text", "formato do relatório: text ou json")
	fs.BoolVar(&opts.failOnDivergence, "fail-on-divergence", false, "sai com código 3 se houver divergência")
	if err := fs.Parse(args); err != nil {
		return opts, err
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
	opts, err := parseReconcileFlags(args, stderr)
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

	report, err := reconcile(ctx, cfg, opts)
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

func reconcile(ctx context.Context, cfg config.Config, opts reconcileOptions) (domain.Report, error) {
	opener, err := objectstore.New(ctx, objectstore.S3Config{
		Endpoint: cfg.S3Endpoint, Region: cfg.AWSRegion, UsePathStyle: cfg.S3UsePathStyle,
	})
	if err != nil {
		return domain.Report{}, err
	}
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return domain.Report{}, err
	}
	defer pool.Close()
	svc := app.NewReconcileService(csvstatement.NewReader(opener), postgres.NewLedgerReader(pool))
	return svc.Reconcile(ctx, opts.period, opts.statement)
}
