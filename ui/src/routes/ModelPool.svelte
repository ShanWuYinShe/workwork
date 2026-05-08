<script>
  import { onMount } from 'svelte';
  import { getModelPool, setModelPool, setModelPoolUser } from '../lib/api.js';

  let pool = $state({ providers: [], authorized_users: [] });
  let loading = $state(true);
  let error = $state('');
  let message = $state('');
  let saving = $state(false);

  let newName = $state(''),
    newURL = $state(''),
    newKey = $state(''),
    newType = $state('openai'),
    newAlias = $state(''),
    newModelName = $state('*'),
    newModelAlias = $state(''),
    tempModels = $state([]);

  onMount(refresh);

  async function refresh() {
    loading = true;
    error = '';
    try {
      const d = await getModelPool();
      pool = { providers: d.providers || [], authorized_users: d.authorized_users || [] };
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  function addProvider() {
    if (!newName || !newURL || !newKey) return;
    let models = tempModels.length > 0 ? tempModels : [{ name: '*', alias: newAlias || newName }];
    pool.providers = [
      ...pool.providers,
      { name: newName, base_url: newURL, api_key: newKey, type: newType, models },
    ];
    newName = '';
    newURL = '';
    newKey = '';
    newType = 'openai';
    newAlias = '';
  }

  function removeProvider(idx) {
    pool.providers = pool.providers.filter((_, i) => i !== idx);
  }

  async function savePool() {
    saving = true;
    error = '';
    message = '';
    try {
      const d = await setModelPool(pool.providers);
      message = `Pool saved${d.regenerated ? ' + configs regenerated for: ' + d.regenerated.join(', ') : ''}.`;
    } catch (e) {
      error = e.message;
    } finally {
      saving = false;
    }
  }

  async function toggleUser(username, granted) {
    error = '';
    message = '';
    try {
      const d = await setModelPoolUser(username, granted);
      pool.authorized_users = d.authorized_users || [];
      message = granted
        ? `${username} granted pool access${d.regenerated ? ' — config regenerated' : ''}.`
        : `${username} removed from pool.`;
    } catch (e) {
      error = e.message;
    }
  }

  function maskKey(key) {
    if (!key) return '';
    if (key.length <= 8) return '****';
    return key.slice(0, 4) + '****' + key.slice(-4);
  }

  let isAuthorized = (username) => pool.authorized_users.includes(username);
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">Model Pool</h1>
    <div class="page-actions">
      <button class="btn btn-sm btn-ghost" onclick={refresh}>Refresh</button>
    </div>
  </div>

  {#if error}<div class="toast toast-error">{error}</div>{/if}
  {#if message}<div class="toast toast-success">{message}</div>{/if}

  {#if loading}
    <div class="card"><div class="loading-skeleton" style="height:120px;"></div></div>
    <div class="card"><div class="loading-skeleton" style="height:100px;"></div></div>
  {:else}
    <div class="card">
      <div class="card-header">
        <div class="card-title">
          Pool Providers
          {#if pool.providers.length}<span class="card-title-count">{pool.providers.length}</span
            >{/if}
        </div>
        <div class="card-actions">
          <button class="btn btn-success btn-sm" onclick={savePool} disabled={saving}>
            {saving ? 'Saving...' : 'Save Pool'}
          </button>
        </div>
      </div>
      <p style="color:var(--c-text-secondary);font-size:0.75rem;margin-bottom:1rem;">
        Configure AI API providers shared in the public model pool. They'll be injected into
        authorized users' cliproxy configs.
      </p>

      {#if pool.providers.length}
        <table style="margin-bottom:1rem;">
          <thead
            ><tr
              ><th>Provider</th><th>Format</th><th>Alias</th><th>Upstream URL</th><th
                >Upstream Key</th
              ><th></th></tr
            ></thead
          >
          <tbody>
            {#each pool.providers as p, i}
              <tr>
                <td style="font-weight:500;">{p.name}</td>
                <td>
                  <span class="badge badge-{p.type === 'anthropic' ? 'purple' : 'blue'}"
                    >{p.type === 'anthropic' ? 'Anthropic' : 'OpenAI'}</span
                  >
                </td>
                <td class="td-mono">{p.alias || p.name}/*</td>
                <td class="td-mono" style="color:#93c5fd;">{p.base_url}</td>
                <td class="td-mono" style="color:var(--c-text-muted);">{maskKey(p.api_key)}</td>
                <td
                  ><button class="btn btn-danger btn-sm" onclick={() => removeProvider(i)}
                    >Del</button
                  ></td
                >
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}
        <div class="empty-state">No pool providers configured.</div>
      {/if}

      <div
        style="padding:0.75rem 1rem;background:var(--c-bg-surface);border-radius:var(--radius);border:1px solid var(--c-border);"
      >
        <div
          style="font-size:0.7rem;color:var(--c-text-muted);text-transform:uppercase;letter-spacing:0.05em;font-weight:600;margin-bottom:0.5rem;"
        >
          Add Provider
        </div>
        <div class="form-row">
          <div class="form-group">
            <label for="mp-name">Provider</label>
            <input
              id="mp-name"
              type="text"
              bind:value={newName}
              placeholder="e.g. openai"
              style="width:100px;font-size:0.78rem;"
            />
          </div>
          <div class="form-group">
            <label for="mp-type">Format</label>
            <select id="mp-type" bind:value={newType} style="width:110px;">
              <option value="openai">OpenAI Compat</option>
              <option value="anthropic">Anthropic</option>
            </select>
          </div>
          <div class="form-group">
            <label for="mp-alias">Alias</label>
            <input
              id="mp-alias"
              type="text"
              bind:value={newAlias}
              placeholder="e.g. gpt"
              style="width:80px;font-size:0.78rem;"
            />
          </div>
          <div class="form-group form-group-grow">
            <label for="mp-url">Upstream URL</label>
            <input
              id="mp-url"
              type="text"
              bind:value={newURL}
              placeholder="https://api.openai.com/v1"
              style="font-size:0.78rem;"
            />
          </div>
          <div class="form-group form-group-grow">
            <label for="mp-key">Upstream Key</label>
            <input
              id="mp-key"
              type="text"
              bind:value={newKey}
              placeholder="sk-xxx"
              style="font-size:0.78rem;"
            />
          </div>
          <button class="btn btn-success btn-sm" onclick={addProvider}>Add</button>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-header">
        <div class="card-title">
          User Access
          {#if pool.authorized_users.length}<span class="card-title-count"
              >{pool.authorized_users.length}</span
            >{/if}
        </div>
      </div>
      <div class="user-grid">
        {#each pool.authorized_users as u}
          <div class="user-chip active">
            <span class="user-chip-name">{u}</span>
            <button class="chip-remove" onclick={() => toggleUser(u, false)} title="Revoke access"
              >×</button
            >
          </div>
        {/each}
        {#if pool.authorized_users.length === 0}
          <div class="empty-state">No users authorized. Use the Cliproxy page to grant access.</div>
        {/if}
      </div>
    </div>
  {/if}
</div>

<style>
  .user-grid {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }
  .user-chip {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.35rem 0.65rem;
    border-radius: var(--radius);
    font-size: 0.78rem;
    font-weight: 500;
    border: 1px solid var(--c-border);
  }
  .user-chip.active {
    background: var(--c-accent-bg);
    color: #93c5fd;
    border-color: var(--c-accent-border);
  }
  .user-chip-name {
    max-width: 140px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .chip-remove {
    width: 18px;
    height: 18px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: none;
    border: none;
    color: inherit;
    cursor: pointer;
    border-radius: 4px;
    font-size: 0.9rem;
    line-height: 1;
    opacity: 0.6;
  }
  .chip-remove:hover {
    opacity: 1;
    background: rgba(255, 255, 255, 0.1);
  }
</style>
