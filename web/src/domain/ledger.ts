import type { RunItem } from '@/api/schemas';

/** Estados de uma linha da partida dobrada; cada um tem texto, ícone e cor próprios, nunca só cor. */
export type RowState = 'matched' | 'missing' | 'extra' | 'mismatch' | 'duplicate';

export const stateOf = (item: RunItem): RowState => {
  switch (item.kind) {
    case 'matched':
      return 'matched';
    case 'missing_in_ledger':
      return 'missing';
    case 'missing_in_statement':
      return 'extra';
    case 'amount_mismatch':
      return 'mismatch';
    default:
      return 'duplicate';
  }
};

export const stateLabel: Record<RowState, string> = {
  matched: 'conciliado',
  missing: 'faltando no ledger',
  extra: 'sobrando no ledger',
  mismatch: 'valor diferente',
  duplicate: 'repetido no extrato',
};

export type StateFilter = 'all' | 'missing' | 'extra' | 'mismatch' | 'open';

export function matchesFilter(item: RunItem, filter: StateFilter, search: string): boolean {
  const state = stateOf(item);
  if (filter === 'open' && state === 'matched') return false;
  if ((filter === 'missing' || filter === 'extra' || filter === 'mismatch') && state !== filter) return false;
  const q = search.trim().toLowerCase();
  if (!q) return true;
  return [item.transaction_id, item.statement?.payment_id, item.ledger?.payment_id].some((v) => v?.toLowerCase().includes(q));
}

export interface Waterfall {
  statementTotal: number;
  matched: number;
  missing: number;
  extra: number;
  mismatch: number;
  other: number;
}

/** Cascata em número de linhas: total do extrato → conciliado → faltando → sobrando → divergente. */
export function waterfallOf(items: RunItem[], statementLines: number): Waterfall {
  const w: Waterfall = { statementTotal: statementLines, matched: 0, missing: 0, extra: 0, mismatch: 0, other: 0 };
  for (const item of items) {
    const state = stateOf(item);
    if (state === 'matched') w.matched++;
    else if (state === 'missing') w.missing++;
    else if (state === 'extra') w.extra++;
    else if (state === 'mismatch') w.mismatch++;
    else w.other++;
  }
  return w;
}
