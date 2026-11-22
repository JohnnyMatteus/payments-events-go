<script setup lang="ts">
import { mdiPlay, mdiUpload } from '@mdi/js';
import { computed, ref, watch } from 'vue';
import { ApiError } from '@/api/client';
import { useLatestRun, usePendingAdjustments, useRunItems, useStartRun, useStatements, useUploadStatement } from '@/api/queries';
import type { RunItem } from '@/api/schemas';
import { canAct } from '@/auth/session';
import CaseDrawer from '@/components/CaseDrawer.vue';
import LedgerSpread from '@/components/signature/LedgerSpread.vue';
import Money from '@/components/signature/Money.vue';
import Waterfall from '@/components/signature/Waterfall.vue';
import { formatDateTime, formatDay, formatMoney, shortDigest, yesterday } from '@/domain/format';
import { matchesFilter, stateOf, waterfallOf, type StateFilter } from '@/domain/ledger';

const date = ref(yesterday());
const filter = ref<StateFilter>('all');
const search = ref('');
const selected = ref<RunItem | null>(null);
const message = ref<{ type: 'success' | 'error'; text: string } | null>(null);

const statements = useStatements();
const statement = computed(() => statements.data.value?.items.find((s) => s.date === date.value));
const run = useLatestRun(date);
const items = useRunItems(() => run.data.value?.id);
const pendingAdj = usePendingAdjustments();

const visible = computed(() => (items.data.value?.items ?? []).filter((i) => matchesFilter(i, filter.value, search.value)));
const wf = computed(() => waterfallOf(items.data.value?.items ?? [], run.data.value?.statement_lines ?? 0));
const delta = computed(() => (run.data.value ? run.data.value.statement_net_cents - run.data.value.ledger_net_cents : 0));
const openCount = computed(() => (items.data.value?.items ?? []).filter((i) => stateOf(i) !== 'matched' && i.case_status !== 'explained' && i.case_status !== 'resolved').length);

watch(date, () => {
  selected.value = null;
  message.value = null;
});

const start = useStartRun();
const upload = useUploadStatement();
const fileInput = ref<HTMLInputElement | null>(null);

async function execute() {
  message.value = null;
  try {
    const r = await start.mutateAsync(date.value);
    message.value = r.status === 'failed' ? { type: 'error', text: `A execução falhou: ${r.error ?? 'erro ao ler o extrato'}` } : { type: 'success', text: `Conciliação #${String(r.id)} concluída.` };
  } catch (e) {
    message.value = { type: 'error', text: e instanceof ApiError ? e.message : 'Erro inesperado.' };
  }
}

async function onFile(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = '';
  if (!file) return;
  message.value = null;
  try {
    const r = await upload.mutateAsync({ date: date.value, csv: await file.text() });
    message.value = { type: 'success', text: `Extrato de ${formatDay(date.value)} gravado: ${String(r.lines)} linhas, hash ${shortDigest(r.statement.sha256)}.` };
  } catch (e) {
    message.value = { type: 'error', text: e instanceof ApiError ? e.message : 'Erro inesperado.' };
  }
}

const filters: { value: StateFilter; label: string }[] = [
  { value: 'all', label: 'Todos' },
  { value: 'open', label: 'Pendentes' },
  { value: 'missing', label: 'Faltando' },
  { value: 'extra', label: 'Sobrando' },
  { value: 'mismatch', label: 'Divergente' },
];
const selectedKey = computed(() => (selected.value ? `${selected.value.kind}|${selected.value.transaction_id}` : null));
const noRun = computed(() => run.isError.value);
</script>

<template>
  <div class="page">
    <header class="page-head">
      <div>
        <div class="colhead">Partida dobrada · extrato do provedor × ledger</div>
        <h1>Conciliação de {{ formatDay(date) }}</h1>
      </div>
      <div class="controls">
        <v-text-field v-model="date" type="date" label="Data de liquidação" style="width: 170px" />
        <input ref="fileInput" type="file" accept=".csv,text/csv" hidden @change="onFile" />
        <v-btn v-if="canAct" :prepend-icon="mdiUpload" :loading="upload.isPending.value" @click="fileInput?.click()">Enviar extrato</v-btn>
        <v-btn v-if="canAct" color="primary" variant="flat" :prepend-icon="mdiPlay" :loading="start.isPending.value" :disabled="!statement" @click="execute">Executar conciliação</v-btn>
      </div>
    </header>

    <v-alert v-if="message" :type="message.type" class="mb-3" closable @click:close="message = null">{{ message.text }}</v-alert>
    <v-alert v-if="pendingAdj.data.value?.items.length" type="info" class="mb-3">
      {{ pendingAdj.data.value.items.length }} ajuste(s) de ledger aguardando aprovação de outra pessoa:
      <span v-for="a in pendingAdj.data.value.items" :key="a.id" class="mono pend">#{{ a.id }} (por {{ a.proposed_by }})</span>
    </v-alert>

    <section class="ruled summary">
      <div class="cell">
        <div class="colhead">Extrato do dia</div>
        <template v-if="statement">
          <div class="mono">settlements/{{ statement.date }}.csv</div>
          <div class="meta mono">sha256 {{ shortDigest(statement.sha256) }} · {{ statement.size_bytes }} bytes</div>
          <div class="meta">recebido em {{ formatDateTime(statement.last_modified) }}</div>
        </template>
        <div v-else class="meta">Extrato de {{ formatDay(date) }} ainda não chegou no bucket. Envie o arquivo ou aguarde o depósito do provedor.</div>
      </div>
      <div class="cell">
        <div class="colhead">Última execução</div>
        <template v-if="run.data.value">
          <div>
            <strong :class="run.data.value.status === 'succeeded' ? 'ok' : 'bad'">{{ run.data.value.status === 'succeeded' ? 'concluída' : 'falhou' }}</strong>
            <span class="meta mono"> #{{ run.data.value.id }}</span>
          </div>
          <div class="meta">{{ formatDateTime(run.data.value.finished_at) }} · {{ run.data.value.triggered_by }} ({{ run.data.value.trigger === 'manual' ? 'painel' : run.data.value.trigger }})</div>
          <div v-if="run.data.value.error" class="bad mono">{{ run.data.value.error }}</div>
        </template>
        <div v-else class="meta">{{ noRun ? 'Este dia ainda não foi conciliado.' : 'Lendo…' }}</div>
      </div>
      <div v-if="run.data.value?.status === 'succeeded'" class="cell nets">
        <div class="colhead">Líquido do dia</div>
        <dl>
          <dt>Extrato</dt>
          <dd><Money :cents="run.data.value.statement_net_cents" /></dd>
          <dt>Ledger</dt>
          <dd><Money :cents="run.data.value.ledger_net_cents" /></dd>
          <dt class="strong">Delta</dt>
          <dd :class="delta === 0 ? 'ok' : 'mm'"><Money :cents="delta" plus /></dd>
        </dl>
        <div class="meta">{{ delta === 0 ? 'Os dois lados fecham no mesmo valor.' : `Faltam ${formatMoney(Math.abs(delta))} para fechar.` }}</div>
      </div>
      <div v-if="run.data.value?.status === 'succeeded'" class="cell chart">
        <div class="colhead mb-1">Cascata</div>
        <Waterfall :data="wf" />
      </div>
    </section>

    <div v-if="run.data.value?.status === 'succeeded'" class="bar">
      <v-btn-toggle v-model="filter" mandatory density="compact" variant="outlined" divided color="primary" aria-label="Filtro por estado">
        <v-btn v-for="f in filters" :key="f.value" :value="f.value">{{ f.label }}</v-btn>
      </v-btn-toggle>
      <v-text-field v-model="search" label="Buscar por id do provedor" style="max-width: 280px" clearable />
      <span class="meta">{{ openCount }} divergência(s) sem tratamento</span>
    </div>

    <div v-if="run.data.value?.status === 'succeeded'" class="layout" :class="{ withDrawer: selected?.case_id }">
      <div class="main">
        <div v-if="items.isPending.value" class="empty">Abrindo o livro…</div>
        <div v-else-if="!visible.length" class="empty">{{ items.data.value?.items.length ? 'Nenhuma linha neste filtro.' : 'O extrato e o ledger não têm linhas neste dia.' }}</div>
        <LedgerSpread v-else :items="visible" :selected-key="selectedKey" @select="selected = $event" />
        <p v-if="selected && !selected.case_id" class="meta mt-2">Pares conciliados não geram caso.</p>
      </div>
      <CaseDrawer v-if="selected?.case_id" :key="selected.case_id" :case-id="selected.case_id" :state="stateOf(selected)" @close="selected = null" />
    </div>
  </div>
</template>

<style scoped>
.controls {
  display: flex;
  gap: 12px;
  align-items: center;
  flex-wrap: wrap;
}
.summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  background: rgb(var(--v-theme-surface));
  border: 1px solid var(--rule);
  margin-bottom: 16px;
}
.summary .cell {
  padding: 12px 16px;
  border-right: 1px solid var(--rule);
}
.summary .chart {
  grid-column: span 2;
}
.nets dl {
  display: grid;
  grid-template-columns: 70px auto;
  row-gap: 2px;
  margin: 4px 0;
}
dd {
  margin: 0;
}
.strong {
  font-weight: 600;
}
.ok {
  color: rgb(var(--v-theme-success));
}
.bad {
  color: rgb(var(--v-theme-error));
}
.mm {
  color: rgb(var(--v-theme-mismatch));
}
.bar {
  display: flex;
  gap: 16px;
  align-items: center;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 16px;
  align-items: start;
}
.layout.withDrawer {
  grid-template-columns: minmax(0, 1fr) 380px;
}
.pend {
  margin-left: 12px;
}
@media (max-width: 1100px) {
  .layout.withDrawer {
    grid-template-columns: minmax(0, 1fr);
  }
  .summary .chart {
    grid-column: auto;
  }
}
</style>
