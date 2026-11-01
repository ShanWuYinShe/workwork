<script>
  import { onMount } from 'svelte';
  import { auth } from '../lib/auth.svelte.js';
  import {
    getUsers,
    addXrayClient,
    deleteXrayClient,
    renameXrayClient,
    getXrayClientConfig,
    getTraffic,
  } from '../lib/api/index.js';
  import { formatSize } from '../lib/format.js';
  import { t } from '../lib/i18n.svelte.js';
  import ClientConfigCard from '../lib/ui/ClientConfigCard.svelte';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import { toast } from '../lib/toast.svelte.js';

  let clients = $state([]);
  // trafficMap: 客户端用户名（Xray email）→ {up, down}（admin 才加载;
  // viewer 看不到该列）。用户名就是日志/统计里看到的 email。
  let trafficMap = $state({});
  let generatedJSON = $state('');
  let loading = $state(true);
  let isAdmin = $derived(auth.isAdmin);
  let currentUser = $derived(auth.user?.username || '');
  let adminUsers = $state([]);
  // 管理员可代其他用户创建客户端；留空表示归自己。
  let ownerInput = $state('');
  let clientName = $state('');
  let deletingId = $state('');
  let renamingId = $state('');
  let renameValue = $state('');
  let savingRename = $state(false);
  let creating = $state(false);
  let selectedUUID = $state('');
  let configLoading = $state(false);
  // 配置请求序号：快速切换客户端时，旧响应不得覆盖新选择。
  let configSeq = 0;

  onMount(async () => {
    try {
      const d = await getUsers();
      const allClients = d.xrayClients || [];
      adminUsers = d.adminUsers || [];
      ownerInput = currentUser;
      clients = isAdmin ? allClients : allClients.filter((c) => c.owner === currentUser);
      if (isAdmin) loadTraffic();

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

  /** 拉取 per-client 流量并转成客户端用户名索引的 map;失败静默(旁路数据)。 */
  async function loadTraffic() {
    try {
      const data = await getTraffic();
      const map = {};
      for (const row of data.traffic || []) {
        if (row.email) map[row.email] = { up: row.up, down: row.down };
      }
      trafficMap = map;
    } catch {
      // 流量数据缺失不影响列表
    }
  }

  async function loadData() {
    const d = await getUsers();
    const allClients = d.xrayClients || [];
    adminUsers = d.adminUsers || [];
    clients = isAdmin ? allClients : allClients.filter((c) => c.owner === currentUser);
    if (isAdmin) loadTraffic();

    // Reset selected if it no longer exists
    if (selectedUUID && !clients.find((c) => c.id === selectedUUID)) {
      configSeq++;
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
    const seq = ++configSeq;
    configLoading = true;
    try {
      const d = await getXrayClientConfig(uuid);
      if (seq !== configSeq) return;
      generatedJSON = d.config || '';
      selectedUUID = uuid;
    } catch (e) {
      if (seq !== configSeq) return;
      toast(e.message, 'error');
    } finally {
      if (seq === configSeq) configLoading = false;
    }
  }

  async function generateAndAdd() {
    if (creating) return;
    const uuid = crypto.randomUUID();
    creating = true;
    try {
      // 管理员填写了其他用户名时按该用户创建；留空/等于自己则走默认归属。
      const owner =
        isAdmin && ownerInput.trim() && ownerInput.trim() !== currentUser
          ? ownerInput.trim()
          : undefined;
      const created = await addXrayClient(uuid, owner, clientName.trim() || undefined);
      clientName = '';
      await loadData();
      selectedUUID = uuid;
      await loadConfig(uuid);
      toast(t('clientCreatedNamed', { name: created?.name || uuid.slice(0, 8) }), 'success');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      creating = false;
    }
  }

  function startRename(client) {
    renamingId = client.id;
    renameValue = client.name || '';
  }

  function cancelRename() {
    renamingId = '';
    renameValue = '';
  }

  async function saveRename(client) {
    const name = renameValue.trim();
    if (!name || savingRename) return;
    savingRename = true;
    try {
      const d = await renameXrayClient(client.id, name);
      renamingId = '';
      renameValue = '';
      await loadData();
      toast(t('clientRenamed', { name: d?.name || name }), 'success');
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      savingRename = false;
    }
  }

  async function deleteClient(uuid) {
    if (
      !(await showConfirm(
        t('confirmDeleteClient', {
          name: clients.find((c) => c.id === uuid)?.name || uuid.slice(0, 8),
        }),
        { danger: true },
      ))
    )
      return;
    deletingId = uuid;
    try {
      await deleteXrayClient(uuid);
      if (selectedUUID === uuid) {
        configSeq++;
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
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('clients')}</h1>
    <div class="page-actions">
      <button class="btn btn-sm btn-ghost" onclick={refresh} disabled={loading || creating}
        >{t('refresh')}</button
      >
    </div>
  </div>

  <div class="card">
    <div class="card-header">
      <div class="card-title">{t('generateClient')}</div>
    </div>
    <div class="inline-actions">
      {#if isAdmin}
        <input
          type="text"
          bind:value={ownerInput}
          list="client-owner-options"
          placeholder={t('ownerPlaceholder')}
          aria-label={t('selectOwner')}
          class="input-w-sm"
        />
        <datalist id="client-owner-options">
          {#each adminUsers as u (u.username)}
            <option value={u.username}></option>
          {/each}
        </datalist>
      {/if}
      <input
        type="text"
        bind:value={clientName}
        placeholder={t('clientNamePlaceholder')}
        aria-label={t('clientName')}
        class="input-w-sm"
        maxlength="64"
      />
      <button class="btn btn-primary" onclick={generateAndAdd} disabled={loading || creating}>
        {creating ? t('generating') : t('generateAndAdd')}
      </button>
      <span class="helper-text">
        {t('generateDesc')}{isAdmin ? ` ${t('ownerHint')}` : ''}
      </span>
    </div>
  </div>

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
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t('clientName')}</th>
                <th>{t('uuid')}</th>
                {#if isAdmin}<th>{t('owner')}</th>{/if}
                {#if isAdmin}<th>{t('traffic')}</th>{/if}
                <th>{t('protocols')}</th>
                <th>{t('actions')}</th>
              </tr>
            </thead>
            <tbody>
              {#each clients as c (c.id)}
                <!-- 行点击仅作鼠标便利；键盘/读屏语义由 UUID 单元格里的真实按钮承载，
                     tr[role=button] 会破坏表格语义并让操作单元格按钮嵌套进"按钮行" -->
                <tr
                  class="clickable-row"
                  class:selected={selectedUUID === c.id}
                  onclick={() => loadConfig(c.id)}
                >
                  <td class="cell-strong td-mono">
                    {#if renamingId === c.id}
                      <div class="rename-cell">
                        <input
                          type="text"
                          class="input-w-sm"
                          bind:value={renameValue}
                          maxlength="64"
                          aria-label={t('clientName')}
                          onkeydown={(e) => {
                            if (e.key === 'Enter') {
                              e.preventDefault();
                              saveRename(c);
                            } else if (e.key === 'Escape') {
                              cancelRename();
                            }
                          }}
                        />
                        <button
                          class="btn btn-sm btn-primary"
                          disabled={savingRename}
                          onclick={(e) => {
                            e.stopPropagation();
                            saveRename(c);
                          }}>{t('save')}</button
                        >
                        <button
                          class="btn btn-sm btn-ghost"
                          disabled={savingRename}
                          onclick={(e) => {
                            e.stopPropagation();
                            cancelRename();
                          }}>{t('cancel')}</button
                        >
                      </div>
                    {:else}
                      <div class="rename-cell">
                        <span>{c.name || '—'}</span>
                        <button
                          class="btn btn-sm btn-ghost"
                          onclick={(e) => {
                            e.stopPropagation();
                            startRename(c);
                          }}>{t('rename')}</button
                        >
                      </div>
                    {/if}
                  </td>
                  <td class="td-mono">
                    <button
                      class="link link-btn cell-strong td-mono"
                      title={c.id}
                      aria-label={t('clientConfig') + ': ' + c.id.slice(0, 8)}
                      onclick={(e) => {
                        e.stopPropagation();
                        loadConfig(c.id);
                      }}>{c.id.slice(0, 16)}...</button
                    >
                  </td>
                  {#if isAdmin}
                    <td class="td-muted">{c.owner || '—'}</td>
                  {/if}
                  {#if isAdmin}
                    {@const usage = trafficMap[c.name]}
                    <td class="td-mono td-muted">
                      {usage ? `${formatSize(usage.up)} / ${formatSize(usage.down)}` : '—'}
                    </td>
                  {/if}
                  <td>
                    <span class="badge badge-blue">{t('protocolXhttp')}</span>
                  </td>
                  <td>
                    {#if deletingId === c.id}
                      <span class="spinner"></span>
                    {:else}
                      <button
                        class="btn btn-danger btn-sm"
                        onclick={(e) => {
                          e.stopPropagation();
                          deleteClient(c.id);
                        }}>{t('delete')}</button
                      >
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}
        <div class="empty-state">
          {isAdmin ? t('noProxyClients') : t('noClientsYet')}
        </div>
      {/if}
    </div>
  {/if}

  {#if generatedJSON}
    <ClientConfigCard json={generatedJSON} uuid={selectedUUID} loading={configLoading} />
  {:else if clients.length && !configLoading}
    <div class="card">
      <div class="empty-state">{t('clickClientToView')}</div>
    </div>
  {/if}
</div>

<style>
  .clickable-row {
    cursor: pointer;
  }
  .clickable-row:hover {
    background: var(--c-bg-hover, rgba(255, 255, 255, 0.03));
  }
  .clickable-row.selected {
    background: var(--c-accent-bg, rgba(59, 130, 246, 0.1));
  }
  .rename-cell {
    display: flex;
    align-items: center;
    gap: 0.375rem;
  }
</style>
