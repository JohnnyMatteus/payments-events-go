import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query';
import { createApp } from 'vue';
import App from './App.vue';
import vuetify from './plugins/vuetify';
import { router } from './router';
import './styles/main.css';

const app = createApp(App);
app.use(vuetify);
app.use(router);
app.use(VueQueryPlugin, {
  queryClient: new QueryClient({ defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: true, staleTime: 2_000 } } }),
});
app.mount('#app');
