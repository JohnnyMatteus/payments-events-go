<script setup lang="ts">
import { mdiArrowRight } from '@mdi/js';
import { computed } from 'vue';
import type { Trail } from '@/api/schemas';
import { formatDateTime } from '@/domain/format';
import { deliveryLabel, outboxLabel, outcomeLabel } from '@/domain/status';
import Money from './Money.vue';

const props = defineProps<{ trail: Trail }>();

const signed = computed(() => {
  const l = props.trail.ledger;
  return l ? (l.direction === 'debit' ? -l.amount_cents : l.amount_cents) : 0;
});
const consumed = computed(() => props.trail.consumers[0] ?? null);
const stuck = computed(() => props.trail.dead_letters.find((d) => d.status === 'open'));
</script>

<template>
  <ol class="trail" aria-label="Caminho do evento: webhook, ledger, outbox e consumidor">
    <li class="stage ok">
      <div class="colhead">1 · Webhook</div>
      <div class="title">{{ outcomeLabel(trail.event.outcome) }}</div>
      <dl>
        <dt>Recebido</dt>
        <dd class="mono">{{ formatDateTime(trail.event.received_at) }}</dd>
        <dt>Entregas</dt>
        <dd class="mono">{{ trail.event.deliveries }} <span class="meta">({{ deliveryLabel(trail.event.deliveries) }})</span></dd>
        <dt>Assinatura</dt>
        <dd>verificada (HMAC-SHA256 dentro da janela)</dd>
      </dl>
    </li>
    <li class="join" aria-hidden="true"><v-icon :icon="mdiArrowRight" size="18" /></li>

    <li class="stage" :class="trail.ledger ? 'ok' : 'gap'">
      <div class="colhead">2 · Ledger</div>
      <template v-if="trail.ledger">
        <div class="title"><Money :cents="signed" :currency="trail.ledger.currency" /></div>
        <dl>
          <dt>Lançamento</dt>
          <dd class="mono">#{{ trail.ledger.id }} · {{ trail.ledger.kind === 'adjustment' ? 'ajuste' : 'provedor' }}</dd>
          <dt>Transação</dt>
          <dd class="mono">{{ trail.ledger.transaction_id }}</dd>
          <dt>Gravado</dt>
          <dd class="mono">{{ formatDateTime(trail.ledger.recorded_at) }}</dd>
        </dl>
      </template>
      <div v-else class="none">
        {{ trail.event.outcome === 'duplicate_transaction' ? 'A transação já tinha lançamento; este evento ficou só como auditoria.' : 'Evento sem efeito financeiro: nada foi lançado.' }}
      </div>
    </li>
    <li class="join" aria-hidden="true"><v-icon :icon="mdiArrowRight" size="18" /></li>

    <li class="stage" :class="trail.outbox ? (trail.outbox.status === 'published' ? 'ok' : 'warn') : 'gap'">
      <div class="colhead">3 · Outbox</div>
      <template v-if="trail.outbox">
        <div class="title">{{ outboxLabel(trail.outbox.status) }}</div>
        <dl>
          <dt>Mensagem</dt>
          <dd class="mono">{{ trail.outbox.message_id }}</dd>
          <dt>Tentativas</dt>
          <dd class="mono">{{ trail.outbox.attempts }}</dd>
          <dt>Publicada</dt>
          <dd class="mono">{{ formatDateTime(trail.outbox.published_at) }}</dd>
          <template v-if="trail.outbox.last_error">
            <dt>Último erro</dt>
            <dd class="mono err">{{ trail.outbox.last_error }}</dd>
          </template>
        </dl>
      </template>
      <div v-else class="none">Sem mensagem de integração.</div>
    </li>
    <li class="join" aria-hidden="true"><v-icon :icon="mdiArrowRight" size="18" /></li>

    <li class="stage" :class="consumed ? 'ok' : stuck ? 'bad' : 'gap'">
      <div class="colhead">4 · Consumidor</div>
      <template v-if="consumed">
        <div class="title">Aplicada na projeção</div>
        <dl>
          <dt>Consumidor</dt>
          <dd class="mono">{{ consumed.consumer }}</dd>
          <dt>Em</dt>
          <dd class="mono">{{ formatDateTime(consumed.consumed_at) }}</dd>
        </dl>
      </template>
      <template v-else-if="stuck">
        <div class="title">Na DLQ</div>
        <dl>
          <dt>Motivo</dt>
          <dd>{{ stuck.reason }}</dd>
          <dt>Erro</dt>
          <dd class="mono err">{{ stuck.error }}</dd>
        </dl>
        <RouterLink to="/dlq">Tratar na DLQ</RouterLink>
      </template>
      <div v-else class="none">{{ trail.outbox ? 'Aguardando o consumidor.' : 'Não se aplica.' }}</div>
    </li>
  </ol>
</template>

<style scoped>
.trail {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 0;
  position: relative;
}
.join {
  display: none;
}
.stage {
  background: rgb(var(--v-theme-surface));
  border: 1px solid var(--rule);
  border-top: 3px solid rgb(var(--v-theme-success));
  padding: 10px 14px 14px;
  margin-left: -1px;
  min-width: 0;
}
.stage.gap {
  border-top: 3px dotted rgba(var(--v-theme-on-surface), 0.5);
}
.stage.warn {
  border-top-color: rgb(var(--v-theme-warning));
}
.stage.bad {
  border-top-color: rgb(var(--v-theme-error));
}
.title {
  font-family: var(--font-serif);
  font-weight: 600;
  font-size: 18px;
  line-height: 26px;
  margin: 2px 0 8px;
}
dl {
  display: grid;
  grid-template-columns: 82px minmax(0, 1fr);
  row-gap: 4px;
  column-gap: 8px;
  margin: 0;
  font-size: 13px;
}
dt {
  color: rgb(var(--v-theme-secondary));
}
dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.err {
  color: rgb(var(--v-theme-error));
}
.none {
  color: rgb(var(--v-theme-secondary));
  font-size: 13px;
}
@media (max-width: 900px) {
  .trail {
    grid-template-columns: 1fr;
  }
  .stage {
    margin-left: 0;
    margin-top: -1px;
  }
}
</style>
