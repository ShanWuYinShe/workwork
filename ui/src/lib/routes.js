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
  { path: '/users', component: Users, adminOnly: true },
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
    key: 'proxy',
    items: [
      { path: '/clients', key: 'clients', icon: 'users' },
      { path: '/services', key: 'services', icon: 'box', adminOnly: true },
    ],
  },
  {
    key: 'downloads',
    items: [
      { path: '/downloads', key: 'downloads', icon: 'download' },
      { path: '/transfer', key: 'transfer', icon: 'upload' },
    ],
  },
  {
    key: 'system',
    items: [
      { path: '/users', key: 'users', icon: 'user-circle', adminOnly: true },
      { path: '/security', key: 'security', icon: 'shield', adminOnly: true },
      { path: '/settings', key: 'settings', icon: 'settings', adminOnly: true },
      { path: '/logs', key: 'logs', icon: 'file-text', adminOnly: true },
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
