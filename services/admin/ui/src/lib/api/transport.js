// @ts-check
/**
 * @typedef {Object} ApiEnvelope
 * @property {boolean} [success]
 * @property {string} [error]
 * @property {string} [error_code]
 * @property {any} [data]
 */

const BASE = '/api/admin';
const FETCH_TIMEOUT = 30000;
// 重启/轮换/DNS 同步在后端最长 2 分钟；请求超时必须覆盖完整操作，否则前端
// 先 abort 后用户看到失败，后端其实已成功，轮询又显示"已重启"。
export const LONG_OPERATION_TIMEOUT = 180000;

/**
 * 拼接管理 API 的绝对路径。页面不得自行拼 `/api/admin`，统一从这里取。
 * @param {string} path
 */
export function apiURL(path) {
  return BASE + path;
}

// ApiError 保留服务端 error_code 与原始响应体，供 UI 按业务分支展示。
// message 始终是服务端 error 原文（原始错误信息完整透传，不被统一文案覆盖）。
export class ApiError extends Error {
  /**
   * @param {string} message
   * @param {{ code?: string, details?: any }} [opts]
   */
  constructor(message, { code = '', details = null } = {}) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.details = details;
  }
}

/**
 * @param {string} path
 * @param {{ headers?: Record<string, string>, body?: BodyInit, method?: string, timeout?: number, signal?: AbortSignal }} [opts]
 * @returns {Promise<any>}
 */
export async function req(path, opts = {}) {
  const hasBody = opts.body !== undefined;
  const headers = { ...opts.headers };
  if (hasBody && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json';
  }
  const ctrl = new AbortController();
  const timeout = opts.timeout || FETCH_TIMEOUT;
  const timer = setTimeout(() => ctrl.abort(), timeout);
  // 合并外部 signal 与超时 signal:此前 opts.signal 存在时直接替换,
  // 超时 abort 的是内部 ctrl 而 fetch 监听的是外部 signal,超时静默失效。
  // AbortSignal.any 让任一信号触发即取消(Chrome 116+/Safari 17+)。
  const signal =
    typeof AbortSignal.any === 'function' && opts.signal
      ? AbortSignal.any([opts.signal, ctrl.signal])
      : opts.signal || ctrl.signal;
  try {
    const r = await fetch(apiURL(path), {
      credentials: 'same-origin',
      ...opts,
      signal,
      headers,
    });
    const json = await parseJSONResponse(r);
    if (!r.ok || !json.success) {
      throw new ApiError(json.error || `${r.status} ${r.statusText}`, {
        code: json.error_code,
        details: json,
      });
    }
    return json.data;
  } catch (e) {
    if (e instanceof ApiError) throw e;
    if (e instanceof Error && e.name === 'AbortError') {
      if (opts.signal?.aborted) {
        throw new ApiError('请求已取消', { code: 'ABORTED' });
      }
      throw new ApiError('请求超时，请稍后重试', { code: 'TIMEOUT' });
    }
    throw e;
  } finally {
    clearTimeout(timer);
  }
}

/** @param {Response} response @returns {Promise<ApiEnvelope>} */
async function parseJSONResponse(response) {
  try {
    return await response.json();
  } catch (error) {
    if (!response.ok) {
      throw new Error(`${response.status} ${response.statusText}`, { cause: error });
    }
    throw error;
  }
}

/** @param {Response} response @returns {Promise<string>} */
async function responseErrorMessage(response) {
  try {
    const json = await response.json();
    if (json?.error) return json.error;
  } catch {
    // Fall through to the HTTP status when the body is not JSON.
  }
  return `${response.status} ${response.statusText}`;
}

/**
 * 消费下载接口的 NDJSON 事件流。
 *
 * 超时是「空闲超时」而非总时长:下载可能持续数分钟,固定总时长会误杀正常
 * 大文件;但服务端卡死或连接半开时又必须能退出,所以每收到一块数据就重置
 * 计时器,超过 FETCH_TIMEOUT 没有任何进度才中止。
 *
 * @param {string} path
 * @param {any} body
 * @param {(event: any) => void} [onProgress]
 * @returns {Promise<{ status: string, filename?: string, size?: number, checksum?: string }>}
 */
export async function streamDownload(path, body, onProgress) {
  const ctrl = new AbortController();
  let timer = setTimeout(() => ctrl.abort(), FETCH_TIMEOUT);
  const bumpIdleTimeout = () => {
    clearTimeout(timer);
    timer = setTimeout(() => ctrl.abort(), FETCH_TIMEOUT);
  };

  try {
    const r = await fetch(apiURL(path), {
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      method: 'POST',
      body: JSON.stringify(body),
      signal: ctrl.signal,
    });
    if (!r.ok) throw new Error(await responseErrorMessage(r));
    if (!r.body) throw new Error('Empty response body');

    const reader = r.body.getReader();
    const decoder = new TextDecoder();
    let buf = '';
    let result = null;

    try {
      while (true) {
        const { done, value } = await reader.read();
        bumpIdleTimeout();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        const lines = buf.split('\n');
        buf = lines.pop() ?? '';
        for (const line of lines) {
          if (!line.trim()) continue;
          let data;
          try {
            data = JSON.parse(line);
          } catch {
            continue;
          }
          if (data.type === 'progress' && onProgress) onProgress(data);
          if (data.type === 'error') throw new Error(data.error);
          if (data.type === 'done') result = data;
          if (data.type === 'start' && onProgress) onProgress({ bytes: 0, total: 0, percent: 0 });
        }
      }
      // Handle remaining buffer
      if (buf.trim()) {
        let data;
        try {
          data = JSON.parse(buf);
        } catch {
          /* ignore partial */
        }
        if (data) {
          if (data.type === 'error') throw new Error(data.error);
          if (data.type === 'done') result = data;
        }
      }
    } finally {
      // 错误中途退出时显式释放连接;正常读完后的 cancel 是无害 no-op。
      reader.cancel().catch(() => {});
    }

    // 服务端只发 NDJSON 事件,终态必然是 {type:'done',...};缺终态说明响应被中途
    // 截断或协议漂移,报错好过静默返回残缺结果。
    if (!result) {
      throw new Error('Download completed but no result received');
    }
    // status 必须透传:already_exists 时服务端只给 filename(无 size/checksum),
    // 硬编码 'ok' 会让调用方的"已存在"分支(Downloads.svelte)永远不可达。
    return {
      status: result.status || 'ok',
      filename: result.filename,
      size: result.size,
      checksum: result.checksum,
    };
  } finally {
    clearTimeout(timer);
  }
}
