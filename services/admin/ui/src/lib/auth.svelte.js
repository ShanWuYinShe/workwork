// @ts-check
import { getCurrentUser } from './api/index.js';

/** @type {{ username: string, role: string, created_at?: string } | null} */
let user = $state(null);
let loading = $state(true);
/** @type {string | null} */
let error = $state(null);
let loaded = false;

/**
 * 拉取当前登录用户。App 是唯一调用点；页面只读 auth.user / auth.isAdmin，
 * 不再各自请求 /auth/me 并各自维护一份 isAdmin。
 */
async function load(force = false) {
  if (loaded && !force) return user;
  loading = true;
  error = null;
  try {
    user = await getCurrentUser();
    loaded = true;
    return user;
  } catch (e) {
    error = e instanceof Error ? e.message : String(e);
    user = null;
    throw e;
  } finally {
    loading = false;
  }
}

export const auth = {
  get user() {
    return user;
  },
  get isAdmin() {
    return user?.role === 'admin';
  },
  get loading() {
    return loading;
  },
  get error() {
    return error;
  },
  load,
};
