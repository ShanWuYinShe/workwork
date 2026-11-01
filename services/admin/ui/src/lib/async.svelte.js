// @ts-check
/**
 * createAsyncTask 统一页面加载状态：首屏 loading、静默刷新 refreshing、
 * 请求序号守卫（旧响应不覆盖新响应）与错误出口。
 *
 * 只负责状态机，不规定数据形状；页面在 fetcher 内写入自己的 $state。
 * 页面不得再手写 try/finally + 布尔竞态。
 *
 * @template T
 * @param {() => Promise<T>} fetcher
 * @param {{ onError?: (error: Error, meta: { silent: boolean }) => void }} [opts]
 */
export function createAsyncTask(fetcher, { onError } = {}) {
  // 初始为 true：页面首帧就显示骨架，而不是等 onMount 启动后闪一下空态。
  let loading = $state(true);
  let refreshing = $state(false);
  /** @type {Error | null} */
  let error = $state(null);
  let seq = 0;

  /**
   * @param {{ silent?: boolean }} [opts]
   * @returns {Promise<T | undefined>}
   */
  async function run({ silent = false } = {}) {
    const id = ++seq;
    if (silent) {
      refreshing = true;
    } else {
      loading = true;
    }
    error = null;
    try {
      const result = await fetcher();
      return id === seq ? result : undefined;
    } catch (e) {
      if (id !== seq) return undefined;
      const err = e instanceof Error ? e : new Error(String(e));
      error = err;
      onError?.(err, { silent });
      return undefined;
    } finally {
      if (id === seq) {
        loading = false;
        refreshing = false;
      }
    }
  }

  // 取消在途请求的状态回写（页面卸载时调用；实际网络请求由 AbortSignal 负责）。
  function cancel() {
    seq++;
    loading = false;
    refreshing = false;
  }

  return {
    get loading() {
      return loading;
    },
    get refreshing() {
      return refreshing;
    },
    get error() {
      return error;
    },
    run,
    cancel,
  };
}
