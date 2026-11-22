package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// LedgerAppendLockKey é a chave do advisory lock que ordena a leitura do ledger
// por cursor (ADR 0019). Exportada para os testes simularem escritores e leitores.
const LedgerAppendLockKey = 7_316_420_003

// lockLedgerAppend marca a transação como escritora do ledger. É o primeiro passo
// de quem vai gravar um lançamento, antes de qualquer lock de linha e antes de o
// id ser sorteado, e fica retido até o commit. Escritores usam o modo
// compartilhado, então não esperam uns aos outros.
func lockLedgerAppend(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock_shared($1)", LedgerAppendLockKey); err != nil {
		return fmt.Errorf("lock ledger append: %w", err)
	}
	return nil
}

// stableLedgerBound devolve o maior id do ledger tal que todo id menor ou igual
// a ele que um dia vá existir já está confirmado. Pega o lock em modo exclusivo,
// o que espera os escritores em andamento (que o seguram desde antes de sortear
// o id até o commit) e segura os novos por um instante; sob o lock, max(id) é
// confirmado e qualquer escritor posterior sorteia um id maior.
func stableLedgerBound(ctx context.Context, pool interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}) (int64, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin ledger bound: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Um escritor travado não pode travar a consulta para sempre.
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
		return 0, fmt.Errorf("set lock timeout: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", LedgerAppendLockKey); err != nil {
		return 0, fmt.Errorf("wait for in-flight ledger writers: %w", err)
	}
	var bound int64
	if err := tx.QueryRow(ctx, "SELECT COALESCE(MAX(id), 0) FROM ledger_entries").Scan(&bound); err != nil {
		return 0, fmt.Errorf("read ledger bound: %w", err)
	}
	return bound, nil
}
