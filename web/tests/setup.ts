import { config } from '@vue/test-utils';
import { createVuetify } from 'vuetify';

// jsdom não tem ResizeObserver, que o Vuetify usa em alguns componentes.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
};
config.global.plugins = [createVuetify()];
