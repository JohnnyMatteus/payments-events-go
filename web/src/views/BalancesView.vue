<script setup lang="ts">
import { computed, ref } from 'vue';
import { useMerchantBalances, usePaymentBalances } from '@/api/queries';
import Money from '@/components/signature/Money.vue';
import { formatDateTime } from '@/domain/format';

const { data, isPending, isError } = useMerchantBalances();
const selected = ref('');
const payments = usePaymentBalances(selected);
const rows = computed(() => data.value?.items ?? []);
const toggle = (id: string) => (selected.value = selected.value === id ? '' : id);
</script>

<template>
  <div class="page">
    <header class="page-head">
      <div>
        <div class="colhead">Ledger × projeção do consumidor</div>
        <h1>Saldos</h1>
      </div>
      <span class="meta">Líquido = capturas − estornos + ajustes aprovados</span>
    </header>

    <v-alert v-if="isError" type="error">Não foi possível carregar os saldos.</v-alert>
    <div v-else-if="isPending" class="empty">Somando o ledger…</div>
    <div v-else-if="!rows.length" class="empty">O ledger ainda não tem lançamentos.</div>
    <div v-else class="scroll">
      <table class="ledger" aria-label="Saldo por lojista">
        <thead>
          <tr>
            <th class="colhead">Lojista</th>
            <th class="colhead num">Pagamentos</th>
            <th class="colhead num">Capturado</th>
            <th class="colhead num">Estornado</th>
            <th class="colhead num">Ajustes</th>
            <th class="colhead num">Líquido no ledger</th>
            <th class="colhead num">Líquido na projeção</th>
            <th class="colhead">Situação</th>
            <th class="colhead">Último lançamento</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="b in rows" :key="b.merchant_id + b.currency" class="clickable" :class="{ selected: selected === b.merchant_id }" tabindex="0" @click="toggle(b.merchant_id)" @keydown.enter="toggle(b.merchant_id)">
            <td class="mono">{{ b.merchant_id }}</td>
            <td class="num mono">{{ b.payments }}</td>
            <td class="num"><Money :cents="b.captured_cents" :currency="b.currency" /></td>
            <td class="num"><Money :cents="-b.refunded_cents" :currency="b.currency" /></td>
            <td class="num"><Money :cents="b.adjustment_cents" :currency="b.currency" plus /></td>
            <td class="num"><Money :cents="b.ledger_net_cents" :currency="b.currency" /></td>
            <td class="num"><Money :cents="b.projected_net_cents" :currency="b.currency" /></td>
            <td>
              <span v-if="b.ledger_net_cents === b.projected_net_cents && !b.unprojected_messages" class="ok">em dia</span>
              <span v-else class="lag">projeção atrasada · {{ b.unprojected_messages }} mensagem{{ b.unprojected_messages === 1 ? '' : 's' }} por consumir</span>
            </td>
            <td class="mono meta">{{ formatDateTime(b.last_entry_at) }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <section v-if="selected" class="mt-6">
      <h2 class="mb-2">Pagamentos de <span class="mono">{{ selected }}</span></h2>
      <div v-if="payments.isPending.value" class="empty">Lendo a projeção…</div>
      <div v-else-if="!payments.data.value?.items.length" class="empty">A projeção ainda não recebeu pagamentos deste lojista.</div>
      <div v-else class="scroll">
        <table class="ledger" aria-label="Saldo por pagamento">
          <thead>
            <tr>
              <th class="colhead">Pagamento</th>
              <th class="colhead num">Capturado</th>
              <th class="colhead num">Estornado</th>
              <th class="colhead num">Líquido na projeção</th>
              <th class="colhead num">Líquido no ledger</th>
              <th class="colhead">Atualizado</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="p in payments.data.value.items" :key="p.payment_id">
              <td class="mono">{{ p.payment_id }}</td>
              <td class="num"><Money :cents="p.captured_cents" :currency="p.currency" /></td>
              <td class="num"><Money :cents="-p.refunded_cents" :currency="p.currency" /></td>
              <td class="num"><Money :cents="p.captured_cents - p.refunded_cents" :currency="p.currency" /></td>
              <td class="num"><Money :cents="p.ledger_net_cents" :currency="p.currency" /></td>
              <td class="mono meta">{{ formatDateTime(p.updated_at) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>

<style scoped>
.scroll {
  overflow-x: auto;
  background: rgb(var(--v-theme-surface));
  border: 1px solid var(--rule);
}
.ok {
  color: rgb(var(--v-theme-success));
  font-weight: 600;
}
.lag {
  color: rgb(var(--v-theme-warning));
  font-weight: 600;
}
</style>
