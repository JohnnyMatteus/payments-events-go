import { differenceInCalendarDays, formatDuration, intervalToDuration, type Duration } from 'date-fns';
import { ptBR } from 'date-fns/locale';
import { formatInTimeZone } from 'date-fns-tz';

/** O produto fecha o dia em horário de Brasília (Etc/GMT+3, sem horário de verão). */
export const ZONE = 'Etc/GMT+3';

export interface MoneyParts {
  sign: '' | '−' | '+';
  whole: string;
  cents: string;
  currency: string;
}

const wholeFormat = new Intl.NumberFormat('pt-BR');

/** Divide o valor para a tela alinhar pela vírgula e reduzir os centavos; o sinal usa o menos tipográfico. */
export function moneyParts(cents: number, currency = 'BRL', showPlus = false): MoneyParts {
  const abs = Math.abs(cents);
  const sign = cents < 0 ? '−' : showPlus && cents > 0 ? '+' : '';
  return {
    sign,
    whole: wholeFormat.format(Math.trunc(abs / 100)),
    cents: String(abs % 100).padStart(2, '0'),
    currency,
  };
}

const symbols: Record<string, string> = { BRL: 'R$' };

export function formatMoney(cents: number, currency = 'BRL', showPlus = false): string {
  const p = moneyParts(cents, currency, showPlus);
  return `${p.sign}${symbols[currency] ?? currency} ${p.whole},${p.cents}`;
}

export function formatDateTime(iso: string | null | undefined): string {
  return iso ? formatInTimeZone(new Date(iso), ZONE, 'dd/MM/yyyy HH:mm:ss') : '—';
}

export function formatTime(iso: string): string {
  return formatInTimeZone(new Date(iso), ZONE, 'HH:mm:ss');
}

export function formatDay(isoDate: string): string {
  const [y, m, d] = isoDate.split('-');
  return `${d ?? ''}/${m ?? ''}/${y ?? ''}`;
}

export function formatAge(seconds: number): string {
  if (seconds < 60) return `${String(Math.max(0, Math.floor(seconds)))} s`;
  const duration = intervalToDuration({ start: 0, end: seconds * 1000 });
  const format: (keyof Duration)[] = seconds >= 86_400 ? ['days', 'hours'] : seconds >= 3_600 ? ['hours', 'minutes'] : ['minutes'];
  return formatDuration(duration, { format, locale: ptBR, delimiter: ' ' })
    .replace(/ dias?/, ' d')
    .replace(/ horas?/, ' h')
    .replace(/ minutos?/, ' min');
}

export function shortDigest(hash: string): string {
  return hash ? `${hash.slice(0, 8)}…${hash.slice(-4)}` : '—';
}

/** Ontem em Brasília, o dia que o analista costuma fechar. */
export function yesterday(now = new Date()): string {
  return formatInTimeZone(new Date(now.getTime() - 86_400_000), ZONE, 'yyyy-MM-dd');
}

export function daysBetween(a: string, b: string): number {
  return differenceInCalendarDays(new Date(a), new Date(b));
}
