const BASE = '/api/admin';

async function req(path, opts = {}) {
  const r = await fetch(BASE + path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', ...opts.headers },
    ...opts
  });
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
  const json = await r.json();
  if (!json.success) throw new Error(json.error || 'API error');
  return json.data;
}

export async function getCurrentUser() {
  return req('/auth/me');
}

export async function getDashboard() {
  return req('/dashboard');
}

export async function getServices() {
  return req('/services');
}

export async function serviceAction(name, action) {
  const r = await fetch(`${BASE}/services/${name}/${action}`, {
    method: 'POST',
    credentials: 'same-origin',
  });
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
  const json = await r.json();
  if (!json.success) throw new Error(json.error || 'API error');
  return json.data;
}

export async function getConfig() {
  return req('/config');
}

export async function getUsers() {
  return req('/users');
}

export async function addXrayClient(uuid) {
  return req('/users/xray/add', { method: 'POST', body: JSON.stringify({ uuid }) });
}

export async function deleteXrayClient(uuid) {
  return req('/users/xray/delete', { method: 'POST', body: JSON.stringify({ uuid }) });
}

export async function changePassword(username, password) {
  return req('/users/password', { method: 'POST', body: JSON.stringify({ username, password }) });
}

export async function addAdminUser(username, password, role = 'viewer') {
  return req('/users/admin/add', { method: 'POST', body: JSON.stringify({ username, password, role }) });
}

export async function deleteAdminUser(username) {
  return req('/users/admin/delete', { method: 'POST', body: JSON.stringify({ username }) });
}

export async function setUserRole(username, role) {
  return req('/users/roles/set', { method: 'POST', body: JSON.stringify({ username, role }) });
}

export async function getNetworkStatus() {
  return req('/network/status');
}

export async function rotateIPv6() {
  return req('/network/rotate-ipv6', { method: 'POST' });
}

export async function syncDNS() {
  return req('/network/sync-dns', { method: 'POST' });
}

export async function getSecurityJails() {
  return req('/security/jails');
}

export async function banIP(ip) {
  return req('/security/blacklist/ban', { method: 'POST', body: JSON.stringify({ ip }) });
}

export async function unbanIP(ip) {
  return req('/security/blacklist/unban', { method: 'POST', body: JSON.stringify({ ip }) });
}

export async function getCliproxyInstances() {
  return req('/cliproxy/instances');
}

export async function cliproxyStart(username) {
  return req('/cliproxy/start', { method: 'POST', body: JSON.stringify({ username }) });
}

export async function cliproxyStop(username) {
  return req('/cliproxy/stop', { method: 'POST', body: JSON.stringify({ username }) });
}

export async function cliproxyGetConfig(username) {
  return req('/cliproxy/get-config', { method: 'POST', body: JSON.stringify({ username }) });
}

export async function cliproxySetConfig(username, api_keys) {
  return req('/cliproxy/set-config', { method: 'POST', body: JSON.stringify({ username, api_keys }) });
}

export async function cliproxyCodexLogin(username) {
  return req('/cliproxy/codex-login', { method: 'POST', body: JSON.stringify({ username }) });
}
