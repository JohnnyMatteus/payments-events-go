<script setup lang="ts">
import { useField, useForm } from 'vee-validate';
import { computed, ref, watch } from 'vue';
import { z } from 'zod';

const props = withDefaults(
  defineProps<{
    modelValue: boolean;
    title: string;
    confirmLabel: string;
    warning?: string;
    /** Palavra que precisa ser digitada para liberar ações que mexem com dinheiro. */
    typedWord?: string;
    busy?: boolean;
    error?: string;
  }>(),
  { warning: '', typedWord: '', busy: false, error: '' },
);
const emit = defineEmits<{ 'update:modelValue': [boolean]; confirm: [reason: string] }>();

const reasonRule = z.string().trim().min(5, 'Descreva o motivo em pelo menos 5 caracteres.');
const { handleSubmit, resetForm } = useForm();
const reason = useField<string>('reason', (v) => {
  const r = reasonRule.safeParse(v ?? '');
  return r.success ? true : (r.error.issues[0]?.message ?? 'Motivo inválido.');
}, { initialValue: '' });
const typed = ref('');
const typedOk = computed(() => !props.typedWord || typed.value.trim() === props.typedWord);

watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      resetForm({ values: { reason: '' } });
      typed.value = '';
    }
  },
);
const submit = handleSubmit((values) => {
  emit('confirm', values.reason.trim());
});
</script>

<template>
  <v-dialog :model-value="modelValue" max-width="520" @update:model-value="emit('update:modelValue', $event)">
    <v-card class="pa-4" variant="flat" border>
      <h2 class="mb-2">{{ title }}</h2>
      <v-alert v-if="warning" type="warning" class="mb-3">{{ warning }}</v-alert>
      <form @submit.prevent="submit">
        <v-textarea
          v-model="reason.value.value"
          label="Motivo (fica na trilha de auditoria)"
          rows="2"
          auto-grow
          :error-messages="reason.errorMessage.value ?? []"
          autofocus
        />
        <v-text-field
          v-if="typedWord"
          v-model="typed"
          :label="`Digite ${typedWord} para confirmar`"
          autocomplete="off"
        />
        <v-alert v-if="error" type="error" class="mb-2">{{ error }}</v-alert>
        <div class="d-flex justify-end ga-2 mt-2">
          <v-btn variant="text" @click="emit('update:modelValue', false)">Cancelar</v-btn>
          <v-btn type="submit" color="primary" variant="flat" :disabled="!typedOk || busy" :loading="busy">{{ confirmLabel }}</v-btn>
        </div>
      </form>
    </v-card>
  </v-dialog>
</template>
