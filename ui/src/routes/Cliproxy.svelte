<script>
  import { onMount } from 'svelte';
  import {
    getCliproxyInstances,
    cliproxyStart,
    cliproxyStop,
    cliproxyGetConfig,
    cliproxySetConfig,
    cliproxyCodexLogin,
    setModelPoolUser,
  } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';

  let instances = $state([]);
  let loading = $state(true);
  let error = $state('');
  let message = $state('');
  let selectedUser = $state(null);
  let configText = $state('');
  let providers = $state([]);
  let configLoading = $state(false);
  let codexOutput = $state('');
  let codexLoading = $state(false);
  let newName = $state(''),
    newURL = $state(''),
    newKey = $state(''),
    newType = $state('openai'),
    newAlias = $state('');
  let newModelName = $state('*'),
    newModelAlias = $state('');
  let tempModels = $state([]); // models being added to current new provider
  let actingUser = $state('');
  let selectedPoolAccess = $state(false);
  let poolToggling = $state(false);

  onMount(refresh);

  async function refresh() {
    loading = true;
    error = '';
    try {
      const d = await getCliproxyInstances();
      instances = d.instances || [];
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  async function doStart(username) {
    actingUser = username;
    error = '';
    message = '';
    try {
      await cliproxyStart(username);
      message = t('cliproxyStarted', { username });
      await refresh();
    } catch (e) {
      error = e.message;
    } finally {
      actingUser = '';
    }
  }

  async function doStop(username) {
    if (!confirm(t('confirmStopCliproxy', { username }))) return;
    actingUser = username;
    error = '';
    message = '';
    try {
      await cliproxyStop(username);
      message = t('cliproxyStopped', { username });
      await refresh();
    } catch (e) {
      error = e.message;
    } finally {
      actingUser = '';
    }
  }

  async function openConfig(username) {
    selectedUser = username;
    configLoading = true;
    error = '';
    const inst = instances.find((i) => i.username === username);
    selectedPoolAccess = inst?.pool_access || false;
    try {
      const d = await cliproxyGetConfig(username);
      configText = d.config || '';
      providers = d.providers || [];
    } catch (e) {
      error = e.message;
    } finally {
      configLoading = false;
    }
  }

  function addProvider() {
    if (!newName || !newURL || !newKey) return;
    let models = tempModels.length > 0 ? tempModels : [{ name: '*', alias: newAlias || newName }];
    providers = [
      ...providers,
      { name: newName, base_url: newURL, api_key: newKey, type: newType, models },
    ];
    newName = '';
    newURL = '';
    newKey = '';
    newType = 'openai';
    newAlias = '';
    newModelName = '*';
    newModelAlias = '';
    tempModels = [];
  }

  function removeProvider(idx) {
    providers = providers.filter((_, i) => i !== idx);
  }

  async function saveConfig() {
    if (!selectedUser) return;
    error = '';
    message = '';
    try {
      await cliproxySetConfig(selectedUser, providers);
      message = t('configSaved', { user: selectedUser });
      selectedUser = null;
    } catch (e) {
      error = e.message;
    }
  }

  async function codexLogin(username) {
    codexLoading = true;
    codexOutput = '';
    error = '';
    try {
      const d = await cliproxyCodexLogin(username);
      codexOutput = d.output || t('codexLoginStarted');
    } catch (e) {
      error = e.message;
    } finally {
      codexLoading = false;
    }
  }

  async function uploadOAuth(e) {
    const file = e.target.files[0];
    if (!file || !selectedUser) return;
    error = '';
    message = '';
    const fd = new FormData();
    fd.append('username', selectedUser);
    fd.append('file', file);
    try {
      const r = await fetch('/api/admin/cliproxy/upload-oauth', {
        method: 'POST',
        body: fd,
        credentials: 'same-origin',
      });
      if (!r.ok) throw new Error(`Upload failed: ${r.status}`);
      const json = await r.json();
      if (!json.success) throw new Error(t('uploadFailed'));
      message = `OAuth file "${json.data.file}" uploaded.`;
    } catch (e) {
      error = e.message;
    }
    e.target.value = '';
  }

  async function togglePoolAccess() {
    if (!selectedUser) return;
    poolToggling = true;
    error = '';
    message = '';
    try {
      const granted = !selectedPoolAccess;
      await setModelPoolUser(selectedUser, granted);
      selectedPoolAccess = granted;
      // Update the instance in the list
      const idx = instances.findIndex((i) => i.username === selectedUser);
      if (idx >= 0) instances[idx].pool_access = granted;
      message = granted
        ? t('poolAccessGranted', { user: selectedUser })
        : t('poolAccessRevoked', { user: selectedUser });
    } catch (e) {
      error = e.message;
    } finally {
      poolToggling = false;
    }
  }

  function addModel() {
    if (!newModelAlias) return;
    tempModels = [...tempModels, { name: newModelName || '*', alias: newModelAlias }];
    newModelName = '*';
    newModelAlias = '';
  }
  function removeModel(idx) {
    tempModels = tempModels.filter((_, i) => i !== idx);
  }
  async function copyUrl(url) {
    try {
      await navigator.clipboard.writeText(url);
    } catch {}
  }

  let runningCount = $derived(instances.filter((i) => i.running).length);
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('cliproxy')}</h1>
    <div class="page-actions">
      <button class="btn btn-sm btn-ghost" onclick={refresh}>{t('refresh')}</button>
    </div>
  </div>

  {#if error}<div class="toast toast-error">{error}</div>{/if}
  {#if message}<div class="toast toast-success">{message}</div>{/if}

  {#if selectedUser}
    <div class="card">
      <div class="card-header">
        <div class="card-title">{selectedUser} — {t('config')}</div>
        <div class="card-actions" style="flex-wrap:wrap;">
          <button
            class="btn btn-sm"
            style={selectedPoolAccess ? 'background:var(--c-purple);color:#fff;' : ''}
            class:btn-ghost={!selectedPoolAccess}
            onclick={togglePoolAccess}
            disabled={poolToggling}
          >
            {poolToggling ? '...' : selectedPoolAccess ? t('poolOn') : t('poolOff')}
          </button>
          <button class="btn btn-success btn-sm" onclick={saveConfig}>{t('save')}</button>
          <button
            class="btn btn-ghost btn-sm"
            onclick={() => {
              selectedUser = null;
              codexOutput = '';
            }}>{t('cancel')}</button
          >
        </div>
      </div>

      {#if configLoading}
        <div class="loading-skeleton" style="height:120px;"></div>
      {:else}
        <table style="margin-bottom:1rem;">
          <thead
            ><tr
              ><th>{t('provider')}</th><th>{t('format')}</th><th>Models</th><th>{t('upstreamURL')}</th><th
                >{t('upstreamKey')}</th
              ><th></th></tr
            ></thead
          >
          <tbody>
            {#each providers as p, i}
              <tr>
                <td style="font-weight:500;">{p.name}</td>
                <td>
                  <span class="badge badge-{p.type === 'anthropic' ? 'purple' : 'blue'}"
                    >{p.type === 'anthropic' ? t('anthropic') : t('openAICompatOpt')}</span
                  >
                </td>
                <td class="td-mono">{p.alias || p.name}/*</td>
                <td class="td-mono" style="color:#93c5fd;">{p.base_url}</td>
                <td class="td-mono" style="color:var(--c-text-muted);">{p.api_key ? p.api_key.slice(0,4) + '****' + p.api_key.slice(-4) : ''}</td>
                <td
                  ><button class="btn btn-danger btn-sm" onclick={() => removeProvider(i)}
                    >{t('del')}</button
                  ></td
                >
              </tr>
            {/each}
            {#if providers.length === 0}
              <tr><td colspan="5" class="empty-state" style="padding:1rem;">{t('noProviders')}</td></tr>
            {/if}
          </tbody>
        </table>

        <div
          style="padding:0.75rem 1rem;background:var(--c-bg-surface);border-radius:var(--radius);border:1px solid var(--c-border);margin-bottom:1rem;"
        >
          <div
            style="font-size:0.7rem;color:var(--c-text-muted);text-transform:uppercase;letter-spacing:0.05em;font-weight:600;margin-bottom:0.5rem;"
          >
            {t('addProvider')}
          </div>
          <div class="form-row">
            <div class="form-group">
              <label for="cp-provider">{t('provider')}</label>
              <input
                id="cp-provider"
                type="text"
                bind:value={newName}
                placeholder={t('providerName')}
                style="width:100px;font-size:0.78rem;"
              />
            </div>
            <div class="form-group">
              <label for="cp-type">{t('format')}</label>
              <select id="cp-type" bind:value={newType} style="width:110px;">
                <option value="openai">{t('openAICompatOpt')}</option>
                <option value="anthropic">{t('anthropic')}</option>
              </select>
            </div>
            <div class="form-group">
              <label for="cp-alias">{t('alias')}</label>
              <input
                id="cp-alias"
                type="text"
                bind:value={newAlias}
                placeholder={t('alias')}
                style="width:80px;font-size:0.78rem;"
              />
            </div>
            <div class="form-group form-group-grow">
              <label for="cp-url">{t('upstreamURL')}</label>
              <input
                id="cp-url"
                type="text"
                bind:value={newURL}
                placeholder={t('upstreamURL')}
                style="font-size:0.78rem;"
              />
            </div>
            <div class="form-group form-group-grow">
              <label for="cp-key">{t('upstreamKey')}</label>
              <input
                id="cp-key"
                type="text"
                bind:value={newKey}
                placeholder={t('apiKey')}
                style="font-size:0.78rem;"
              />
            </div>
            <button class="btn btn-success btn-sm" onclick={addProvider}>{t('add')}</button>
          </div>
        </div>

        <div class="tool-row">
          <div class="tool-info">
            <div class="tool-title">{t('codexOAuth')}</div>
            <div class="tool-desc">{t('codexOAuthDesc')}</div>
          </div>
          <button
            class="btn btn-sm"
            style="background:#7c3aed;color:#fff;"
            onclick={() => codexLogin(selectedUser)}
            disabled={codexLoading}
          >
            {codexLoading ? t('starting') : t('loginWithCodex')}
          </button>
        </div>
        {#if codexOutput}
          <pre class="code-block">{codexOutput}</pre>
        {/if}

        <div class="tool-row">
          <div class="tool-info">
            <div class="tool-title">{t('oauthFiles')}</div>
            <div class="tool-desc">{t('oauthFilesDesc')}</div>
          </div>
          <input
            type="file"
            accept=".json"
            style="display:none;"
            id="oauthFileInput"
            onchange={uploadOAuth}
          />
          <button
            class="btn btn-sm btn-ghost"
            onclick={() => document.getElementById('oauthFileInput').click()}>{t('uploadJson')}</button
          >
        </div>

        {#if configText}
          <details style="margin-top:1rem;">
            <summary style="color:var(--c-text-muted);font-size:0.75rem;cursor:pointer;"
              >Raw config.yaml</summary
            >
            <pre class="code-block">{configText}</pre>
          </details>
        {/if}
      {/if}
    </div>
  {/if}

  {#if !loading}
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
        <div class="label">{t('instances')}</div>
        <div class="value">{instances.length}</div>
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
        <div class="value">{runningCount}</div>
      </div>
    </div>

    <div class="card">
      <div class="card-header">
        <div class="card-title">
          {t('userInstances')}
          {#if instances.length}<span class="card-title-count">{instances.length}</span>{/if}
        </div>
      </div>
      {#if loading}
        <div class="loading-skeleton" style="height:120px;"></div>
      {:else if instances.length}
        <table>
          <thead
            ><tr><th>{t('tableUser')}</th><th>Pool</th><th>{t('tableBaseURL')}</th><th>{t('status')}</th><th>{t('serviceActions')}</th></tr
            ></thead
          >
          <tbody>
            {#each instances as ci}
              <tr>
                <td style="font-weight:500;">{ci.username}</td>
                <td>
                  {#if ci.pool_access}
                    <span class="badge badge-purple"><span class="badge-dot"></span>{t('pool')}</span>
                  {:else}
                    <span class="badge badge-gray">—</span>
                  {/if}
                </td>
                <td>
                  <div style="display:flex;align-items:center;gap:0.375rem;">
                    <span class="td-mono" style="color:#93c5fd;">{ci.base_url || '—'}</span>
                    {#if ci.base_url}
                      <button
                        class="btn btn-sm btn-ghost"
                        style="padding:0.15rem 0.35rem;font-size:0.65rem;"
                        onclick={() => copyUrl(ci.base_url)}
                        title={t('copyUrlTitle')}>Copy</button
                      >
                    {/if}
                  </div>
                </td>
                <td>
                  <span class="badge badge-{ci.running ? 'green' : 'red'}">
                    <span class="badge-dot"></span>{ci.status}
                  </span>
                </td>
                <td>
                  <div class="td-actions">
                    {#if actingUser === ci.username}
                      <span class="spinner"></span>
                    {:else}
                      {#if ci.running}
                        <button class="btn btn-danger btn-sm" onclick={() => doStop(ci.username)}
                          >{t('stop')}</button
                        >
                      {:else}
                        <button class="btn btn-success btn-sm" onclick={() => doStart(ci.username)}
                          >{t('start')}</button
                        >
                      {/if}
                      <button class="btn btn-primary btn-sm" onclick={() => openConfig(ci.username)}
                        >{t('config')}</button
                      >
                    {/if}
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}
        <div class="empty-state">{t('noInstances')}</div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .tool-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    flex-wrap: wrap;
    padding: 0.75rem 1rem;
    background: var(--c-bg-surface);
    border-radius: var(--radius);
    border: 1px solid var(--c-border);
    margin-bottom: 0.5rem;
  }
  .tool-info {
    min-width: 0;
  }
  .tool-title {
    font-size: 0.8rem;
    font-weight: 500;
    color: var(--c-text-heading);
  }
  .tool-desc {
    font-size: 0.7rem;
    color: var(--c-text-muted);
    margin-top: 0.1rem;
  }

  .code-block {
    background: var(--c-bg-surface);
    padding: 0.65rem 0.85rem;
    border-radius: var(--radius);
    font-size: 0.7rem;
    overflow-x: auto;
    margin-top: 0.5rem;
    color: var(--c-text);
    border: 1px solid var(--c-border);
  }
</style>
