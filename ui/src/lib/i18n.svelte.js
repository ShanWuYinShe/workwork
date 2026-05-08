const zh = {
  overview: '概览', manage: '管理', system: '系统',
  dashboard: '仪表盘', services: '服务', clients: '客户端',
  logs: '日志',
  users: '用户管理', security: '安全', cliproxy: 'Cliproxy',
  settings: '设置', modelPool: '模型池', downloads: '下载',
  adminPanel: '管理面板',
  // Clients
  generateClient: '生成客户端',
  generateDesc: '生成随机UUID并添加为新的xray客户端',
  myXrayClients: '我的客户端', allXrayClients: '全部客户端',
  clientConfig: '客户端配置', copyConfig: '复制配置',
  downloadConfig: '下载配置', configCopied: '配置已复制到剪贴板',
  noProxyClients: '还没有代理客户端，在上面生成一个。',
  noClientsYet: '你还没有代理客户端。前往"客户端"页面创建第一个。',
  // Dashboard
  welcomeBack: '欢迎回来', welcomeDesc: '在这里管理你的代理客户端和配置。',
  servicesCount: '服务数量', running: '运行中',
  dockerEngine: 'Docker 引擎', cpuMemory: 'CPU / 内存',
  // Services
  serviceActions: '操作', restart: '重启', noServices: '未找到服务。',
  // Cliproxy + Providers
  addProvider: '添加提供商',
  providerName: '提供商名称', baseURL: '基础URL',
  apiKey: 'API 密钥', upstreamURL: '上游URL', upstreamKey: '上游密钥',
  noProviders: '尚未配置 API 提供商。',
  provider: '提供商', format: '格式', alias: '别名',
  openAICompat: 'OpenAI兼容', anthropic: 'Anthropic',
  save: '保存', cancel: '取消', delete: '删除',
  config: '配置', start: '启动', stop: '停止',
  userConfig: '配置', userInstances: '用户实例',
  // Model Pool
  poolProviders: '池提供商', poolDesc: '配置共享给授权用户的 AI API 提供商。',
  userAccess: '用户权限', noPoolProviders: '尚未配置池提供商。',
  noUsersAuthorized: '暂无授权用户。在 Cliproxy 页面中授权。',
  // Users
  username: '用户名', role: '角色', pool: '模型池',
  created: '创建时间', actions: '操作',
  setAdmin: '设为管理员', setViewer: '设为普通用户',
  grantPool: '授予池权限', revokePool: '撤销池权限', noPool: '无池权限',
  changePassword: '修改密码', newPassword: '新密码',
  addUser: '添加用户', noUsers: '暂无用户。',
  // Settings
  externalServices: '外部服务', publicIPv6: 'IPv6 公网地址',
  service: '服务', ipv6Address: 'IPv6 地址', dns: 'DNS',
  synced: '已同步', mismatch: '不一致', noDNS: '无 DNS',
  webGateway: 'Web 网关 (80/443)', realityDesc: 'VLESS Reality (443)',
  ipv6Allocation: 'IPv6 分配', subnet: '子网',
  internalServices: '内部服务', access: '访问方式',
  port: '端口', internalDocker: '内部 Docker，经由 nginx 代理',
  dnsSync: 'DNS 同步', domain: '域名', dnsRecord: 'DNS 记录',
  expected: '期望值', status: '状态',
  syncAllDNS: '同步全部 DNS', dnsSyncFailed: '同步失败',
  ipv6Rotation: 'IPv6 旋转',
  rotateDesc: '在轮转范围 ({range}) 中生成新 IPv6，添加到 xray，更新 DNS 后移除旧 IP。',
  rotateReality: '轮转 Reality IPv6',
  rotating: '轮转中...',
  // Security
  blockedIPs: '已封禁 IP', ipAddress: 'IP 地址', reason: '原因',
  blockedAt: '封禁时间', expiresAt: '过期时间',
  blockIP: '封禁 IP', unblock: '解封', durationSec: '时长 (秒)',
  noBlockedIPs: '暂无封禁 IP。',
  // Downloads
  storedDownloads: '已存储下载', availableVersions: '可用版本',
  platform: '平台', stored: '已存储', available: '可用',
  filename: '文件名', size: '大小', checksum: '校验值',
  downloaded: '已下载', downloadFromServer: '下载到服务器',
  noBinaries: '暂无 Claude Code 二进制文件。请联系管理员下载。',
  fromGH: '来自 GitHub 发布页。最多保留 3 个版本，旧版本自动清理。',
  binarySize: '二进制 ~150-180 MB',
  // Logs
  serviceLogs: '服务日志', lastLines: '最近 {n} 行',
  noLogEntries: '无日志条目。',
  // Common
  refresh: '刷新', download: '下载', copy: '复制',
  copied: '已复制', loading: '加载中...',
  forbidden: '无权限', notFound: '未找到',
  logout: '退出登录',
  // State badges
  stateRunning: '运行中', stateStopped: '已停止',
  // Actions
  delete: '删除', del: '删',
  // misc
  fetching: '获取中...',
};

const en = {
  overview: 'Overview', manage: 'Manage', system: 'System',
  dashboard: 'Dashboard', services: 'Services', clients: 'Clients',
  logs: 'Logs',
  users: 'Users', security: 'Security', cliproxy: 'Cliproxy',
  settings: 'Settings', modelPool: 'Model Pool', downloads: 'Downloads',
  adminPanel: 'Admin Panel',
  // Clients
  generateClient: 'Generate Client',
  generateDesc: 'Generates a random UUID and adds it as a new xray client',
  myXrayClients: 'My Clients', allXrayClients: 'All Clients',
  clientConfig: 'Client Configuration', copyConfig: 'Copy Config',
  downloadConfig: 'Download Config', configCopied: 'Config copied to clipboard',
  noProxyClients: 'No proxy clients configured.',
  noClientsYet: 'You have no proxy clients yet. Go to Clients to create one.',
  // Dashboard
  welcomeBack: 'Welcome back', welcomeDesc: 'Manage your proxy clients and configuration below.',
  servicesCount: 'Services', running: 'Running',
  dockerEngine: 'Docker Engine', cpuMemory: 'CPU / Memory',
  // Services
  serviceActions: 'Actions', restart: 'Restart', noServices: 'No services found.',
  // Cliproxy + Providers
  addProvider: 'Add Provider',
  providerName: 'Provider Name', baseURL: 'Base URL',
  apiKey: 'API Key', upstreamURL: 'Upstream URL', upstreamKey: 'Upstream Key',
  noProviders: 'No API providers configured.',
  provider: 'Provider', format: 'Format', alias: 'Alias',
  openAICompat: 'OpenAI Compat', anthropic: 'Anthropic',
  save: 'Save', cancel: 'Cancel', delete: 'Delete',
  config: 'Config', start: 'Start', stop: 'Stop',
  userConfig: 'Configuration', userInstances: 'User Instances',
  // Model Pool
  poolProviders: 'Pool Providers', poolDesc: 'Configure AI API providers shared with authorized users.',
  userAccess: 'User Access', noPoolProviders: 'No pool providers configured.',
  noUsersAuthorized: 'No users authorized. Grant access from Cliproxy page.',
  // Users
  username: 'Username', role: 'Role', pool: 'Pool',
  created: 'Created', actions: 'Actions',
  setAdmin: 'Set Admin', setViewer: 'Set Viewer',
  grantPool: 'Grant Pool', revokePool: 'Revoke Pool', noPool: 'No Pool',
  changePassword: 'Change Password', newPassword: 'New Password',
  addUser: 'Add User', noUsers: 'No users.',
  // Settings
  externalServices: 'External Services', publicIPv6: 'Public IPv6 on ipvlan',
  service: 'Service', ipv6Address: 'IPv6 Address', dns: 'DNS',
  synced: 'synced', mismatch: 'mismatch', noDNS: 'no DNS',
  webGateway: 'Web gateway (80/443)', realityDesc: 'VLESS Reality (443)',
  ipv6Allocation: 'IPv6 Allocation', subnet: 'Subnet',
  internalServices: 'Internal Services', access: 'Access',
  port: 'Port', internalDocker: 'Internal Docker, nginx-proxied',
  dnsSync: 'DNS Sync', domain: 'Domain', dnsRecord: 'DNS Record',
  expected: 'Expected', status: 'Status',
  syncAllDNS: 'Sync All DNS', dnsSyncFailed: 'Sync failed',
  ipv6Rotation: 'IPv6 Rotation',
  rotateDesc: 'Generates a new IPv6 in the rotating range ({range}), adds to xray, updates DNS, then removes old IP.',
  rotateReality: 'Rotate Reality IPv6',
  rotating: 'Rotating...',
  // Security
  blockedIPs: 'Blocked IPs', ipAddress: 'IP Address', reason: 'Reason',
  blockedAt: 'Blocked At', expiresAt: 'Expires',
  blockIP: 'Block IP', unblock: 'Unblock', durationSec: 'Duration (s)',
  noBlockedIPs: 'No blocked IPs.',
  // Downloads
  storedDownloads: 'Stored Downloads', availableVersions: 'Available Versions',
  platform: 'Platform', stored: 'Stored', available: 'Available',
  filename: 'Filename', size: 'Size', checksum: 'Checksum',
  downloaded: 'Downloaded', downloadFromServer: 'Download to server',
  noBinaries: 'No binaries available. Ask an admin to download them.',
  fromGH: 'From GitHub releases. Max 3 stored, old ones auto-removed.',
  binarySize: 'Binary ~150-180 MB',
  // Logs
  serviceLogs: 'Service Logs', lastLines: 'Last {n} lines',
  noLogEntries: 'No log entries.',
  // Common
  refresh: 'Refresh', download: 'Download', copy: 'Copy',
  copied: 'Copied', loading: 'Loading...',
  forbidden: 'Forbidden', notFound: 'Not Found',
  logout: 'Logout',
  // State badges
  stateRunning: 'Running', stateStopped: 'Stopped',
  // Actions
  delete: 'Delete', del: 'Del',
  // misc
  fetching: 'Fetching...',
};

const dicts = { zh, en };

let lang = $state(typeof localStorage !== 'undefined' ? (localStorage.getItem('lang') || 'zh') : 'zh');

export function t(key, vars) {
  let s = dicts[lang]?.[key] || dicts.en[key] || key;
  if (vars) {
    for (const [k, v] of Object.entries(vars)) s = s.replace(`{${k}}`, v);
  }
  return s;
}

export function toggleLang() {
  lang = lang === 'zh' ? 'en' : 'zh';
  if (typeof localStorage !== 'undefined') localStorage.setItem('lang', lang);
}

export function getLang() { return lang; }
