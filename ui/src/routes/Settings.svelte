<script>
import { onMount } from 'svelte';
import { getNetworkStatus, rotateIPv6, syncDNS } from '../lib/api.js';

let net = $state({ nginx_ip:'-', xray_primary:'-', xray_rotating:null, xray_active:'-', subnet:'-', cf_configured:false, dns_reality:null, dns_nginx:null });
let loading = $state(true);
let rotating = $state(false);
let syncMsg = $state('');
let result = $state({ ok: false, msg: '' });

onMount(refresh);

async function refresh() {
  loading = true;
  try {
    const ns = await getNetworkStatus();
    net = { ...net, ...ns };
  } catch(e) {}
  finally { loading = false; }
}

async function doSyncDNS() {
  syncMsg = 'Syncing...';
  try {
    const d = await syncDNS();
    syncMsg = d.results.join('; ');
    await refresh();
  } catch(e) { syncMsg = 'Sync failed: ' + e.message; }
}

async function doRotate() {
  if (!confirm('Rotate Reality IPv6? Clients using xray.yinghuoren.com will follow.')) return;
  rotating = true; result = { ok: false, msg: '' };
  try {
    const d = await rotateIPv6();
    result = { ok: true, msg: `New IP: ${d.new_ip}` };
    await refresh();
  } catch(e) {
    result = { ok: false, msg: e.message };
  }
  finally { rotating = false; }
}

let realitySynced = $derived(net.dns_reality?.synced);
let nginxSynced = $derived(net.dns_nginx?.synced);
let rotatingIP = $derived(net.xray_rotating);
</script>

<h1>Settings</h1>
{#if loading}<p style="color:#64748b;">Loading...</p>{/if}

<div class="card">
  <h2>External Services <span style="font-size:0.75rem;color:#64748b;font-weight:400;">— public IPv6 on ipvlan</span></h2>
  <table>
    <thead><tr><th>Service</th><th>IPv6 Address</th><th>Role</th><th>DNS</th></tr></thead>
    <tbody>
      <tr>
        <td style="font-weight:500;">nginx</td>
        <td style="font-family:monospace;">{net.nginx_ip}</td>
        <td style="color:#94a3b8;">Web gateway (80/443)</td>
        <td>
          {#if net.dns_nginx}
            <span class="badge" class:badge-green={nginxSynced} class:badge-red={!nginxSynced}>{nginxSynced ? 'synced' : 'mismatch'}</span>
          {:else}
            <span class="badge badge-red">no DNS</span>
          {/if}
        </td>
      </tr>
      <tr>
        <td style="font-weight:500;">xray reality</td>
        <td style="font-family:monospace;color:#60a5fa;">{net.xray_active}</td>
        <td style="color:#94a3b8;">VLESS Reality (443)</td>
        <td>
          {#if net.dns_reality}
            <span class="badge" class:badge-green={realitySynced} class:badge-red={!realitySynced}>{realitySynced ? 'synced' : 'mismatch'}</span>
          {:else}
            <span class="badge badge-red">no DNS</span>
          {/if}
        </td>
      </tr>
    </tbody>
  </table>
</div>

<div class="card">
  <h2>Reality IPv6 Details</h2>
  <table>
    <tbody>
      <tr><td style="color:#64748b;width:140px;">Primary (static)</td><td style="font-family:monospace;">{net.xray_primary}</td><td style="color:#94a3b8;font-size:0.8rem;">Docker-assigned, permanent fallback</td></tr>
      <tr><td style="color:#64748b;">Rotating</td><td style="font-family:monospace;color:#60a5fa;">{rotatingIP || '—'}</td><td style="color:#94a3b8;font-size:0.8rem;">Dynamically rotated, DNS points here</td></tr>
      <tr><td style="color:#64748b;">Active</td><td style="font-family:monospace;color:#60a5fa;">{net.xray_active}</td><td style="color:#94a3b8;font-size:0.8rem;">Effective Reality endpoint</td></tr>
    </tbody>
  </table>
</div>

<div class="card">
  <h2>IPv6 Allocation <span style="font-size:0.75rem;color:#64748b;font-weight:400;">— {net.subnet}</span></h2>
  <table>
    <thead><tr><th>Range</th><th>Prefix</th><th>Usage</th></tr></thead>
    <tbody>
      <tr>
        <td style="font-weight:500;color:#6ee7b7;">Static</td>
        <td style="font-family:monospace;font-size:0.8rem;">:1::/80</td>
        <td style="color:#94a3b8;">nginx, xray primary</td>
      </tr>
      <tr>
        <td style="font-weight:500;color:#60a5fa;">Rotating</td>
        <td style="font-family:monospace;font-size:0.8rem;">:f::/80</td>
        <td style="color:#94a3b8;">xray reality dynamic IPs</td>
      </tr>
    </tbody>
  </table>
</div>

<div class="card">
  <h2>Internal Services <span style="font-size:0.75rem;color:#64748b;font-weight:400;">— internal_docker only, nginx-proxied</span></h2>
  <table>
    <thead><tr><th>Service</th><th>Port</th><th>Access</th></tr></thead>
    <tbody>
      <tr><td style="font-weight:500;">admin</td><td style="font-family:monospace;">8318</td><td style="color:#94a3b8;">via nginx → /admin/ + /api/admin/</td></tr>
      <tr><td style="font-weight:500;">xray xhttp</td><td style="font-family:monospace;">8447</td><td style="color:#94a3b8;">via nginx → /proxy/query</td></tr>
    </tbody>
  </table>
</div>

<div class="card">
  <h2>DNS Sync</h2>
  <table>
    <thead><tr><th>Domain</th><th>DNS Record</th><th>Expected</th><th>Status</th></tr></thead>
    <tbody>
      {#if net.dns_nginx}
      <tr>
        <td style="font-weight:500;">{net.dns_nginx.domain}</td>
        <td style="font-family:monospace;font-size:0.8rem;">{net.dns_nginx.record?.content || '—'}</td>
        <td style="font-family:monospace;font-size:0.8rem;">{net.nginx_ip}</td>
        <td><span class="badge" class:badge-green={nginxSynced} class:badge-red={!nginxSynced}>{nginxSynced ? 'synced' : 'mismatch'}</span></td>
      </tr>
      {/if}
      {#if net.dns_reality}
      <tr>
        <td style="font-weight:500;">{net.dns_reality.domain}</td>
        <td style="font-family:monospace;font-size:0.8rem;">{net.dns_reality.record?.content || '—'}</td>
        <td style="font-family:monospace;font-size:0.8rem;color:#60a5fa;">{net.xray_active}</td>
        <td><span class="badge" class:badge-green={realitySynced} class:badge-red={!realitySynced}>{realitySynced ? 'synced' : 'mismatch'}</span></td>
      </tr>
      {/if}
    </tbody>
  </table>
  <button class="btn btn-sm btn-blue" on:click={doSyncDNS} style="margin-top:0.75rem;">Sync All DNS</button>
  {#if syncMsg}
    <div style="margin-top:0.5rem;font-size:0.8rem;color:#94a3b8;">{syncMsg}</div>
  {/if}
</div>

<div class="card">
  <h2>IPv6 Rotation</h2>
  <p style="color:#94a3b8;font-size:0.8rem;margin-bottom:0.75rem;">
    Generates a new IPv6 in the rotating range (:f::/80), adds it to xray,<br>
    updates xray.yinghuoren.com DNS, then removes the old rotating IP.
  </p>
  <button class="btn btn-blue" on:click={doRotate} disabled={rotating}>
    {rotating ? 'Rotating...' : 'Rotate Reality IPv6'}
  </button>
  {#if result.msg}
    <div style="background:{result.ok ? '#064e3b' : '#7f1d1d'};color:{result.ok ? '#6ee7b7' : '#fca5a5'};padding:0.5rem 0.75rem;border-radius:0.375rem;margin-top:0.75rem;font-size:0.875rem;">{result.msg}</div>
  {/if}
</div>
