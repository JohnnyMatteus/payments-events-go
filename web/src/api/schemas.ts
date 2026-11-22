import { z } from 'zod';

const iso = z.string();
const nullableIso = iso.nullable();

export const eventSchema = z.object({
  event_id: z.string(),
  event_type: z.string(),
  outcome: z.string(),
  merchant_id: z.string(),
  transaction_id: z.string(),
  payment_id: z.string(),
  direction: z.string(),
  amount_cents: z.number(),
  currency: z.string(),
  occurred_at: nullableIso,
  received_at: iso,
  outbox_status: z.string(),
  deliveries: z.number(),
});
export type EventRow = z.infer<typeof eventSchema>;

export const eventsPageSchema = z.object({ items: z.array(eventSchema), next_cursor: z.string().optional() });

export const deadLetterSchema = z.object({
  id: z.number(),
  source: z.enum(['broker', 'outbox']),
  message_id: z.string(),
  routing_key: z.string(),
  payload: z.string(),
  reason: z.string(),
  error: z.string(),
  deliveries: z.number(),
  first_failed_at: iso,
  age_seconds: z.number(),
  status: z.string(),
  resolved_by: z.string().optional(),
  resolved_at: nullableIso,
  resolution_note: z.string().optional(),
  can_reprocess: z.boolean(),
});
export type DeadLetter = z.infer<typeof deadLetterSchema>;
export const deadLettersSchema = z.object({ items: z.array(deadLetterSchema) });

export const trailSchema = z.object({
  event: eventSchema,
  payload: z.unknown(),
  ledger: z
    .object({
      id: z.number(),
      transaction_id: z.string(),
      payment_id: z.string(),
      merchant_id: z.string(),
      direction: z.string(),
      amount_cents: z.number(),
      currency: z.string(),
      kind: z.string(),
      adjusts_transaction_id: z.string().optional(),
      occurred_at: iso,
      recorded_at: iso,
    })
    .nullable(),
  outbox: z
    .object({
      id: z.number(),
      message_id: z.string(),
      routing_key: z.string(),
      status: z.string(),
      attempts: z.number(),
      last_error: z.string().optional(),
      created_at: iso,
      published_at: nullableIso,
      next_attempt_at: iso,
    })
    .nullable(),
  consumers: z.array(z.object({ consumer: z.string(), consumed_at: iso })),
  dead_letters: z.array(deadLetterSchema),
});
export type Trail = z.infer<typeof trailSchema>;

export const merchantBalanceSchema = z.object({
  merchant_id: z.string(),
  currency: z.string(),
  payments: z.number(),
  captured_cents: z.number(),
  refunded_cents: z.number(),
  adjustment_cents: z.number(),
  ledger_net_cents: z.number(),
  projected_net_cents: z.number(),
  last_entry_at: nullableIso,
  unprojected_messages: z.number(),
});
export type MerchantBalance = z.infer<typeof merchantBalanceSchema>;
export const merchantBalancesSchema = z.object({ items: z.array(merchantBalanceSchema) });

export const paymentBalancesSchema = z.object({
  items: z.array(
    z.object({
      payment_id: z.string(),
      merchant_id: z.string(),
      currency: z.string(),
      captured_cents: z.number(),
      refunded_cents: z.number(),
      ledger_net_cents: z.number(),
      updated_at: iso,
    }),
  ),
  next_cursor: z.string().optional(),
});

export const pipelineSchema = z.object({
  outbox_pending: z.number(),
  outbox_dead: z.number(),
  oldest_pending_age_seconds: z.number(),
  published_last_minute: z.number(),
  published_last_hour: z.number(),
  dead_letters_open: z.number(),
  oldest_dead_letter_age_seconds: z.number(),
  last_consumed_at: nullableIso,
  last_reconciliation_at: nullableIso,
});
export type Pipeline = z.infer<typeof pipelineSchema>;

export const statementSchema = z.object({
  date: z.string(),
  ref: z.string(),
  size_bytes: z.number(),
  sha256: z.string(),
  last_modified: iso,
});
export type Statement = z.infer<typeof statementSchema>;
export const statementsSchema = z.object({ items: z.array(statementSchema) });
export const uploadSchema = z.object({ statement: statementSchema, lines: z.number() });

export const runSchema = z.object({
  id: z.number(),
  date: z.string(),
  statement_ref: z.string(),
  trigger: z.string(),
  triggered_by: z.string(),
  status: z.string(),
  error: z.string().optional(),
  statement_lines: z.number(),
  ledger_entries: z.number(),
  matched: z.number(),
  reversed: z.number(),
  statement_net_cents: z.number(),
  ledger_net_cents: z.number(),
  divergences: z.record(z.string(), z.number()),
  started_at: iso,
  finished_at: iso,
});
export type Run = z.infer<typeof runSchema>;

const statementLineSchema = z.object({
  line_number: z.number(),
  transaction_id: z.string(),
  payment_id: z.string(),
  direction: z.string(),
  amount_cents: z.number(),
  currency: z.string(),
  occurred_at: iso,
});
const ledgerSideSchema = z.object({
  transaction_id: z.string(),
  payment_id: z.string(),
  merchant_id: z.string().optional(),
  direction: z.string(),
  amount_cents: z.number(),
  currency: z.string(),
  occurred_at: iso,
  event_id: z.string().optional(),
});
export type StatementLine = z.infer<typeof statementLineSchema>;
export type LedgerSide = z.infer<typeof ledgerSideSchema>;

export const runItemSchema = z.object({
  kind: z.string(),
  transaction_id: z.string(),
  statement: statementLineSchema.nullable(),
  ledger: ledgerSideSchema.nullable(),
  adjusted: z.boolean(),
  delta_cents: z.number(),
  detail: z.string().optional(),
  case_id: z.number().nullable(),
  case_status: z.string().optional(),
});
export type RunItem = z.infer<typeof runItemSchema>;
export const runItemsSchema = z.object({ items: z.array(runItemSchema) });

export const adjustmentSchema = z.object({
  id: z.number(),
  case_id: z.number(),
  entry: ledgerSideSchema.nullable(),
  adjusts_transaction_id: z.string().optional(),
  reason: z.string(),
  status: z.enum(['proposed', 'approved', 'rejected']),
  proposed_by: z.string(),
  proposed_at: iso,
  decided_by: z.string().optional(),
  decided_at: nullableIso,
  decision_note: z.string().optional(),
  event_id: z.string().optional(),
});
export type Adjustment = z.infer<typeof adjustmentSchema>;

export const caseSchema = z.object({
  id: z.number(),
  date: z.string(),
  transaction_id: z.string(),
  kind: z.string(),
  status: z.enum(['open', 'adjustment_proposed', 'explained', 'resolved']),
  statement: statementLineSchema.nullable(),
  ledger: ledgerSideSchema.nullable(),
  delta_cents: z.number(),
  detail: z.string().optional(),
  explanation: z.string().optional(),
  resolved_by: z.string().optional(),
  resolved_at: nullableIso,
  updated_at: iso,
  adjustments: z.array(adjustmentSchema),
});
export type Case = z.infer<typeof caseSchema>;

export const meSchema = z.object({ operator: z.string() });
