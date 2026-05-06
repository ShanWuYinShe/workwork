<script>
import { onMount } from 'svelte';
import { getDashboard } from '../lib/api.js';

let stats = $state({ services: '-', running: '-', dockerVersion: '-', cpu: '-', memory: '-', os: '-', arch: '-' });
let services = $state([]);
let error = $state('');
let loading = $state(true);

onMount(async () => {
  try {
    const d = await getDashboard();
    stats = d.stats || stats;
    services = d.services || [];
  } catch (e) { error = e.message; }
  finally { loading = false; }
});
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">Dashboard</h1>
  </div>

  {#if error}<div class="toast toast-error">{error}</div>{/if}

  {#if loading}
    <div class="stats">
      {#each Array(5) as _}
        <div class="stat"><div class="loading-skeleton" style="height:80px;"></div></div>
      {/each}
    </div>
  {:else}
    <div class="stats">
      <div class="stat">
        <div class="stat-icon blue">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M20.25 7.5l-.625 10.632a2.25 2.25 0 01-2.247 2.118H6.622a2.25 2.25 0 01-2.247-2.118L3.75 7.5M10 11.25h4M3.375 7.5h17.25c.621 0 1.125-.504 1.125-1.125v-1.5c0-.621-.504-1.125-1.125-1.125H3.375c-.621 0-1.125.504-1.125 1.125v1.5c0 .621.504 1.125 1.125 1.125Z"/></svg>
        </div>
        <div class="label">Services</div>
        <div class="value">{stats.services}</div>
      </div>
      <div class="stat">
        <div class="stat-icon green">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path stroke-linecap="round" stroke-linejoin="round" d="M9 12.75L11.25 15 15 9.75M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/></svg>
        </div>
        <div class="label">Running</div>
        <div class="value">{stats.running}</div>
      </div>
      <div class="stat">
        <div class="stat-icon purple">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path stroke-linecap="round" stroke-linejoin="round" d="M5.25 14.25h13.5m-13.5 0a3 3 0 01-3-3m3 3a3 3 0 100 6h13.5a3 3 0 100-6m-16.5-3a3 3 0 013-3h13.5a3 3 0 013 3m-19.5 0a4.5 4.5 0 01.9-2.7L5.737 5.1a3.375 3.375 0 012.7-1.35h7.126c1.062 0 2.062.5 2.7 1.35l2.587 3.45a4.5 4.5 0 01.9 2.7m0 0a3 3 0 01-3 3m0 3h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008zm-3 6h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008z"/></svg>
        </div>
        <div class="label">Docker Engine</div>
        <div class="value" style="font-size:0.9rem">{stats.dockerVersion}</div>
      </div>
      <div class="stat">
        <div class="stat-icon orange">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path stroke-linecap="round" stroke-linejoin="round" d="M15.75 3.75L18 6m0 0l2.25 2.25M18 6l2.25-2.25M18 6l-2.25 2.25m1.5 13.5c-8.284 0-15-6.716-15-15V4.5A2.25 2.25 0 014.5 2.25h1.372c.516 0 .966.351 1.091.852l1.106 4.423c.11.44-.054.902-.417 1.173l-1.293.97a1.062 1.062 0 00-.38 1.21 12.035 12.035 0 007.143 7.143c.441.162.928-.004 1.21-.38l.97-1.293a1.125 1.125 0 011.173-.417l4.423 1.106c.5.125.852.575.852 1.091V19.5a2.25 2.25 0 01-2.25 2.25h-2.25z"/></svg>
        </div>
        <div class="label">CPU / Memory</div>
        <div class="value" style="font-size:0.85rem">{stats.cpu} / {stats.memory}</div>
      </div>
      <div class="stat">
        <div class="stat-icon blue">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path stroke-linecap="round" stroke-linejoin="round" d="M6.75 7.5l3 2.25-3 2.25m4.5 0h3m-9 8.25h13.5A2.25 2.25 0 0021 18V6a2.25 2.25 0 00-2.25-2.25H5.25A2.25 2.25 0 003 6v12a2.25 2.25 0 002.25 2.25z"/></svg>
        </div>
        <div class="label">OS / Arch</div>
        <div class="value" style="font-size:0.85rem">{stats.os} / {stats.arch}</div>
      </div>
    </div>
  {/if}

  <div class="card">
    <div class="card-header">
      <div class="card-title">
        Services
        {#if services.length}<span class="card-title-count">{services.length}</span>{/if}
      </div>
    </div>
    {#if loading}
      <div class="loading-skeleton" style="height:120px;"></div>
    {:else if services.length}
      <table>
        <thead><tr><th>Service</th><th>State</th><th>Image</th></tr></thead>
        <tbody>
          {#each services as s}
          <tr>
            <td style="font-weight:500;">{s.name}</td>
            <td>
              <span class="badge badge-{s.state === 'running' ? 'green' : 'red'}">
                <span class="badge-dot"></span>{s.state}
              </span>
            </td>
            <td class="td-muted">{s.image}</td>
          </tr>
          {/each}
        </tbody>
      </table>
    {:else}
      <div class="empty-state">No services found.</div>
    {/if}
  </div>
</div>
