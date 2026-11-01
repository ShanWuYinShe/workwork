// @ts-check
/**
 * 管理 API 的全部端点。页面只调用这些函数/URL 构造器，不关心传输层细节。
 */
import { apiURL, LONG_OPERATION_TIMEOUT, req, streamDownload } from './transport.js';

// ── 认证 / 健康 ──

export async function getCurrentUser() {
  return req('/auth/me');
}

/** 登出探测地址:Sidebar 用假凭证请求一次以覆盖浏览器缓存的 Basic Auth。 */
export function authProbeURL() {
  return apiURL('/auth/me');
}

/** 服务端健康信息(含构建版本):面板页脚展示,排障时报版本用。 */
export async function getHealth() {
  return req('/health');
}

// ── 仪表盘 / 服务 ──

export async function getDashboard() {
  return req('/dashboard');
}

export async function getServices() {
  return req('/services');
}

export async function serviceAction(name, action) {
  return req(`/services/${name}/${action}`, {
    method: 'POST',
    timeout: LONG_OPERATION_TIMEOUT,
  });
}

/** 日志 SSE 地址(EventSource 不能带自定义头,认证依赖同源 Cookie/Basic Auth)。 */
export function serviceLogsStreamURL(name, tail = 200) {
  return apiURL(`/services/${encodeURIComponent(name)}/logs/stream?tail=${tail}`);
}

// ── 用户 / Xray 客户端 ──

export async function getUsers() {
  return req('/users');
}

export async function addXrayClient(uuid, owner, name) {
  const body = { uuid };
  if (owner) body.owner = owner;
  if (name) body.name = name;
  return req('/users/xray/add', { method: 'POST', body: JSON.stringify(body) });
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

// ── 网络 / 安全 ──

export async function getNetworkStatus() {
  return req('/network/status');
}

export async function rotateIPv6() {
  return req('/network/rotate-ipv6', { method: 'POST', timeout: LONG_OPERATION_TIMEOUT });
}

export async function syncDNS() {
  return req('/network/sync-dns', { method: 'POST', timeout: LONG_OPERATION_TIMEOUT });
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

// ── Claude Code 版本下载 ──

export async function refreshClaudeVersions() {
  return req('/claude-code/refresh', { method: 'POST', timeout: 60000 });
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

export async function downloadClaudeVersion(version, plat, onProgress, opts = {}) {
  return streamDownload('/claude-code/download', { version, plat }, onProgress, opts);
}

export async function deleteClaudeDownload(filename) {
  // DELETE 统一 query 传参(不带 body)
  return req(`/claude-code/downloads?filename=${encodeURIComponent(filename)}`, {
    method: 'DELETE',
  });
}

// ── Xray 版本下载 ──

export async function refreshXrayVersions() {
  return req('/xray/refresh', { method: 'POST', timeout: 60000 });
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

export async function downloadXrayVersion(version, plat, onProgress, opts = {}) {
  return streamDownload('/xray/download', { version, plat }, onProgress, opts);
}

export async function deleteXrayDownload(filename) {
  return req(`/xray/downloads?filename=${encodeURIComponent(filename)}`, { method: 'DELETE' });
}

/**
 * 已下载文件的服务端地址(<a href> 直链下载)。
 * @param {'claude-code' | 'xray'} tool
 * @param {string} filename
 */
export function downloadFileURL(tool, filename) {
  // 文件名进 URL 路径段须编码:含 #/? 的名字会被截断成错误 URL。
  return apiURL(`/${tool}/downloads/${encodeURIComponent(filename)}`);
}

// ── 文件中转 / 流量 ──

export async function getTransferFiles() {
  return req('/transfer/files');
}

export async function deleteTransferFile(code) {
  return req(`/transfer/files?code=${encodeURIComponent(code)}`, { method: 'DELETE' });
}

export const transferUploadUrl = apiURL('/transfer/upload');

/** 对外分享链接(公开 /api/transfer/<code>,不属于管理 API 前缀)。 */
export function transferShareURL(code) {
  return window.location.origin + '/api/transfer/' + encodeURIComponent(code);
}

/** 拉取按用户累计的代理流量(xray StatsService,每分钟采集)。 */
export async function getTraffic() {
  return req('/traffic');
}
