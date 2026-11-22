<script setup lang="ts">
import { mdiCheck, mdiLinkVariantOff } from '@mdi/js';
import { computed, ref, watch } from 'vue';
import type { RunItem } from '@/api/schemas';
import { stateLabel, stateOf, type RowState } from '@/domain/ledger';
import { formatTime } from '@/domain/format';
import Money from './Money.vue';

const props = defineProps<{ items: RunItem[]; selectedKey?: string | null }>();
const emit = defineEmits<{ select: [item: RunItem] }>();

const caseLabel: Record<string, string> = {
  open: 'aberta',
  adjustment_proposed: 'ajuste proposto',
  explained: 'justificada',
  resolved: 'resolvida',
};

const keyOf = (i: RunItem) => `${i.kind}|${i.transaction_id}`;
const signed = (side: { direction: string; amount_cents: number }) => (side.direction === 'debit' ? -side.amount_cents : side.amount_cents);

// Linhas que acabaram de virar par desenham a ligação entre as páginas.
const drawn = ref(new Set<string>());
let previous = new Map<string, RowState>();
watch(
  () => props.items,
  (items) => {
    const fresh = new Set<string>();
    const next = new Map<string, RowState>();
    for (const item of items) {
      const state = stateOf(item);
      next.set(item.transaction_id, state);
      const before = previous.get(item.transaction_id);
      if (state === 'matched' && before && before !== 'matched') fresh.add(item.transaction_id);
    }
    previous = next;
    drawn.value = fresh;
  },
  { immediate: true },
);

const rows = computed(() =>
  props.items.map((item) => {
    const state = stateOf(item);
    return { item, state, key: keyOf(item), label: describe(item, state) };
  }),
);

function describe(item: RunItem, state: RowState): string {
  const delta = item.delta_cents !== 0 ? `, diferença de ${String(Math.abs(item.delta_cents) / 100).replace('.', ',')} reais` : '';
  return `${stateLabel[state]}: transação ${item.transaction_id}${delta}`;
}

function onKey(event: KeyboardEvent, item: RunItem) {
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    emit('select', item);
  }
}
</script>

<template>
  <div class="spread" role="table" aria-label="Extrato do provedor e ledger, lado a lado">
    <div class="spread-head" role="row">
      <div role="columnheader" class="colhead page-title">Extrato do provedor</div>
      <div role="columnheader" class="colhead gutter-title">calha</div>
      <div role="columnheader" class="colhead page-title">Ledger</div>
      <div role="columnheader" class="colhead case-title">Caso</div>
    </div>
    <div
      v-for="row in rows"
      :key="row.key"
      class="spread-row"
      :class="[`is-${row.state}`, { selected: row.key === selectedKey, clickable: row.state !== 'matched' }]"
      role="row"
      :tabindex="row.state === 'matched' ? -1 : 0"
      :aria-label="row.label"
      :aria-selected="row.key === selectedKey"
      @click="emit('select', row.item)"
      @keydown="onKey($event, row.item)"
    >
      <div class="side left" role="cell">
        <template v-if="row.item.statement">
          <span class="mono tx">{{ row.item.statement.transaction_id }}</span>
          <span class="meta mono time">{{ formatTime(row.item.statement.occurred_at) }}</span>
          <Money class="amount" :cents="signed(row.item.statement)" :currency="row.item.statement.currency" />
          <v-chip v-if="row.state === 'duplicate'" class="chip-extra" size="x-small">repetido no extrato</v-chip>
        </template>
        <template v-else>
          <span class="gap" aria-hidden="true"></span>
          <v-chip class="chip-extra" size="x-small">sobrando no ledger</v-chip>
        </template>
      </div>

      <div class="gutter" role="cell" aria-hidden="true">
        <template v-if="row.state === 'matched'">
          <span class="link" :class="{ draw: drawn.has(row.item.transaction_id) }"></span>
          <v-icon class="check" :icon="mdiCheck" size="18" />
        </template>
        <template v-else-if="row.state === 'mismatch'">
          <span class="link broken"></span>
          <span class="delta mono">{{ row.item.delta_cents < 0 ? '−' : '+' }}{{ (Math.abs(row.item.delta_cents) / 100).toFixed(2).replace('.', ',') }}</span>
        </template>
        <template v-else>
          <v-icon class="off" :icon="mdiLinkVariantOff" size="18" />
        </template>
      </div>

      <div class="side right" role="cell">
        <template v-if="row.item.ledger">
          <span class="mono tx">{{ row.item.ledger.transaction_id }}</span>
          <span class="meta mono time">{{ formatTime(row.item.ledger.occurred_at) }}</span>
          <Money class="amount" :cents="signed(row.item.ledger)" :currency="row.item.ledger.currency" />
          <v-chip v-if="row.item.adjusted" class="chip-info" size="x-small">com ajuste</v-chip>
        </template>
        <template v-else>
          <span class="gap" aria-hidden="true"></span>
          <v-chip class="chip-missing" size="x-small">faltando no ledger</v-chip>
        </template>
      </div>

      <div class="case" role="cell">
        <span v-if="row.item.case_status" class="case-status" :class="`case-${row.item.case_status}`">
          {{ caseLabel[row.item.case_status] ?? row.item.case_status }}
        </span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.spread {
  --cols: minmax(0, 1fr) var(--gutter) minmax(0, 1fr) 124px;
  border-top: 1px solid rgb(var(--v-theme-on-surface));
}
.spread-head,
.spread-row {
  display: grid;
  grid-template-columns: var(--cols);
}
.spread-head > div {
  padding: 6px 10px;
  background: rgb(var(--v-theme-surface));
}
.gutter-title {
  text-align: center;
  background: rgb(var(--v-theme-surface-variant)) !important;
  opacity: 0.8;
}
.spread-row {
  min-height: var(--row);
  border-bottom: 1px solid var(--rule);
  position: relative;
}
.spread-row.clickable {
  cursor: pointer;
}
.spread-row.selected .side,
.spread-row.clickable:hover .side {
  background: rgba(var(--v-theme-primary), 0.07);
}
.spread-row.selected {
  outline: 2px solid rgb(var(--v-theme-primary));
  outline-offset: -2px;
}
.side {
  background: rgb(var(--v-theme-surface));
  display: grid;
  grid-template-columns: minmax(0, 1.4fr) 76px 128px auto;
  align-items: center;
  column-gap: 10px;
  padding: 0 10px;
  min-width: 0;
}
.side.right {
  grid-template-columns: minmax(0, 1.4fr) 76px 128px auto;
}
.tx {
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.time {
  font-size: 12px;
}
.amount {
  justify-self: end;
}
.gap {
  grid-column: 1 / 4;
  height: 18px;
  border: 1px dotted rgba(var(--v-theme-on-surface), 0.5);
  align-self: center;
}
.gutter {
  position: relative;
  background: rgb(var(--v-theme-surface-variant));
  display: grid;
  place-items: center;
}
.link {
  position: absolute;
  left: 0;
  right: 0;
  top: 50%;
  height: 1px;
  background: rgb(var(--v-theme-success));
  transform-origin: left center;
}
.link.draw {
  animation: draw 180ms ease-out;
}
.link.broken {
  background: linear-gradient(90deg, rgb(var(--v-theme-mismatch)) 0 22%, transparent 22% 78%, rgb(var(--v-theme-mismatch)) 78% 100%);
  height: 2px;
}
@keyframes draw {
  from {
    transform: scaleX(0);
  }
  to {
    transform: scaleX(1);
  }
}
.check {
  color: rgb(var(--v-theme-success));
  background: rgb(var(--v-theme-surface-variant));
  position: relative;
  border-radius: 0;
}
.off {
  color: rgb(var(--v-theme-secondary));
}
.delta {
  position: relative;
  font-size: 12px;
  font-weight: 500;
  color: rgb(var(--v-theme-mismatch));
  background: rgb(var(--v-theme-surface-variant));
  padding: 0 3px;
  white-space: nowrap;
}
.case {
  display: flex;
  align-items: center;
  padding: 0 10px;
}
.case-status {
  font-size: 12px;
  font-weight: 600;
}
.case-open {
  color: rgb(var(--v-theme-error));
}
.case-adjustment_proposed {
  color: rgb(var(--v-theme-mismatch));
}
.case-explained {
  color: rgb(var(--v-theme-warning));
}
.case-resolved {
  color: rgb(var(--v-theme-success));
}
.chip-missing {
  background: rgb(var(--v-theme-missing-container)) !important;
  color: rgb(var(--v-theme-on-missing-container)) !important;
  grid-column: 4;
}
.chip-extra {
  background: rgb(var(--v-theme-extra-container)) !important;
  color: rgb(var(--v-theme-on-extra-container)) !important;
  grid-column: 4;
}
.chip-info {
  background: rgb(var(--v-theme-mismatch-container)) !important;
  color: rgb(var(--v-theme-on-mismatch-container)) !important;
}
.is-mismatch .amount {
  color: rgb(var(--v-theme-mismatch));
}
@media (max-width: 900px) {
  .spread {
    --cols: minmax(0, 1fr) 40px minmax(0, 1fr);
  }
  .case,
  .case-title {
    display: none;
  }
  .side,
  .side.right {
    grid-template-columns: minmax(0, 1fr) auto;
  }
  .time {
    display: none;
  }
  .gap {
    grid-column: 1 / 3;
  }
  .chip-missing,
  .chip-extra {
    grid-column: 1 / 3;
  }
}
</style>
