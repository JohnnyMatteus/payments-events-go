import vue from '@vitejs/plugin-vue';
import { fileURLToPath, URL } from 'node:url';
import { defineConfig } from 'vitest/config';
import vuetify from 'vite-plugin-vuetify';

// O painel fala com a API sempre por /api/v1; o nginx (produção) e o proxy do Vite (dev) encaminham ao serviço.
const api = process.env['VITE_API_URL'] ?? 'http://localhost:8085';

export default defineConfig({
  plugins: [vue(), vuetify({ autoImport: true })],
  resolve: { alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) } },
  server: {
    port: Number(process.env['WEB_PORT'] ?? 5173),
    proxy: { '/api': { target: api, changeOrigin: true } },
  },
  test: {
    environment: 'happy-dom',
    include: ['tests/**/*.test.ts'],
    server: { deps: { inline: ['vuetify'] } },
    setupFiles: ['tests/setup.ts'],
  },
});
