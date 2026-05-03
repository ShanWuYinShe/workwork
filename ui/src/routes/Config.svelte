<script>
import { onMount } from 'svelte';
let config = { xrayVersion: '-', xrayClients: 0, xrayInbounds: 0, goVersion: '-', configStatus: '-' };
let loading = true;

onMount(async () => {
  try {
    const r = await fetch('/api/admin/config', { credentials: 'same-origin' });
    if (r.ok) config = { ...config, ...(await r.json()) };
  } catch(e) {}
  finally { loading = false; }
});
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
