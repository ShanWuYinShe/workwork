<script>
  import { onMount } from 'svelte';
  import {
    getMusicLibrary,
    musicLibraryUploadUrl,
    deleteMusicLibraryFile,
    neteaseLoginStart,
    neteaseLoginPoll,
    neteaseLoginState,
    neteaseLogout,
  } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import { toast } from '../lib/toast.svelte.js';

  let loading = $state(true);
  let loadError = $state('');
  let files = $state([]);

  // 上传队列:拖拽/点击多选进入队列,可单个移除或清空后再一次性提交
  let fileInput = $state(null);
  let picked = $state([]); // File[]
  let dragOver = $state(false);
  let uploading = $state(false);
  let uploadProgress = $state(null); // {bytes, total, percent}
  let uploadXhr = $state(null);
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

  async function upload() {
    if (picked.length === 0) return;
    const form = new FormData();
    for (const f of picked) form.append('files', f);
    uploading = true;
    uploadProgress = null;
    lastSkipped = [];
    try {
      // 批量最多 50 个 × 200MB,fetch 无上传进度且无法取消,走 XHR(同 Transfer 页)。
      const result = await new Promise((resolve, reject) => {
        const xhr = new XMLHttpRequest();
        uploadXhr = xhr;
        xhr.timeout = 2 * 60 * 60 * 1000;
        xhr.ontimeout = () => reject(new Error(t('uploadTimeout')));
        xhr.open('POST', musicLibraryUploadUrl);
        xhr.withCredentials = true;
        xhr.upload.onprogress = (e) => {
          if (e.lengthComputable) {
            uploadProgress = {
              bytes: e.loaded,
              total: e.total,
              percent: Math.round((e.loaded * 100) / e.total),
            };
          }
        };
        xhr.onload = () => {
          try {
            const json = JSON.parse(xhr.responseText);
            if (xhr.status >= 200 && xhr.status < 300 && json.success) {
              resolve(json.data);
            } else {
              reject(new Error(json.error || `${xhr.status} ${xhr.statusText}`));
            }
          } catch {
            reject(new Error(`${xhr.status} ${xhr.statusText}`));
          }
        };
        xhr.onerror = () => reject(new Error('Network error'));
        xhr.send(form);
      });

      const savedCount = result?.saved?.length ?? 0;
      lastSkipped = result?.skipped ?? [];
      if (savedCount > 0) toast(t('musicUploadDone', { n: savedCount }), 'success');
      picked = [];
      await load();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      uploading = false;
      uploadProgress = null;
      uploadXhr = null;
    }
  }

  function cancelUpload() {
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

  function fmtSize(bytes) {
    if (bytes >= 1 << 20) return (bytes / (1 << 20)).toFixed(1) + ' MB';
    if (bytes >= 1 << 10) return (bytes / (1 << 10)).toFixed(0) + ' KB';
    return bytes + ' B';
  }
</script>

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
    <div class="qr-overlay" onclick={closeQr} role="presentation">
      <div
        class="qr-modal"
        onclick={(e) => e.stopPropagation()}
        role="dialog"
        aria-label={t('neteaseScanTitle')}
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
          <span>{t('musicQueueCount', { n: picked.length, size: fmtSize(pickedTotalBytes) })}</span>
          <div class="queue-head-actions">
            <button class="btn btn-sm btn-primary" onclick={upload} disabled={uploading}>
              {#if uploading}
                <span class="spinner"></span>
                {uploadProgress ? uploadProgress.percent + '%' : t('uploading')}
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
              <div class="progress-fill" style="width:{uploadProgress.percent || 0}%;"></div>
            </div>
            <div class="progress-caption">
              {fmtSize(uploadProgress.bytes)} / {uploadProgress.total
                ? fmtSize(uploadProgress.total)
                : '?'}
            </div>
          </div>
        {/if}
        <ul class="queue-list">
          {#each picked as f, idx (f.name + '-' + f.size)}
            <li class="queue-item">
              <span class="queue-name" title={f.name}>{f.name}</span>
              <span class="queue-size">{fmtSize(f.size)}</span>
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
          {#each lastSkipped as s}
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
              <th class="th-sortable" onclick={() => toggleSort('name')}
                >{t('musicColFile')}{sortIndicator('name')}</th
              >
              <th class="th-sortable" onclick={() => toggleSort('size')}
                >{t('musicColSize')}{sortIndicator('size')}</th
              >
              <th class="th-sortable" onclick={() => toggleSort('time')}
                >{t('musicColTime')}{sortIndicator('time')}</th
              >
              <th></th>
            </tr>
          </thead>
          <tbody>
            {#each filtered as f (f.name)}
              <tr>
                <td class="name-cell" title={f.name}>{f.name}</td>
                <td>{fmtSize(f.sizeBytes)}</td>
                <td>{new Date(f.modTime).toLocaleString()}</td>
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
    color: var(--success, #16a34a);
    font-weight: 600;
  }
  .account-state .muted {
    color: var(--text-secondary, #888);
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
  .qr-modal {
    background: var(--bg-card, #fff);
    border-radius: 12px;
    padding: 24px;
    text-align: center;
    max-width: 320px;
    box-shadow: 0 12px 40px rgba(0, 0, 0, 0.25);
  }
  .qr-title {
    font-weight: 600;
    margin-bottom: 4px;
  }
  .qr-hint {
    font-size: 12px;
    color: var(--text-secondary, #888);
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
    color: var(--text-secondary, #888);
    margin-bottom: 8px;
  }
  .qr-status.ok {
    color: var(--success, #16a34a);
  }
  .qr-status.err {
    color: var(--danger, #dc2626);
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
    border: 1px solid var(--border, #e5e7eb);
    border-radius: 8px;
  }
  .queue-item {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 10px;
    font-size: 13px;
    border-bottom: 1px solid var(--border, #f0f0f0);
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
    color: var(--text-secondary, #888);
    white-space: nowrap;
  }
  .queue-remove {
    padding: 0 8px;
    line-height: 1.4;
  }
  .skip-box {
    margin-top: 12px;
    padding: 10px 12px;
    border: 1px solid var(--danger, #dc2626);
    border-radius: 8px;
    font-size: 13px;
  }
  .skip-title {
    font-weight: 600;
    margin-bottom: 6px;
    color: var(--danger, #dc2626);
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
    color: var(--text-secondary, #888);
  }
  .search-input {
    min-width: 0;
  }
  .th-sortable {
    cursor: pointer;
    user-select: none;
    white-space: nowrap;
  }
  .th-sortable:hover {
    color: var(--accent, #2563eb);
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
    color: var(--text-secondary, #888);
    padding: 18px 0;
  }
</style>
