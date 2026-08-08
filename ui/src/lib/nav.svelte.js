const ADMIN_PREFIX = '/admin';

function readPath() {
  if (typeof window === 'undefined') return '/';
  const path = window.location.pathname;
  if (path.startsWith(ADMIN_PREFIX)) {
    return path.slice(ADMIN_PREFIX.length) || '/';
  }
  return path || '/';
}

/** 当前 SPA 路由路径（不含 /admin 前缀），跨组件共享，路由变化时联动。 */
export const nav = $state({ path: readPath() });

/** SPA 导航：更新共享状态并写入 history，供侧边栏、快捷入口等所有组件复用。 */
export function navigate(path) {
  nav.path = path;
  window.history.pushState({}, '', ADMIN_PREFIX + path);
}

/** 浏览器前进/后退后同步共享状态。 */
export function syncPath() {
  nav.path = readPath();
}
