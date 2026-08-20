<script setup>
import { onMounted, reactive, ref, computed } from 'vue'
import { apiFetch, auth } from '../api.js'

const LAYERS = ['L0_working', 'L1_episodic', 'L2_semantic', 'L3_procedural', 'L5_mental', 'L6_predictive']
const TYPES = ['note', 'event', 'fact', 'code_file', 'code_symbol', 'playbook', 'snippet', 'mental_model', 'forward_intent']
const PAGE_SIZE = 50

const filters = reactive({ workspace: '', layer: '', type: '' })
const workspaces = ref([])
const memories = ref([])
const total = ref(0)
const offset = ref(0)
const loading = ref(false)
const error = ref('')
const toast = ref('')

const page = computed(() => Math.floor(offset.value / PAGE_SIZE) + 1)
const pages = computed(() => Math.max(1, Math.ceil(total.value / PAGE_SIZE)))

function showToast(msg) {
  toast.value = msg
  setTimeout(() => { toast.value = '' }, 4000)
}

async function loadWorkspaces() {
  try {
    const st = await apiFetch('/api/stats', { method: 'POST', body: {} })
    workspaces.value = st.workspaces || []
  } catch {
    workspaces.value = []
  }
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const q = new URLSearchParams()
    if (filters.workspace) q.set('workspace', filters.workspace)
    if (filters.layer) q.set('layer', filters.layer)
    if (filters.type) q.set('type', filters.type)
    q.set('limit', String(PAGE_SIZE))
    q.set('offset', String(offset.value))
    const data = await apiFetch(`/api/memories?${q}`)
    memories.value = data.memories || []
    total.value = data.total || 0
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

function applyFilters() {
  offset.value = 0
  load()
}

function prev() { if (offset.value >= PAGE_SIZE) { offset.value -= PAGE_SIZE; load() } }
function next() { if (offset.value + PAGE_SIZE < total.value) { offset.value += PAGE_SIZE; load() } }

function fmtTime(ts) {
  if (!ts) return ''
  return new Date(ts * 1000).toLocaleString()
}

/* ---------- new memory ---------- */
const showNew = ref(false)
const newMem = reactive({ content: '', tags: '', workspace: '' })
const newError = ref('')
const newBusy = ref(false)

function openNew() {
  newMem.content = ''
  newMem.tags = ''
  newMem.workspace = auth.noauth || auth.admin ? (filters.workspace || '') : auth.workspace
  newError.value = ''
  showNew.value = true
}

function parseTags(s) {
  return s.split(',').map(t => t.trim()).filter(Boolean)
}

async function submitNew() {
  newError.value = ''
  if (!newMem.content.trim()) {
    newError.value = 'content 不能为空'
    return
  }
  newBusy.value = true
  try {
    const body = { content: newMem.content, tags: parseTags(newMem.tags) }
    if (newMem.workspace) body.workspace = newMem.workspace
    const res = await apiFetch('/api/remember', { method: 'POST', body })
    if (res.gated === 'dropped') {
      showToast(`未写入(注意力门控):${res.reason || 'duplicate/noise'}`)
    } else {
      showToast(`已创建 ${res.id}`)
    }
    showNew.value = false
    load()
  } catch (e) {
    newError.value = e.message
  } finally {
    newBusy.value = false
  }
}

/* ---------- edit ---------- */
const editing = ref(null) // {id, content, summary, tags}
const editError = ref('')
const editBusy = ref(false)

function openEdit(m) {
  editing.value = {
    id: m.id,
    content: m.content,
    summary: m.summary || '',
    tags: (m.tags || []).join(', '),
  }
  editError.value = ''
}

async function submitEdit() {
  editError.value = ''
  editBusy.value = true
  try {
    await apiFetch(`/api/memories/${encodeURIComponent(editing.value.id)}`, {
      method: 'PUT',
      body: {
        content: editing.value.content,
        summary: editing.value.summary,
        tags: parseTags(editing.value.tags),
      },
    })
    editing.value = null
    showToast('已保存')
    load()
  } catch (e) {
    editError.value = e.message
  } finally {
    editBusy.value = false
  }
}

/* ---------- delete ---------- */
async function remove(m) {
  if (!window.confirm(`删除记忆 ${m.id} ?`)) return
  try {
    await apiFetch(`/api/memories/${encodeURIComponent(m.id)}`, { method: 'DELETE' })
    showToast(`已删除 ${m.id}`)
    load()
  } catch (e) {
    showToast(`删除失败:${e.message}`)
  }
}

onMounted(() => {
  loadWorkspaces()
  load()
})
</script>

<template>
  <div class="page-head">
    <h1>Memories</h1>
    <button @click="openNew">新建记忆</button>
  </div>

  <div class="toolbar">
    <label>
      <span>Workspace</span>
      <select v-model="filters.workspace" @change="applyFilters">
        <option value="">(all)</option>
        <option v-for="w in workspaces" :key="w" :value="w">{{ w }}</option>
      </select>
    </label>
    <label>
      <span>Layer</span>
      <select v-model="filters.layer" @change="applyFilters">
        <option value="">(all)</option>
        <option v-for="l in LAYERS" :key="l" :value="l">{{ l }}</option>
      </select>
    </label>
    <label>
      <span>Type</span>
      <select v-model="filters.type" @change="applyFilters">
        <option value="">(all)</option>
        <option v-for="t in TYPES" :key="t" :value="t">{{ t }}</option>
      </select>
    </label>
    <div class="spacer"></div>
    <button class="secondary" @click="load">刷新</button>
  </div>

  <p v-if="error" class="error-text">{{ error }}</p>
  <div v-if="!loading && memories.length === 0 && !error" class="panel empty">没有匹配的记忆</div>

  <div v-else class="panel" style="padding:0; overflow:auto">
    <table>
      <thead>
        <tr>
          <th>ID</th><th>Layer</th><th>Type</th><th>Content</th>
          <th>Tags</th><th>Workspace</th><th>Created</th><th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="m in memories" :key="m.id">
          <td class="muted" :title="m.id">{{ m.id.slice(0, 8) }}</td>
          <td><span class="badge">{{ m.layer }}</span></td>
          <td>{{ m.type }}</td>
          <td><span class="snippet" :title="m.content">{{ m.summary || m.content }}</span></td>
          <td><span v-for="t in m.tags" :key="t" class="badge">{{ t }}</span></td>
          <td class="muted">{{ m.workspace }}</td>
          <td class="muted">{{ fmtTime(m.created_at) }}</td>
          <td style="white-space:nowrap">
            <button class="link" @click="openEdit(m)">编辑</button>
            <button class="link" style="color:var(--danger)" @click="remove(m)">删除</button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>

  <div class="pager">
    <button class="secondary" :disabled="offset === 0" @click="prev">← 上一页</button>
    <span>第 {{ page }} / {{ pages }} 页 · 共 {{ total }} 条</span>
    <button class="secondary" :disabled="offset + PAGE_SIZE >= total" @click="next">下一页 →</button>
  </div>

  <!-- new memory modal -->
  <div v-if="showNew" class="modal-backdrop" @click.self="showNew = false">
    <div class="modal">
      <h2>新建记忆</h2>
      <label>
        <span>Content</span>
        <textarea v-model="newMem.content" rows="5" placeholder="要记住的事实 / 笔记"></textarea>
      </label>
      <label>
        <span>Tags(逗号分隔)</span>
        <input v-model="newMem.tags" type="text" placeholder="ops, deploy" />
      </label>
      <label>
        <span>Workspace</span>
        <input v-model="newMem.workspace" type="text"
               :disabled="!auth.noauth && !auth.admin"
               :placeholder="auth.noauth || auth.admin ? '留空 = 服务端默认' : ''" />
      </label>
      <p v-if="!auth.noauth && !auth.admin" class="muted" style="margin-top:-4px">
        非 admin 用户的 workspace 由服务端强制为你的 workspace。
      </p>
      <p v-if="newError" class="error-text">{{ newError }}</p>
      <div class="modal-actions">
        <button class="secondary" @click="showNew = false">取消</button>
        <button :disabled="newBusy" @click="submitNew">创建</button>
      </div>
    </div>
  </div>

  <!-- edit modal -->
  <div v-if="editing" class="modal-backdrop" @click.self="editing = null">
    <div class="modal">
      <h2>编辑记忆 <span class="muted">{{ editing.id.slice(0, 8) }}</span></h2>
      <label>
        <span>Content</span>
        <textarea v-model="editing.content" rows="6"></textarea>
      </label>
      <label>
        <span>Summary</span>
        <input v-model="editing.summary" type="text" />
      </label>
      <label>
        <span>Tags(逗号分隔)</span>
        <input v-model="editing.tags" type="text" />
      </label>
      <p v-if="editError" class="error-text">{{ editError }}</p>
      <div class="modal-actions">
        <button class="secondary" @click="editing = null">取消</button>
        <button :disabled="editBusy" @click="submitEdit">保存</button>
      </div>
    </div>
  </div>

  <div v-if="toast" class="toast" :class="{ ok: !toast.includes('失败') }">{{ toast }}</div>
</template>
