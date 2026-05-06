<script>
import { onMount } from 'svelte';
import { getUsers, getCurrentUser, addXrayClient, deleteXrayClient } from '../lib/api.js';

let clients = $state([]);
let generatedJSON = $state('');
let newUUID = $state('');
let loading = $state(true);
let error = $state('');
let copied = $state(false);
let isAdmin = $state(false);
let deletingId = $state('');

onMount(async () => {
  try {
    const [d, user] = await Promise.all([getUsers(), getCurrentUser()]);
    clients = d.xrayClients || [];
    generatedJSON = d.generatedJSON || '';
    isAdmin = user.role === 'admin';
  } catch(e) { error = e.message; }
  finally { loading = false; }
});

async function addClient() {
  if (!newUUID) return;
  error = '';
  try { await addXrayClient(newUUID); newUUID = ''; location.reload(); }
  catch(e) { error = e.message; }
}

async function deleteClient(uuid) {
  if (!confirm('Delete client ' + uuid.slice(0,8) + '...?')) return;
  deletingId = uuid;
  error = '';
  try { await deleteXrayClient(uuid); location.reload(); }
  catch(e) { error = e.message; }
  finally { deletingId = ''; }
}

function genUUID() { newUUID = crypto.randomUUID(); }

async function copyConfig() {
  try {
    await navigator.clipboard.writeText(generatedJSON);
    copied = true;
    setTimeout(() => copied = false, 2000);
  } catch {
    const ta = document.createElement('textarea');
    ta.value = generatedJSON;
    ta.style.position = 'fixed'; ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand('copy');
      copied = true;
      setTimeout(() => copied = false, 2000);
    } catch { error = 'Copy failed — select and copy manually'; }
    document.body.removeChild(ta);
  }
}

function downloadConfig() {
  const blob = new Blob([generatedJSON], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url; a.download = 'xray-config.json';
  a.click();
  URL.revokeObjectURL(url);
}
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">Clients</h1>
  </div>

  {#if error}<div class="toast toast-error">{error}</div>{/if}

  {#if isAdmin}
  <div class="card">
    <div class="card-header">
      <div class="card-title">Add Client</div>
    </div>
    <div class="form-row">
      <div class="form-group form-group-grow">
        <label for="client-uuid">UUID</label>
        <input id="client-uuid" type="text" bind:value={newUUID} placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" style="font-family:'SF Mono','Fira Code',ui-monospace,monospace;">
      </div>
      <button class="btn btn-success" onclick={addClient}>Add</button>
      <button class="btn btn-ghost btn-sm" onclick={genUUID}>Generate</button>
    </div>
  </div>
  {/if}

  {#if loading}
    <div class="card"><div class="loading-skeleton" style="height:120px;"></div></div>
  {:else}
    <div class="card">
      <div class="card-header">
        <div class="card-title">
          Clients
          {#if clients.length}<span class="card-title-count">{clients.length}</span>{/if}
        </div>
      </div>
      {#if clients.length}
      <table>
        <thead><tr><th>UUID</th>{#if isAdmin}<th>Owner</th>{/if}<th>Protocols</th><th>Actions</th></tr></thead>
        <tbody>
          {#each clients as c}
          <tr>
            <td class="td-mono" title={c.id}>{c.id.slice(0, 16)}...</td>
            {#if isAdmin}
            <td class="td-muted">{c.owner || '—'}</td>
            {/if}
            <td>
              <span class="badge badge-green"><span class="badge-dot"></span>reality</span>
              <span class="badge badge-blue">xhttp</span>
            </td>
            <td>
              {#if isAdmin}
                {#if deletingId === c.id}
                  <span class="spinner"></span>
                {:else}
                  <button class="btn btn-danger btn-sm" onclick={() => deleteClient(c.id)}>Delete</button>
                {/if}
              {/if}
            </td>
          </tr>
          {/each}
        </tbody>
      </table>
      {:else}
        <div class="empty-state">No proxy clients configured.</div>
      {/if}
    </div>
  {/if}

  <div class="card">
    <div class="card-header">
      <div class="card-title">Client Configuration</div>
      <div class="card-actions">
        <button class="btn btn-sm btn-primary" onclick={copyConfig} aria-label="Copy configuration to clipboard">
          {#if copied}Copied{:else}Copy{/if}
        </button>
        <button class="btn btn-sm btn-ghost" onclick={downloadConfig} aria-label="Download configuration as JSON">Download</button>
      </div>
    </div>
    <div style="display:flex;gap:0.75rem;margin-bottom:0.75rem;flex-wrap:wrap;">
      <span class="badge badge-gray">SOCKS5 :10808</span>
      <span class="badge badge-gray">HTTP :10809</span>
      <span class="badge badge-gray">Balancer: reality + xhttp</span>
    </div>
    <textarea rows="16" readonly>{generatedJSON}</textarea>
    {#if copied}
      <div class="toast toast-success" style="margin-top:0.75rem;">Copied to clipboard</div>
    {/if}
  </div>
</div>
