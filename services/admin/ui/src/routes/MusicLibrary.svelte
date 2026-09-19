<script>
  import { onMount } from 'svelte';
  import {
    getMusicLibrary,
    getMusicChunks,
    musicLibraryUploadUrl,
    musicChunkUploadUrl,
    deleteMusicLibraryFile,
    neteaseLoginStart,
    neteaseLoginPoll,
    neteaseLoginState,
    neteaseLogout,
  } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import { toast } from '../lib/toast.svelte.js';
  import { formatDateTime, formatSize } from '../lib/format.js';

  let loading = $state(true);
  let loadError = $state('');
  let files = $state([]);

  // 上传队列:拖拽/点击多选进入队列,可单个移除或清空后再一次性提交
  let fileInput = $state(null);
  let picked = $state([]); // File[]
  let dragOver = $state(false);
  let uploading = $state(false);
  let uploadProgress = $state(null); // {bytes, total} 全队列字节聚合
  let uploadXhr = $state(null);
  let phaseLabel = $state(''); // 当前阶段(批量第 n 批 / 分片上传 xxx)
  let lastSkipped = $state([]); // 最近一次上传被跳过的文件明细

  // 曲库列表:搜索 + 列头排序
  let query = $state('');
  let sortKey = $state('name'); // name | size | time
  let sortDesc = $state(false);

  // 网易账号扫码登录状态
  let account = $state(null); // {loggedIn, nickname?, updatedAt?}
  let accountBusy = $state(false);
  let qr = $state(null); // {key, qrimg}
  let qrStatus = $state('waiting'); // waiting | scanned | expired
  let pollTimer = $state(null);
  let qrModal = $state(null); // 扫码弹层元素(focus trap 用)

  const audioExts = ['.mp3', '.wav', '.flac', '.m4a', '.ogg', '.oga', '.aac', '.opus'];

  let pickedTotalBytes = $derived(picked.reduce((sum, f) => sum + f.size, 0));
  let filtered = $derived.by(() => {
    let list = files;
    const q = query.trim().toLowerCase();
    if (q) list = list.filter((f) => f.name.toLowerCase().includes(q));
    const dir = sortDesc ? -1 : 1;
    return [...list].sort((a, b) => {
      if (sortKey === 'size') return (a.sizeBytes - b.sizeBytes) * dir;
      if (sortKey === 'time') return (new Date(a.modTime) - new Date(b.modTime)) * dir;
      return a.name.localeCompare(b.name, 'zh-Hans-CN') * dir;
    });
  });

  onMount(() => {
    load();
    loadAccount();
    // 组件销毁时停掉网易扫码轮询并中止进行中的上传：否则离开页面后仍每 2s
    // 打 poll 接口，且 XHR 回调会继续写已卸载组件的状态（uploadProgress 等）。
    return () => {
      stopPolling();
      uploadXhr?.abort();
      uploadXhr = null;
    };
  });

  async function load() {
    loading = true;
    loadError = '';
    try {
      const data = await getMusicLibrary();
      files = data.files ?? [];
    } catch (e) {
      loadError = e?.message || String(e);
      files = [];
    } finally {
      loading = false;
    }
  }

  async function loadAccount() {
    try {
      account = await neteaseLoginState();
    } catch {
      account = null;
    }
  }

  function onQrKeydown(e) {
    if (!qr) return;
    if (e.key === 'Escape') {
      closeQr();
      return;
    }
    // Tab 焦点圈(与 ConfirmModal 行为一致):弹层内可聚焦元素极少(重试按钮),
    // 没有 focus trap 时 Tab 会焦点逃逸到背景页面。
    if (e.key === 'Tab' && qrModal) {
      const focusable = qrModal.querySelectorAll('button:not([disabled])');
      if (focusable.length === 0) {
        e.preventDefault();
        qrModal.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
  }

  function stopPolling() {
    if (pollTimer) {
      clearTimeout(pollTimer);
      pollTimer = null;
    }
  }

  async function startLogin() {
    accountBusy = true;
    try {
      qr = await neteaseLoginStart();
      qrStatus = 'waiting';
      pollOnce();
      // 弹层渲染后把焦点移入,键盘用户不需要 Tab 穿过整个页面找弹层。
      requestAnimationFrame(() => qrModal?.focus());
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      accountBusy = false;
    }
  }

  function closeQr() {
    stopPolling();
    qr = null;
  }

  async function pollOnce() {
    if (!qr) return;
    try {
      const r = await neteaseLoginPoll(qr.key);
      if (r.code === 803) {
        closeQr();
        toast(t('neteaseLoginOk', { name: r.nickname || '' }), 'success');
        await loadAccount();
        return;
      }
      if (r.code === 802) qrStatus = 'scanned';
      pollTimer = setTimeout(pollOnce, 2000);
    } catch {
      // 410 过期 / 网络错误:停止轮询,展示重新生成入口。
      qrStatus = 'expired';
    }
  }

  async function logout() {
    if (!(await showConfirm(t('neteaseLogoutConfirm'), { danger: true }))) return;
    try {
      await neteaseLogout();
      await loadAccount();
    } catch (e) {
      toast(e.message, 'error');
    }
  }

  // ── 上传队列 ──

  function addFiles(list) {
    let bad = 0;
    let dup = 0;
    for (const f of list) {
      const ext = f.name.match(/\.[^.]+$/)?.[0]?.toLowerCase() ?? '';
      if (!audioExts.includes(ext)) {
        bad++;
        continue;
      }
      if (picked.some((p) => p.name === f.name && p.size === f.size)) {
        dup++;
        continue;
      }
      picked.push(f);
    }
    if (bad > 0) toast(t('musicIgnoredBad', { n: bad }), 'error');
    if (dup > 0) toast(t('musicIgnoredDup', { n: dup }));
  }

  function onPick(event) {
    addFiles(event.target.files);
    event.target.value = ''; // 同批文件再次选择也要触发 change
  }

  function onDrop(event) {
    event.preventDefault();
    dragOver = false;
    if (event.dataTransfer.files.length > 0) addFiles(event.dataTransfer.files);
  }

  function removeFromQueue(idx) {
    picked = picked.filter((_, i) => i !== idx);
  }

  function clearQueue() {
    picked = [];
  }

  // ── 上传执行 ──
  // 小文件(≤16MB)聚合 multipart 批量上传;大文件走 8MB 分片(绕过 Cloudflare
  // 单请求 100MB 上限,支持断点续传)。进度按全队列字节聚合。

  const CHUNK_SIZE = 8 << 20;
  const SMALL_FILE_THRESHOLD = 16 << 20;
  const BATCH_BYTES_LIMIT = 80 << 20; // 单批总量上限,留出 CF 100MB 余量
  const MAX_FILE_BYTES = 200 << 20;

  async function sha256Hex(buf) {
    const digest = await crypto.subtle.digest('SHA-256', buf);
    return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, '0')).join('');
  }

  // uploadId 跨会话稳定(文件名/大小/mtime/首1MB 内容派生),刷新后重传同一
  // 文件可续传;文件内容变化(首1MB/mtime/大小)则派生新 id,不会错拼。
  async function deriveUploadId(file) {
    const headHash = await sha256Hex(await file.slice(0, 1 << 20).arrayBuffer());
    const seed = new TextEncoder().encode(
      `${file.name}:${file.size}:${file.lastModified}:${headHash}`,
    );
    return sha256Hex(seed);
  }

  // 通用 multipart XHR POST(进度/取消/超时);multipart 批量与分片单片共用。
  function xhrPostForm(url, form, onProgress) {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      uploadXhr = xhr;
      xhr.timeout = 10 * 60 * 1000; // 单请求最大 80MB 批 / 8MB 片,10min 足够
      xhr.ontimeout = () => reject(new Error(t('uploadTimeout')));
      xhr.open('POST', url);
      xhr.withCredentials = true;
      if (onProgress) {
        xhr.upload.onprogress = (e) => {
          if (e.lengthComputable) onProgress(e.loaded);
        };
      }
      xhr.onload = () => {
        uploadXhr = null;
        try {
          const json = JSON.parse(xhr.responseText);
          if (xhr.status >= 200 && xhr.status < 300 && json.success) resolve(json.data);
          else reject(new Error(json.error || `${xhr.status} ${xhr.statusText}`));
        } catch {
          reject(new Error(`${xhr.status} ${xhr.statusText}`));
        }
      };
      xhr.onerror = () => {
        uploadXhr = null;
        reject(new Error('Network error'));
      };
      // xhr.abort() 只触发 abort 事件（不走 onerror），必须单独 reject，
      // 否则取消上传后 Promise 永不 settle，上传状态永久卡死。
      xhr.onabort = () => {
        uploadXhr = null;
        reject(new DOMException('aborted', 'AbortError'));
      };
      xhr.send(form);
    });
  }

  const chunkBytes = (file, index) => Math.min(CHUNK_SIZE, file.size - index * CHUNK_SIZE);

  // 分片上传单个大文件:onBytes(fileOffset) 报告该文件已传字节(含当前片进度)。
  async function uploadFileChunked(file, onBytes) {
    const total = Math.max(1, Math.ceil(file.size / CHUNK_SIZE));
    const uploadId = await deriveUploadId(file);

    // 续传:查询已收分片,跳过已传部分。布局(total)不一致时服务端首片会重置,
    // 这里同样按无进度处理。
    let state = { received: [], total: 0 };
    try {
      state = await getMusicChunks(uploadId);
    } catch {
      // 查询失败不阻断:按全新上传继续。
    }
    // eslint-disable-next-line svelte/prefer-svelte-reactivity -- 断点续传的局部索引集,非响应式状态
    const received = new Set(state.total === total ? state.received : []);
    let doneBytes = 0;
    for (const i of received) doneBytes += chunkBytes(file, i);
    if (received.size > 0) toast(t('musicChunkResumed', { name: file.name, n: received.size }));
    onBytes(doneBytes);

    for (let i = 0; i < total; i++) {
      if (received.has(i)) continue;
      const blob = file.slice(i * CHUNK_SIZE, (i + 1) * CHUNK_SIZE);
      const sha = await sha256Hex(await blob.arrayBuffer());
      let resp;
      // 单片重试:网络抖动/超时常见,重传单片代价小。
      for (let attempt = 0; ; attempt++) {
        try {
          const form = new FormData();
          form.append('uploadId', uploadId);
          form.append('index', String(i));
          form.append('total', String(total));
          form.append('fileName', file.name);
          form.append('sha256', sha);
          form.append('chunk', blob);
          resp = await xhrPostForm(musicChunkUploadUrl, form, (loaded) =>
            onBytes(doneBytes + loaded),
          );
          break;
        } catch (e) {
          if (attempt >= 2 || cancelledUpload) throw e;
          await new Promise((r) => setTimeout(r, 500 * (attempt + 1)));
        }
      }
      for (const idx of resp.received ?? []) received.add(idx);
      doneBytes += chunkBytes(file, i);
      onBytes(doneBytes);
      if (resp.complete) return resp.saved ?? file.name;
    }
    throw new Error(t('musicChunkIncomplete'));
  }

  let cancelledUpload = false;

  async function upload() {
    if (picked.length === 0) return;
    cancelledUpload = false;
    uploading = true;
    uploadProgress = null;
    phaseLabel = '';
    lastSkipped = [];

    // 前置过滤超限文件(后端 200MB 也会拒,提前给明确提示)。
    const oversize = picked.filter((f) => f.size > MAX_FILE_BYTES);
    const active = picked.filter((f) => f.size <= MAX_FILE_BYTES);
    lastSkipped = oversize.map((f) => `${f.name}(${t('musicOversize')})`);

    const small = active.filter((f) => f.size <= SMALL_FILE_THRESHOLD);
    const big = active.filter((f) => f.size > SMALL_FILE_THRESHOLD);
    const totalBytes = active.reduce((s, f) => s + f.size, 0);
    let sentBytes = 0;
    const report = () => {
      uploadProgress = { bytes: sentBytes, total: totalBytes };
    };

    try {
      const savedNames = [];

      // Phase 1:小文件按 80MB 滚动分批 multipart(单批避开 CF 100MB)。
      let batch = [];
      let batchBytes = 0;
      const flushBatch = async () => {
        if (batch.length === 0) return;
        const form = new FormData();
        for (const f of batch) form.append('files', f);
        phaseLabel = t('musicPhaseBatch', { n: batch.length });
        const base = sentBytes;
        const result = await xhrPostForm(musicLibraryUploadUrl, form, (loaded) => {
          sentBytes = base + loaded;
          report();
        });
        sentBytes = base + batchBytes;
        report();
        savedNames.push(...(result?.saved ?? []));
        lastSkipped = [...lastSkipped, ...(result?.skipped ?? [])];
        batch = [];
        batchBytes = 0;
      };
      for (const f of small) {
        if (batchBytes + f.size > BATCH_BYTES_LIMIT && batch.length > 0) await flushBatch();
        batch.push(f);
        batchBytes += f.size;
      }
      await flushBatch();

      // Phase 2:大文件逐个分片(串行,服务端收齐自动合并)。
      for (const f of big) {
        phaseLabel = t('musicPhaseChunk', { name: f.name });
        try {
          const base = sentBytes;
          const saved = await uploadFileChunked(f, (fileBytes) => {
            sentBytes = base + fileBytes;
            report();
          });
          savedNames.push(saved);
        } catch (e) {
          if (cancelledUpload) throw e;
          lastSkipped = [...lastSkipped, `${f.name}(${e.message})`];
        }
      }

      sentBytes = totalBytes;
      report();
      if (savedNames.length > 0) toast(t('musicUploadDone', { n: savedNames.length }), 'success');
      picked = [];
      await load();
    } catch (e) {
      if (cancelledUpload) {
        toast(t('musicUploadCancelled'));
        await load(); // 已完成的批次/分片可能已入库,刷新列表
      } else {
        toast(e.message, 'error');
      }
    } finally {
      uploading = false;
      uploadProgress = null;
      uploadXhr = null;
      phaseLabel = '';
    }
  }

  function cancelUpload() {
    cancelledUpload = true;
    uploadXhr?.abort();
  }

  // ── 曲库列表 ──

  function toggleSort(key) {
    if (sortKey === key) {
      sortDesc = !sortDesc;
    } else {
      sortKey = key;
      sortDesc = false;
    }
  }

  function sortIndicator(key) {
    if (sortKey !== key) return '';
    return sortDesc ? ' ↓' : ' ↑';
  }

  async function remove(name) {
    if (!(await showConfirm(t('musicDeleteConfirm', { name }), { danger: true }))) return;
    try {
      await deleteMusicLibraryFile(name);
      toast(t('musicDeleteDone'), 'success');
      await load();
    } catch (e) {
      toast(e.message, 'error');
    }
  }
</script>

<!-- Esc 关闭扫码弹层（svelte:window 必须位于组件顶层） -->
<svelte:window onkeydown={onQrKeydown} />

<div class="page">
  <!-- 网易云账号（扫码登录，解锁完整音频） -->
  <div class="card account-card">
    <div class="card-header">
      <div>
        <div class="card-title">{t('neteaseAccount')}</div>
        <div class="card-subtitle">{t('neteaseAccountDesc')}</div>
      </div>
      <div class="card-actions">
        {#if account?.loggedIn}
          <button class="btn btn-sm btn-danger" onclick={logout}>{t('neteaseLogout')}</button>
        {:else}
          <button class="btn btn-sm btn-primary" onclick={startLogin} disabled={accountBusy}>
            {t('neteaseScanLogin')}
          </button>
        {/if}
      </div>
    </div>
    {#if account?.loggedIn}
      <div class="account-state">
        <span class="ok"
          >{account.nickname
            ? t('neteaseLoggedIn', { name: account.nickname })
            : t('neteaseLoggedInNoName')}</span
        >
        {#if account.updatedAt}
          <span class="muted"
            >{t('neteaseUpdatedAt', { time: new Date(account.updatedAt).toLocaleString() })}</span
          >
        {/if}
      </div>
    {/if}
  </div>

  <!-- 扫码弹层 -->
  {#if qr}
    <!-- 背景点击关闭仅指针便利;Esc 走组件顶层 onQrKeydown,背景层 aria-hidden 退出可访问树 -->
    <div class="qr-overlay">
      <div class="qr-backdrop" aria-hidden="true" onclick={closeQr}></div>
      <div
        class="qr-modal"
        bind:this={qrModal}
        role="dialog"
        aria-modal="true"
        aria-label={t('neteaseScanTitle')}
        tabindex="-1"
      >
        <div class="qr-title">{t('neteaseScanTitle')}</div>
        <div class="qr-hint">{t('neteaseScanHint')}</div>
        <img src={qr.qrimg} alt="QR" class="qr-img" />
        {#if qrStatus === 'waiting'}
          <div class="qr-status">{t('neteaseScanWaiting')}</div>
        {:else if qrStatus === 'scanned'}
          <div class="qr-status ok">{t('neteaseScanScanned')}</div>
        {:else if qrStatus === 'expired'}
          <div class="qr-status err">{t('neteaseScanExpired')}</div>
          <button class="btn btn-sm btn-primary" onclick={startLogin}>{t('retry')}</button>
        {/if}
      </div>
    </div>
  {/if}

  <!-- 上传 -->
  <div class="card">
    <div class="card-header">
      <div>
        <div class="card-title">{t('musicUploadButton')}</div>
        <div class="card-subtitle">{t('musicUploadHint')}</div>
      </div>
    </div>

    <div
      class="drop-zone"
      class:drag-over={dragOver}
      role="button"
      tabindex="0"
      ondragover={(e) => {
        e.preventDefault();
        dragOver = true;
      }}
      ondragleave={() => {
        dragOver = false;
      }}
      ondrop={onDrop}
      onkeydown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          fileInput?.click();
        }
      }}
    >
      <input
        type="file"
        multiple
        accept=".mp3,.wav,.flac,.m4a,.ogg,.oga,.aac,.opus,audio/*"
        bind:this={fileInput}
        onchange={onPick}
        class="file-input-hidden"
        id="music-file-input"
      />
      <label for="music-file-input" class="drop-zone-content">
        <svg
          class="drop-zone-icon"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.5"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            d="M3 16.5v2.25A2.25 2.25 0 0 0 5.25 21h13.5A2.25 2.25 0 0 0 21 18.75V16.5m-13.5-9L12 3m0 0 4.5 4.5M12 3v13.5"
          />
        </svg>
        <span class="drop-zone-text">{t('musicDropHint')}</span>
      </label>
    </div>

    {#if picked.length > 0}
      <div class="queue">
        <div class="queue-head">
          <span
            >{t('musicQueueCount', { n: picked.length, size: formatSize(pickedTotalBytes) })}</span
          >
          <div class="queue-head-actions">
            <button class="btn btn-sm btn-primary" onclick={upload} disabled={uploading}>
              {#if uploading}
                <span class="spinner"></span>
                {uploadProgress && uploadProgress.total
                  ? Math.round((uploadProgress.bytes * 100) / uploadProgress.total) + '%'
                  : t('uploading')}
              {:else}
                {t('musicUploadCount', { n: picked.length })}
              {/if}
            </button>
            {#if uploading}
              <button class="btn btn-sm btn-ghost" onclick={cancelUpload}
                >{t('cancelUpload')}</button
              >
            {:else}
              <button class="btn btn-sm btn-ghost" onclick={clearQueue}
                >{t('musicQueueClear')}</button
              >
            {/if}
          </div>
        </div>
        {#if uploading && uploadProgress}
          <div class="queue-progress">
            <div class="progress-bar progress-bar-thin">
              <div
                class="progress-fill"
                style="width:{uploadProgress.total
                  ? Math.min(100, (uploadProgress.bytes * 100) / uploadProgress.total)
                  : 0}%;"
              ></div>
            </div>
            <div class="progress-caption">
              {phaseLabel ? phaseLabel + ' · ' : ''}
              {formatSize(uploadProgress.bytes)} / {uploadProgress.total
                ? formatSize(uploadProgress.total)
                : '?'}
            </div>
          </div>
        {/if}
        <ul class="queue-list">
          {#each picked as f, idx (f.name + '-' + f.size)}
            <li class="queue-item">
              <span class="queue-name" title={f.name}>{f.name}</span>
              <span class="queue-size">{formatSize(f.size)}</span>
              {#if !uploading}
                <button
                  class="btn btn-sm btn-ghost queue-remove"
                  onclick={() => removeFromQueue(idx)}
                  title={t('musicQueueRemove')}
                  aria-label={t('musicQueueRemove')}>×</button
                >
              {/if}
            </li>
          {/each}
        </ul>
      </div>
    {/if}

    {#if lastSkipped.length > 0}
      <div class="skip-box">
        <div class="skip-title">{t('musicSkippedTitle', { n: lastSkipped.length })}</div>
        <ul class="skip-list">
          {#each lastSkipped as s, i (i)}
            <li class="skip-item" title={s}>{s}</li>
          {/each}
        </ul>
      </div>
    {/if}
  </div>

  <!-- 曲库列表 -->
  <div class="card">
    <div class="card-header">
      <div>
        <div class="card-title">
          {t('musicLibrary')}
          {#if !loading && !loadError}
            <span class="card-title-count">{files.length}</span>
          {/if}
        </div>
        <div class="card-subtitle">{t('musicLibraryDesc')}</div>
      </div>
      <div class="card-actions">
        {#if files.length > 0}
          <input
            type="search"
            class="input-compact input-w-sm search-input"
            placeholder={t('musicSearchPlaceholder')}
            bind:value={query}
          />
        {/if}
        <button class="btn btn-sm btn-ghost" onclick={load} disabled={loading}
          >{t('refresh')}</button
        >
      </div>
    </div>

    {#if loading}
      <div class="loading-skeleton" style="height:120px;"></div>
    {:else if loadError}
      <div class="empty-state">{loadError}</div>
    {:else if files.length === 0}
      <div class="empty-state">{t('musicLibraryEmpty')}</div>
    {:else}
      <div class="table-wrap">
        <table class="table">
          <thead>
            <tr>
              <th>
                <button class="th-sort-btn" onclick={() => toggleSort('name')}>
                  {t('musicColFile')}{sortIndicator('name')}
                </button>
              </th>
              <th>
                <button class="th-sort-btn" onclick={() => toggleSort('size')}>
                  {t('musicColSize')}{sortIndicator('size')}
                </button>
              </th>
              <th>
                <button class="th-sort-btn" onclick={() => toggleSort('time')}>
                  {t('musicColTime')}{sortIndicator('time')}
                </button>
              </th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {#each filtered as f (f.name)}
              <tr>
                <td class="name-cell" title={f.name}>{f.name}</td>
                <td>{formatSize(f.sizeBytes)}</td>
                <td>{formatDateTime(f.modTime)}</td>
                <td class="actions-cell">
                  <button class="btn btn-sm btn-danger" onclick={() => remove(f.name)}
                    >{t('remove')}</button
                  >
                </td>
              </tr>
            {/each}
            {#if filtered.length === 0}
              <tr>
                <td colspan="4" class="no-match">{t('musicNoMatch')}</td>
              </tr>
            {/if}
          </tbody>
        </table>
      </div>
    {/if}
  </div>
</div>

<style>
  .account-card {
    margin-bottom: 16px;
  }
  .account-state {
    display: flex;
    gap: 12px;
    align-items: baseline;
    font-size: 13px;
  }
  .account-state .ok {
    color: var(--c-text-success);
    font-weight: 600;
  }
  .account-state .muted {
    color: var(--c-text-secondary);
  }
  .qr-overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 100;
  }
  .qr-backdrop {
    position: absolute;
    inset: 0;
  }
  .qr-modal {
    background: var(--c-bg-elevated);
    border: 1px solid var(--c-border-strong);
    border-radius: var(--radius-lg);
    padding: 24px;
    text-align: center;
    max-width: 320px;
    box-shadow: 0 12px 40px rgba(0, 0, 0, 0.25);
    position: relative;
  }
  .qr-title {
    color: var(--c-text-heading);
    font-weight: 600;
    margin-bottom: 4px;
  }
  .qr-hint {
    font-size: 12px;
    color: var(--c-text-secondary);
    margin-bottom: 12px;
  }
  .qr-img {
    width: 220px;
    height: 220px;
    display: block;
    margin: 0 auto 12px;
  }
  .qr-status {
    font-size: 13px;
    color: var(--c-text-secondary);
    margin-bottom: 8px;
  }
  .qr-status.ok {
    color: var(--c-text-success);
  }
  .qr-status.err {
    color: var(--c-text-danger);
  }
  .queue {
    margin-top: 12px;
  }
  .queue-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    font-size: 13px;
    margin-bottom: 8px;
  }
  .queue-head-actions {
    display: flex;
    gap: 8px;
  }
  .queue-progress {
    margin-bottom: 8px;
  }
  .queue-list {
    list-style: none;
    margin: 0;
    padding: 0;
    max-height: 220px;
    overflow-y: auto;
    border: 1px solid var(--c-border);
    border-radius: var(--radius);
  }
  .queue-item {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 10px;
    font-size: 13px;
    border-bottom: 1px solid var(--c-border);
  }
  .queue-item:last-child {
    border-bottom: none;
  }
  .queue-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .queue-size {
    color: var(--c-text-secondary);
    white-space: nowrap;
  }
  .queue-remove {
    padding: 0 8px;
    line-height: 1.4;
  }
  .skip-box {
    margin-top: 12px;
    padding: 10px 12px;
    border: 1px solid var(--c-danger);
    border-radius: var(--radius);
    font-size: 13px;
  }
  .skip-title {
    font-weight: 600;
    margin-bottom: 6px;
    color: var(--c-text-danger);
  }
  .skip-list {
    list-style: none;
    margin: 0;
    padding: 0;
    max-height: 160px;
    overflow-y: auto;
  }
  .skip-item {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--c-text-secondary);
  }
  .search-input {
    min-width: 0;
  }
  .th-sort-btn {
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
    user-select: none;
    white-space: nowrap;
    color: inherit;
    font: inherit;
    text-transform: inherit;
    letter-spacing: inherit;
  }
  .th-sort-btn:hover {
    color: var(--c-accent);
  }
  .th-sort-btn:focus-visible {
    outline: 2px solid var(--c-accent);
    outline-offset: 2px;
  }
  .name-cell {
    max-width: 420px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .actions-cell {
    text-align: right;
  }
  .no-match {
    text-align: center;
    color: var(--c-text-secondary);
    padding: 18px 0;
  }
</style>
