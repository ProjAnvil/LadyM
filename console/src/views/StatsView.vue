<script setup>
import { onMounted, ref, computed } from 'vue'
import { apiFetch } from '../api.js'

const stats = ref(null)
const error = ref('')

async function load() {
  error.value = ''
  try {
    stats.value = await apiFetch('/api/stats', { method: 'POST', body: {} })
  } catch (e) {
    error.value = e.message
  }
}

const byLayer = computed(() => Object.entries(stats.value?.by_layer || {}))
const byType = computed(() => Object.entries(stats.value?.by_type || {}))

onMounted(load)
</script>

<template>
  <div class="page-head">
    <h1>Stats</h1>
    <button class="secondary" @click="load">刷新</button>
  </div>

  <p v-if="error" class="error-text">{{ error }}</p>

  <template v-if="stats">
    <div class="cards">
      <div class="card">
        <div class="num">{{ stats.total_memories }}</div>
        <div class="label">Total memories</div>
      </div>
      <div class="card">
        <div class="num">{{ stats.edges }}</div>
        <div class="label">Edges (L4)</div>
      </div>
      <div class="card">
        <div class="num">{{ stats.code_symbols }}</div>
        <div class="label">Code symbols</div>
      </div>
      <div class="card">
        <div class="num">{{ stats.avg_tokens_per_memory.toFixed(1) }}</div>
        <div class="label">Avg tokens / memory</div>
      </div>
      <div class="card">
        <div class="num">{{ (stats.workspaces || []).length }}</div>
        <div class="label">Workspaces</div>
      </div>
    </div>

    <div class="panel" style="margin-top:16px">
      <h3 style="margin-top:0">By layer</h3>
      <table>
        <tbody>
          <tr v-for="[k, v] in byLayer" :key="k">
            <td><span class="badge">{{ k }}</span></td>
            <td>{{ v }}</td>
          </tr>
          <tr v-if="byLayer.length === 0"><td class="muted">(none)</td></tr>
        </tbody>
      </table>
    </div>

    <div class="panel">
      <h3 style="margin-top:0">By type</h3>
      <table>
        <tbody>
          <tr v-for="[k, v] in byType" :key="k">
            <td>{{ k }}</td>
            <td>{{ v }}</td>
          </tr>
          <tr v-if="byType.length === 0"><td class="muted">(none)</td></tr>
        </tbody>
      </table>
    </div>

    <div class="panel">
      <h3 style="margin-top:0">Workspaces</h3>
      <span v-for="w in stats.workspaces" :key="w" class="badge">{{ w }}</span>
      <span v-if="!(stats.workspaces || []).length" class="muted">(none)</span>
      <p class="muted" style="margin-bottom:0">db: {{ stats.db_path || '(postgres)' }}</p>
    </div>
  </template>
</template>
