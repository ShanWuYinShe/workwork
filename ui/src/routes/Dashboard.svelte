<script>
import { onMount } from 'svelte';
import { getDashboard } from '../lib/api.js';

let stats = { services: '-', running: '-', dockerVersion: '-', cpu: '-', memory: '-', os: '-', arch: '-' };
let services = [];
let error = '';
let loading = true;

onMount(async () => {
  try {
    const d = await getDashboard();
    stats = d.stats || stats;
    services = d.services || [];
  } catch (e) { error = e.message; }
  finally { loading = false; }
});
</script>

<h1>Dashboard</h1>
{#if loading}<p style="color:#64748b;">Loading...</p>{/if}
{#if error}<div style="background:#7f1d1d;color:#fca5a5;padding:0.75rem 1rem;border-radius:0.5rem;margin-bottom:1rem;">{error}</div>{/if}

<div class="stats">
  <div class="stat"><div class="label">Services</div><div class="value">{stats.services}</div></div>
  <div class="stat"><div class="label">Running</div><div class="value">{stats.running}</div></div>
  <div class="stat"><div class="label">Docker Engine</div><div class="value" style="font-size:0.9rem">{stats.dockerVersion}</div></div>
  <div class="stat"><div class="label">CPU / Memory</div><div class="value" style="font-size:0.9rem">{stats.cpu} / {stats.memory}</div></div>
  <div class="stat"><div class="label">OS / Arch</div><div class="value" style="font-size:0.9rem">{stats.os} / {stats.arch}</div></div>
</div>

<div class="card">
  <h2>Services</h2>
  <table>
    <thead><tr><th>Service</th><th>State</th><th>Image</th></tr></thead>
    <tbody>
      {#each services as s}
      <tr><td>{s.name}</td><td><span class="badge badge-{s.state === 'running' ? 'green' : 'red'}">{s.state}</span></td><td style="color:#94a3b8;font-size:0.8rem">{s.image}</td></tr>
      {/each}
    </tbody>
  </table>
</div>
