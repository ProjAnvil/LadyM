import { createRouter, createWebHistory } from 'vue-router'
import { auth, isAuthenticated } from './api.js'
import LoginView from './views/LoginView.vue'
import MemoriesView from './views/MemoriesView.vue'
import UsersView from './views/UsersView.vue'
import StatsView from './views/StatsView.vue'
import SettingsView from './views/SettingsView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: LoginView },
    { path: '/', name: 'memories', component: MemoriesView },
    { path: '/users', name: 'users', component: UsersView, meta: { admin: true } },
    { path: '/stats', name: 'stats', component: StatsView },
    { path: '/settings', name: 'settings', component: SettingsView, meta: { admin: true } },
  ],
})

router.beforeEach((to) => {
  if (to.name === 'login') return true
  if (!isAuthenticated()) return { name: 'login' }
  if (to.meta.admin && !auth.admin) return { name: 'memories' }
  return true
})

export default router
