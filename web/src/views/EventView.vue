<script setup lang="ts">
import { computed } from 'vue';
import { useTrail } from '@/api/queries';
import EventTrail from '@/components/signature/EventTrail.vue';
import { eventTypeLabel } from '@/domain/status';

const props = defineProps<{ id: string }>();
const { data, isPending, isError } = useTrail(() => props.id);
const payload = computed(() => JSON.stringify(data.value?.payload ?? {}, null, 2));
</script>

<template>
  <div class="page">
    <header class="page-head">
      <div>
        <div class="colhead"><RouterLink to="/eventos">Eventos</RouterLink> / detalhe</div>
        <h1 class="mono id">{{ id }}</h1>
      </div>
      <span v-if="data" class="meta">{{ eventTypeLabel(data.event.event_type) }} · lojista {{ data.event.merchant_id || '—' }} · pagamento {{ data.event.payment_id || '—' }}</span>
    </header>

    <v-alert v-if="isError" type="error">Evento não encontrado.</v-alert>
    <div v-else-if="isPending" class="empty">Seguindo o evento pelo pipeline…</div>
    <template v-else-if="data">
      <EventTrail :trail="data" />
      <section class="mt-6">
        <div class="colhead mb-1">Payload recebido (assinatura já verificada)</div>
        <pre class="payload mono">{{ payload }}</pre>
      </section>
    </template>
  </div>
</template>

<style scoped>
.id {
  font-family: var(--font-mono);
  font-size: 20px;
  font-weight: 500;
}
.payload {
  background: rgb(var(--v-theme-surface));
  border: 1px solid var(--rule);
  padding: 12px 14px;
  font-size: 13px;
  line-height: 18px;
  overflow-x: auto;
  margin: 0;
  user-select: all;
}
a {
  color: rgb(var(--v-theme-primary));
}
</style>
