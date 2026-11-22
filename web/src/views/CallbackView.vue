<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { get } from '@/api/client';
import { meSchema } from '@/api/schemas';
import { finishLogin } from '@/auth/oidc';
import { operator, role, setToken } from '@/auth/session';

const router = useRouter();
const error = ref('');

onMounted(async () => {
  try {
    const destination = await finishLogin();
    const me = await get(meSchema, '/me');
    operator.value = me.operator;
    role.value = me.role;
    await router.replace(destination);
  } catch {
    setToken(null);
    error.value = 'Não foi possível concluir o login. Tente entrar de novo; se persistir, confira se a sua conta tem acesso ao Razão.';
  }
});
</script>

<template>
  <main class="login">
    <v-alert v-if="error" type="error">{{ error }} <router-link to="/entrar">Voltar</router-link></v-alert>
    <p v-else class="meta">Concluindo o login...</p>
  </main>
</template>

<style scoped>
.login {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 24px;
}
</style>
