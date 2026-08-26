<script setup>
import { computed, onMounted, ref } from 'vue'
import { apiFetch } from '../api.js'

const status = ref(null)
const variants = ref([])
const selected = ref('zh')
const error = ref('')
const busy = ref(false) // download/remove in flight

async function load() {
  error.value = ''
  try {
    const data = await apiFetch('/api/cjk_dict')
    status.value = data.status
    variants.value = data.variants || []
    if (status.value?.variant) selected.value = status.value.variant
  } catch (e) {
    error.value = e.message
  }
}

const dictInstalled = computed(() => !!status.value?.available)
const sourceLabel = computed(() => {
  switch (status.value?.source) {
    case 'file': return `已下载 (${status.value.variant})`
    case 'embedded': return '内嵌于构建 (fulldict, zh)'
    default: return '未安装 (逐字回退分词)'
  }
})
const selectedInfo = computed(() =>
  variants.value.find((v) => v.name === selected.value))
const switchPending = computed(() =>
  dictInstalled.value && status.value?.source === 'file' &&
  selected.value !== status.value?.variant)

async function download() {
  error.value = ''
  busy.value = true
  try {
    const data = await apiFetch('/api/cjk_dict/download', {
      method: 'POST',
      body: { dict: selected.value },
    })
    status.value = data.status
  } catch (e) {
    error.value = e.message
    await load()
  } finally {
    busy.value = false
  }
}

async function remove() {
  error.value = ''
  if (!confirm('删除已下载的分词词典?对应语言将回退到逐字分词。')) return
  busy.value = true
  try {
    status.value = await apiFetch('/api/cjk_dict', { method: 'DELETE' })
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="page-head">
    <h1>Settings</h1>
    <button class="secondary" @click="load">刷新</button>
  </div>

  <p v-if="error" class="error-text">{{ error }}</p>

  <div class="panel" style="margin-top:16px">
    <h3 style="margin-top:0">Memory</h3>
    <template v-if="status">
      <p class="muted" style="margin-bottom:10px">
        状态: <span class="badge">{{ sourceLabel }}</span>
        <template v-if="status.source === 'file'">
          版本 {{ status.version }},{{ (status.bytes / 1048576).toFixed(1) }} MB,目录
          <code>{{ status.dir }}</code>
        </template>
      </p>

      <div class="toolbar">
        <label>
          <span>分词词典变体</span>
          <select v-model="selected" :disabled="busy || status.source === 'embedded'">
            <option v-for="v in variants" :key="v.name" :value="v.name">
              {{ v.name }} — {{ v.desc }}
            </option>
          </select>
        </label>
        <button :disabled="busy || status.source === 'embedded'" @click="download">
          {{ dictInstalled ? (switchPending ? '切换到此变体' : '重新下载') : '下载词典' }}
        </button>
        <button
          v-if="dictInstalled && status.source === 'file'"
          class="danger" :disabled="busy" @click="remove"
        >删除词典</button>
      </div>

      <p v-if="selectedInfo" class="muted" style="margin-bottom:6px">
        下载 {{ (selectedInfo.bytes / 1048576).toFixed(1) }} MB(sha256 校验,镜像
        jsDelivr → GitHub raw),下载后立即生效,无需重启。
      </p>
      <p v-if="status.source === 'embedded'" class="muted" style="margin-bottom:0">
        此构建以 <code>-tags fulldict</code> 编译,词典已内嵌,无需下载。
      </p>
      <p v-else-if="!dictInstalled" class="muted" style="margin-bottom:0">
        未下载词典时中文/日文/韩文仍可检索(逐字+二元组),词级分词效果更好。
      </p>
    </template>
    <p v-else class="muted">loading…</p>
  </div>
</template>
