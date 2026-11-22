import { computed, ref } from 'vue';

const KEY = 'razao.token';

function read(): string | null {
  try {
    return sessionStorage.getItem(KEY);
  } catch {
    return null;
  }
}

/** Token do operador: fica só na sessão da aba, não em localStorage. */
export const token = ref<string | null>(read());
export const operator = ref<string | null>(null);
export const role = ref<'leitor' | 'operador' | null>(null);
/** O leitor só consulta; a API devolve 403 nas ações, aqui só as escondemos. */
export const canAct = computed(() => role.value === 'operador');

export function setToken(value: string | null): void {
  token.value = value;
  if (!value) {
    operator.value = null;
    role.value = null;
  }
  try {
    if (value) sessionStorage.setItem(KEY, value);
    else sessionStorage.removeItem(KEY);
  } catch {
    // Sem storage (janela privada), a sessão vale até recarregar.
  }
}
