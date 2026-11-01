// @ts-check
// 跨组件共享的字面量：/admin 前缀与登出标记。集中后改名只需要动这里。

export const ADMIN_PREFIX = '/admin';
export const LOGOUT_PENDING_KEY = 'ww_logout_pending';

/**
 * 把 SPA 路由路径转成浏览器地址（含 /admin 前缀）。
 * @param {string} path
 */
export function adminHref(path) {
  return ADMIN_PREFIX + (path.startsWith('/') ? path : '/' + path);
}
