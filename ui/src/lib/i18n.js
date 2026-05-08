const zh = {
  overview: '概览', manage: '管理', system: '系统',
  dashboard: '仪表盘', services: '服务', clients: '客户端',
  users: '用户管理', security: '安全', cliproxy: 'Cliproxy',
  settings: '设置', modelPool: '模型池', downloads: '下载',
  adminPanel: '管理面板',
  generateClient: '生成客户端',
  generateDesc: '生成随机UUID并添加为新的xray客户端',
  addProvider: '添加提供商',
  provider: '提供商', format: '格式', alias: '别名',
  upstreamURL: '上游URL', upstreamKey: '上游密钥',
  openAICompat: 'OpenAI兼容', anthropic: 'Anthropic',
  save: '保存', cancel: '取消', delete: '删除',
  config: '配置', start: '启动', stop: '停止',
  refresh: '刷新', download: '下载', copy: '复制',
  copied: '已复制', loading: '加载中...',
  forbidden: '无权限', notFound: '未找到',
  poolAccess: '模型池访问', grantPool: '授予池权限',
  revokePool: '撤销池权限', noPool: '无池权限',
  rotate: '轮转 Reality IPv6', rotating: '轮转中...',
  syncDNS: '同步 DNS', syncing: '同步中...',
  role: '角色', admin: '管理员', viewer: '观察者',
  logout: '退出登录',
  allClients: '全部客户端', myClients: '我的客户端',
  storedDownloads: '已存储下载', availableVersions: '可用版本',
  platform: '平台', stored: '已存储', available: '可用',
};

const en = {
  overview: 'Overview', manage: 'Manage', system: 'System',
  dashboard: 'Dashboard', services: 'Services', clients: 'Clients',
  users: 'Users', security: 'Security', cliproxy: 'Cliproxy',
  settings: 'Settings', modelPool: 'Model Pool', downloads: 'Downloads',
  adminPanel: 'Admin Panel',
  generateClient: 'Generate Client',
  generateDesc: 'Generates a random UUID and adds it as a new xray client',
  addProvider: 'Add Provider',
  provider: 'Provider', format: 'Format', alias: 'Alias',
  upstreamURL: 'Upstream URL', upstreamKey: 'Upstream Key',
  openAICompat: 'OpenAI Compat', anthropic: 'Anthropic',
  save: 'Save', cancel: 'Cancel', delete: 'Delete',
  config: 'Config', start: 'Start', stop: 'Stop',
  refresh: 'Refresh', download: 'Download', copy: 'Copy',
  copied: 'Copied', loading: 'Loading...',
  forbidden: 'Forbidden', notFound: 'Not Found',
  poolAccess: 'Pool Access', grantPool: 'Grant Pool',
  revokePool: 'Revoke Pool', noPool: 'No Pool',
  rotate: 'Rotate Reality IPv6', rotating: 'Rotating...',
  syncDNS: 'Sync DNS', syncing: 'Syncing...',
  role: 'Role', admin: 'Admin', viewer: 'Viewer',
  logout: 'Logout',
  allClients: 'All Clients', myClients: 'My Clients',
  storedDownloads: 'Stored Downloads', availableVersions: 'Available Versions',
  platform: 'Platform', stored: 'Stored', available: 'Available',
};

const dicts = { zh, en };

let lang = $state(typeof localStorage !== 'undefined' ? (localStorage.getItem('lang') || 'zh') : 'zh');

export function t(key) {
  return dicts[lang]?.[key] || dicts.en[key] || key;
}

export function toggleLang() {
  lang = lang === 'zh' ? 'en' : 'zh';
  if (typeof localStorage !== 'undefined') localStorage.setItem('lang', lang);
}

export function getLang() { return lang; }
