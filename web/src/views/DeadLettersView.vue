<script setup lang="ts">
import { mdiReplay, mdiTrayAlert } from '@mdi/js';
import { computed, ref } from 'vue';
import { ApiError } from '@/api/client';
import { useDeadLetters, useDiscard, useReprocess } from '@/api/queries';
import type { DeadLetter } from '@/api/schemas';
import ReasonDialog from '@/components/ReasonDialog.vue';
import AgeHistogram from '@/components/signature/AgeHistogram.vue';
import { formatAge, formatDateTime } from '@/domain/format';
import { ageLevel, reasonLabel } from '@/domain/status';

const status = ref<'open' | 'all'>('open');
const { data, isPending, isError } = useDeadLetters(status);
const items = computed(() => data.value?.items ?? []);
const open = computed(() => items.value.filter((d) => d.status === 'open'));
const groups = computed(() => {
  const map = new Map<string, DeadLetter[]>();
  for (const d of items.value) map.set(d.reason, [...(map.get(d.reason) ?? []), d]);
  return [...map.entries()];
});

const picked = ref(new Set<string>());
const key = (d: DeadLetter) => `${d.source}:${String(d.id)}`;
const toggle = (d: DeadLetter) => {
  const next = new Set(picked.value);
  if (!next.delete(key(d))) next.add(key(d));
  picked.value = next;
};
const selection = computed(() => open.value.filter((d) => picked.value.has(key(d))));
const pickAll = (group: DeadLetter[], on: boolean) => {
  const next = new Set(picked.value);
  for (const d of group.filter((g) => g.status === 'open')) {
    if (on) next.add(key(d));
    else next.delete(key(d));
  }
  picked.value = next;
};

const reprocess = useReprocess();
const discard = useDiscard();
const dialog = ref<'reprocess' | 'discard' | null>(null);
const failure = ref('');
const done = ref('');
const expanded = ref<string | null>(null);

async function confirm(reason: string) {
  failure.value = '';
  const action = dialog.value === 'reprocess' ? reprocess : discard;
  let ok = 0;
  try {
    for (const d of selection.value) {
      await action.mutateAsync({ id: d.id, source: d.source, reason });
      ok++;
    }
    done.value = `${String(ok)} mensagem${ok === 1 ? '' : 's'} ${dialog.value === 'reprocess' ? 'devolvida' : 'descartada'}${ok === 1 ? '' : 's'}.`;
    picked.value = new Set();
    dialog.value = null;
  } catch (e) {
    failure.value = `${String(ok)} tratada${ok === 1 ? '' : 's'} antes da falha: ${e instanceof ApiError ? e.message : 'erro inesperado'}`;
  }
}
const busy = computed(() => reprocess.isPending.value || discard.isPending.value);
const canReprocess = computed(() => selection.value.length > 0 && selection.value.every((d) => d.can_reprocess));
const firstLine = (s: string) => s.split('\n')[0] ?? '';
</script>

<template>
  <div class="page">
    <header class="page-head">
      <div>
        <div class="colhead">Mensagens que não chegaram ao consumidor</div>
        <h1>DLQ</h1>
      </div>
      <v-btn-toggle v-model="status" mandatory density="compact" variant="outlined" divided color="primary" aria-label="Filtro de estado">
        <v-btn value="open">Abertas</v-btn>
        <v-btn value="all">Histórico</v-btn>
      </v-btn-toggle>
    </header>

    <v-alert v-if="done" type="success" class="mb-3" closable @click:close="done = ''">{{ done }}</v-alert>
    <v-alert v-if="isError" type="error">Não foi possível carregar a DLQ.</v-alert>
    <div v-else-if="isPending" class="empty">Lendo a fila morta…</div>
    <div v-else-if="!items.length" class="empty">
      {{ status === 'open' ? 'DLQ vazia: todas as mensagens chegaram ao consumidor.' : 'Nenhuma mensagem passou pela DLQ.' }}
    </div>
    <template v-else>
      <div class="top">
        <div>
          <div class="colhead mb-1">Idade das mensagens abertas</div>
          <AgeHistogram :ages="open.map((d) => d.age_seconds)" />
        </div>
        <div class="actions">
          <div class="colhead mb-1">Ações por lote</div>
          <div class="meta mb-2">{{ selection.length }} selecionada{{ selection.length === 1 ? '' : 's' }}</div>
          <div class="d-flex ga-2 flex-wrap">
            <v-btn color="primary" :prepend-icon="mdiReplay" :disabled="!canReprocess" @click="failure = ''; dialog = 'reprocess'">Reprocessar</v-btn>
            <v-btn :disabled="!selection.length" @click="failure = ''; dialog = 'discard'">Descartar com motivo</v-btn>
          </div>
          <div v-if="selection.length && !canReprocess" class="meta mt-2">Alguma selecionada não tem origem no outbox e só pode ser descartada.</div>
        </div>
      </div>

      <section v-for="[reason, group] in groups" :key="reason" class="group">
        <div class="group-head">
          <v-icon :icon="mdiTrayAlert" size="18" />
          <h2>{{ reasonLabel(reason) }}</h2>
          <span class="meta mono">{{ group.length }}</span>
          <v-spacer />
          <v-btn v-if="group.some((g) => g.status === 'open')" size="x-small" variant="text" @click="pickAll(group, !group.filter((g) => g.status === 'open').every((g) => picked.has(key(g))))">
            Selecionar o grupo
          </v-btn>
        </div>
        <table class="ledger" :aria-label="reasonLabel(reason)">
          <thead>
            <tr>
              <th style="width: 36px"></th>
              <th class="colhead">Mensagem</th>
              <th class="colhead">Origem</th>
              <th class="colhead">Idade</th>
              <th class="colhead num">Tentativas</th>
              <th class="colhead">Primeira linha do erro</th>
              <th class="colhead">Estado</th>
            </tr>
          </thead>
          <tbody>
            <template v-for="d in group" :key="key(d)">
              <tr class="clickable" @click="expanded = expanded === key(d) ? null : key(d)">
                <td @click.stop>
                  <v-checkbox-btn v-if="d.status === 'open'" :model-value="picked.has(key(d))" density="compact" :aria-label="`Selecionar ${d.message_id}`" @update:model-value="toggle(d)" />
                </td>
                <td class="mono">{{ d.message_id }}</td>
                <td>{{ d.source === 'broker' ? 'DLQ do broker' : 'Outbox' }}</td>
                <td><span class="age" :class="`lvl${String(ageLevel(d.age_seconds))}`">{{ formatAge(d.age_seconds) }}</span></td>
                <td class="num mono">{{ d.deliveries }}</td>
                <td class="mono err">{{ firstLine(d.error) }}</td>
                <td>
                  <template v-if="d.status === 'open'">aberta</template>
                  <template v-else>{{ d.status === 'requeued' ? 'reprocessada' : 'descartada' }} por {{ d.resolved_by }}</template>
                </td>
              </tr>
              <tr v-if="expanded === key(d)" class="detail">
                <td></td>
                <td colspan="6">
                  <div class="meta mb-1">Routing key <span class="mono">{{ d.routing_key }}</span> · falhou pela primeira vez em {{ formatDateTime(d.first_failed_at) }}</div>
                  <pre class="mono payload">{{ d.payload }}</pre>
                  <div v-if="d.resolution_note" class="meta">Motivo registrado: {{ d.resolution_note }}</div>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </section>
    </template>

    <ReasonDialog
      :model-value="dialog !== null"
      :title="dialog === 'reprocess' ? `Reprocessar ${String(selection.length)} mensagem(ns)` : `Descartar ${String(selection.length)} mensagem(ns)`"
      :confirm-label="dialog === 'reprocess' ? 'Reprocessar' : 'Descartar'"
      :warning="dialog === 'reprocess' ? 'Isto mexe com dinheiro: as mensagens voltam ao outbox e serão aplicadas aos saldos pelo consumidor, com o mesmo id (reentregas continuam sendo ignoradas).' : 'A mensagem sai da fila de tratamento e o saldo da projeção continua sem ela.'"
      :typed-word="dialog === 'reprocess' ? 'REPROCESSAR' : ''"
      :busy="busy"
      :error="failure"
      @update:model-value="dialog = $event ? dialog : null"
      @confirm="confirm"
    />
  </div>
</template>

<style scoped>
.top {
  display: flex;
  gap: 48px;
  flex-wrap: wrap;
  margin-bottom: 20px;
}
.group {
  background: rgb(var(--v-theme-surface));
  border: 1px solid var(--rule);
  margin-bottom: 16px;
  overflow-x: auto;
}
.group-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  border-bottom: 1px solid rgb(var(--v-theme-on-surface));
}
.age {
  font-family: var(--font-mono);
  font-weight: 500;
  padding: 1px 6px;
  border-left: 3px solid rgb(var(--v-theme-warning));
}
.lvl0 {
  background: transparent;
  border-left-color: rgba(var(--v-theme-warning), 0.35);
}
.lvl1 {
  background: rgba(var(--v-theme-warning), 0.12);
  border-left-color: rgba(var(--v-theme-warning), 0.6);
}
.lvl2 {
  background: rgba(var(--v-theme-warning), 0.22);
}
.lvl3 {
  background: rgb(var(--v-theme-extra-container));
  color: rgb(var(--v-theme-on-extra-container));
}
.err {
  color: rgb(var(--v-theme-error));
  max-width: 420px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.detail td {
  height: auto;
  padding: 10px;
}
.payload {
  margin: 4px 0;
  padding: 8px 10px;
  background: rgb(var(--v-theme-surface-variant));
  font-size: 12px;
  overflow-x: auto;
  user-select: all;
}
</style>
