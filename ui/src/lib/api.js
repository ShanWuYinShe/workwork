const BASE = '/api/admin';

async function req(path, opts = {}) {
  const hasBody = opts.body !== undefined;
  const headers = { ...opts.headers };
  if (hasBody && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json';
  }
  const r = await fetch(BASE + path, {
    credentials: 'same-origin',
    ...opts,
    headers,
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

export async function getServiceLogs(name, tail = 100) {
  return req(`/services/${name}/logs?tail=${tail}`);
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

export async function getXrayClientConfig(uuid) {
  return req(`/users/xray/config/${uuid}`);
}

export async function changePassword(username, oldPassword, password) {
  return req('/users/password', { method: 'POST', body: JSON.stringify({ username, old_password: oldPassword, password }) });
}

export async function addAdminUser(username, password, role = 'viewer') {
  return req('/users/admin/add', {
    method: 'POST',
    body: JSON.stringify({ username, password, role }),
  });
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

export async function getBlockedIPs() {
  return req('/security/blocked');
}

export async function blockIP(ip, reason, duration) {
  return req('/security/block', { method: 'POST', body: JSON.stringify({ ip, reason, duration }) });
}

export async function unblockIP(ip) {
  return req('/security/unblock', { method: 'POST', body: JSON.stringify({ ip }) });
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

export async function cliproxySetConfig(username, providers) {
  return req('/cliproxy/set-config', {
    method: 'POST',
    body: JSON.stringify({ username, providers }),
  });
}

export async function cliproxyCodexLogin(username) {
  return req('/cliproxy/codex-login', { method: 'POST', body: JSON.stringify({ username }) });
}

export async function getModelPool() {
  return req('/model-pool');
}

export async function setModelPool(providers) {
  return req('/model-pool', { method: 'POST', body: JSON.stringify({ providers }) });
}

export async function setModelPoolUser(username, granted) {
  return req('/model-pool/users', { method: 'POST', body: JSON.stringify({ username, granted }) });
}

export async function refreshClaudeVersions() {
  return req('/claude-code/refresh', { method: 'POST' });
}

export async function getClaudeReleases() {
  return req('/claude-code/releases');
}

export async function getClaudeStored() {
  return req('/claude-code/stored');
}

export async function getClaudePlatforms(version) {
  return req(`/claude-code/platforms/${version}`);
}

export async function downloadClaudeVersion(version, plat, onProgress) {
  const r = await fetch(BASE + '/claude-code/download', {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    method: 'POST',
    body: JSON.stringify({ version, plat }),
  });
  if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);

  const reader = r.body.getReader();
  const decoder = new TextDecoder();
  let buf = '';
  let result = null;

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    const lines = buf.split('\n');
    buf = lines.pop();
    for (const line of lines) {
      if (!line.trim()) continue;
      let data;
      try { data = JSON.parse(line); } catch { continue; }
      if (data.type === 'progress' && onProgress) onProgress(data);
      if (data.type === 'error') throw new Error(data.error);
      if (data.type === 'done') result = data;
      if (data.type === 'start' && onProgress) onProgress({ bytes: 0, total: 0, percent: 0 });
    }
  }
  // Handle remaining buffer
  if (buf.trim()) {
    let data;
    try { data = JSON.parse(buf); } catch { /* ignore partial */ }
    if (data) {
      if (data.type === 'error') throw new Error(data.error);
      if (data.type === 'done') result = data;
    }
  }

  // Non-streaming fallback (already_exists etc.)
  if (!result) {
    throw new Error('Download completed but no result received');
  }
  return { status: 'ok', filename: result.filename, size: result.size, checksum: result.checksum };
}

export async function deleteClaudeDownload(filename) {
  return req('/claude-code/downloads', { method: 'DELETE', body: JSON.stringify({ filename }) });
}
