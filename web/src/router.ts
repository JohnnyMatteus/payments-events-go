import { createRouter, createWebHistory } from 'vue-router';
import { token } from '@/auth/session';

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/conciliacao' },
    { path: '/entrar', name: 'login', component: () => import('@/views/LoginView.vue'), meta: { public: true } },
    { path: '/conciliacao', name: 'reconciliation', component: () => import('@/views/ReconciliationView.vue') },
    { path: '/eventos', name: 'events', component: () => import('@/views/EventsView.vue') },
    { path: '/eventos/:id', name: 'event', component: () => import('@/views/EventView.vue'), props: true },
    { path: '/dlq', name: 'dlq', component: () => import('@/views/DeadLettersView.vue') },
    { path: '/saldos', name: 'balances', component: () => import('@/views/BalancesView.vue') },
  ],
});

router.beforeEach((to) => {
  if (!to.meta['public'] && !token.value) return { name: 'login', query: { voltar: to.fullPath } };
  if (to.name === 'login' && token.value) return { path: '/' };
  return true;
});
