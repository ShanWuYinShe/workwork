import { afterEach, expect, test } from 'bun:test';
import { ApiError, apiURL, req, streamDownload } from './transport.js';

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
});

/** @param {(url: string, init: RequestInit) => Promise<Response>} handler */
function mockFetch(handler) {
  const calls = [];
  globalThis.fetch = async (url, init) => {
    calls.push({ url: String(url), init });
    return handler(String(url), init);
  };
  return calls;
}

function jsonResponse(body, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function ndjsonResponse(lines) {
  const payload = lines.map((line) => JSON.stringify(line) + '\n').join('');
  const stream = new ReadableStream({
    start(controller) {
      controller.enqueue(new TextEncoder().encode(payload));
      controller.close();
    },
  });
  return new Response(stream, {
    status: 200,
    headers: { 'Content-Type': 'application/x-ndjson' },
  });
}

test('apiURL 统一管理 API 前缀', () => {
  expect(apiURL('/health')).toBe('/api/admin/health');
});

test('req 返回信封中的 data，并带上同源凭证与 JSON 头', async () => {
  const calls = mockFetch(() => Promise.resolve(jsonResponse({ success: true, data: { ok: 1 } })));
  const data = await req('/x', { method: 'POST', body: JSON.stringify({ a: 1 }) });

  expect(data).toEqual({ ok: 1 });
  expect(calls[0].url).toBe('/api/admin/x');
  expect(calls[0].init.credentials).toBe('same-origin');
  expect(calls[0].init.headers['Content-Type']).toBe('application/json');
});

test('业务失败抛 ApiError，保留 error_code 与响应体', async () => {
  mockFetch(() =>
    Promise.resolve(
      jsonResponse({ success: false, error: '用户不存在', error_code: 'user_not_found' }, 404),
    ),
  );

  const error = await req('/x').catch((e) => e);
  expect(error).toBeInstanceOf(ApiError);
  expect(error.message).toBe('用户不存在');
  expect(error.code).toBe('user_not_found');
  expect(error.details.error_code).toBe('user_not_found');
});

test('非 JSON 错误响应退化为 HTTP 状态文本', async () => {
  mockFetch(() => Promise.resolve(new Response('bad gateway', { status: 502 })));

  const error = await req('/x').catch((e) => e);
  expect(error).toBeInstanceOf(Error);
  expect(error.message).toContain('502');
});

test('streamDownload 解析 NDJSON 进度与完成事件', async () => {
  mockFetch(() =>
    Promise.resolve(
      ndjsonResponse([
        { type: 'start' },
        { type: 'progress', bytes: 5, total: 10, percent: 50 },
        { type: 'done', status: 'already_exists', filename: 'a.zip' },
      ]),
    ),
  );
  const events = [];

  const result = await streamDownload('/download', { version: '1' }, (event) => events.push(event));

  expect(events).toEqual([
    { bytes: 0, total: 0, percent: 0 },
    { type: 'progress', bytes: 5, total: 10, percent: 50 },
  ]);
  expect(result).toEqual({
    status: 'already_exists',
    filename: 'a.zip',
    size: undefined,
    checksum: undefined,
  });
});

test('streamDownload 服务端 error 事件直接失败', async () => {
  mockFetch(() => Promise.resolve(ndjsonResponse([{ type: 'error', error: '磁盘已满' }])));

  const error = await streamDownload('/download', {}).catch((e) => e);
  expect(error.message).toBe('磁盘已满');
});

test('streamDownload 缺少 done 终态时失败，避免静默返回残缺结果', async () => {
  mockFetch(() => Promise.resolve(ndjsonResponse([{ type: 'progress', bytes: 1 }])));

  const error = await streamDownload('/download', {}).catch((e) => e);
  expect(error.message).toContain('no result received');
});

test('req 把超时 AbortError 归一化为 ApiError(TIMEOUT)', async () => {
  mockFetch(
    (_url, init) =>
      new Promise((_resolve, reject) => {
        init.signal.addEventListener('abort', () =>
          reject(new DOMException('Aborted', 'AbortError')),
        );
      }),
  );

  const error = await req('/slow', { timeout: 5 }).catch((e) => e);
  expect(error).toBeInstanceOf(ApiError);
  expect(error.code).toBe('TIMEOUT');
});

test('req 把外部取消归一化为 ApiError(ABORTED)', async () => {
  const abort = new AbortController();
  mockFetch(
    (_url, init) =>
      new Promise((_resolve, reject) => {
        init.signal.addEventListener('abort', () =>
          reject(new DOMException('Aborted', 'AbortError')),
        );
      }),
  );

  const pending = req('/slow', { signal: abort.signal }).catch((e) => e);
  abort.abort();
  const error = await pending;
  expect(error).toBeInstanceOf(ApiError);
  expect(error.code).toBe('ABORTED');
});
