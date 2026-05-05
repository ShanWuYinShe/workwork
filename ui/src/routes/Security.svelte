<script>
import { onMount } from 'svelte';
import { getSecurityJails, banIP, unbanIP } from '../lib/api.js';

let jails = $state([]);
let loading = $state(true);
let error = $state('');
let message = $state('');
let banIP_ = $state('');

onMount(async () => {
  await refresh();
});

async function refresh() {
  loading = true;
  error = '';
  try {
    const d = await getSecurityJails();
    jails = d.jails || [];
  } catch(e) { error = e.message; }
  finally { loading = false; }
}

async function doBan() {
  if (!banIP_) return;
  error = ''; message = '';
  try {
    await banIP(banIP_);
    message = `IP ${banIP_} banned.`;
    banIP_ = '';
    await refresh();
  } catch(e) { error = e.message; }
}

async function doUnban(ip) {
  if (!confirm(`Unban ${ip}?`)) return;
  error = ''; message = '';
  try {
    await unbanIP(ip);
    message = `IP ${ip} unbanned.`;
    await refresh();
  } catch(e) { error = e.message; }
}

let allBannedIPs = $derived(jails.flatMap(j => j.banned_ips || []));
</script>

<h1>Security</h1>
{#if loading}<p style="color:#64748b;">Loading...</p>{/if}
{#if error}<div class="toast toast-error">{error}</div>{/if}
{#if message}<div class="toast toast-success">{message}</div>{/if}

<div class="card">
  <h2>Manual Ban</h2>
  <div style="display:flex;gap:0.5rem;align-items:end;flex-wrap:wrap;">
    <div style="flex:1;min-width:200px;">
      <label>IP Address</label>
      <input type="text" bind:value={banIP_} placeholder="1.2.3.4" style="width:100%;">
    </div>
    <button class="btn btn-red" on:click={doBan}>Ban IP</button>
  </div>
  <p style="color:#64748b;font-size:0.75rem;margin-top:0.5rem;">Bans last 24 hours via iptables (all ports).</p>
</div>

<div class="card">
  <h2>Banned IPs ({allBannedIPs.length})</h2>
  {#if allBannedIPs.length}
  <table>
    <thead><tr><th>IP</th><th>Jail</th><th>Actions</th></tr></thead>
    <tbody>
      {#each jails as jail}
        {#each jail.banned_ips || [] as ip}
        <tr>
          <td style="font-family:monospace;font-size:0.8rem;">{ip}</td>
          <td><span class="badge" style="background:#1e3a5f;color:#93c5fd;">{jail.name}</span></td>
          <td><button class="btn btn-red btn-sm" on:click={() => doUnban(ip)}>Unban</button></td>
        </tr>
        {/each}
      {/each}
    </tbody>
  </table>
  {:else}<p style="color:#64748b;">No IPs currently banned.</p>{/if}
</div>

<div class="card">
  <h2>Jail Status</h2>
  {#if jails.length}
  <table>
    <thead><tr><th>Jail</th><th>Banned</th><th>Failed</th><th>Config</th></tr></thead>
    <tbody>
      {#each jails as jail}
      <tr>
        <td style="font-weight:500;">
          {jail.name}
          <span class="badge" class:badge-green={jail.enabled} class:badge-red={!jail.enabled}>{jail.enabled ? 'active' : 'inactive'}</span>
        </td>
        <td>{jail.currently_banned} / {jail.total_banned}</td>
        <td>{jail.currently_failed} / {jail.total_failed}</td>
        <td style="font-size:0.75rem;color:#94a3b8;">
          bantime {jail.bantime}s · findtime {jail.findtime}s · maxretry {jail.maxretry}
        </td>
      </tr>
      {/each}
    </tbody>
  </table>
  {:else}<p style="color:#64748b;">No jails found.</p>{/if}
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
</style>
