import { describe, expect, it } from 'vitest';
import { formatAge, formatMoney, moneyParts } from '@/domain/format';
import { matchesFilter, stateOf, waterfallOf } from '@/domain/ledger';
import { ageLevel, deliveryLabel } from '@/domain/status';
import { runItemsSchema, type RunItem } from '@/api/schemas';

const item = (kind: string, tx: string, extra: Partial<RunItem> = {}): RunItem => ({
  kind,
  transaction_id: tx,
  statement: null,
  ledger: null,
  adjusted: false,
  delta_cents: 0,
  case_id: null,
  ...extra,
});

describe('dinheiro', () => {
  it('formata em pt-BR com menos tipográfico e centavos sempre com dois dígitos', () => {
    expect(formatMoney(123456)).toBe('R$ 1.234,56');
    expect(formatMoney(-500)).toBe('−R$ 5,00');
    expect(formatMoney(5, 'BRL', true)).toBe('+R$ 0,05');
    expect(moneyParts(-1999)).toMatchObject({ sign: '−', whole: '19', cents: '99' });
  });
  it('mostra a idade da DLQ em unidades curtas', () => {
    expect(formatAge(42)).toBe('42 s');
    expect(formatAge(7_500)).toBe('2 h 5 min');
    expect(formatAge(200_000)).toContain('d');
  });
});

describe('partida dobrada', () => {
  it('classifica cada tipo de divergência em um estado com nome próprio', () => {
    expect(stateOf(item('matched', 'a'))).toBe('matched');
    expect(stateOf(item('missing_in_ledger', 'a'))).toBe('missing');
    expect(stateOf(item('missing_in_statement', 'a'))).toBe('extra');
    expect(stateOf(item('amount_mismatch', 'a'))).toBe('mismatch');
    expect(stateOf(item('duplicate_in_statement', 'a'))).toBe('duplicate');
  });
  it('filtra por estado e busca pelo id do provedor', () => {
    const ok = item('matched', 'txn_1');
    const bad = item('amount_mismatch', 'txn_2');
    expect(matchesFilter(ok, 'open', '')).toBe(false);
    expect(matchesFilter(bad, 'open', '')).toBe(true);
    expect(matchesFilter(bad, 'missing', '')).toBe(false);
    expect(matchesFilter(bad, 'all', 'TXN_2')).toBe(true);
    expect(matchesFilter(bad, 'all', 'outro')).toBe(false);
  });
  it('conta a cascata', () => {
    const w = waterfallOf([item('matched', 'a'), item('matched', 'b'), item('missing_in_ledger', 'c'), item('amount_mismatch', 'd')], 4);
    expect(w).toMatchObject({ statementTotal: 4, matched: 2, missing: 1, mismatch: 1, extra: 0 });
  });
});

describe('contrato da API', () => {
  it('valida a linha da conciliação e rejeita campo com tipo errado', () => {
    const ok = { items: [{ ...item('amount_mismatch', 'x'), delta_cents: -500 }] };
    expect(runItemsSchema.parse(ok).items[0]?.delta_cents).toBe(-500);
    expect(() => runItemsSchema.parse({ items: [{ ...ok.items[0], delta_cents: '5' }] })).toThrow();
  });
});

describe('DLQ', () => {
  it('escala a idade em níveis e descreve reenvios', () => {
    expect([ageLevel(10), ageLevel(600), ageLevel(3_600), ageLevel(90_000)]).toEqual([0, 1, 2, 3]);
    expect(deliveryLabel(1)).toBe('sem reenvios');
    expect(deliveryLabel(3)).toBe('2 reenvios descartados');
  });
});
