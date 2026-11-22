import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import type { RunItem } from '@/api/schemas';
import LedgerSpread from '@/components/signature/LedgerSpread.vue';
import Money from '@/components/signature/Money.vue';

const at = '2026-11-20T15:00:00Z';
const stmt = (tx: string, cents: number) => ({ line_number: 2, transaction_id: tx, payment_id: 'p', direction: 'credit', amount_cents: cents, currency: 'BRL', occurred_at: at });
const led = (tx: string, cents: number) => ({ transaction_id: tx, payment_id: 'p', direction: 'credit', amount_cents: cents, currency: 'BRL', occurred_at: at });
const base = { adjusted: false, delta_cents: 0, case_id: null };

const items: RunItem[] = [
  { ...base, kind: 'matched', transaction_id: 'ok', statement: stmt('ok', 100), ledger: led('ok', 100) },
  { ...base, kind: 'missing_in_ledger', transaction_id: 'miss', statement: stmt('miss', 4200), ledger: null, delta_cents: 4200, case_id: 1, case_status: 'open' },
  { ...base, kind: 'missing_in_statement', transaction_id: 'extra', statement: null, ledger: led('extra', 8000), delta_cents: -8000 },
  { ...base, kind: 'amount_mismatch', transaction_id: 'diff', statement: stmt('diff', 3000), ledger: led('diff', 2500), delta_cents: 500 },
];

describe('Money', () => {
  it('alinha pela vírgula e fala o valor por extenso para leitor de tela', () => {
    const w = mount(Money, { props: { cents: -123456 } });
    expect(w.text()).toContain('−1.234,56');
    expect(w.attributes('aria-label')).toBe('menos 1.234 reais e 56 centavos');
  });
});

describe('LedgerSpread', () => {
  const w = mount(LedgerSpread, { props: { items } });
  const rows = w.findAll('.spread-row');

  it('desenha uma linha por item, com a lacuna e o chip do lado que falta', () => {
    expect(rows).toHaveLength(4);
    expect(rows[1]?.find('.chip-missing').text()).toBe('faltando no ledger');
    expect(rows[2]?.find('.chip-extra').text()).toBe('sobrando no ledger');
    expect(rows[1]?.find('.gap').exists()).toBe(true);
  });
  it('mostra o delta violeta na calha só nos pares divergentes', () => {
    expect(rows[3]?.find('.delta').text()).toBe('+5,00');
    expect(rows[0]?.find('.delta').exists()).toBe(false);
    expect(rows[0]?.find('.link').exists()).toBe(true);
  });
  it('nomeia o estado em texto para leitor de tela, nunca só por cor', () => {
    expect(rows[3]?.attributes('aria-label')).toContain('valor diferente');
    expect(rows[1]?.attributes('aria-label')).toContain('faltando no ledger');
  });
  it('emite a seleção ao clicar numa divergência e mostra o estado do caso', async () => {
    await rows[1]?.trigger('click');
    expect(w.emitted('select')?.[0]?.[0]).toMatchObject({ transaction_id: 'miss' });
    expect(rows[1]?.find('.case-status').text()).toBe('aberta');
  });
});
