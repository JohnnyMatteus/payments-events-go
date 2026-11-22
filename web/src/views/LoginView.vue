<script setup lang="ts">
import { ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { get } from '@/api/client';
import { meSchema } from '@/api/schemas';
import { operator, setToken } from '@/auth/session';

const route = useRoute();
const router = useRouter();
const value = ref('');
const error = ref('');
const busy = ref(false);

async function enter() {
  busy.value = true;
  error.value = '';
  setToken(value.value.trim());
  try {
    operator.value = (await get(meSchema, '/me')).operator;
    await router.replace(typeof route.query['voltar'] === 'string' ? route.query['voltar'] : '/');
  } catch {
    setToken(null);
    error.value = 'Token não reconhecido. Peça o seu a quem administra a API.';
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <main class="login">
    <section class="sheet" aria-labelledby="t">
      <div class="colhead">payments-events-go</div>
      <h1 id="t">Razão</h1>
      <p class="meta">Extrato do provedor de um lado, ledger do outro. Entre com o token de operador: toda decisão fica registrada com o seu nome.</p>
      <form @submit.prevent="enter">
        <v-text-field v-model="value" label="Token de operador" type="password" autocomplete="current-password" autofocus />
        <v-alert v-if="error" type="error" class="mb-3">{{ error }}</v-alert>
        <v-btn type="submit" color="primary" variant="flat" :loading="busy" :disabled="!value.trim()">Entrar</v-btn>
      </form>
    </section>
  </main>
</template>

<style scoped>
.login {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 24px;
}
.sheet {
  width: min(420px, 100%);
  background: rgb(var(--v-theme-surface));
  border: 1px solid var(--rule);
  border-top: 3px solid rgb(var(--v-theme-primary));
  padding: 28px 28px 24px;
}
h1 {
  margin: 4px 0 8px;
}
p {
  margin-bottom: 16px;
}
</style>
