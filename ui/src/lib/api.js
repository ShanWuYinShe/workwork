const BASE = '/api/admin';

async function req(path, opts = {}) {
  const r = await fetch(BASE + path, {
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', ...opts.headers },
    ...opts
  });
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
  return r.json();
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
    credentials: 'include',
  });
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
  return r.json();
}

export async function getLogs(name, tail = 100) {
  return req(`/services/${name}/logs?tail=${tail}`);
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

export async function addAdminUser(username, password) {
  return req('/users/admin/add', { method: 'POST', body: JSON.stringify({ username, password }) });
}

export async function deleteAdminUser(username) {
  return req('/users/admin/delete', { method: 'POST', body: JSON.stringify({ username }) });
}
