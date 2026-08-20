<script setup>
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { login, probeAuthDisabled, enterNoAuth } from '../api.js'

const router = useRouter()
const username = ref('')
const password = ref('')
const error = ref('')
const busy = ref(false)
const authDisabled = ref(null) // null = probing

onMounted(async () => {
  try {
    authDisabled.value = await probeAuthDisabled()
  } catch {
    authDisabled.value = false
  }
})

async function submit() {
  error.value = ''
  busy.value = true
  try {
    await login(username.value, password.value)
    router.push({ name: 'memories' })
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}

function enter() {
  enterNoAuth()
  router.push({ name: 'memories' })
}
</script>

<template>
  <div class="login-box">
    <h1>LadyM Console</h1>

    <template v-if="authDisabled === true">
      <p class="muted">此实例未启用认证(auth.enabled = false),无需登录。</p>
      <button type="button" style="width:100%" @click="enter">直接进入</button>
    </template>

    <form v-else @submit.prevent="submit">
      <label>
        <span>Username</span>
        <input v-model="username" type="text" autocomplete="username" required />
      </label>
      <label>
        <span>Password</span>
        <input v-model="password" type="password" autocomplete="current-password" placeholder="留空 = 免密用户" />
      </label>
      <p v-if="error" class="error-text">{{ error }}</p>
      <button type="submit" :disabled="busy">{{ busy ? 'Signing in…' : 'Sign in' }}</button>
    </form>
  </div>
</template>
