<script setup lang="ts">
import { refDebounced } from '@vueuse/core';
import { computed, ref } from 'vue';
import { useEvents } from '@/api/queries';
import Money from '@/components/signature/Money.vue';
import { formatDateTime } from '@/domain/format';
import { eventTypeLabel, outboxLabel, outcomeLabel } from '@/domain/status';

const search = ref('');
const type = ref('');
const outcome = ref('');
const debounced = refDebounced(search, 300);
const filters = computed(() => ({ q: debounced.value, type: type.value, outcome: outcome.value }));
const { data, isPending, isError, hasNextPage, fetchNextPage, isFetchingNextPage } = useEvents(filters);
const rows = computed(() => data.value?.pages.flatMap((p) => p.items) ?? []);

const types = [
  { title: 'Todos os tipos', value: '' },
  { title: 'Captura', value: 'payment.captured' },
  { title: 'Estorno', value: 'payment.refunded' },
  { title: 'Ajuste', value: 'ledger.adjustment' },
];
const outcomes = [
  { title: 'Todos os resultados', value: '' },
  { title: 'Registrado', value: 'recorded' },
  { title: 'Ignorado', value: 'ignored' },
  { title: 'Transação já lançada', value: 'duplicate_transaction' },
];
const signed = (e: { direction: string; amount_cents: number }) => (e.direction === 'debit' ? -e.amount_cents : e.amount_cents);
</script>

<template>
  <div class="page">
    <header class="page-head">
      <div>
        <div class="colhead">Webhooks do PagaFácil Sandbox</div>
        <h1>Eventos</h1>
      </div>
      <div class="filters">
        <v-text-field v-model="search" label="Buscar por evento, transação, pagamento ou lojista" style="min-width: 340px" clearable />
        <v-select v-model="type" :items="types" label="Tipo" style="min-width: 170px" />
        <v-select v-model="outcome" :items="outcomes" label="Resultado" style="min-width: 200px" />
      </div>
    </header>

    <v-alert v-if="isError" type="error">Não foi possível carregar os eventos.</v-alert>
    <div v-else-if="isPending" class="empty">Lendo o livro de eventos…</div>
    <div v-else-if="!rows.length" class="empty">
      {{ search || type || outcome ? 'Nenhum evento com esses filtros.' : 'Nenhum webhook recebido ainda. Envie um com scripts/send-webhook.sh ou make seed.' }}
    </div>
    <div v-else class="scroll">
      <table class="ledger" aria-label="Eventos recebidos">
        <thead>
          <tr>
            <th class="colhead">Hora</th>
            <th class="colhead">Tipo</th>
            <th class="colhead">Evento (chave de idempotência)</th>
            <th class="colhead">Transação</th>
            <th class="colhead num">Valor</th>
            <th class="colhead">Processamento</th>
            <th class="colhead">Outbox</th>
            <th class="colhead num">Entregas</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="e in rows" :key="e.event_id" class="clickable" @click="$router.push(`/eventos/${encodeURIComponent(e.event_id)}`)">
            <td class="mono">{{ formatDateTime(e.received_at) }}</td>
            <td>{{ eventTypeLabel(e.event_type) }}</td>
            <td class="mono">
              <RouterLink :to="`/eventos/${encodeURIComponent(e.event_id)}`" @click.stop>{{ e.event_id }}</RouterLink>
            </td>
            <td class="mono meta">{{ e.transaction_id || '—' }}</td>
            <td class="num"><Money v-if="e.amount_cents && e.direction" :cents="signed(e)" :currency="e.currency || 'BRL'" /><span v-else class="meta">—</span></td>
            <td>{{ outcomeLabel(e.outcome) }}</td>
            <td>{{ e.outbox_status ? outboxLabel(e.outbox_status) : '—' }}</td>
            <td class="num mono">{{ e.deliveries }}<span v-if="e.deliveries > 1" class="meta"> (+{{ e.deliveries - 1 }} descartada{{ e.deliveries > 2 ? 's' : '' }})</span></td>
          </tr>
        </tbody>
      </table>
      <div v-if="hasNextPage" class="more">
        <v-btn :loading="isFetchingNextPage" @click="fetchNextPage()">Carregar eventos mais antigos</v-btn>
      </div>
    </div>
  </div>
</template>

<style scoped>
.filters {
  display: flex;
  gap: 16px;
  flex-wrap: wrap;
}
.scroll {
  overflow-x: auto;
  background: rgb(var(--v-theme-surface));
  border: 1px solid var(--rule);
}
.more {
  padding: 12px;
  text-align: center;
}
</style>
