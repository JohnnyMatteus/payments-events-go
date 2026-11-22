<script setup lang="ts">
import { computed } from 'vue';
import { moneyParts } from '@/domain/format';

const props = withDefaults(defineProps<{ cents: number; currency?: string; plus?: boolean; compact?: boolean }>(), {
  currency: 'BRL',
  plus: false,
  compact: false,
});
const parts = computed(() => moneyParts(props.cents, props.currency, props.plus));
const symbol = computed(() => (props.currency === 'BRL' ? 'R$' : props.currency));
const spoken = computed(() => `${parts.value.sign === '−' ? 'menos ' : ''}${parts.value.whole} reais e ${parts.value.cents} centavos`);
</script>

<template>
  <span class="money" :aria-label="spoken">
    <span aria-hidden="true">
      <span v-if="!compact" class="cur">{{ symbol }}</span>{{ parts.sign }}{{ parts.whole }},<span class="cents">{{ parts.cents }}</span>
    </span>
  </span>
</template>
