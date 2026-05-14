<script>
  import { onMount } from 'svelte';
  import { getUsers, getCurrentUser, addXrayClient, deleteXrayClient, getXrayClientConfig } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { copyToClipboard } from '../lib/clipboard.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import Toast from '../lib/Toast.svelte';

  let clients = $state([]);
  let generatedJSON = $state('');
  let loading = $state(true);
  let error = $state('');
  let copied = $state(false);
  let isAdmin = $state(false);
  let currentUser = $state('');
  let deletingId = $state('');
  let selectedUUID = $state('');
  let configLoading = $state(false);

  onMount(async () => {
    try {
      const [d, user] = await Promise.all([getUsers(), getCurrentUser()]);
      const allClients = d.xrayClients || [];
      isAdmin = user.role === 'admin';
      currentUser = user.username;
      clients = isAdmin ? allClients : allClients.filter((c) => c.owner === currentUser);

      if (!isAdmin && clients.length === 1) {
        selectedUUID = clients[0].id;
        await loadConfig(selectedUUID);
      }
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
    isAdmin = user.role === 'admin';
    currentUser = user.username;
    clients = isAdmin ? allClients : allClients.filter((c) => c.owner === currentUser);

    // Reset selected if it no longer exists
    if (selectedUUID && !clients.find((c) => c.id === selectedUUID)) {
      selectedUUID = '';
      generatedJSON = '';
    }
  }

  async function loadConfig(uuid) {
    configLoading = true;
    error = '';
    try {
      const d = await getXrayClientConfig(uuid);
      generatedJSON = d.config || '';
      selectedUUID = uuid;
    } catch (e) {
      error = e.message;
    } finally {
      configLoading = false;
    }
  }

  async function generateAndAdd() {
    error = '';
    const uuid = crypto.randomUUID();
    try {
      await addXrayClient(uuid);
      await loadData();
      selectedUUID = uuid;
      await loadConfig(uuid);
    } catch (e) {
      error = e.message;
    }
  }

  async function deleteClient(uuid) {
    if (!(await showConfirm(t('confirmDeleteClient', { uuid: uuid.slice(0, 8) })))) return;
    deletingId = uuid;
    error = '';
    try {
      await deleteXrayClient(uuid);
      if (selectedUUID === uuid) {
        selectedUUID = '';
        generatedJSON = '';
      }
      await loadData();
    } catch (e) {
      error = e.message;
    } finally {
      deletingId = '';
    }
  }

  async function copyConfig() {
    if (await copyToClipboard(generatedJSON)) {
      copied = true;
      setTimeout(() => (copied = false), 2000);
    } else {
      error = t('copyFailedManual');
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

  let hasOwnClient = $derived(!isAdmin && clients.length >= 1);
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('clients')}</h1>
  </div>

  <Toast type="error" visible={!!error} onclose={() => error = ''}>{error}</Toast>

  {#if !isAdmin}
    <div class="card">
      <div class="card-header">
        <div class="card-title">{t('generateClient')}</div>
      </div>
      <div style="display:flex;align-items:center;gap:0.75rem;">
        {#if hasOwnClient}
          <span style="font-size:0.75rem;color:var(--c-text-muted);">{t('oneClientLimit')}</span>
        {:else}
          <button class="btn btn-success" onclick={generateAndAdd} disabled={loading}>
            {loading ? t('generating') : t('generateAndAdd')}
          </button>
          <span style="font-size:0.75rem;color:var(--c-text-muted);">{t('generateDesc')}</span>
        {/if}
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
          <thead>
            <tr>
              <th>{t('uuid')}</th>
              {#if isAdmin}<th>{t('owner')}</th>{/if}
              <th>{t('protocols')}</th>
              <th>{t('actions')}</th>
            </tr>
          </thead>
          <tbody>
            {#each clients as c}
              <tr
                class="clickable-row"
                class:selected={selectedUUID === c.id}
                tabindex="0"
                role="button"
                onclick={() => loadConfig(c.id)}
                onkeydown={(e) => e.key === 'Enter' && loadConfig(c.id)}
              >
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
                    <button
                      class="btn btn-danger btn-sm"
                      onclick={(e) => { e.stopPropagation(); deleteClient(c.id); }}
                    >{t('delete')}</button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}
        <div class="empty-state">
          {isAdmin ? t('noProxyClients') : t('noClientsYet')}
        </div>
      {/if}
    </div>
  {/if}

  {#if generatedJSON}
    <div class="card">
      <div class="card-header">
        <div class="card-title">
          {t('clientConfig')}
          {#if selectedUUID}
            <span class="badge badge-gray" style="margin-left:0.5rem;font-family:monospace;font-size:0.65rem;">
              {selectedUUID.slice(0, 8)}...
            </span>
          {/if}
          {#if configLoading}<span class="spinner" style="margin-left:0.5rem;"></span>{/if}
        </div>
        <div class="card-actions">
          <button class="btn btn-sm btn-primary" onclick={copyConfig} aria-label={t('copyConfiguration')}>
            {copied ? t('copied') : t('copyConfig')}
          </button>
          <button class="btn btn-sm btn-ghost" onclick={downloadConfig} aria-label={t('downloadConfiguration')}>
            {t('download')}
          </button>
        </div>
      </div>
      <div style="display:flex;gap:0.75rem;margin-bottom:0.75rem;flex-wrap:wrap;">
        <span class="badge badge-gray">{t('socks5')}:10808</span>
        <span class="badge badge-gray">{t('http')}:10809</span>
        <span class="badge badge-gray">{t('balancer')}: {t('protocolReality')} + {t('protocolXhttp')}</span>
      </div>
      <textarea rows="16" readonly>{generatedJSON}</textarea>
      {#if copied}
        <Toast type="success" visible={copied} onclose={() => copied = false}>{t('configCopied')}</Toast>
      {/if}
    </div>
  {:else if clients.length && !configLoading}
    <div class="card">
      <div class="empty-state">
        {isAdmin ? t('clickClientToView') : t('noProxyClients')}
      </div>
    </div>
  {/if}
</div>

<style>
  .clickable-row {
    cursor: pointer;
  }
  .clickable-row:hover {
    background: var(--c-bg-hover, rgba(255,255,255,0.03));
  }
  .clickable-row.selected {
    background: var(--c-accent-bg, rgba(59,130,246,0.1));
  }
</style>
