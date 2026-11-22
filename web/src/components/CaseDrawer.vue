<script setup lang="ts">
import { computed, ref } from 'vue';
import { ApiError } from '@/api/client';
import { useApprove, useCase, useExplain, usePropose, useReject, useTrail } from '@/api/queries';
import ReasonDialog from '@/components/ReasonDialog.vue';
import Money from '@/components/signature/Money.vue';
import { canAct, operator } from '@/auth/session';
import { formatDateTime } from '@/domain/format';
import { deliveryLabel, outboxLabel } from '@/domain/status';
import { stateLabel, type RowState } from '@/domain/ledger';

const props = defineProps<{ caseId: number; state: RowState }>();
const emit = defineEmits<{ close: [] }>();

const { data: c, isPending } = useCase(() => props.caseId);
const eventId = computed(() => c.value?.ledger?.event_id ?? '');
const trail = useTrail(eventId);
const pending = computed(() => c.value?.adjustments.find((a) => a.status === 'proposed'));
const closed = computed(() => c.value?.status === 'explained' || c.value?.status === 'resolved');
const mine = computed(() => pending.value?.proposed_by.toLowerCase() === operator.value?.toLowerCase());

const statusLabel = { open: 'Aberta', adjustment_proposed: 'Ajuste proposto', explained: 'Justificada', resolved: 'Resolvida' } as const;

const explain = useExplain();
const propose = usePropose();
const approve = useApprove();
const reject = useReject();

type Act = 'explain' | 'propose' | 'approve' | 'reject';
const act = ref<Act | null>(null);
const error = ref('');
const copy: Record<Act, { title: string; button: string; warning: string }> = {
  explain: { title: 'Marcar como explicada', button: 'Marcar como explicada', warning: 'O ledger não muda. A divergência fica encerrada com o seu motivo.' },
  propose: { title: 'Propor ajuste no ledger', button: 'Propor ajuste', warning: 'O lançamento de ajuste só entra no ledger quando outra pessoa aprovar.' },
  approve: { title: 'Aprovar ajuste', button: 'Aprovar e lançar', warning: 'Isto grava um lançamento novo no ledger e publica a mensagem no outbox. Não há como desfazer, só corrigir com outro ajuste.' },
  reject: { title: 'Recusar ajuste', button: 'Recusar', warning: 'O caso volta para aberto.' },
};

async function confirm(reason: string) {
  error.value = '';
  try {
    if (act.value === 'explain') await explain.mutateAsync({ id: props.caseId, reason });
    else if (act.value === 'propose') await propose.mutateAsync({ id: props.caseId, reason });
    else if (pending.value && act.value === 'approve') await approve.mutateAsync({ id: pending.value.id, reason });
    else if (pending.value && act.value === 'reject') await reject.mutateAsync({ id: pending.value.id, reason });
    act.value = null;
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : 'Erro inesperado.';
  }
}
const busy = computed(() => [explain, propose, approve, reject].some((m) => m.isPending.value));
const signed = (s: { direction: string; amount_cents: number }) => (s.direction === 'debit' ? -s.amount_cents : s.amount_cents);
</script>

<template>
  <aside class="drawer" aria-label="Caso de divergência">
    <header class="head">
      <div>
        <div class="colhead">Caso #{{ caseId }} · {{ stateLabel[state] }}</div>
        <h2 class="mono">{{ c?.transaction_id ?? '…' }}</h2>
      </div>
      <v-btn variant="text" size="small" @click="emit('close')">Fechar</v-btn>
    </header>
    <div v-if="isPending" class="meta">Abrindo o caso…</div>
    <template v-else-if="c">
      <p class="status">
        <span class="colhead">Estado</span>
        <strong :class="`st-${c.status}`">{{ statusLabel[c.status] }}</strong>
        <span v-if="c.resolved_by" class="meta"> por {{ c.resolved_by }} em {{ formatDateTime(c.resolved_at) }}</span>
      </p>
      <p v-if="c.detail" class="meta">{{ c.detail }}</p>
      <dl class="sides">
        <dt class="colhead">Extrato</dt>
        <dd><Money v-if="c.statement" :cents="signed(c.statement)" /><span v-else class="meta">sem linha</span></dd>
        <dt class="colhead">Ledger</dt>
        <dd><Money v-if="c.ledger" :cents="signed(c.ledger)" /><span v-else class="meta">sem lançamento</span></dd>
        <dt class="colhead">Falta somar</dt>
        <dd><Money :cents="c.delta_cents" plus class="delta" /></dd>
      </dl>

      <section v-if="c.explanation" class="block">
        <div class="colhead">Justificativa</div>
        <p>{{ c.explanation }}</p>
      </section>

      <section class="block">
        <div class="colhead mb-1">Histórico do lançamento</div>
        <ul v-if="trail.data.value" class="hist">
          <li>Webhook recebido em <span class="mono">{{ formatDateTime(trail.data.value.event.received_at) }}</span></li>
          <li>{{ trail.data.value.event.deliveries - 1 }} reenvio(s) ignorado(s) por idempotência <span class="meta">({{ deliveryLabel(trail.data.value.event.deliveries) }})</span></li>
          <li v-if="trail.data.value.outbox">Evento no outbox: {{ outboxLabel(trail.data.value.outbox.status) }}</li>
          <li><RouterLink :to="`/eventos/${encodeURIComponent(eventId)}`">Ver o caminho completo do evento</RouterLink></li>
        </ul>
        <p v-else class="meta">{{ c.ledger ? 'Lendo o histórico…' : 'Sem lançamento no ledger, então não há webhook para este caso.' }}</p>
      </section>

      <section v-if="c.adjustments.length" class="block">
        <div class="colhead mb-1">Ajustes</div>
        <div v-for="a in c.adjustments" :key="a.id" class="adj">
          <div>
            <strong>#{{ a.id }}</strong>
            <Money v-if="a.entry" :cents="signed(a.entry)" plus class="ml-2" />
            · {{ { proposed: 'aguardando aprovação', approved: 'aprovado', rejected: 'recusado' }[a.status] }}
          </div>
          <div class="meta">Proposto por {{ a.proposed_by }}: {{ a.reason }}</div>
          <div v-if="a.decided_by" class="meta">Decidido por {{ a.decided_by }}{{ a.decision_note ? `: ${a.decision_note}` : '' }}</div>
        </div>
      </section>

      <section v-if="!closed && canAct" class="actions">
        <template v-if="pending">
          <v-btn color="primary" variant="flat" :disabled="mine" @click="act = 'approve'">Aprovar ajuste</v-btn>
          <v-btn @click="act = 'reject'">Recusar</v-btn>
          <p v-if="mine" class="meta">Você propôs este ajuste. A aprovação precisa ser de outra pessoa.</p>
        </template>
        <template v-else>
          <v-btn color="primary" variant="flat" @click="act = 'propose'">Propor ajuste</v-btn>
          <v-btn @click="act = 'explain'">Marcar como explicado</v-btn>
        </template>
      </section>
    </template>

    <ReasonDialog
      :model-value="act !== null"
      :title="act ? copy[act].title : ''"
      :confirm-label="act ? copy[act].button : ''"
      :warning="act ? copy[act].warning : ''"
      :busy="busy"
      :error="error"
      @update:model-value="act = $event ? act : null"
      @confirm="confirm"
    />
  </aside>
</template>

<style scoped>
.drawer {
  background: rgb(var(--v-theme-surface));
  border: 1px solid var(--rule);
  border-top: 3px solid rgb(var(--v-theme-primary));
  padding: 14px 16px 20px;
  position: sticky;
  top: 16px;
  max-height: calc(100vh - 32px);
  overflow-y: auto;
}
.head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 8px;
}
h2 {
  font-size: 16px;
  line-height: 22px;
  word-break: break-all;
}
.sides {
  display: grid;
  grid-template-columns: 110px 1fr;
  row-gap: 4px;
  margin: 12px 0;
}
dd {
  margin: 0;
  justify-self: start;
}
.delta {
  color: rgb(var(--v-theme-mismatch));
}
.block {
  border-top: 1px solid var(--rule);
  padding-top: 10px;
  margin-top: 12px;
}
.hist {
  margin: 0;
  padding-left: 18px;
  font-size: 13px;
}
.adj {
  padding: 6px 0;
}
.actions {
  border-top: 1px solid var(--rule);
  margin-top: 14px;
  padding-top: 12px;
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.st-open {
  color: rgb(var(--v-theme-error));
}
.st-adjustment_proposed {
  color: rgb(var(--v-theme-mismatch));
}
.st-explained {
  color: rgb(var(--v-theme-warning));
}
.st-resolved {
  color: rgb(var(--v-theme-success));
}
a {
  color: rgb(var(--v-theme-primary));
}
</style>
