<script>
import { onMount } from 'svelte';
import { getUsers, addXrayClient, deleteXrayClient } from '../lib/api.js';

let clients = [];
let generatedJSON = '';
let newUUID = '';
let loading = true;
let error = '';
let copied = false;
let copyError = '';

onMount(async () => {
  try {
    const d = await getUsers();
    clients = d.xrayClients || [];
    generatedJSON = d.generatedJSON || '';
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
  error = '';
  try { await deleteXrayClient(uuid); location.reload(); }
  catch(e) { error = e.message; }
}
function genUUID() { newUUID = crypto.randomUUID(); }

async function copyConfig() {
  copyError = '';
  try {
    await navigator.clipboard.writeText(generatedJSON);
    copied = true;
    setTimeout(() => copied = false, 2000);
  } catch {
    // Fallback for insecure contexts
    const ta = document.createElement('textarea');
    ta.value = generatedJSON;
    ta.style.position = 'fixed'; ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand('copy');
      copied = true;
      setTimeout(() => copied = false, 2000);
    } catch {
      copyError = 'Copy failed — select and copy manually';
      setTimeout(() => copyError = '', 3000);
    }
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

<h1>Clients</h1>
{#if loading}<p style="color:#64748b;">Loading...</p>{/if}
{#if error}<div class="toast toast-error">{error}</div>{/if}

<div class="card">
  <h2>Add Client</h2>
  <div style="display:flex;gap:0.5rem;align-items:end;flex-wrap:wrap;">
    <div style="flex:1;min-width:280px;">
      <label>UUID</label>
      <input type="text" bind:value={newUUID} placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" style="width:100%;font-family:monospace;">
    </div>
    <button class="btn btn-green" on:click={addClient}>Add</button>
    <button class="btn btn-gray btn-sm" on:click={genUUID}>Generate</button>
  </div>
</div>

<div class="card">
  <h2>Clients ({clients.length})</h2>
  {#if clients.length}
  <table>
    <thead><tr><th>UUID</th><th>Protocols</th><th>Actions</th></tr></thead>
    <tbody>
      {#each clients as c}
      <tr>
        <td style="font-family:monospace;font-size:0.8rem" title={c.id}>{c.id.slice(0, 16)}...</td>
        <td><span class="badge badge-green">reality</span> <span class="badge" style="background:#1e3a5f;color:#93c5fd;">xhttp</span></td>
        <td><button class="btn btn-red btn-sm" on:click={() => deleteClient(c.id)}>Delete</button></td>
      </tr>
      {/each}
    </tbody>
  </table>
  {:else}<p style="color:#64748b;">No proxy clients configured.</p>{/if}
</div>

<div class="card">
  <h2 style="display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:0.5rem;">
    <span>Client Configuration</span>
    <div style="display:flex;gap:0.5rem;">
      <button class="btn btn-sm btn-blue" on:click={copyConfig}>
        {copied ? '✓ Copied' : 'Copy'}
      </button>
      <button class="btn btn-sm btn-gray" on:click={downloadConfig}>Download</button>
    </div>
  </h2>
  <p style="color:#94a3b8;font-size:0.75rem;margin-bottom:0.75rem;">
    SOCKS5 :10808 · HTTP :10809 · Balancer: reality ↔ xhttp
  </p>
  <textarea rows="18" readonly>{generatedJSON}</textarea>
  {#if copied}
    <div class="toast toast-success">Copied to clipboard</div>
  {/if}
  {#if copyError}
    <div class="toast toast-error">{copyError}</div>
  {/if}
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
