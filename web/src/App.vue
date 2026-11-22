<script setup lang="ts">
import { mdiLogout, mdiThemeLightDark } from '@mdi/js';
import { useStorage } from '@vueuse/core';
import { watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useTheme } from 'vuetify';
import { operator, setToken, token } from '@/auth/session';
import { useMe } from '@/api/queries';
import PipelineStrip from '@/components/PipelineStrip.vue';

const theme = useTheme();
const mode = useStorage<'razaoLight' | 'razaoDark'>('razao.theme', 'razaoLight');
watch(
  mode,
  (m) => {
    theme.global.name.value = m;
  },
  { immediate: true },
);

const route = useRoute();
const router = useRouter();
const me = useMe(() => !!token.value);
watch(
  () => me.data.value,
  (d) => {
    if (d) operator.value = d.operator;
  },
);
watch(token, (t) => {
  if (!t && route.name !== 'login') void router.push({ name: 'login' });
});

const tabs = [
  { to: '/conciliacao', label: 'Conciliação' },
  { to: '/eventos', label: 'Eventos' },
  { to: '/dlq', label: 'DLQ' },
  { to: '/saldos', label: 'Saldos' },
];
const isActive = (to: string) => route.path === to || route.path.startsWith(`${to}/`);

function logout() {
  setToken(null);
}
</script>

<template>
  <v-app>
    <v-app-bar v-if="token">
      <span class="brand serif">Razão</span>
      <nav class="tabs" aria-label="Seções">
        <RouterLink v-for="t in tabs" :key="t.to" :to="t.to" class="tab" :class="{ active: isActive(t.to) }" :aria-current="isActive(t.to) ? 'page' : undefined">
          {{ t.label }}
        </RouterLink>
      </nav>
      <v-spacer />
      <span v-if="operator" class="meta mono op">{{ operator }}</span>
      <v-btn variant="text" icon size="small" aria-label="Alternar tema claro e escuro" @click="mode = mode === 'razaoLight' ? 'razaoDark' : 'razaoLight'">
        <v-icon :icon="mdiThemeLightDark" size="18" />
      </v-btn>
      <v-btn variant="text" icon size="small" aria-label="Sair" @click="logout"><v-icon :icon="mdiLogout" size="18" /></v-btn>
    </v-app-bar>
    <v-main>
      <PipelineStrip v-if="token" />
      <RouterView />
    </v-main>
  </v-app>
</template>

<style scoped>
.brand {
  font-size: 20px;
  padding: 0 24px 0 20px;
  color: rgb(var(--v-theme-primary));
}
.tabs {
  display: flex;
  height: 100%;
}
.tab {
  display: flex;
  align-items: center;
  padding: 0 16px;
  color: rgb(var(--v-theme-on-surface));
  text-decoration: none;
  border-bottom: 2px solid transparent;
  height: 100%;
}
.tab:hover {
  background: rgba(var(--v-theme-primary), 0.06);
}
.tab.active {
  color: rgb(var(--v-theme-primary));
  border-bottom-color: rgb(var(--v-theme-primary));
  font-weight: 600;
}
.op {
  margin-right: 8px;
  font-size: 12px;
}
@media (max-width: 700px) {
  .brand {
    padding: 0 12px;
  }
  .tab {
    padding: 0 8px;
  }
  .op {
    display: none;
  }
}
</style>
