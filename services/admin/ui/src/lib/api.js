// @ts-check
/**
 * @typedef {Object} ApiEnvelope
 * @property {boolean} [success]
 * @property {string} [error]
 * @property {string} [error_code]
 * @property {any} [data]
 */
export const BASE = '/api/admin';
const FETCH_TIMEOUT = 30000;

// ApiError 保留服务端 error_code 与原始响应体，供 UI 按业务分支展示。
// message 始终是服务端 error 原文（原始错误信息完整透传，不被统一文案覆盖）。
export class ApiError extends Error {
  /**
   * @param {string} message
   * @param {{ code?: string, details?: any }} [opts]
   */
  constructor(message, { code = '', details = null } = {}) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.details = details;
  }
}

/**
 * @param {string} path
 * @param {{ headers?: Record<string, string>, body?: BodyInit, method?: string, timeout?: number, signal?: AbortSignal }} [opts]
 * @returns {Promise<any>}
 */
async function req(path, opts = {}) {
  const hasBody = opts.body !== undefined;
  const headers = { ...opts.headers };
  if (hasBody && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json';
  }
  const ctrl = new AbortController();
  const timeout = opts.timeout || FETCH_TIMEOUT;
  const timer = setTimeout(() => ctrl.abort(), timeout);
  // 合并外部 signal 与超时 signal:此前 opts.signal 存在时直接替换,
  // 超时 abort 的是内部 ctrl 而 fetch 监听的是外部 signal,超时静默失效。
  // AbortSignal.any 让任一信号触发即取消(Chrome 116+/Safari 17+)。
  const signal =
    typeof AbortSignal.any === 'function' && opts.signal
      ? AbortSignal.any([opts.signal, ctrl.signal])
      : opts.signal || ctrl.signal;
  try {
    const r = await fetch(BASE + path, {
      credentials: 'same-origin',
      ...opts,
      signal,
      headers,
    });
    const json = await parseJSONResponse(r);
    if (!r.ok || !json.success) {
      throw new ApiError(json.error || `${r.status} ${r.statusText}`, {
        code: json.error_code,
        details: json,
      });
    }
    return json.data;
  } finally {
    clearTimeout(timer);
  }
}

/** @param {Response} response @returns {Promise<ApiEnvelope>} */
async function parseJSONResponse(response) {
  try {
    return await response.json();
  } catch (error) {
    if (!response.ok) {
      throw new Error(`${response.status} ${response.statusText}`, { cause: error });
    }
    throw error;
  }
}

/** @param {Response} response @returns {Promise<string>} */
async function responseErrorMessage(response) {
  try {
    const json = await response.json();
    if (json?.error) return json.error;
  } catch {
    // Fall through to the HTTP status when the body is not JSON.
  }
  return `${response.status} ${response.statusText}`;
}

export async function getCurrentUser() {
  return req('/auth/me');
}

/** 服务端健康信息(含构建版本):面板页脚展示,排障时报版本用。 */
export async function getHealth() {
  return req('/health');
}

export async function getDashboard() {
  return req('/dashboard');
}

export async function getServices() {
  return req('/services');
}

export async function serviceAction(name, action) {
  return req(`/services/${name}/${action}`, { method: 'POST' });
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
  return req('/users/password', {
    method: 'POST',
    body: JSON.stringify({ username, old_password: oldPassword, password }),
  });
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

// ── App 版本管理：历史 / 回滚 / 删除 / 下载 ──

export async function getBlockedIPs() {
  return req('/security/blocked');
}

export async function blockIP(ip, reason, duration) {
  return req('/security/block', { method: 'POST', body: JSON.stringify({ ip, reason, duration }) });
}

export async function unblockIP(ip) {
  return req('/security/unblock', { method: 'POST', body: JSON.stringify({ ip }) });
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

async function streamDownload(path, body, onProgress) {
  const ctrl = new AbortController();
  const timeout = setTimeout(() => ctrl.abort(), FETCH_TIMEOUT);
  const r = await fetch(BASE + path, {
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    method: 'POST',
    body: JSON.stringify(body),
    signal: ctrl.signal,
  });
  clearTimeout(timeout);
  if (!r.ok) throw new Error(await responseErrorMessage(r));
  if (!r.body) throw new Error('Empty response body');

  const reader = r.body.getReader();
  const decoder = new TextDecoder();
  let buf = '';
  let result = null;

  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });
      const lines = buf.split('\n');
      buf = lines.pop() ?? '';
      for (const line of lines) {
        if (!line.trim()) continue;
        let data;
        try {
          data = JSON.parse(line);
        } catch {
          continue;
        }
        if (data.type === 'progress' && onProgress) onProgress(data);
        if (data.type === 'error') throw new Error(data.error);
        if (data.type === 'done') result = data;
        if (data.type === 'start' && onProgress) onProgress({ bytes: 0, total: 0, percent: 0 });
      }
    }
    // Handle remaining buffer
    if (buf.trim()) {
      let data;
      try {
        data = JSON.parse(buf);
      } catch {
        /* ignore partial */
      }
      if (data) {
        if (data.type === 'error') throw new Error(data.error);
        if (data.type === 'done') result = data;
      }
    }
  } finally {
    // 错误中途退出时显式释放连接;正常读完后的 cancel 是无害 no-op。
    reader.cancel().catch(() => {});
  }

  // Non-streaming fallback (already_exists etc.)
  if (!result) {
    throw new Error('Download completed but no result received');
  }
  return { status: 'ok', filename: result.filename, size: result.size, checksum: result.checksum };
}

export async function downloadClaudeVersion(version, plat, onProgress) {
  return streamDownload('/claude-code/download', { version, plat }, onProgress);
}

export async function deleteClaudeDownload(filename) {
  // DELETE 统一 query 传参(不带 body)
  return req(`/claude-code/downloads?filename=${encodeURIComponent(filename)}`, {
    method: 'DELETE',
  });
}

// ── Xray downloads ──

export async function refreshXrayVersions() {
  return req('/xray/refresh', { method: 'POST' });
}

export async function getXrayReleases() {
  return req('/xray/releases');
}

export async function getXrayStored() {
  return req('/xray/stored');
}

export async function getXrayPlatforms(version) {
  return req(`/xray/platforms/${version}`);
}

export async function downloadXrayVersion(version, plat, onProgress) {
  return streamDownload('/xray/download', { version, plat }, onProgress);
}

export async function deleteXrayDownload(filename) {
  return req(`/xray/downloads?filename=${encodeURIComponent(filename)}`, { method: 'DELETE' });
}

// ── File transfer ──

export async function getTransferFiles() {
  return req('/transfer/files');
}

export async function deleteTransferFile(code) {
  return req(`/transfer/files?code=${encodeURIComponent(code)}`, { method: 'DELETE' });
}

export const transferUploadUrl = BASE + '/transfer/upload';

/** 拉取按用户累计的代理流量(xray StatsService,每分钟采集)。 */
export async function getTraffic() {
  return req('/traffic');
}
