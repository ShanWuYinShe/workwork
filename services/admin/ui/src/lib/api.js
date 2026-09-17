const BASE = '/api/admin';
const FETCH_TIMEOUT = 30000;
const MEDIA_CENTER_SAVE_TIMEOUT = 90000;
const APK_UPLOAD_TIMEOUT = 30 * 60 * 1000;

// ApiError 保留服务端 error_code 与原始响应体，供 UI 按业务分支展示。
// message 始终是服务端 error 原文（原始错误信息完整透传，不被统一文案覆盖）。
export class ApiError extends Error {
  constructor(message, { code = '', details = null } = {}) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.details = details;
  }
}

async function req(path, opts = {}) {
  const hasBody = opts.body !== undefined;
  const headers = { ...opts.headers };
  if (hasBody && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json';
  }
  const ctrl = new AbortController();
  const timeout = opts.timeout || FETCH_TIMEOUT;
  const timer = setTimeout(() => ctrl.abort(), timeout);
  const signal = opts.signal || ctrl.signal;
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

export async function getMediaCenterConfig() {
  return req('/media-center/config');
}

export async function saveMediaCenterConfig(config) {
  return req('/media-center/config', {
    method: 'PUT',
    body: JSON.stringify(config),
    timeout: MEDIA_CENTER_SAVE_TIMEOUT,
  });
}

export async function getMediaCenterCredentials() {
  return req('/media-center/credentials');
}

export async function saveMediaCenterCredentials(credentials) {
  return req('/media-center/credentials', {
    method: 'PUT',
    body: JSON.stringify({ credentials }),
    timeout: MEDIA_CENTER_SAVE_TIMEOUT,
  });
}

export async function getMediaCatalogOverview() {
  return req('/media-center/catalog/overview');
}

export async function getMediaCatalogItems(category, status = '', limit = 200, offset = 0) {
  const params = new URLSearchParams({ category });
  if (status) params.set('status', status);
  if (limit) params.set('limit', String(limit));
  if (offset > 0) params.set('offset', String(offset));
  return req(`/media-center/catalog/items?${params.toString()}`);
}

export async function reviewMediaCatalogItem(change) {
  return req('/media-center/catalog/review', {
    method: 'POST',
    body: JSON.stringify(change),
  });
}

export async function deleteMediaCatalogItem(item) {
  return req('/media-center/catalog/delete', {
    method: 'POST',
    body: JSON.stringify({
      categoryId: item.categoryId,
      provider: item.provider,
      nativeId: item.nativeId,
    }),
  });
}

export async function exportMediaCatalog(category = '', status = '') {
  const params = new URLSearchParams();
  if (category) params.set('category', category);
  if (status) params.set('status', status);
  const qs = params.toString();
  return req(`/media-center/catalog/export${qs ? `?${qs}` : ''}`, {
    timeout: MEDIA_CENTER_SAVE_TIMEOUT,
  });
}

export async function importMediaCatalog(doc, approved = false) {
  const list = (Array.isArray(doc?.items) ? doc.items : []).map((it) =>
    approved ? { ...it, status: 'approved' } : it,
  );
  return req('/media-center/catalog/import', {
    method: 'POST',
    body: JSON.stringify({ ...doc, items: list }),
    timeout: MEDIA_CENTER_SAVE_TIMEOUT,
  });
}

export async function getMediaAppRelease(variant) {
  const q = variant ? `?variant=${encodeURIComponent(variant)}` : '';
  return req('/media-center/app-release' + q);
}

export async function uploadMediaAppRelease(formData, variant) {
  const ctrl = new AbortController();
  // APK 最大 300MB，90s 的通用保存超时会在慢速网络下掐断上传，单独放宽。
  const timeout = setTimeout(() => ctrl.abort(), APK_UPLOAD_TIMEOUT);
  // clearTimeout 必须在 finally：fetch 抛错（网络/超时/4xx）时定时器若悬挂，
  // 30 分钟内会一直持有引用并在触发时 abort 一个已失效的 controller。
  try {
    if (variant) formData.append('variant', variant);
    const r = await fetch(BASE + '/media-center/app-release', {
      credentials: 'same-origin',
      method: 'POST',
      body: formData,
      signal: ctrl.signal,
    });
    const json = await parseJSONResponse(r);
    if (!r.ok || !json.success) throw new Error(json.error || `${r.status} ${r.statusText}`);
    return json.data;
  } finally {
    clearTimeout(timeout);
  }
}

// ── App 版本管理：历史 / 回滚 / 删除 / 下载 ──

export async function getMediaAppReleaseHistory(variant) {
  const q = variant ? `?variant=${encodeURIComponent(variant)}` : '';
  return req('/media-center/app-release/history' + q);
}

export async function rollbackMediaAppRelease(variant, versionCode) {
  return req('/media-center/app-release/rollback', {
    method: 'POST',
    body: JSON.stringify({ variant, versionCode }),
  });
}

export async function deleteMediaAppReleaseVersion(variant, versionCode) {
  return req('/media-center/app-release/history', {
    method: 'DELETE',
    body: JSON.stringify({ variant, versionCode }),
  });
}

// 管理员下载 APK（basic auth 同源自动携带凭证），触发浏览器另存为。
export function mediaAppReleaseDownloadUrl(variant, versionCode) {
  const params = new URLSearchParams();
  if (variant) params.set('variant', variant);
  if (versionCode) params.set('versionCode', String(versionCode));
  const qs = params.toString();
  // 必须指向 /download 端点：GET /app-release(无 download)返回的是 manifest JSON
  return `${BASE}/media-center/app-release/download${qs ? `?${qs}` : ''}`;
}

export function triggerMediaAppReleaseDownload(variant, versionCode) {
  const a = document.createElement('a');
  a.href = mediaAppReleaseDownloadUrl(variant, versionCode);
  a.download = '';
  document.body.appendChild(a);
  a.click();
  a.remove();
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
  return req('/claude-code/downloads', { method: 'DELETE', body: JSON.stringify({ filename }) });
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
  return req('/xray/downloads', { method: 'DELETE', body: JSON.stringify({ filename }) });
}

// ── File transfer ──

export async function getTransferFiles() {
  return req('/transfer/files');
}

export async function deleteTransferFile(code) {
  return req('/transfer/files', { method: 'DELETE', body: JSON.stringify({ code }) });
}

export const transferUploadUrl = BASE + '/transfer/upload';

// ── Music library ──

export async function getMusicLibrary() {
  return req('/music-library');
}

// 上传走组件内 XHR(进度/取消),这里只暴露端点 URL(同 transferUploadUrl 模式)。
export const musicLibraryUploadUrl = BASE + '/music-library/upload';
export const musicChunkUploadUrl = BASE + '/music-library/upload-chunk';

// 分片续传查询:返回 {received, total, fileName}(无会话时 received 为空)。
export async function getMusicChunks(uploadId) {
  return req(`/music-library/upload-chunk?uploadId=${encodeURIComponent(uploadId)}`);
}

export async function deleteMusicLibraryFile(name) {
  return req(`/music-library?name=${encodeURIComponent(name)}`, { method: 'DELETE' });
}

// ── NetEase account (QR login) ──

export async function neteaseLoginStart() {
  return req('/music/netease/login/start', { method: 'POST' });
}

export async function neteaseLoginPoll(key) {
  return req(`/music/netease/login/poll?key=${encodeURIComponent(key)}`);
}

export async function neteaseLoginState() {
  return req('/music/netease/login/state');
}

export async function neteaseLogout() {
  return req('/music/netease/login/logout', { method: 'POST' });
}
