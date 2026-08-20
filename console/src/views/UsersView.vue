<script setup>
import { onMounted, reactive, ref } from 'vue'
import { apiFetch, auth } from '../api.js'

const users = ref([])
const loading = ref(false)
const error = ref('')
const toast = ref('')

function showToast(msg) {
  toast.value = msg
  setTimeout(() => { toast.value = '' }, 4000)
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await apiFetch('/api/users')
    users.value = data.users || []
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

function fmtTime(ts) {
  if (!ts) return ''
  return new Date(ts * 1000).toLocaleString()
}

/* ---------- create ---------- */
const showNew = ref(false)
const newUser = reactive({ username: '', password: '', workspace: '', admin: false })
const newError = ref('')
const newBusy = ref(false)

function openNew() {
  newUser.username = ''
  newUser.password = ''
  newUser.workspace = ''
  newUser.admin = false
  newError.value = ''
  showNew.value = true
}

async function submitNew() {
  newError.value = ''
  if (!newUser.username.trim()) {
    newError.value = 'username 不能为空'
    return
  }
  newBusy.value = true
  try {
    await apiFetch('/api/users', {
      method: 'POST',
      body: {
        username: newUser.username.trim(),
        password: newUser.password, // 空 = 免密用户
        workspace: newUser.workspace,
        admin: newUser.admin,
      },
    })
    showNew.value = false
    showToast(`已创建用户 ${newUser.username}`)
    load()
  } catch (e) {
    newError.value = e.message
  } finally {
    newBusy.value = false
  }
}

/* ---------- edit (password / workspace / admin) ---------- */
const editing = ref(null) // {username, password, workspace, admin}
const editError = ref('')
const editBusy = ref(false)

function openEdit(u) {
  editing.value = {
    username: u.username,
    password: '', // 留空 = 不改密码;显式清空用复选框
    clearPassword: false,
    workspace: u.workspace,
    admin: u.admin,
  }
  editError.value = ''
}

async function submitEdit() {
  editError.value = ''
  editBusy.value = true
  try {
    const body = { workspace: editing.value.workspace, admin: editing.value.admin }
    if (editing.value.clearPassword) body.password = ''
    else if (editing.value.password !== '') body.password = editing.value.password
    await apiFetch(`/api/users/${encodeURIComponent(editing.value.username)}`, {
      method: 'PUT',
      body,
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
async function remove(u) {
  if (!window.confirm(`删除用户 ${u.username} ?`)) return
  try {
    await apiFetch(`/api/users/${encodeURIComponent(u.username)}`, { method: 'DELETE' })
    showToast(`已删除 ${u.username}`)
    load()
  } catch (e) {
    showToast(`删除失败:${e.message}`)
  }
}

const isSelf = (u) => !auth.noauth && auth.username === u.username

onMounted(load)
</script>

<template>
  <div class="page-head">
    <h1>Users</h1>
    <button @click="openNew">新建用户</button>
  </div>

  <p v-if="error" class="error-text">{{ error }}</p>
  <div v-if="!loading && users.length === 0 && !error" class="panel empty">还没有用户</div>

  <div v-else class="panel" style="padding:0; overflow:auto">
    <table>
      <thead>
        <tr><th>Username</th><th>Workspace</th><th>Admin</th><th>Created</th><th></th></tr>
      </thead>
      <tbody>
        <tr v-for="u in users" :key="u.username">
          <td>
            {{ u.username }}
            <span v-if="isSelf(u)" class="badge">you</span>
          </td>
          <td class="muted">{{ u.workspace || '(server default)' }}</td>
          <td>{{ u.admin ? '✓' : '' }}</td>
          <td class="muted">{{ fmtTime(u.created_at) }}</td>
          <td style="white-space:nowrap">
            <button class="link" @click="openEdit(u)">编辑</button>
            <button class="link" style="color:var(--danger)" :disabled="isSelf(u)"
                    :title="isSelf(u) ? '不能删除自己' : ''" @click="remove(u)">删除</button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>

  <!-- create modal -->
  <div v-if="showNew" class="modal-backdrop" @click.self="showNew = false">
    <div class="modal">
      <h2>新建用户</h2>
      <label>
        <span>Username</span>
        <input v-model="newUser.username" type="text" autocomplete="off" />
      </label>
      <label>
        <span>Password(留空 = 免密用户)</span>
        <input v-model="newUser.password" type="password" autocomplete="new-password" />
      </label>
      <label>
        <span>Workspace(非 admin 用户的强制 workspace;留空 = 服务端默认)</span>
        <input v-model="newUser.workspace" type="text" />
      </label>
      <label class="checkbox-label">
        <input v-model="newUser.admin" type="checkbox" /> Admin
      </label>
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
      <h2>编辑用户 <span class="muted">{{ editing.username }}</span></h2>
      <label>
        <span>新密码(留空 = 不变)</span>
        <input v-model="editing.password" type="password" autocomplete="new-password"
               :disabled="editing.clearPassword" />
      </label>
      <label class="checkbox-label">
        <input v-model="editing.clearPassword" type="checkbox" /> 清空密码(改为免密用户)
      </label>
      <label>
        <span>Workspace</span>
        <input v-model="editing.workspace" type="text" />
      </label>
      <label class="checkbox-label">
        <input v-model="editing.admin" type="checkbox" /> Admin
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
