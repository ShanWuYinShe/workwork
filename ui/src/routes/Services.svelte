<script>
import { onMount } from 'svelte';
let services = [];
let loading = true;
let error = '';

onMount(async () => {
  try {
    const r = await fetch('/api/admin/services', { credentials: 'same-origin' });
    if (!r.ok) throw new Error('Auth required');
    services = await r.json();
  } catch(e) { error = e.message; }
  finally { loading = false; }
});

async function act(name, action) {
  try {
    const r = await fetch(`/api/admin/services/${name}/${action}`, { method: 'POST', credentials: 'same-origin' });
    if (r.ok) {
      const d = await r.json();
      services = d.services || services;
    }
  } catch(e) { error = e.message; }
}
</script>

<h1>Services</h1>
{#if loading}<p style="color:#64748b;">Loading...</p>{/if}
{#if error}<div style="background:#7f1d1d;color:#fca5a5;padding:0.75rem 1rem;border-radius:0.5rem;margin-bottom:1rem;">{error}</div>{/if}

<div class="card">
  <table>
    <thead><tr><th>Service</th><th>State</th><th>Ports</th><th>Image</th><th>Actions</th></tr></thead>
    <tbody>
      {#each services as s}
      <tr>
        <td style="font-weight:500;">{s.name}</td>
        <td><span class="badge badge-{s.state === 'running' ? 'green' : 'red'}">{s.state}</span></td>
        <td style="font-size:0.75rem;color:#94a3b8;font-family:monospace">{s.ports || '-'}</td>
        <td style="font-size:0.75rem;color:#94a3b8;">{s.image}</td>
        <td>
          <button class="btn btn-blue btn-sm" on:click={() => act(s.name, 'restart')}>Restart</button>
          {#if s.state === 'running'}
            <button class="btn btn-red btn-sm" on:click={() => act(s.name, 'stop')}>Stop</button>
          {:else}
            <button class="btn btn-green btn-sm" on:click={() => act(s.name, 'start')}>Start</button>
          {/if}
        </td>
      </tr>
      {/each}
    </tbody>
  </table>
</div>
