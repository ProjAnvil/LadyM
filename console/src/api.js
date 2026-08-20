// Fetch wrapper + session-scoped credential store for the LadyM console.
//
// Credentials live only in sessionStorage (base64 Basic token + the login
// response) for the lifetime of the tab. With auth disabled there are no
// credentials at all: requests go out without a header and every page works.

import { reactive } from 'vue'

const KEY = 'ladym.console.auth'

function load() {
  try {
    return JSON.parse(sessionStorage.getItem(KEY)) || null
  } catch {
    return null
  }
}

export const auth = reactive(load() || {
  // token: base64(username:password); null when auth is disabled or logged out.
  token: null,
  username: '',
  workspace: '',
  admin: false,
  noauth: false,
})

function persist() {
  sessionStorage.setItem(KEY, JSON.stringify({
    token: auth.token,
    username: auth.username,
    workspace: auth.workspace,
    admin: auth.admin,
    noauth: auth.noauth,
  }))
}

export function setSession(token, info) {
  auth.token = token
  auth.username = info.username || ''
  auth.workspace = info.workspace || ''
  auth.admin = !!info.admin
  auth.noauth = false
  persist()
}

// enterNoAuth marks an auth-disabled deployment so the router lets every page
// through without credentials.
export function enterNoAuth() {
  auth.token = null
  auth.username = ''
  auth.workspace = ''
  auth.admin = true // auth off = implicit trust; users management is open
  auth.noauth = true
  persist()
}

export function clearSession() {
  auth.token = null
  auth.username = ''
  auth.workspace = ''
  auth.admin = false
  auth.noauth = false
  sessionStorage.removeItem(KEY)
}

export function isAuthenticated() {
  return auth.noauth || auth.token !== null
}

// onUnauthorized is wired by main.js to redirect to /login.
let onUnauthorized = () => {}
export function setUnauthorizedHandler(fn) { onUnauthorized = fn }

// apiFetch performs a JSON request, attaching the Basic header when a session
// exists. A 401 clears the session and bounces to /login. Non-2xx responses
// throw Error with the server's {"error": ...} message.
export async function apiFetch(path, { method = 'GET', body } = {}) {
  const headers = {}
  if (auth.token) headers['Authorization'] = `Basic ${auth.token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const res = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  // Non-admin workspace enforcement is echoed back; keep it for the UI.
  const forcedWS = res.headers.get('X-Ladym-Workspace')
  if (forcedWS && forcedWS !== auth.workspace) {
    auth.workspace = forcedWS
    persist()
  }
  if (res.status === 401) {
    clearSession()
    onUnauthorized()
    throw new Error('unauthorized')
  }
  let data = null
  try {
    data = await res.json()
  } catch {
    // empty body
  }
  if (!res.ok) {
    throw new Error((data && data.error) || `HTTP ${res.status}`)
  }
  return data
}

// probeAuthDisabled returns true when /api/* answers without credentials
// (auth.enabled = false). Any 401 means auth is on.
export async function probeAuthDisabled() {
  const res = await fetch('/api/stats', { method: 'POST' })
  return res.status !== 401
}

// login verifies credentials against POST /api/login. The endpoint sits
// behind the auth middleware like every /api/* route, so the Basic header is
// sent together with the JSON body.
export async function login(username, password) {
  const token = btoa(`${username}:${password}`)
  const res = await fetch('/api/login', {
    method: 'POST',
    headers: {
      'Authorization': `Basic ${token}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ username, password }),
  })
  const data = await res.json().catch(() => null)
  if (!res.ok) {
    throw new Error((data && data.error) || `HTTP ${res.status}`)
  }
  setSession(token, data)
  return data
}
