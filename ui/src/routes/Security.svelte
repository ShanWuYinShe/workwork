<script>
import { onMount } from 'svelte';
import { getBlockedIPs, blockIP, unblockIP } from '../lib/api.js';

let entries = $state([]);
let loading = $state(true);
let error = $state('');
let message = $state('');
let newIP = $state(''), newReason = $state(''), newDuration = $state('86400');

onMount(refresh);

async function refresh() {
  loading = true;
  error = '';
  try {
    const d = await getBlockedIPs();
    entries = d.entries || [];
  } catch(e) { error = e.message; }
  finally { loading = false; }
}

async function doBlock() {
  if (!newIP) return;
  error = ''; message = '';
  try {
    await blockIP(newIP, newReason, parseInt(newDuration));
    message = `IP ${newIP} blocked.`;
    newIP = ''; newReason = '';
    await refresh();
  } catch(e) { error = e.message; }
}

async function doUnblock(ip) {
  if (!confirm(`Unblock ${ip}?`)) return;
  error = ''; message = '';
  try {
    await unblockIP(ip);
    message = `IP ${ip} unblocked.`;
    await refresh();
  } catch(e) { error = e.message; }
}
</script>

<h1>Security</h1>
{#if loading}<p style="color:#64748b;">Loading...</p>{/if}
{#if error}<div class="toast toast-error">{error}</div>{/if}
{#if message}<div class="toast toast-success">{message}</div>{/if}

<div class="card">
  <h2>Block IP</h2>
  <div style="display:flex;gap:0.5rem;align-items:end;flex-wrap:wrap;">
    <div><label>IP Address</label><input type="text" bind:value={newIP} placeholder="1.2.3.4" style="width:160px;"></div>
    <div><label>Reason</label><input type="text" bind:value={newReason} placeholder="Brute force" style="width:160px;"></div>
    <div><label>Duration</label><select bind:value={newDuration} style="background:#0f172a;border:1px solid #334155;color:#e2e8f0;padding:0.5rem 0.75rem;border-radius:0.375rem;font-size:0.875rem;">
      <option value="3600">1 hour</option>
      <option value="86400">24 hours</option>
      <option value="604800">7 days</option>
      <option value="0">Forever</option>
    </select></div>
    <button class="btn btn-red" on:click={doBlock}>Block</button>
  </div>
  <p style="color:#64748b;font-size:0.75rem;margin-top:0.5rem;">Blocks at HTTP level, works behind Cloudflare proxy. Real IP detected via CF-Connecting-IP header.</p>
</div>

<div class="card">
  <h2>Blocked IPs ({entries.length})</h2>
  {#if entries.length}
  <table>
    <thead><tr><th>IP</th><th>Reason</th><th>Blocked</th><th>Expires</th><th>Actions</th></tr></thead>
    <tbody>
      {#each entries as e}
      <tr>
        <td style="font-family:monospace;font-size:0.8rem;">{e.ip}</td>
        <td style="color:#94a3b8;font-size:0.8rem;">{e.reason || '—'}</td>
        <td style="font-size:0.8rem;color:#64748b;">{e.blocked_at?.slice(0,16) || '—'}</td>
        <td style="font-size:0.8rem;color:#64748b;">{e.expires_at ? e.expires_at.slice(0,16) : '∞'}</td>
        <td><button class="btn btn-red btn-sm" on:click={() => doUnblock(e.ip)}>Unblock</button></td>
      </tr>
      {/each}
    </tbody>
  </table>
  {:else}<p style="color:#64748b;">No IPs currently blocked.</p>{/if}
  <button class="btn btn-sm btn-gray" on:click={refresh} style="margin-top:0.75rem;">Refresh</button>
</div>

<style>
  .toast { padding: 0.5rem 0.75rem; border-radius: 0.375rem; margin-bottom: 0.75rem; font-size: 0.875rem; animation: fadeIn 0.2s ease; }
  .toast-success { background: #064e3b; color: #6ee7b7; }
  .toast-error { background: #7f1d1d; color: #fca5a5; }
  @keyframes fadeIn { from { opacity: 0; transform: translateY(-4px); } to { opacity: 1; transform: translateY(0); } }
</style>
