<script setup lang="ts">
import { computed } from 'vue';
import type { Waterfall } from '@/domain/ledger';

const props = defineProps<{ data: Waterfall }>();

interface Step {
  label: string;
  value: number;
  start: number;
  tone: string;
}

const total = computed(() => Math.max(1, props.data.matched + props.data.missing + props.data.extra + props.data.mismatch + props.data.other));
const steps = computed<Step[]>(() => {
  const d = props.data;
  const out: Step[] = [];
  let at = 0;
  const push = (label: string, value: number, tone: string) => {
    out.push({ label, value, start: at, tone });
    at += value;
  };
  push('Conciliado', d.matched, 'success');
  push('Faltando no ledger', d.missing, 'error');
  push('Sobrando no ledger', d.extra, 'warning');
  push('Valor diferente', d.mismatch, 'mismatch');
  if (d.other > 0) push('Repetido no extrato', d.other, 'secondary');
  return out;
});

const W = 560;
const left = 150;
const right = 56;
const rowH = 26;
const scale = (n: number) => ((W - left - right) * n) / total.value;
const height = computed(() => (steps.value.length + 1) * rowH + 8);
</script>

<template>
  <svg :viewBox="`0 0 ${String(W)} ${String(height)}`" class="waterfall" role="img" aria-label="Cascata da conciliação em número de linhas">
    <title>Cascata da conciliação</title>
    <g>
      <text :x="left - 8" y="17" text-anchor="end" class="lbl">Total de linhas</text>
      <rect :x="left" y="6" :width="scale(total)" height="16" class="bar total" />
      <text :x="left + scale(total) + 6" y="18" class="val">{{ total }}</text>
    </g>
    <g v-for="(s, i) in steps" :key="s.label" :transform="`translate(0 ${String((i + 1) * rowH)})`">
      <text :x="left - 8" y="17" text-anchor="end" class="lbl">{{ s.label }}</text>
      <line :x1="left" :x2="W - right" y1="14" y2="14" class="guide" />
      <rect :x="left + scale(s.start)" y="6" :width="Math.max(scale(s.value), s.value > 0 ? 2 : 0)" height="16" :class="['bar', s.tone]" />
      <text :x="left + scale(s.start + s.value) + 6" y="18" class="val">{{ s.value }}</text>
    </g>
  </svg>
</template>

<style scoped>
.waterfall {
  width: 100%;
  max-width: 620px;
  height: auto;
}
.lbl {
  font-size: 12px;
  fill: rgb(var(--v-theme-secondary));
  font-family: var(--font-ui);
}
.val {
  font-size: 12px;
  font-family: var(--font-mono);
  font-weight: 500;
  fill: rgb(var(--v-theme-on-surface));
}
.guide {
  stroke: rgba(var(--v-theme-on-surface), 0.12);
  stroke-dasharray: 2 3;
}
.bar.total {
  fill: rgba(var(--v-theme-on-surface), 0.35);
}
.bar.success {
  fill: rgb(var(--v-theme-success));
}
.bar.error {
  fill: rgb(var(--v-theme-error));
}
.bar.warning {
  fill: rgb(var(--v-theme-warning));
}
.bar.mismatch {
  fill: rgb(var(--v-theme-mismatch));
}
.bar.secondary {
  fill: rgb(var(--v-theme-secondary));
}
</style>
