<script>
  import { onMount } from 'svelte';
  import {
    getCurrentUser,
    getClaudeReleases,
    getClaudeStored,
    downloadClaudeVersion,
    deleteClaudeDownload,
    refreshClaudeVersions,
  } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';

  let releases = $state([]);
  let platforms = $state([]);
  let stored = $state([]);
  let loading = $state(true);
  let error = $state('');
  let message = $state('');
  let isAdmin = $state(false);
  let refreshing = $state(false);
  let downloading = $state(false);
  let downloadKey = $state('');
  let deletingFile = $state('');
  let selectedPlat = $state('');

  onMount(async () => {
    try {
      const user = await getCurrentUser();
      isAdmin = user.role === 'admin';
      if (isAdmin) {
        const d = await getClaudeReleases();
        releases = d.releases || [];
        platforms = d.platforms || [];
        stored = d.stored || [];
        if (platforms.length && !selectedPlat) selectedPlat = platforms[0].plat;
      } else {
        const s = await getClaudeStored();
        stored = s.stored || [];
      }
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  });

  async function reloadPageData() {
    loading = true;
    error = '';
    try {
      if (isAdmin) {
        const d = await getClaudeReleases();
        releases = d.releases || [];
        platforms = d.platforms || [];
        stored = d.stored || [];
        if (platforms.length && !selectedPlat) selectedPlat = platforms[0].plat;
      } else {
        const s = await getClaudeStored();
        stored = s.stored || [];
      }
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  async function doRefreshVersions() {
    refreshing = true;
    error = '';
    message = '';
    try {
      const d = await refreshClaudeVersions();
      message = `已刷新 ${d.count} 个版本。`;
      await reloadPageData();
    } catch (e) {
      error = e.message;
    } finally {
      refreshing = false;
    }
  }

  async function doDownload(version, plat) {
    const key = version + '-' + plat;
    downloadKey = key;
    downloading = true;
    error = '';
    message = '';
    try {
      const d = await downloadClaudeVersion(version, plat);
      if (d.status === 'already_exists') {
        message = `${version} (${plat}) 已下载。`;
      } else {
        message = `已下载 ${d.filename}（${formatSize(d.size)}）— SHA256: ${(d.checksum || '').slice(0, 16)}...`;
      }
      await reloadPageData();
    } catch (e) {
      error = e.message;
    } finally {
      downloading = false;
      downloadKey = '';
    }
  }

  async function doDelete(filename) {
    deletingFile = filename;
    error = '';
    message = '';
    try {
      await deleteClaudeDownload(filename);
      message = `已删除 ${filename}。`;
      await reloadPageData();
    } catch (e) {
      error = e.message;
    } finally {
      deletingFile = '';
    }
  }

  function formatSize(bytes) {
    if (!bytes) return '? MB';
    if (bytes > 1024 * 1024 * 1024) return (bytes / 1024 / 1024 / 1024).toFixed(1) + ' GB';
    return (bytes / 1024 / 1024).toFixed(0) + ' MB';
  }

  function formatDate(s) {
    if (!s) return '';
    return s.slice(0, 10);
  }

  let latestReleases = $derived(releases.slice(0, 15));
  let alreadyDownloaded = $derived(new Set(stored.map((s) => s.version + '-' + s.plat)));
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('downloads')}</h1>
    <div class="page-actions">
      {#if isAdmin}
        <button class="btn btn-sm btn-ghost" onclick={doRefreshVersions} disabled={refreshing}>
          {#if refreshing}<span class="spinner" style="margin-right:0.25rem;"></span>{/if}
          {t('refreshVersions')}
        </button>
      {/if}
      <button class="btn btn-sm btn-ghost" onclick={reloadPageData}>{t('refresh')}</button>
    </div>
  </div>

  {#if error}<div class="toast toast-error">{error}</div>{/if}
  {#if message}<div class="toast toast-success">{message}</div>{/if}

  <div class="stats">
    <div class="stat">
      <div class="stat-icon purple">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"
          ><path
            stroke-linecap="round"
            stroke-linejoin="round"
            d="M3 16.5v2.25A2.25 2.25 0 0 0 5.25 21h13.5A2.25 2.25 0 0 0 21 18.75V16.5M16.5 12 12 16.5m0 0L7.5 12m4.5 4.5V3"
          /></svg
        >
      </div>
      <div class="label">{t('stored')}</div>
      <div class="value">{stored.length}</div>
    </div>
    {#if isAdmin}
      <div class="stat">
        <div class="stat-icon blue">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"
            ><path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="M9.813 15.904 9 18.75l-.813-2.846a4.5 4.5 0 0 0-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 0 0 3.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 0 0 3.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 0 0-3.09 3.09ZM18.259 8.715 18 9.75l-.259-1.035a3.375 3.375 0 0 0-2.455-2.456L14.25 6l1.036-.259a3.375 3.375 0 0 0 2.455-2.456L18 2.25l.259 1.035a3.375 3.375 0 0 0 2.455 2.456L21.75 6l-1.036.259a3.375 3.375 0 0 0-2.455 2.456ZM16.894 20.567 16.5 21.75l-.394-1.183a2.25 2.25 0 0 0-1.423-1.423L13.5 18.75l1.183-.394a2.25 2.25 0 0 0 1.423-1.423l.394-1.183.394 1.183a2.25 2.25 0 0 0 1.423 1.423l1.183.394-1.183.394a2.25 2.25 0 0 0-1.423 1.423Z"
            /></svg
          >
        </div>
        <div class="label">{t('available')}</div>
        <div class="value">{releases.length}</div>
      </div>
    {/if}
  </div>

  {#if stored.length}
    <div class="card">
      <div class="card-header">
        <div class="card-title">
          {t('storedDownloads')}
          <span class="card-title-count">{stored.length}</span>
        </div>
        <div class="card-subtitle">{t('officialBinaries')}</div>
      </div>
      <table>
        <thead
          ><tr><th>{t('version')}</th><th>{t('platform')}</th><th>{t('size')}</th><th>{t('checksum')}</th><th></th></tr></thead
        >
        <tbody>
          {#each stored as s}
            <tr>
              <td style="font-weight:500;">
                <a
                  href="/api/admin/claude-code/downloads/{s.filename}"
                  style="color:#93c5fd;text-decoration:none;"
                  title={t('downloadToLocal')}
                >
                  {s.version}
                </a>
              </td>
              <td><span class="badge badge-purple">{s.platform_name || s.plat}</span></td>
              <td class="td-muted">{formatSize(s.size)}</td>
              <td
                class="td-mono"
                style="font-size:0.65rem;color:var(--c-text-muted);"
                title={s.checksum}
              >
                {s.checksum ? s.checksum.slice(0, 12) + '…' : '—'}
              </td>
              <td>
                {#if isAdmin}
                  {#if deletingFile === s.filename}
                    <span class="spinner"></span>
                  {:else}
                    <button class="btn btn-danger btn-sm" onclick={() => doDelete(s.filename)}
                      >{t('delete')}</button
                    >
                  {/if}
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}

  {#if isAdmin}
    <div class="card">
      <div class="card-header">
        <div class="card-title">
          {t('availableVersions')}
          <span class="card-title-count">{latestReleases.length}</span>
        </div>
        <div class="card-subtitle">{t('fromGH')}</div>
      </div>
      {#if loading}
        <div class="loading-skeleton" style="height:200px;"></div>
      {:else}
        <div
          style="padding:0.75rem 0;margin-bottom:0.5rem;display:flex;align-items:center;gap:0.75rem;"
        >
          <label for="plat-select" style="margin-bottom:0;white-space:nowrap;">{t('platformLabel')}</label>
          <select id="plat-select" bind:value={selectedPlat} style="max-width:220px;">
            {#each platforms as p}
              <option value={p.plat}>{p.label}</option>
            {/each}
          </select>
        </div>
        <table>
          <thead><tr><th>{t('version')}</th><th>{t('created')}</th><th>{t('status')}</th><th></th></tr></thead>
          <tbody>
            {#each latestReleases as rel}
              {@const key = rel.version + '-' + selectedPlat}
              <tr>
                <td style="font-weight:500;">{rel.version}</td>
                <td class="td-muted">{formatDate(rel.date)}</td>
                <td>
                  {#if alreadyDownloaded.has(key)}
                    <span class="badge badge-green"><span class="badge-dot"></span>{t('downloadedStatus')}</span>
                  {:else}
                    <span class="badge badge-gray">—</span>
                  {/if}
                </td>
                <td>
                  {#if downloadKey === key}
                    <span class="spinner"></span>
                  {:else if alreadyDownloaded.has(key)}
                    <button class="btn btn-ghost btn-sm" disabled>{t('downloadedStatus')}</button>
                  {:else}
                    <button
                      class="btn btn-primary btn-sm"
                      onclick={() => doDownload(rel.version, selectedPlat)}
                      disabled={downloading}
                    >
                      {t('download')}
                    </button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    </div>
  {/if}

  {#if !loading && !isAdmin && stored.length === 0}
    <div class="card">
      <div class="empty-state">
        {t('noClaudeCodeAskAdmin')}
      </div>
    </div>
  {/if}
</div>
