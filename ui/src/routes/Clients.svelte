<script>
  import { onMount } from 'svelte';
  import { getUsers, getCurrentUser, addXrayClient, deleteXrayClient } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';

  let clients = $state([]);
  let generatedJSON = $state('');
  let newUUID = $state('');
  let loading = $state(true);
  let error = $state('');
  let copied = $state(false);
  let isAdmin = $state(false);
  let currentUser = $state('');
  let deletingId = $state('');

  onMount(async () => {
    try {
      const [d, user] = await Promise.all([getUsers(), getCurrentUser()]);
      const allClients = d.xrayClients || [];
      generatedJSON = d.generatedJSON || '';
      isAdmin = user.role === 'admin';
      currentUser = user.username;
      clients = isAdmin ? allClients : allClients.filter((c) => c.owner === currentUser);
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  });

  async function loadData() {
    const d = await getUsers();
    const user = await getCurrentUser();
    const allClients = d.xrayClients || [];
    generatedJSON = d.generatedJSON || '';
    isAdmin = user.role === 'admin';
    currentUser = user.username;
    clients = isAdmin ? allClients : allClients.filter((c) => c.owner === currentUser);
  }

  async function generateAndAdd() {
    error = '';
    const uuid = crypto.randomUUID();
    try {
      await addXrayClient(uuid);
      await loadData();
    } catch (e) {
      error = e.message;
    }
  }

  async function deleteClient(uuid) {
    if (!confirm(t('confirmDeleteClient', { uuid: uuid.slice(0, 8) }))) return;
    deletingId = uuid;
    error = '';
    try {
      await deleteXrayClient(uuid);
      await loadData();
    } catch (e) {
      error = e.message;
    } finally {
      deletingId = '';
    }
  }

  async function copyConfig() {
    try {
      await navigator.clipboard.writeText(generatedJSON);
      copied = true;
      setTimeout(() => (copied = false), 2000);
    } catch {
      const ta = document.createElement('textarea');
      ta.value = generatedJSON;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      try {
        document.execCommand('copy');
        copied = true;
        setTimeout(() => (copied = false), 2000);
      } catch {
        error = t('copyFailedManual');
      }
      document.body.removeChild(ta);
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
    <h1 class="page-title">{t('clients')}</h1>
  </div>

  {#if error}<div class="toast toast-error">{error}</div>{/if}

  {#if !isAdmin}
    <div class="card">
      <div class="card-header">
        <div class="card-title">{t('generateClient')}</div>
      </div>
      <div style="display:flex;align-items:center;gap:0.75rem;">
        <button class="btn btn-success" onclick={generateAndAdd} disabled={loading}>
          {loading ? t('generating') : t('generateAndAdd')}
        </button>
        <span style="font-size:0.75rem;color:var(--c-text-muted);"
          >{t('generateDesc')}</span
        >
      </div>
    </div>
  {/if}

  {#if loading}
    <div class="card"><div class="loading-skeleton" style="height:120px;"></div></div>
  {:else}
    <div class="card">
      <div class="card-header">
        <div class="card-title">
          {isAdmin ? t('allXrayClients') : t('myXrayClients')}
          {#if clients.length}<span class="card-title-count">{clients.length}</span>{/if}
        </div>
      </div>
      {#if clients.length}
        <table>
          <thead
            ><tr
              ><th>{t('uuid')}</th>{#if isAdmin}<th>{t('owner')}</th>{/if}<th>{t('protocols')}</th><th>{t('actions')}</th></tr
            ></thead
          >
          <tbody>
            {#each clients as c}
              <tr>
                <td class="td-mono" title={c.id}>{c.id.slice(0, 16)}...</td>
                {#if isAdmin}
                  <td class="td-muted">{c.owner || '—'}</td>
                {/if}
                <td>
                  <span class="badge badge-green"><span class="badge-dot"></span>{t('protocolReality')}</span>
                  <span class="badge badge-blue">{t('protocolXhttp')}</span>
                </td>
                <td>
                  {#if deletingId === c.id}
                    <span class="spinner"></span>
                  {:else}
                    <button class="btn btn-danger btn-sm" onclick={() => deleteClient(c.id)}
                      >{t('delete')}</button
                    >
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}
        <div class="empty-state">
          {isAdmin
            ? t('noProxyClients')
            : t('noClientsYet')}
        </div>
      {/if}
    </div>
  {/if}

  {#if clients.length}
    <div class="card">
      <div class="card-header">
        <div class="card-title">{t('clientConfig')}</div>
        <div class="card-actions">
          <button
            class="btn btn-sm btn-primary"
            onclick={copyConfig}
            aria-label={t('copyConfiguration')}
          >
            {#if copied}{t('copied')}{:else}{t('copyConfig')}{/if}
          </button>
          <button
            class="btn btn-sm btn-ghost"
            onclick={downloadConfig}
            aria-label={t('downloadConfiguration')}>{t('download')}</button
          >
        </div>
      </div>
      <div style="display:flex;gap:0.75rem;margin-bottom:0.75rem;flex-wrap:wrap;">
        <span class="badge badge-gray">{t('socks5')}:10808</span>
        <span class="badge badge-gray">{t('http')}:10809</span>
        <span class="badge badge-gray">{t('balancer')}: {t('protocolReality')} + {t('protocolXhttp')}</span>
      </div>
      <textarea rows="16" readonly>{generatedJSON}</textarea>
      {#if copied}
        <div class="toast toast-success" style="margin-top:0.75rem;">{t('configCopied')}</div>
      {/if}
    </div>
  {/if}
</div>
