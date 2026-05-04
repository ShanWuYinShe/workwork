<script>
import { onMount } from 'svelte';
import { getConfig, getNetworkStatus, rotateIPv6 } from '../lib/api.js';

let config = { xrayVersion: '-', xrayClients: 0, xrayInbounds: 0, goVersion: '-', configStatus: '-' };
let network = { subnet: '-', primary_ip: '-', rotating_ip: null, domain: '-', cf_configured: false, dns_record: null };
let loading = true;
let rotating = false;
let rotateError = '';
let rotateResult = '';

onMount(async () => {
  try {
    const [cfg, net] = await Promise.all([getConfig(), getNetworkStatus()]);
    config = { ...config, ...cfg };
    network = { ...network, ...net };
  } catch(e) {}
  finally { loading = false; }
});

async function doRotate() {
  if (!confirm('轮换 Reality IPv6 地址？客户端将通过域名自动获取新 IP。')) return;
  rotating = true;
  rotateError = '';
  rotateResult = '';
  try {
    const d = await rotateIPv6();
    rotateResult = `新 IP: ${d.new_ip}`;
    network = { ...network, rotating_ip: d.new_ip };
  } catch(e) {
    rotateError = e.message;
  }
  finally { rotating = false; }
}
</script>

<h1>Config</h1>
{#if loading}<p style="color:#64748b;">Loading...</p>{/if}

<div class="stats">
  <div class="stat"><div class="label">Xray</div><div class="value" style="font-size:0.85rem">{config.xrayVersion}</div></div>
  <div class="stat"><div class="label">Inbounds</div><div class="value">{config.xrayInbounds}</div></div>
  <div class="stat"><div class="label">Clients</div><div class="value">{config.xrayClients}</div></div>
  <div class="stat"><div class="label">Runtime</div><div class="value" style="font-size:0.85rem">{config.goVersion}</div></div>
  <div class="stat"><div class="label">Status</div><div class="value" style="font-size:0.85rem">{config.configStatus}</div></div>
</div>

<div class="card">
  <h2>Reality Network</h2>
  <table>
    <tbody>
      <tr><td style="color:#64748b;width:140px;">Subnet</td><td style="font-family:monospace;">{network.subnet}</td></tr>
      <tr><td style="color:#64748b;">Primary IP</td><td style="font-family:monospace;">{network.primary_ip}</td></tr>
      <tr><td style="color:#64748b;">Rotating IP</td><td style="font-family:monospace;color:#60a5fa;">{network.rotating_ip || '—'}</td></tr>
      <tr><td style="color:#64748b;">Domain</td><td style="font-family:monospace;">{network.domain}</td></tr>
      <tr><td style="color:#64748b;">DNS</td><td>
        {#if network.dns_record}
          <span class="badge badge-green">已配置</span>
          <span style="font-size:0.75rem;color:#94a3b8;margin-left:0.5rem;">→ {network.dns_record.content}</span>
        {:else}
          <span class="badge badge-red">未配置</span>
        {/if}
      </td></tr>
    </tbody>
  </table>
  <div style="margin-top:1rem;">
    <button class="btn btn-blue" on:click={doRotate} disabled={rotating}>
      {rotating ? '轮换中...' : '轮换 IPv6'}
    </button>
  </div>
  {#if rotateError}
    <div style="background:#7f1d1d;color:#fca5a5;padding:0.5rem 0.75rem;border-radius:0.375rem;margin-top:0.75rem;font-size:0.875rem;">{rotateError}</div>
  {/if}
  {#if rotateResult}
    <div style="background:#064e3b;color:#6ee7b7;padding:0.5rem 0.75rem;border-radius:0.375rem;margin-top:0.75rem;font-size:0.875rem;">{rotateResult}</div>
  {/if}
</div>
