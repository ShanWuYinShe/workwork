<script>
  import { onMount } from 'svelte';
  import { getDashboard, getCurrentUser } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { copyToClipboard } from '../lib/clipboard.js';

  let stats = $state({
    services: '-',
    running: '-',
    dockerVersion: '-',
    cpu: '-',
    memory: '-',
    os: '-',
    arch: '-',
  });
  let services = $state([]);
  let role = $state('');
  let username = $state('');
  let myClients = $state([]);
  let generatedJSON = $state('');
  let error = $state('');
  let loading = $state(true);
  let copied = $state(false);
  let isAdmin = $state(false);

  onMount(async () => {
    try {
      const [user, d] = await Promise.all([getCurrentUser(), getDashboard()]);
      isAdmin = user.role === 'admin';
      role = d.role || user.role;
      username = d.username || user.username;

      if (isAdmin) {
        stats = d.stats || stats;
        const raw = d.services || [];
        const seen = new Set();
        services = raw.filter((s) => {
          if (seen.has(s.name)) return false;
          seen.add(s.name);
          return true;
        });
      } else {
        myClients = d.myClients || [];
        generatedJSON = d.generatedJSON || '';
      }
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  });

  async function copyConfig() {
    if (await copyToClipboard(generatedJSON)) {
      copied = true;
      setTimeout(() => (copied = false), 2000);
    } else {
      error = t('copyFailed');
    }
  }

  function downloadConfig() {
    const blob = new Blob([generatedJSON], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'xray-config.json';
    a.click();
    URL.revokeObjectURL(url);
  }
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('dashboard')}</h1>
  </div>

  {#if error}<div class="toast toast-error">{error}</div>{/if}

  {#if loading}
    <div class="stats">
      {#each Array(4) as _}
        <div class="stat"><div class="loading-skeleton" style="height:80px;"></div></div>
      {/each}
    </div>
  {:else if isAdmin}
    <!-- Admin: system dashboard -->
    <div class="stats">
      <div class="stat">
        <div class="stat-icon blue">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"
            ><path
              d="M20.25 7.5l-.625 10.632a2.25 2.25 0 01-2.247 2.118H6.622a2.25 2.25 0 01-2.247-2.118L3.75 7.5M10 11.25h4M3.375 7.5h17.25c.621 0 1.125-.504 1.125-1.125v-1.5c0-.621-.504-1.125-1.125-1.125H3.375c-.621 0-1.125.504-1.125 1.125v1.5c0 .621.504 1.125 1.125 1.125Z"
            /></svg
          >
        </div>
        <div class="label">{t('servicesCount')}</div>
        <div class="value">{stats.services}</div>
      </div>
      <div class="stat">
        <div class="stat-icon green">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"
            ><path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="M9 12.75L11.25 15 15 9.75M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
            /></svg
          >
        </div>
        <div class="label">{t('running')}</div>
        <div class="value">{stats.running}</div>
      </div>
      <div class="stat">
        <div class="stat-icon purple">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"
            ><path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="M5.25 14.25h13.5m-13.5 0a3 3 0 01-3-3m3 3a3 3 0 100 6h13.5a3 3 0 100-6m-16.5-3a3 3 0 013-3h13.5a3 3 0 013 3m-19.5 0a4.5 4.5 0 01.9-2.7L5.737 5.1a3.375 3.375 0 012.7-1.35h7.126c1.062 0 2.062.5 2.7 1.35l2.587 3.45a4.5 4.5 0 01.9 2.7m0 0a3 3 0 01-3 3m0 3h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008zm-3 6h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008z"
            /></svg
          >
        </div>
        <div class="label">{t('dockerEngine')}</div>
        <div class="value" style="font-size:0.9rem">{stats.dockerVersion}</div>
      </div>
      <div class="stat">
        <div class="stat-icon orange">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"
            ><path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="M15.75 3.75L18 6m0 0l2.25 2.25M18 6l2.25-2.25M18 6l-2.25 2.25m1.5 13.5c-8.284 0-15-6.716-15-15V4.5A2.25 2.25 0 014.5 2.25h1.372c.516 0 .966.351 1.091.852l1.106 4.423c.11.44-.054.902-.417 1.173l-1.293.97a1.062 1.062 0 00-.38 1.21 12.035 12.035 0 007.143 7.143c.441.162.928-.004 1.21-.38l.97-1.293a1.125 1.125 0 011.173-.417l4.423 1.106c.5.125.852.575.852 1.091V19.5a2.25 2.25 0 01-2.25 2.25h-2.25z"
            /></svg
          >
        </div>
        <div class="label">{t('cpuMemory')}</div>
        <div class="value" style="font-size:0.85rem">{stats.cpu} / {stats.memory}</div>
      </div>
    </div>

    <div class="card">
      <div class="card-header">
        <div class="card-title">
          {t('servicesCount')}
          {#if services.length}<span class="card-title-count">{services.length}</span>{/if}
        </div>
      </div>
      {#if services.length}
        <table>
          <thead><tr><th>{t('service')}</th><th>{t('state')}</th><th>{t('image')}</th></tr></thead>
          <tbody>
            {#each services as s}
              <tr>
                <td style="font-weight:500;">{s.name}</td>
                <td>
                  <span class="badge badge-{s.state === 'running' ? 'green' : 'red'}">
                    <span class="badge-dot"></span>{s.state}
                  </span>
                </td>
                <td class="td-muted">{s.image}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}
        <div class="empty-state">{t('noServices')}</div>
      {/if}
    </div>
  {:else}
    <!-- Viewer: personal dashboard -->
    <div class="welcome-card">
      <div class="welcome-avatar">{username[0].toUpperCase()}</div>
      <div>
        <div class="welcome-title">{t('welcomeTitle', { username })}</div>
        <div class="welcome-sub">{t('welcomeSubTitle')}</div>
      </div>
    </div>

    <div class="stats">
      <div class="stat">
        <div class="stat-icon blue">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"
            ><path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="M18 18.72a9.094 9.094 0 0 0 3.741-.479 3 3 0 0 0-4.682-2.72m.94 3.198.001.031c0 .225-.012.447-.037.666A11.944 11.944 0 0 1 12 21c-2.17 0-4.207-.576-5.963-1.584A6.062 6.062 0 0 1 6 18.719m12 0a5.971 5.971 0 0 0-.941-3.197m0 0A5.995 5.995 0 0 0 12 12.75a5.995 5.995 0 0 0-5.058 2.772m0 0a3 3 0 0 0-4.681 2.72 8.986 8.986 0 0 0 3.74.477m.94-3.197a5.971 5.971 0 0 0-.94 3.197M15 6.75a3 3 0 1 1-6 0 3 3 0 0 1 6 0Zm6 3a2.25 2.25 0 1 1-4.5 0 2.25 2.25 0 0 1 4.5 0Zm-13.5 0a2.25 2.25 0 1 1-4.5 0 2.25 2.25 0 0 1 4.5 0Z"
            /></svg
          >
        </div>
        <div class="label">{t('myClients')}</div>
        <div class="value">{myClients.length}</div>
      </div>
    </div>

    {#if myClients.length}
      <div class="card">
        <div class="card-header">
          <div class="card-title">
            {t('myClients')}
            <span class="card-title-count">{myClients.length}</span>
          </div>
        </div>
        <table>
          <thead><tr><th>{t('uuid')}</th><th>{t('protocols')}</th></tr></thead>
          <tbody>
            {#each myClients as c}
              <tr>
                <td class="td-mono" title={c.id}>{c.id.slice(0, 16)}...</td>
                <td>
                  <span class="badge badge-green"><span class="badge-dot"></span>{t('protocolReality')}</span>
                  <span class="badge badge-blue">{t('protocolXhttp')}</span>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>

      <div class="card">
        <div class="card-header">
          <div class="card-title">{t('clientConfig')}</div>
          <div class="card-actions">
            <button
              class="btn btn-sm btn-primary"
              onclick={copyConfig}
              aria-label={t('copyConfiguration')}
            >
              {#if copied}{t('copied')}{:else}{t('copy')}{/if}
            </button>
            <button
              class="btn btn-sm btn-ghost"
              onclick={downloadConfig}
              aria-label={t('downloadConfiguration')}>{t('download')}</button
            >
          </div>
        </div>
        <div style="display:flex;gap:0.75rem;margin-bottom:0.75rem;flex-wrap:wrap;">
          <span class="badge badge-gray">{t('socks5')} :10808</span>
          <span class="badge badge-gray">{t('http')} :10809</span>
          <span class="badge badge-gray">{t('balancer')}: {t('protocolReality')} + {t('protocolXhttp')}</span>
        </div>
        <textarea rows="16" readonly>{generatedJSON}</textarea>
        {#if copied}
          <div class="toast toast-success" style="margin-top:0.75rem;">{t('configCopied')}</div>
        {/if}
      </div>
    {:else}
      <div class="card">
        <div class="empty-state">
          <p style="margin-bottom:0.5rem;">{t('noProxyClientsYet')}</p>
          <p style="font-size:0.78rem;">
            {t('goToClients')}
          </p>
        </div>
      </div>
    {/if}
  {/if}
</div>

<style>
  .welcome-card {
    display: flex;
    align-items: center;
    gap: 1rem;
    background: var(--c-bg-elevated);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-lg);
    padding: 1.5rem;
    margin-bottom: 1.5rem;
  }
  .welcome-avatar {
    width: 48px;
    height: 48px;
    border-radius: var(--radius-lg);
    background: linear-gradient(135deg, #2563eb, #7c3aed);
    color: #fff;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 1.25rem;
    font-weight: 700;
    flex-shrink: 0;
  }
  .welcome-title {
    font-size: 1.1rem;
    font-weight: 700;
    color: var(--c-text-heading);
    margin-bottom: 0.2rem;
  }
  .welcome-sub {
    font-size: 0.78rem;
    color: var(--c-text-secondary);
  }
</style>
