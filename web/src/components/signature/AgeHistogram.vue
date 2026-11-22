<script setup lang="ts">
import { computed } from 'vue';

const props = defineProps<{ ages: number[] }>();

const buckets = [
  { label: '< 5 min', max: 300 },
  { label: '5–30 min', max: 1_800 },
  { label: '30 min–2 h', max: 7_200 },
  { label: '2–24 h', max: 86_400 },
  { label: '> 24 h', max: Infinity },
];

const counts = computed(() => {
  const out = buckets.map(() => 0);
  for (const age of props.ages) {
    const i = buckets.findIndex((b) => age < b.max);
    out[i === -1 ? out.length - 1 : i] = (out[i === -1 ? out.length - 1 : i] ?? 0) + 1;
  }
  return out;
});
const peak = computed(() => Math.max(1, ...counts.value));
const W = 360;
const H = 120;
const barW = 48;
const gap = 20;
</script>

<template>
  <svg :viewBox="`0 0 ${String(W)} ${String(H)}`" class="hist" role="img" aria-label="Mensagens na DLQ por faixa de idade">
    <title>Idade das mensagens na DLQ</title>
    <g v-for="(b, i) in buckets" :key="b.label" :transform="`translate(${String(8 + i * (barW + gap))} 0)`">
      <rect
        :y="84 - (84 * (counts[i] ?? 0)) / peak"
        :width="barW"
        :height="(84 * (counts[i] ?? 0)) / peak"
        class="bar"
        :style="{ opacity: String(0.35 + i * 0.16) }"
      />
      <text :x="barW / 2" :y="78 - (84 * (counts[i] ?? 0)) / peak" text-anchor="middle" class="val">{{ counts[i] }}</text>
      <text :x="barW / 2" y="102" text-anchor="middle" class="lbl">{{ b.label }}</text>
    </g>
    <line x1="0" :x2="W" y1="84" y2="84" class="axis" />
  </svg>
</template>

<style scoped>
.hist {
  width: 100%;
  max-width: 420px;
  height: auto;
}
.bar {
  fill: rgb(var(--v-theme-warning));
}
.axis {
  stroke: rgb(var(--v-theme-on-surface));
  stroke-width: 1;
}
.lbl {
  font-size: 10px;
  fill: rgb(var(--v-theme-secondary));
  font-family: var(--font-ui);
}
.val {
  font-size: 11px;
  font-family: var(--font-mono);
  font-weight: 500;
  fill: rgb(var(--v-theme-on-surface));
}
</style>
