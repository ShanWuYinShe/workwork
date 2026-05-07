<script>
import { onMount } from 'svelte';
import { getNetworkStatus, rotateIPv6, syncDNS } from '../lib/api.js';

let net = $state({ nginx_ip: '-', xray_ip: '-', subnet: '-', static_range: '-', rotating_range: '-', cf_configured: false, dns_reality: null, dns_nginx: null });
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
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">Settings</h1>
    <div class="page-actions">
      <button class="btn btn-sm btn-ghost" onclick={refresh}>Refresh</button>
    </div>
  </div>

  {#if loading}
    <div class="card"><div class="loading-skeleton" style="height:150px;"></div></div>
    <div class="card"><div class="loading-skeleton" style="height:100px;"></div></div>
    <div class="card"><div class="loading-skeleton" style="height:80px;"></div></div>
  {:else}
    <div class="card">
      <div class="card-header">
        <div class="card-title">External Services</div>
        <div class="card-subtitle">Public IPv6 on ipvlan</div>
      </div>
      <table>
        <thead><tr><th>Service</th><th>IPv6 Address</th><th>Role</th><th>DNS</th></tr></thead>
        <tbody>
          <tr>
            <td style="font-weight:500;">nginx</td>
            <td class="td-mono">{net.nginx_ip}</td>
            <td class="td-muted">Web gateway (80/443)</td>
            <td>
              {#if net.dns_nginx}
                <span class="badge badge-{nginxSynced ? 'green' : 'red'}">
                  <span class="badge-dot"></span>{nginxSynced ? 'synced' : 'mismatch'}
                </span>
              {:else}
                <span class="badge badge-red">no DNS</span>
              {/if}
            </td>
          </tr>
          <tr>
            <td style="font-weight:500;">xray reality</td>
            <td class="td-mono" style="color:#93c5fd;">{net.xray_ip}</td>
            <td class="td-muted">VLESS Reality (443)</td>
            <td>
              {#if net.dns_reality}
                <span class="badge badge-{realitySynced ? 'green' : 'red'}">
                  <span class="badge-dot"></span>{realitySynced ? 'synced' : 'mismatch'}
                </span>
              {:else}
                <span class="badge badge-red">no DNS</span>
              {/if}
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="card">
      <div class="card-header">
        <div class="card-title">IPv6 Allocation</div>
        <div class="card-subtitle">{net.subnet}</div>
      </div>
      <table>
        <thead><tr><th>Range</th><th>Prefix</th><th>Usage</th></tr></thead>
        <tbody>
          <tr>
            <td><span class="badge badge-green">Static</span></td>
            <td class="td-mono">{net.static_range}</td>
            <td class="td-muted">nginx, xray</td>
          </tr>
          <tr>
            <td><span class="badge badge-blue">Rotating</span></td>
            <td class="td-mono">{net.rotating_range}</td>
            <td class="td-muted">xray reality dynamic IPs</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="card">
      <div class="card-header">
        <div class="card-title">Internal Services</div>
        <div class="card-subtitle">Internal docker, nginx-proxied</div>
      </div>
      <table>
        <thead><tr><th>Service</th><th>Port</th><th>Access</th></tr></thead>
        <tbody>
          <tr><td style="font-weight:500;">admin</td><td class="td-mono">8318</td><td class="td-muted">via nginx /admin/ + /api/admin/</td></tr>
          <tr><td style="font-weight:500;">xray xhttp</td><td class="td-mono">8447</td><td class="td-muted">via nginx /proxy/query</td></tr>
        </tbody>
      </table>
    </div>
  {/if}

  <div class="card">
    <div class="card-header">
      <div class="card-title">DNS Sync</div>
    </div>
    {#if !loading}
      <table>
        <thead><tr><th>Domain</th><th>DNS Record</th><th>Expected</th><th>Status</th></tr></thead>
        <tbody>
          {#if net.dns_nginx}
          <tr>
            <td style="font-weight:500;">{net.dns_nginx.domain}</td>
            <td class="td-mono">{net.dns_nginx.record?.content || '—'}</td>
            <td class="td-mono">{net.nginx_ip}</td>
            <td>
              <span class="badge badge-{nginxSynced ? 'green' : 'red'}">
                <span class="badge-dot"></span>{nginxSynced ? 'synced' : 'mismatch'}
              </span>
            </td>
          </tr>
          {/if}
          {#if net.dns_reality}
          <tr>
            <td style="font-weight:500;">{net.dns_reality.domain}</td>
            <td class="td-mono">{net.dns_reality.record?.content || '—'}</td>
            <td class="td-mono" style="color:#93c5fd;">{net.xray_ip}</td>
            <td>
              <span class="badge badge-{realitySynced ? 'green' : 'red'}">
                <span class="badge-dot"></span>{realitySynced ? 'synced' : 'mismatch'}
              </span>
            </td>
          </tr>
          {/if}
        </tbody>
      </table>
    {/if}
    <div style="display:flex;align-items:center;gap:0.75rem;margin-top:0.75rem;flex-wrap:wrap;">
      <button class="btn btn-sm btn-primary" onclick={doSyncDNS}>Sync All DNS</button>
      {#if syncMsg}<span style="font-size:0.78rem;color:var(--c-text-secondary);">{syncMsg}</span>{/if}
    </div>
  </div>

  <div class="card">
    <div class="card-header">
      <div class="card-title">IPv6 Rotation</div>
    </div>
    <p style="color:var(--c-text-secondary);font-size:0.78rem;margin-bottom:1rem;line-height:1.6;">
      Generates a new IPv6 in the rotating range (:f::/80), adds it to xray,
      updates xray.yinghuoren.com DNS, then removes the old rotating IP.
    </p>
    <div style="display:flex;align-items:center;gap:0.75rem;flex-wrap:wrap;">
      <button class="btn btn-primary" onclick={doRotate} disabled={rotating}>
        {#if rotating}<span class="spinner" style="margin-right:0.35rem;"></span>{/if}
        {rotating ? 'Rotating...' : 'Rotate Reality IPv6'}
      </button>
      {#if result.msg}
        <div class="toast toast-{result.ok ? 'success' : 'error'}" style="margin:0;">{result.msg}</div>
      {/if}
    </div>
  </div>
</div>
