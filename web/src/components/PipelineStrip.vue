<script setup lang="ts">
import { computed } from 'vue';
import { usePipeline } from '@/api/queries';
import { formatAge } from '@/domain/format';

const { data, isError } = usePipeline();

const cells = computed(() => {
  const p = data.value;
  if (!p) return [];
  return [
    { label: 'Outbox pendente', value: String(p.outbox_pending), note: p.outbox_pending ? `mais antiga ${formatAge(p.oldest_pending_age_seconds)}` : 'vazio', alert: p.oldest_pending_age_seconds > 60 },
    { label: 'Publicadas', value: `${String(p.published_last_minute)}/min`, note: `${String(p.published_last_hour)} na última hora`, alert: false },
    { label: 'Consumidor', value: p.last_consumed_at ? 'ativo' : 'sem consumo', note: p.last_consumed_at ? `último há ${formatAge((Date.now() - new Date(p.last_consumed_at).getTime()) / 1000)}` : 'nada consumido ainda', alert: false },
    { label: 'DLQ aberta', value: String(p.dead_letters_open), note: p.dead_letters_open ? `mais antiga ${formatAge(p.oldest_dead_letter_age_seconds)}` : 'nenhuma', alert: p.dead_letters_open > 0 },
  ];
});
</script>

<template>
  <div class="strip" role="status" aria-label="Saúde do pipeline">
    <span v-if="isError" class="err">Não foi possível ler a saúde do pipeline.</span>
    <span v-for="c in cells" :key="c.label" class="cell">
      <span class="colhead">{{ c.label }}</span>
      <span class="mono val" :class="{ alert: c.alert }">{{ c.value }}</span>
      <span class="meta note">{{ c.note }}</span>
    </span>
  </div>
</template>

<style scoped>
.strip {
  display: flex;
  gap: 28px;
  flex-wrap: wrap;
  align-items: baseline;
  padding: 6px 24px;
  background: rgb(var(--v-theme-surface-variant));
  border-bottom: 1px solid var(--rule);
  min-height: 32px;
}
.cell {
  display: inline-flex;
  gap: 8px;
  align-items: baseline;
}
.val {
  font-weight: 500;
  font-size: 13px;
}
.val.alert {
  color: rgb(var(--v-theme-warning));
}
.note {
  font-size: 12px;
}
.err {
  color: rgb(var(--v-theme-error));
}
@media (max-width: 700px) {
  .strip {
    gap: 12px;
    padding: 6px 12px;
  }
  .note {
    display: none;
  }
}
</style>
