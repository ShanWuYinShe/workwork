// @ts-check
import Dashboard from '../routes/Dashboard.svelte';
import Services from '../routes/Services.svelte';
import Clients from '../routes/Clients.svelte';
import Users from '../routes/Users.svelte';
import Security from '../routes/Security.svelte';
import Settings from '../routes/Settings.svelte';
import Downloads from '../routes/Downloads.svelte';
import Transfer from '../routes/Transfer.svelte';
import Logs from '../routes/Logs.svelte';

export const routes = [
  { path: '/', component: Dashboard },
  { path: '/services', component: Services, adminOnly: true },
  { path: '/clients', component: Clients },
  // /users 不加 adminOnly:侧边栏(adminOnly)对 viewer 隐藏入口,但页面本身支持
  // viewer 改自己密码(POST /users/password 为 viewer+,旧密码字段仅在非 admin 时
  // 出现),加 adminOnly 会让 viewer 连按 URL 都进不去,丢掉唯一的自助改密入口。
  { path: '/users', component: Users },
  { path: '/security', component: Security, adminOnly: true },
  { path: '/settings', component: Settings, adminOnly: true },
  { path: '/downloads', component: Downloads },
  { path: '/transfer', component: Transfer },
  { path: '/logs', component: Logs, adminOnly: true },
];

export const navSections = [
  {
    key: 'overview',
    items: [{ path: '/', key: 'dashboard', icon: 'grid' }],
  },
  {
    key: 'dailyOps',
    items: [
      { path: '/clients', key: 'clients', icon: 'users' },
      { path: '/transfer', key: 'transfer', icon: 'upload' },
      { path: '/downloads', key: 'downloads', icon: 'download' },
    ],
  },
  {
    key: 'operations',
    items: [
      { path: '/services', key: 'services', icon: 'box', adminOnly: true },
      { path: '/settings', key: 'settings', icon: 'settings', adminOnly: true },
      { path: '/logs', key: 'logs', icon: 'file-text', adminOnly: true },
    ],
  },
  {
    key: 'accessControl',
    items: [
      { path: '/users', key: 'users', icon: 'user-circle', adminOnly: true },
      { path: '/security', key: 'security', icon: 'shield', adminOnly: true },
    ],
  },
];

export const routeTitles = {
  '/': 'dashboard',
  '/services': 'services',
  '/clients': 'clients',
  '/users': 'users',
  '/security': 'security',
  '/settings': 'settings',
  '/downloads': 'downloads',
  '/transfer': 'transfer',
  '/logs': 'logs',
};
