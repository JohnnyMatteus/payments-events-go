import { keepPreviousData, useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/vue-query';
import { computed, type MaybeRefOrGetter, toValue } from 'vue';
import { get, post, putCsv } from './client';
import {
  caseSchema,
  deadLettersSchema,
  eventsPageSchema,
  merchantBalancesSchema,
  meSchema,
  paymentBalancesSchema,
  pipelineSchema,
  runItemsSchema,
  runSchema,
  statementsSchema,
  trailSchema,
  uploadSchema,
  adjustmentSchema,
} from './schemas';
import { z } from 'zod';

const casesSchema = z.object({ items: z.array(caseSchema) });
const adjustmentsSchema = z.object({ items: z.array(adjustmentSchema) });

export const useMe = (enabled: MaybeRefOrGetter<boolean>) =>
  useQuery({ queryKey: ['me'], queryFn: () => get(meSchema, '/me'), enabled: computed(() => toValue(enabled)), retry: false });

export const usePipeline = () =>
  useQuery({ queryKey: ['pipeline'], queryFn: () => get(pipelineSchema, '/pipeline'), refetchInterval: 5_000 });

export interface EventFilters {
  q: string;
  type: string;
  outcome: string;
}

export const useEvents = (filters: MaybeRefOrGetter<EventFilters>) =>
  useInfiniteQuery({
    queryKey: ['events', computed(() => ({ ...toValue(filters) }))],
    queryFn: ({ pageParam }) => {
      const f = toValue(filters);
      return get(eventsPageSchema, '/events', { q: f.q, type: f.type, outcome: f.outcome, cursor: pageParam, limit: '50' });
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor,
    refetchInterval: 5_000,
  });

export const useTrail = (id: MaybeRefOrGetter<string>) =>
  useQuery({
    queryKey: ['trail', computed(() => toValue(id))],
    queryFn: () => get(trailSchema, `/events/${encodeURIComponent(toValue(id))}`),
    refetchInterval: 4_000,
  });

export const useDeadLetters = (status: MaybeRefOrGetter<string>) =>
  useQuery({
    queryKey: ['dead-letters', computed(() => toValue(status))],
    queryFn: () => get(deadLettersSchema, '/dead-letters', { status: toValue(status) }),
    refetchInterval: 5_000,
  });

export const useMerchantBalances = () =>
  useQuery({ queryKey: ['balances', 'merchants'], queryFn: () => get(merchantBalancesSchema, '/balances/merchants'), refetchInterval: 5_000 });

export const usePaymentBalances = (merchantId: MaybeRefOrGetter<string>) =>
  useQuery({
    queryKey: ['balances', 'payments', computed(() => toValue(merchantId))],
    queryFn: () => get(paymentBalancesSchema, '/balances/payments', { merchant_id: toValue(merchantId), limit: '200' }),
    enabled: computed(() => toValue(merchantId) !== ''),
    placeholderData: keepPreviousData,
  });

export const useStatements = () => useQuery({ queryKey: ['statements'], queryFn: () => get(statementsSchema, '/statements') });

export const useLatestRun = (date: MaybeRefOrGetter<string>) =>
  useQuery({
    queryKey: ['run', computed(() => toValue(date))],
    queryFn: () => get(runSchema, '/reconciliation/runs/latest', { date: toValue(date) }),
    retry: false,
  });

export const useRunItems = (runId: MaybeRefOrGetter<number | undefined>) =>
  useQuery({
    queryKey: ['run-items', computed(() => toValue(runId))],
    queryFn: () => get(runItemsSchema, `/reconciliation/runs/${String(toValue(runId))}/items`),
    enabled: computed(() => toValue(runId) !== undefined),
  });

export const usePendingAdjustments = () =>
  useQuery({ queryKey: ['adjustments'], queryFn: () => get(adjustmentsSchema, '/adjustments'), refetchInterval: 5_000 });

export const useCase = (id: MaybeRefOrGetter<number | null>) =>
  useQuery({
    queryKey: ['case', computed(() => toValue(id))],
    queryFn: () => get(caseSchema, `/cases/${String(toValue(id))}`),
    enabled: computed(() => toValue(id) !== null),
  });

/** Depois de qualquer ação, tudo o que ela pode ter mudado é rebuscado. */
function useAction<V, R>(fn: (v: V) => Promise<R>) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => client.invalidateQueries(),
  });
}

export const useStartRun = () => useAction((date: string) => post(runSchema, '/reconciliation/runs', { date }));
export const useUploadStatement = () =>
  useAction((v: { date: string; csv: string }) => putCsv(uploadSchema, `/statements/${v.date}`, v.csv));

export interface Decision {
  id: number;
  reason: string;
}

export const useReprocess = () =>
  useAction((v: Decision & { source: string }) => post(null, `/dead-letters/${v.source}/${String(v.id)}/reprocess`, { reason: v.reason }));
export const useDiscard = () =>
  useAction((v: Decision & { source: string }) => post(null, `/dead-letters/${v.source}/${String(v.id)}/discard`, { reason: v.reason }));
export const useExplain = () => useAction((v: Decision) => post(caseSchema, `/cases/${String(v.id)}/explain`, { reason: v.reason }));
export const usePropose = () => useAction((v: Decision) => post(caseSchema, `/cases/${String(v.id)}/adjustments`, { reason: v.reason }));
export const useApprove = () => useAction((v: Decision) => post(adjustmentSchema, `/adjustments/${String(v.id)}/approve`, { reason: v.reason }));
export const useReject = () => useAction((v: Decision) => post(adjustmentSchema, `/adjustments/${String(v.id)}/reject`, { reason: v.reason }));
export { casesSchema };
