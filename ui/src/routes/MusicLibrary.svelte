<script>
  import { onMount } from 'svelte';
  import {
    getMusicLibrary,
    uploadMusicLibrary,
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
  let fileInput = $state(null);
  let picked = $state([]);
  let uploading = $state(false);

  // 网易账号扫码登录状态
  let account = $state(null); // {loggedIn, nickname?, updatedAt?}
  let accountBusy = $state(false);
  let qr = $state(null); // {key, qrimg}
  let qrStatus = $state('waiting'); // waiting | scanned | expired
  let pollTimer = $state(null);

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
    } catch (e) {
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


  function onPick(event) {
    picked = [...event.target.files];
  }

  async function upload() {
    if (picked.length === 0) return;
    const form = new FormData();
    for (const f of picked) form.append('files', f);
    uploading = true;
    try {
      const result = await uploadMusicLibrary(form);
      const savedCount = result?.saved?.length ?? 0;
      const skippedCount = result?.skipped?.length ?? 0;
      if (savedCount > 0) toast(t('musicUploadDone', { n: savedCount }), 'success');
      if (skippedCount > 0) toast(t('musicUploadSkipped', { n: skippedCount }), 'error');
      picked = [];
      if (fileInput) fileInput.value = '';
      await load();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      uploading = false;
    }
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
        <span class="ok">{account.nickname ? t('neteaseLoggedIn', { name: account.nickname }) : t('neteaseLoggedInNoName')}</span>
        {#if account.updatedAt}
          <span class="muted">{t('neteaseUpdatedAt', { time: new Date(account.updatedAt).toLocaleString() })}</span>
        {/if}
      </div>
    {/if}
  </div>

  <!-- 扫码弹层 -->
  {#if qr}
    <div class="qr-overlay" onclick={closeQr} role="presentation">
      <div class="qr-modal" onclick={(e) => e.stopPropagation()} role="dialog" aria-label={t('neteaseScanTitle')}>
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

  <div class="card">
    <div class="card-header">
      <div>
        <div class="card-title">{t('musicLibrary')}</div>
        <div class="card-subtitle">{t('musicLibraryDesc')}</div>
      </div>
      <div class="card-actions">
        <button class="btn btn-sm btn-ghost" onclick={load} disabled={loading}>{t('refresh')}</button>
      </div>
    </div>

    <div class="upload-row">
      <input
        type="file"
        multiple
        accept=".mp3,.wav,.flac,.m4a,.ogg,.oga,.aac,.opus,audio/*"
        bind:this={fileInput}
        onchange={onPick}
      />
      <button class="btn btn-primary" onclick={upload} disabled={uploading || picked.length === 0}>
        {#if uploading}<span class="spinner"></span>{/if}
        {picked.length > 0 ? t('musicUploadCount', { n: picked.length }) : t('musicUploadButton')}
      </button>
    </div>
    <div class="hint">{t('musicUploadHint')}</div>

    {#if loading}
      <div class="loading-skeleton" style="height:120px;"></div>
    {:else if loadError}
      <div class="empty-state">{loadError}</div>
    {:else if files.length === 0}
      <div class="empty-state">{t('musicLibraryEmpty')}</div>
    {:else}
      <table class="table">
        <thead>
          <tr>
            <th>{t('musicColFile')}</th>
            <th>{t('musicColSize')}</th>
            <th>{t('musicColTime')}</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {#each files as f (f.name)}
            <tr>
              <td class="name-cell">{f.name}</td>
              <td>{fmtSize(f.sizeBytes)}</td>
              <td>{new Date(f.modTime).toLocaleString()}</td>
              <td class="actions-cell">
                <button class="btn btn-sm btn-danger" onclick={() => remove(f.name)}>{t('remove')}</button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
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
  .upload-row {
    display: flex;
    gap: 12px;
    align-items: center;
    margin-bottom: 8px;
  }
  .upload-row input[type='file'] {
    flex: 1;
    font-size: 13px;
  }
  .hint {
    font-size: 12px;
    color: var(--text-secondary, #888);
    margin-bottom: 16px;
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
</style>
