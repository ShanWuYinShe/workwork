// 路由的唯一事实来源：path/title/section/icon/adminOnly。routes.js 只负责
// 把 path 映射到 Svelte 组件；侧栏与标题都从这里派生，避免三张表漂移。
export const routeMeta = [
  { path: '/', title: 'dashboard', section: 'overview', icon: 'grid' },
  { path: '/services', title: 'services', section: 'operations', icon: 'box', adminOnly: true },
  { path: '/clients', title: 'clients', section: 'dailyOps', icon: 'users' },
  { path: '/users', title: 'users', section: 'accessControl', icon: 'user-circle' },
  {
    path: '/security',
    title: 'security',
    section: 'accessControl',
    icon: 'shield',
    adminOnly: true,
  },
  {
    path: '/settings',
    title: 'settings',
    section: 'operations',
    icon: 'settings',
    adminOnly: true,
  },
  { path: '/downloads', title: 'downloads', section: 'dailyOps', icon: 'download' },
  { path: '/transfer', title: 'transfer', section: 'dailyOps', icon: 'upload' },
  { path: '/logs', title: 'logs', section: 'operations', icon: 'file-text', adminOnly: true },
];

export const routeTitles = Object.fromEntries(routeMeta.map((r) => [r.path, r.title]));

const sectionOrder = ['overview', 'dailyOps', 'operations', 'accessControl'];

export const navSections = sectionOrder
  .map((key) => ({
    key,
    items: routeMeta
      .filter((r) => r.section === key)
      .map(({ path, title, icon, adminOnly }) => ({ path, key: title, icon, adminOnly })),
  }))
  .filter((section) => section.items.length > 0);
