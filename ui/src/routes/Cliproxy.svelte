<script>
import { onMount } from 'svelte';
import { getCliproxyInstances, cliproxyStart, cliproxyStop, cliproxyGetConfig, cliproxySetConfig } from '../lib/api.js';

let instances = $state([]);
let loading = $state(true);
let error = $state('');
let message = $state('');
let selectedUser = $state(null);
let configText = $state('');
let apiKeysInput = $state('');
let configLoading = $state(false);

onMount(refresh);

async function refresh() {
  loading = true;
  error = '';
  try {
    const d = await getCliproxyInstances();
    instances = d.instances || [];
  } catch(e) { error = e.message; }
  finally { loading = false; }
}

async function doStart(username) {
  error = ''; message = '';
  try {
    await cliproxyStart(username);
    message = `Started cliproxy for ${username}.`;
    await refresh();
  } catch(e) { error = e.message; }
}

async function doStop(username) {
  if (!confirm(`Stop cliproxy for ${username}?`)) return;
  error = ''; message = '';
  try {
    await cliproxyStop(username);
    message = `Stopped cliproxy for ${username}.`;
    await refresh();
  } catch(e) { error = e.message; }
}

async function openConfig(username) {
  selectedUser = username;
  configLoading = true;
  error = '';
  try {
    const d = await cliproxyGetConfig(username);
    configText = d.config || '';
    apiKeysInput = (d.api_keys || []).join('\n');
  } catch(e) { error = e.message; }
  finally { configLoading = false; }
}

async function saveConfig() {
  if (!selectedUser) return;
  error = ''; message = '';
  const keys = apiKeysInput.split('\n').map(k => k.trim()).filter(k => k);
  try {
    await cliproxySetConfig(selectedUser, keys);
    message = `Config saved for ${selectedUser}.`;
    selectedUser = null;
  } catch(e) { error = e.message; }
}
</script>

<h1>Cliproxy</h1>
{#if loading}<p style="color:#64748b;">Loading...</p>{/if}
{#if error}<div class="toast toast-error">{error}</div>{/if}
{#if message}<div class="toast toast-success">{message}</div>{/if}

{#if selectedUser}
<div class="card">
  <h2>{selectedUser} — API Keys</h2>
  {#if configLoading}
    <p style="color:#64748b;">Loading config...</p>
  {:else}
    <div style="margin-bottom:0.75rem;">
      <label>API Keys (one per line)</label>
      <textarea rows="6" bind:value={apiKeysInput} placeholder="sk-xxxx&#10;sk-yyyy"></textarea>
    </div>
    <div style="display:flex;gap:0.5rem;">
      <button class="btn btn-green" on:click={saveConfig}>Save</button>
      <button class="btn btn-gray" on:click={() => selectedUser = null}>Cancel</button>
    </div>
    {#if configText}
      <details style="margin-top:1rem;">
        <summary style="color:#64748b;font-size:0.75rem;cursor:pointer;">Raw config.yaml</summary>
        <pre style="background:#0f172a;padding:0.75rem;border-radius:0.375rem;font-size:0.7rem;overflow-x:auto;margin-top:0.5rem;">{configText}</pre>
      </details>
    {/if}
  {/if}
</div>
{/if}

<div class="card">
  <h2>Instances ({instances.length})</h2>
  {#if instances.length}
  <table>
    <thead><tr><th>User</th><th>Status</th><th>Actions</th></tr></thead>
    <tbody>
      {#each instances as ci}
      <tr>
        <td style="font-weight:500;">
          {ci.username}
          {#if ci.username === 'public'}<span class="badge" style="background:#1e3a5f;color:#93c5fd;">shared</span>{/if}
        </td>
        <td>
          <span class="badge" class:badge-green={ci.running} class:badge-red={!ci.running}>
            {ci.status}
          </span>
        </td>
        <td style="display:flex;gap:0.25rem;">
          {#if ci.username !== 'public'}
            {#if ci.running}
              <button class="btn btn-red btn-sm" on:click={() => doStop(ci.username)}>Stop</button>
            {:else}
              <button class="btn btn-green btn-sm" on:click={() => doStart(ci.username)}>Start</button>
            {/if}
            <button class="btn btn-blue btn-sm" on:click={() => openConfig(ci.username)}>Keys</button>
          {/if}
        </td>
      </tr>
      {/each}
    </tbody>
  </table>
  {:else}<p style="color:#64748b;">No instances.</p>{/if}
  <button class="btn btn-sm btn-gray" on:click={refresh} style="margin-top:0.75rem;">Refresh</button>
</div>

<style>
  .toast {
    padding: 0.5rem 0.75rem;
    border-radius: 0.375rem;
    margin-bottom: 0.75rem;
    font-size: 0.875rem;
    animation: fadeIn 0.2s ease;
  }
  .toast-success { background: #064e3b; color: #6ee7b7; }
  .toast-error { background: #7f1d1d; color: #fca5a5; }
  @keyframes fadeIn { from { opacity: 0; transform: translateY(-4px); } to { opacity: 1; transform: translateY(0); } }
  textarea { resize: vertical; }
</style>
