<script>
  import { onMount } from 'svelte';
  import { getMusicLibrary, uploadMusicLibrary, deleteMusicLibraryFile } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import { toast } from '../lib/toast.svelte.js';

  let loading = $state(true);
  let loadError = $state('');
  let files = $state([]);
  let fileInput = $state(null);
  let picked = $state([]);
  let uploading = $state(false);

  onMount(load);

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
