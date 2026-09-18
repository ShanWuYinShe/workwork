<script>
  import { onMount } from 'svelte';
  import {
    getCurrentUser,
    getClaudeReleases,
    getClaudeStored,
    getClaudePlatforms,
    downloadClaudeVersion,
    deleteClaudeDownload,
    refreshClaudeVersions,
    getXrayReleases,
    getXrayStored,
    getXrayPlatforms,
    downloadXrayVersion,
    deleteXrayDownload,
    refreshXrayVersions,
  } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import { toast } from '../lib/toast.svelte.js';
  import { formatDate, formatSize } from '../lib/format.js';

  let tab = $state('claude');
  let releases = $state([]);
  let stored = $state([]);
  let loading = $state(true);
  let isAdmin = $state(false);
  let refreshing = $state(false);
  let downloading = $state(false);
  let downloadKey = $state('');
  let downloadProgress = $state(null);
  let deletingFile = $state('');
  let versionPlatforms = $state({});

  onMount(async () => {
    try {
      const user = await getCurrentUser();
      isAdmin = user.role === 'admin';
      await loadTabData();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      loading = false;
    }
  });

  // 竞态守卫：快速切换 tab 时，旧 tab 的响应后到会覆盖新 tab 数据
  // （统计/表格/删除目标错位）。响应回来时丢弃已过期的结果。
  let tabLoadSeq = 0;

  async function loadTabData() {
    const seq = ++tabLoadSeq;
    loading = true;
    try {
      if (tab === 'claude') {
        if (isAdmin) {
          const d = await getClaudeReleases();
          if (seq !== tabLoadSeq) return;
          releases = d.releases || [];
          stored = d.stored || [];
        } else {
          const s = await getClaudeStored();
          if (seq !== tabLoadSeq) return;
          stored = s.stored || [];
          releases = [];
        }
      } else {
        if (isAdmin) {
          const d = await getXrayReleases();
          if (seq !== tabLoadSeq) return;
          releases = d.releases || [];
          stored = d.stored || [];
        } else {
          const s = await getXrayStored();
          if (seq !== tabLoadSeq) return;
          stored = s.stored || [];
          releases = [];
        }
      }
      if (seq !== tabLoadSeq) return;
      versionPlatforms = {};
    } catch (e) {
      if (seq !== tabLoadSeq) return;
      toast(e.message, 'error');
    } finally {
      if (seq === tabLoadSeq) {
        loading = false;
      }
    }
  }

  async function switchTab(next) {
    // 参数改名 next：原名 t 会遮蔽 i18n 翻译函数 t
    tab = next;
    releases = [];
    stored = [];
    versionPlatforms = {};
    await loadTabData();
  }

  async function doRefreshVersions() {
    refreshing = true;
    try {
      const fn = tab === 'claude' ? refreshClaudeVersions : refreshXrayVersions;
      const d = await fn();
      toast(t('versionsRefreshed', { count: d.count }), 'success');
      await loadTabData();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      refreshing = false;
    }
  }

  async function loadPlatforms(version) {
    if (versionPlatforms[version]) return;
    try {
      const fn = tab === 'claude' ? getClaudePlatforms : getXrayPlatforms;
      const d = await fn(version);
      versionPlatforms = { ...versionPlatforms, [version]: d.platforms || [] };
    } catch (e) {
      // 加载失败时不要缓存为空数组——否则会被渲染成红色"无平台"徽章，
      // 让一次性的网络错误看起来像该版本真的没有产物。
      // 保持 undefined（灰色"—"）让用户可重试，并明确报错。
      toast(e?.message || t('loadFailed'), 'error');
    }
  }

  async function doDownload(version, plat) {
    const key = version + '-' + plat;
    downloadKey = key;
    downloading = true;
    downloadProgress = null;
    try {
      const fn = tab === 'claude' ? downloadClaudeVersion : downloadXrayVersion;
      const d = await fn(version, plat, (p) => {
        downloadProgress = p;
      });
      if (d.status === 'already_exists') {
        const msg =
          tab === 'claude'
            ? t('alreadyDownloaded', { version, plat })
            : t('alreadyDownloadedXray', { version, plat });
        toast(msg, 'info');
      } else {
        toast(
          t('downloadSuccess', {
            filename: d.filename,
            size: formatSize(d.size),
            checksum: (d.checksum || '').slice(0, 16),
          }),
          'success',
        );
      }
      await loadTabData();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      downloading = false;
      downloadKey = '';
      downloadProgress = null;
    }
  }

  async function doDelete(filename) {
    if (!(await showConfirm(t('confirmDeleteDownload', { filename }), { danger: true }))) return;
    deletingFile = filename;
    try {
      const fn = tab === 'claude' ? deleteClaudeDownload : deleteXrayDownload;
      await fn(filename);
      toast(t('deleted', { filename }), 'success');
      await loadTabData();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      deletingFile = '';
    }
  }

  function downloadUrl(filename) {
    const prefix = tab === 'claude' ? 'claude-code' : 'xray';
    // 文件名进 URL 路径段须编码:含 #/? 的名字会被截断成错误 URL。
    return `/api/admin/${prefix}/downloads/${encodeURIComponent(filename)}`;
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
          {#if refreshing}<span class="spinner"></span>{/if}
          {t('refreshVersions')}
        </button>
      {/if}
    </div>
  </div>

  <div class="tab-bar">
    <button
      class="tab-btn"
      class:active={tab === 'claude'}
      aria-current={tab === 'claude' ? 'page' : undefined}
      onclick={() => switchTab('claude')}
    >
      {t('downloadsClaude')}
    </button>
    <button
      class="tab-btn"
      class:active={tab === 'xray'}
      aria-current={tab === 'xray' ? 'page' : undefined}
      onclick={() => switchTab('xray')}
    >
      {t('downloadsXray')}
    </button>
  </div>

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
        <div class="card-subtitle">
          {tab === 'claude' ? t('officialBinaries') : t('xrayBinaries')}
        </div>
      </div>
      <div class="table-wrap">
        <table>
          <thead
            ><tr
              ><th>{t('version')}</th><th>{t('platform')}</th><th>{t('size')}</th><th
                >{t('checksum')}</th
              ><th></th></tr
            ></thead
          >
          <tbody>
            {#each stored as s (s.filename)}
              <tr>
                <td class="cell-strong">
                  <a
                    href={downloadUrl(s.filename)}
                    download={s.filename}
                    class="link"
                    title={t('downloadToLocal')}
                  >
                    {s.version}
                  </a>
                </td>
                <td><span class="badge badge-purple">{s.platform_name || s.plat}</span></td>
                <td class="td-muted">{formatSize(s.size)}</td>
                <td class="td-mono muted-text" title={s.checksum}>
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
    </div>
  {/if}

  {#if isAdmin}
    <div class="card">
      <div class="card-header">
        <div class="card-title">
          {t('availableVersions')}
          <span class="card-title-count">{releases.length}</span>
        </div>
        <div class="card-subtitle">
          {t('fromGH')}{latestReleases.length < releases.length
            ? ` · ${t('latestOnly', { count: latestReleases.length })}`
            : ''}
        </div>
      </div>
      {#if loading}
        <div class="loading-skeleton" style="height:200px;"></div>
      {:else}
        <div class="table-wrap">
          <table>
            <thead
              ><tr
                ><th>{t('version')}</th><th>{t('created')}</th><th>{t('platform')}</th><th></th></tr
              ></thead
            >
            <tbody>
              {#each latestReleases as rel (rel.version)}
                {@const vPlats = versionPlatforms[rel.version]}
                <tr>
                  <td class="cell-strong">
                    {rel.version}
                    <button
                      class="btn btn-ghost btn-xs badge-inline"
                      onclick={() => loadPlatforms(rel.version)}
                      disabled={vPlats}
                      title={t('loadPlatforms')}
                    >
                      {vPlats ? '✓' : t('check')}
                    </button>
                  </td>
                  <td class="td-muted">{formatDate(rel.date)}</td>
                  <td>
                    {#if vPlats === undefined}
                      <span class="badge badge-gray">—</span>
                    {:else if vPlats.length === 0}
                      <span class="badge badge-red">{t('noPlatforms')}</span>
                    {:else}
                      <div class="badge-list">
                        {#each vPlats as p (p.plat)}
                          {@const key = rel.version + '-' + p.plat}
                          {#if alreadyDownloaded.has(key)}
                            <span class="badge badge-green">{p.label}</span>
                          {:else if downloadKey === key && downloadProgress}
                            <span class="badge badge-blue badge-progress"
                              >{downloadProgress.percent || 0}%</span
                            >
                          {:else if downloadKey === key}
                            <span class="badge badge-blue"
                              ><span class="spinner spinner-xs"></span></span
                            >
                          {:else}
                            <button
                              class="badge badge-blue badge-action"
                              onclick={() => doDownload(rel.version, p.plat)}
                              disabled={downloading}
                            >
                              {p.label}
                            </button>
                          {/if}
                        {/each}
                      </div>
                    {/if}
                  </td>
                  <td>
                    {#if downloadKey.startsWith(rel.version) && downloadProgress}
                      <div class="progress-bar progress-bar-sm">
                        <div
                          class="progress-fill"
                          style="width:{downloadProgress.percent || 0}%;"
                        ></div>
                      </div>
                      <div class="progress-caption">
                        {formatSize(downloadProgress.bytes)} / {downloadProgress.total
                          ? formatSize(downloadProgress.total)
                          : '?'}
                      </div>
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </div>
  {/if}

  {#if !loading && !isAdmin && stored.length === 0}
    <div class="card">
      <div class="empty-state">
        {tab === 'claude' ? t('noClaudeCodeAskAdmin') : t('noXrayAskAdmin')}
      </div>
    </div>
  {/if}
</div>
