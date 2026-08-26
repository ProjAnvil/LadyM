<script setup>
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { auth, clearSession } from './api.js'

const route = useRoute()
const router = useRouter()

const isLogin = computed(() => route.name === 'login')

function logout() {
  clearSession()
  router.push({ name: 'login' })
}
</script>

<template>
  <div v-if="isLogin" class="login-wrap">
    <router-view />
  </div>

  <div v-else class="layout">
    <aside class="sidebar">
      <div class="brand">LadyM Console</div>
      <nav>
        <router-link to="/" :class="{ active: route.name === 'memories' }">Memories</router-link>
        <router-link v-if="auth.admin" to="/users" :class="{ active: route.name === 'users' }">Users</router-link>
        <router-link to="/stats" :class="{ active: route.name === 'stats' }">Stats</router-link>
        <router-link v-if="auth.admin" to="/settings" :class="{ active: route.name === 'settings' }">Settings</router-link>
      </nav>
      <div class="who">
        <template v-if="auth.noauth">auth disabled</template>
        <template v-else>
          {{ auth.username }}
          <span v-if="auth.admin" class="badge">admin</span>
          <div v-if="auth.workspace" class="muted">ws: {{ auth.workspace }}</div>
        </template>
        <button v-if="!auth.noauth" class="secondary" @click="logout">Log out</button>
      </div>
    </aside>
    <main class="content">
      <router-view />
    </main>
  </div>
</template>
