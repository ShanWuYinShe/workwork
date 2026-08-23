<script>
  import { onMount } from 'svelte';
  import {
    getTransferFiles,
    deleteTransferFile,
    transferUploadUrl,
    getCurrentUser,
  } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { copyToClipboard } from '../lib/clipboard.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import { toast } from '../lib/toast.svelte.js';

  let files = $state([]);
  let loading = $state(true);
  let uploading = $state(false);
  let uploadProgress = $state(null);
  let deletingCode = $state('');
  let isAdmin = $state(false);
  let totalSize = $state(0);
  let maxTotal = $state(20 * 1024 * 1024 * 1024);
  let fileInput = $state(null);
  let selectedFile = $state(null);
  let expiresIn = $state('168h');
  let maxDownloads = $state(0);
  let dragOver = $state(false);
  let lastUploadedLink = $state('');
  let uploadXhr = $state(null);

  onMount(async () => {
    try {
      const user = await getCurrentUser();
      isAdmin = user.role === 'admin';
      await loadFiles();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      loading = false;
    }
  });

  async function loadFiles() {
    try {
      const d = await getTransferFiles();
      files = d.files || [];
      totalSize = d.total_size || 0;
      maxTotal = d.max_total || maxTotal;
    } catch (e) {
      toast(e.message, 'error');
    }
  }

  function onFileChange(e) {
    selectedFile = e.target.files[0] || null;
  }

  function onDrop(e) {
    e.preventDefault();
    dragOver = false;
    if (e.dataTransfer.files.length > 0) {
      selectedFile = e.dataTransfer.files[0];
      if (fileInput) fileInput.value = '';
    }
  }

  async function doUpload() {
    if (!selectedFile) {
      toast(t('selectFileFirst'), 'error');
      return;
    }
    uploading = true;
    uploadProgress = null;
    try {
      const formData = new FormData();
      formData.append('file', selectedFile);
      formData.append('expires_in', expiresIn);
      formData.append('max_downloads', String(maxDownloads));

      const result = await new Promise((resolve, reject) => {
        const xhr = new XMLHttpRequest();
        uploadXhr = xhr;
        // 上传没有默认超时会永久卡在"上传中"；给一个宽松的整体超时（2 小时），
        // 并允许用户手动取消（xhr.abort() 会走 onerror 分支）。
        xhr.timeout = 2 * 60 * 60 * 1000;
        xhr.ontimeout = () => reject(new Error(t('uploadTimeout')));
        xhr.open('POST', transferUploadUrl);
        // Include credentials for basic auth
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
          if (xhr.status >= 200 && xhr.status < 300) {
            try {
              const json = JSON.parse(xhr.responseText);
              if (json.success) {
                resolve(json.data);
              } else {
                reject(new Error(json.error || 'Upload failed'));
              }
            } catch {
              reject(new Error('Invalid response'));
            }
          } else {
            try {
              const json = JSON.parse(xhr.responseText);
              reject(new Error(json.error || `${xhr.status} ${xhr.statusText}`));
            } catch {
              reject(new Error(`${xhr.status} ${xhr.statusText}`));
            }
          }
        };
        xhr.onerror = () => reject(new Error('Network error'));
        xhr.send(formData);
      });

      toast(t('uploadSuccess'), 'success');
      if (result?.code) {
        lastUploadedLink = shareUrl(result.code);
      }
      selectedFile = null;
      if (fileInput) fileInput.value = '';
      await loadFiles();
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

  async function doDelete(code) {
    const target = files.find((f) => f.code === code);
    if (
      !(await showConfirm(
        t('confirmDeleteTransferFile', { filename: target?.filename || code }),
        { danger: true },
      ))
    )
      return;
    deletingCode = code;
    try {
      await deleteTransferFile(code);
      toast(t('fileDeleted'), 'success');
      await loadFiles();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      deletingCode = '';
    }
  }

  function shareUrl(code) {
    return window.location.origin + '/api/transfer/' + encodeURIComponent(code);
  }

  async function copyLink(code) {
    if (await copyToClipboard(shareUrl(code))) {
      toast(t('linkCopied'), 'success');
    } else {
      toast(t('copyFailed'), 'error');
    }
  }

  async function copyLastUploadedLink() {
    if (await copyToClipboard(lastUploadedLink)) {
      toast(t('linkCopied'), 'success');
    } else {
      toast(t('copyFailed'), 'error');
    }
  }

  function formatSize(bytes) {
    if (bytes == null) return '? MB';
    if (bytes >= 1024 * 1024 * 1024) return (bytes / 1024 / 1024 / 1024).toFixed(1) + ' GB';
    if (bytes >= 1024 * 1024) return (bytes / 1024 / 1024).toFixed(0) + ' MB';
    if (bytes >= 1024) return (bytes / 1024).toFixed(0) + ' KB';
    return bytes + ' B';
  }

  function formatDate(s) {
    if (!s) return '';
    return s.slice(0, 16).replace('T', ' ');
  }

  function isExpired(f) {
    if (!f.expires_at) return false;
    return new Date(f.expires_at) < new Date();
  }

  function downloadCount(f) {
    if (f.max_downloads === 0) {
      return f.downloads + ' / ∞';
    }
    return f.downloads + ' / ' + f.max_downloads;
  }

  let usagePercent = $derived(totalSize > 0 ? Math.min(100, (totalSize / maxTotal) * 100) : 0);
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('transfer')}</h1>
  </div>

  <!-- Stats -->
  <div class="stats">
    <div class="stat">
      <div class="stat-icon purple">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            d="M3 16.5v2.25A2.25 2.25 0 0 0 5.25 21h13.5A2.25 2.25 0 0 0 21 18.75V16.5m-13.5-9L12 3m0 0 4.5 4.5M12 3v13.5"
          />
        </svg>
      </div>
      <div class="label">{t('storageUsed')}</div>
      <div class="value">{formatSize(totalSize)}</div>
    </div>
    <div class="stat">
      <div class="stat-icon blue">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            d="M19.5 14.25v-2.625a3.375 3.375 0 0 0-3.375-3.375h-1.5A1.125 1.125 0 0 1 13.5 7.125v-1.5a3.375 3.375 0 0 0-3.375-3.375H8.25m2.25 0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 0 0-9-9Z"
          />
        </svg>
      </div>
      <div class="label">{t('totalFiles')}</div>
      <div class="value">{files.length}</div>
    </div>
  </div>

  <!-- Usage bar -->
  <div class="card card-compact">
    <div class="metric-row">
      <span>{t('storageUsed')}: {formatSize(totalSize)} / {formatSize(maxTotal)}</span>
      <span>{usagePercent.toFixed(1)}%</span>
    </div>
    <div class="progress-bar">
      <div class="progress-fill" style="width:{usagePercent}%;"></div>
    </div>
  </div>

  <!-- Upload card -->
  <div class="card">
    <div class="card-header">
      <div class="card-title">{t('uploadFile')}</div>
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
        bind:this={fileInput}
        onchange={onFileChange}
        class="file-input-hidden"
        id="transfer-file-input"
      />
      <label for="transfer-file-input" class="drop-zone-content">
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
        <span class="drop-zone-text">
          {selectedFile
            ? selectedFile.name + ' (' + formatSize(selectedFile.size) + ')'
            : t('dragOrClick')}
        </span>
      </label>
    </div>
    <div class="inline-actions upload-options">
      <div class="inline-field">
        <label for="transfer-expiry" class="inline-label">{t('expiry')}:</label>
        <select id="transfer-expiry" bind:value={expiresIn} class="input-compact">
          <option value="24h">{t('1d')}</option>
          <option value="72h">{t('3d')}</option>
          <option value="168h">{t('7d')}</option>
          <option value="720h">{t('30d')}</option>
        </select>
      </div>
      <div class="inline-field">
        <label for="transfer-max-dl" class="inline-label">{t('maxDownloads')}:</label>
        <select id="transfer-max-dl" bind:value={maxDownloads} class="input-compact">
          <option value={0}>{t('unlimited')}</option>
          <option value={1}>1</option>
          <option value={5}>5</option>
          <option value={10}>10</option>
          <option value={50}>50</option>
          <option value={100}>100</option>
        </select>
      </div>
      <button
        class="btn btn-primary btn-sm"
        onclick={doUpload}
        disabled={!selectedFile || uploading}
      >
        {#if uploading}
          <span class="spinner"></span>
          {uploadProgress ? uploadProgress.percent + '%' : t('uploading')}
        {:else}
          {t('upload')}
        {/if}
      </button>
      {#if uploading}
        <button class="btn btn-ghost btn-sm" onclick={cancelUpload}>{t('cancelUpload')}</button>
      {/if}
    </div>
    {#if uploading && uploadProgress}
      <div class="upload-progress">
        <div class="progress-bar progress-bar-thin">
          <div class="progress-fill" style="width:{uploadProgress.percent || 0}%;"></div>
        </div>
        <div class="progress-caption">
          {formatSize(uploadProgress.bytes)} / {uploadProgress.total
            ? formatSize(uploadProgress.total)
            : '?'}
        </div>
      </div>
    {/if}
    {#if lastUploadedLink}
      <div class="upload-result">
        <span class="td-mono">{lastUploadedLink}</span>
        <button class="btn btn-ghost btn-sm" onclick={copyLastUploadedLink}>
          {t('copyLink')}
        </button>
      </div>
    {/if}
  </div>

  <!-- File list -->
  {#if files.length}
    <div class="card">
      <div class="card-header">
        <div class="card-title">
          {t('sharedFiles')}
          <span class="card-title-count">{files.length}</span>
        </div>
      </div>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t('filename')}</th>
              <th>{t('size')}</th>
              {#if isAdmin}<th>{t('uploader')}</th>{/if}
              <th>{t('expiresAt')}</th>
              <th>{t('downloadCount')}</th>
              <th>{t('shareLink')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {#each files as f}
              <tr>
                <td class="cell-strong cell-ellipsis" title={f.filename}>
                  {f.filename}
                </td>
                <td class="td-muted">{formatSize(f.size)}</td>
                {#if isAdmin}<td class="td-muted">{f.uploader}</td>{/if}
                <td class={isExpired(f) ? 'td-muted text-danger' : 'td-muted'}
                  >{formatDate(f.expires_at)}</td
                >
                <td class="td-muted">{downloadCount(f)}</td>
                <td>
                  <button
                    class="btn btn-ghost btn-sm"
                    onclick={() => copyLink(f.code)}
                    title={t('copyLink')}
                  >
                    {t('copyLink')}
                  </button>
                </td>
                <td>
                  {#if deletingCode === f.code}
                    <span class="spinner"></span>
                  {:else}
                    <button class="btn btn-danger btn-sm" onclick={() => doDelete(f.code)}
                      >{t('delete')}</button
                    >
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
  {:else if !loading}
    <div class="card">
      <div class="empty-state">{t('noTransferFiles')}</div>
    </div>
  {/if}
</div>

<style>
  .upload-options {
    margin-top: 0.75rem;
  }
  .upload-progress {
    margin-top: 0.5rem;
  }
  .upload-result {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-top: 0.75rem;
    padding: 0.6rem 0.75rem;
    border: 1px solid var(--c-accent-border);
    border-radius: var(--radius);
    background: var(--c-accent-bg);
    color: var(--c-text-secondary);
    overflow: hidden;
  }
  .upload-result .td-mono {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
