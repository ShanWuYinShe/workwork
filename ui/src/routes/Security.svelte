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

<div class="page">
  <div class="page-header">
    <h1 class="page-title">Security</h1>
  </div>

  {#if error}<div class="toast toast-error">{error}</div>{/if}
  {#if message}<div class="toast toast-success">{message}</div>{/if}

  <div class="card">
    <div class="card-header">
      <div class="card-title">Block IP</div>
      <div class="card-subtitle">Works behind Cloudflare proxy (CF-Connecting-IP)</div>
    </div>
    <div class="form-row">
      <div class="form-group">
        <label for="block-ip">IP Address</label>
        <input id="block-ip" type="text" bind:value={newIP} placeholder="1.2.3.4" style="width:160px;">
      </div>
      <div class="form-group form-group-grow">
        <label for="block-reason">Reason</label>
        <input id="block-reason" type="text" bind:value={newReason} placeholder="Brute force">
      </div>
      <div class="form-group">
        <label for="block-duration">Duration</label>
        <select id="block-duration" bind:value={newDuration}>
          <option value="3600">1 hour</option>
          <option value="86400">24 hours</option>
          <option value="604800">7 days</option>
          <option value="0">Forever</option>
        </select>
      </div>
      <button class="btn btn-danger" onclick={doBlock}>Block</button>
    </div>
  </div>

  <div class="card">
    <div class="card-header">
      <div class="card-title">
        Blocked IPs
        {#if entries.length}<span class="card-title-count">{entries.length}</span>{/if}
      </div>
      <div class="card-actions">
        <button class="btn btn-sm btn-ghost" onclick={refresh}>Refresh</button>
      </div>
    </div>
    {#if loading}
      <div class="loading-skeleton" style="height:100px;"></div>
    {:else if entries.length}
      <table>
        <thead><tr><th>IP</th><th>Reason</th><th>Blocked</th><th>Expires</th><th>Actions</th></tr></thead>
        <tbody>
          {#each entries as e}
          <tr>
            <td class="td-mono">{e.ip}</td>
            <td class="td-muted">{e.reason || '—'}</td>
            <td class="td-muted">{e.blocked_at?.slice(0,16) || '—'}</td>
            <td>
              {#if e.expires_at}
                <span class="td-muted">{e.expires_at.slice(0,16)}</span>
              {:else}
                <span class="badge badge-red">Permanent</span>
              {/if}
            </td>
            <td>
              <button class="btn btn-ghost btn-sm" onclick={() => doUnblock(e.ip)}>Unblock</button>
            </td>
          </tr>
          {/each}
        </tbody>
      </table>
    {:else}
      <div class="empty-state">No IPs currently blocked.</div>
    {/if}
  </div>
</div>
