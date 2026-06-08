<script>
  import { onMount } from 'svelte';
  import { getUsers, getCurrentUser, addXrayClient, deleteXrayClient, getXrayClientConfig } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { copyToClipboard } from '../lib/clipboard.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import { toast } from '../lib/toast.svelte.js';

  let clients = $state([]);
  let generatedJSON = $state('');
  let loading = $state(true);
  let isAdmin = $state(false);
  let currentUser = $state('');
  let deletingId = $state('');
  let creating = $state(false);
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
      toast(e.message, 'error');
    } finally {
      loading = false;
    }
  });

  async function loadData() {
    const [d, user] = await Promise.all([getUsers(), getCurrentUser()]);
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

  async function refresh() {
    loading = true;
    try {
      await loadData();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      loading = false;
    }
  }

  async function loadConfig(uuid) {
    configLoading = true;
    try {
      const d = await getXrayClientConfig(uuid);
      generatedJSON = d.config || '';
      selectedUUID = uuid;
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      configLoading = false;
    }
  }

  async function generateAndAdd() {
    if (creating || hasOwnClient) return;
    const uuid = crypto.randomUUID();
    creating = true;
    try {
      await addXrayClient(uuid);
      await loadData();
      selectedUUID = uuid;
      await loadConfig(uuid);
      toast(t('clientCreated'), 'success');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      creating = false;
    }
  }

  async function deleteClient(uuid) {
    if (!(await showConfirm(t('confirmDeleteClient', { uuid: uuid.slice(0, 8) })))) return;
    deletingId = uuid;
    try {
      await deleteXrayClient(uuid);
      if (selectedUUID === uuid) {
        selectedUUID = '';
        generatedJSON = '';
      }
      await loadData();
      toast(t('clientDeleted'), 'success');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      deletingId = '';
    }
  }

  async function copyConfig() {
    if (await copyToClipboard(generatedJSON)) {
      toast(t('configCopied'), 'success', 2000);
    } else {
      toast(t('copyFailedManual'), 'error');
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
    <div class="page-actions">
      <button class="btn btn-sm btn-ghost" onclick={refresh} disabled={loading || creating}>{t('refresh')}</button>
    </div>
  </div>

  {#if !isAdmin}
    <div class="card">
      <div class="card-header">
        <div class="card-title">{t('generateClient')}</div>
      </div>
      <div class="inline-actions">
        {#if hasOwnClient}
          <span class="helper-text">{t('oneClientLimit')}</span>
        {:else}
          <button class="btn btn-success" onclick={generateAndAdd} disabled={loading || creating}>
            {creating ? t('generating') : t('generateAndAdd')}
          </button>
          <span class="helper-text">{t('generateDesc')}</span>
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
            <span class="badge badge-gray badge-code">
              {selectedUUID.slice(0, 8)}...
            </span>
          {/if}
          {#if configLoading}<span class="spinner spinner-inline"></span>{/if}
        </div>
        <div class="card-actions">
          <button class="btn btn-sm btn-primary" onclick={copyConfig} aria-label={t('copyConfiguration')}>
            {t('copyConfig')}
          </button>
          <button class="btn btn-sm btn-ghost" onclick={downloadConfig} aria-label={t('downloadConfiguration')}>
            {t('download')}
          </button>
        </div>
      </div>
      <div class="badge-list config-badges">
        <span class="badge badge-gray">{t('socks5')}:10808</span>
        <span class="badge badge-gray">{t('http')}:10809</span>
        <span class="badge badge-gray">{t('balancer')}: {t('protocolReality')} + {t('protocolXhttp')}</span>
      </div>
      <textarea rows="16" readonly>{generatedJSON}</textarea>

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
