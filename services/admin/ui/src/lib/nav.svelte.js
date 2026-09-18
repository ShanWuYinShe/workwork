// @ts-check
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
  // nav.path 只存路径部分：路由表与侧边栏高亮都按纯路径精确匹配，带上
  // ?query 会匹配失败渲染「未找到」；query 仅写入浏览器 URL，由目标页
  // 自行从 location.search 读取（如 /logs?service=）。
  const q = path.indexOf('?');
  nav.path = q === -1 ? path : path.slice(0, q);
  window.history.pushState({}, '', ADMIN_PREFIX + path);
}

/** 浏览器前进/后退后同步共享状态。 */
export function syncPath() {
  nav.path = readPath();
}
