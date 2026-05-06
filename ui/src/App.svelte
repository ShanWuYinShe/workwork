<script>
import { onMount } from 'svelte';
import Dashboard from './routes/Dashboard.svelte';
import Services from './routes/Services.svelte';
import Clients from './routes/Clients.svelte';
import Users from './routes/Users.svelte';
import Settings from './routes/Settings.svelte';
import Security from './routes/Security.svelte';
import Cliproxy from './routes/Cliproxy.svelte';
import { getCurrentUser } from './lib/api.js';

let currentPath = $state(window.location.pathname.replace('/admin', '') || '/');
let userInfo = $state(null);

onMount(async () => {
  try { userInfo = await getCurrentUser(); } catch(e) { /* ignore */ }
});

function navigate(path) {
  currentPath = path;
  window.history.pushState({}, '', '/admin' + path);
}

window.addEventListener('popstate', () => {
  currentPath = window.location.pathname.replace('/admin', '') || '/';
});

let isAdmin = $derived(userInfo?.role === 'admin');

const navItems = [
  { path: '/', label: 'Dashboard', icon: 'M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6' },
  { path: '/services', label: 'Services', icon: 'M4 6a2 2 0 012-2h12a2 2 0 012 2v12a2 2 0 01-2 2H6a2 2 0 01-2-2V6z' },
  { path: '/clients', label: 'Clients', icon: 'M9 5H7a2 2 0 00-2 2v10a2 2 0 002 2h8a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 012-2h2a2 2 0 012 2M9 5h6m-6 4h.01M9 13h.01M13 9h.01M13 13h.01' },
  { path: '/users', label: 'Users', icon: 'M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z', adminOnly: true },
  { path: '/security', label: 'Security', icon: 'M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z', adminOnly: true },
  { path: '/cliproxy', label: 'Cliproxy', icon: 'M13 10V3L4 14h7v7l9-11h-7z', adminOnly: true },
  { path: '/settings', label: 'Settings', icon: 'M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z', adminOnly: true },
];
</script>

<div class="layout">
  <nav class="nav">
    <div class="brand">Admin</div>
    <div class="links">
      {#each navItems as item}
        {#if !item.adminOnly || isAdmin}
          <button class="nav-link" class:active={currentPath === item.path} onclick={() => navigate(item.path)}>
            <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d={item.icon} stroke-linecap="round" stroke-linejoin="round"/></svg>
            {item.label}
          </button>
        {/if}
      {/each}
    </div>
    {#if userInfo}
      <div style="margin-top:auto;padding:1rem 1.5rem;border-top:1px solid #334155;">
        <div style="font-size:0.75rem;color:#64748b;">Signed in as</div>
        <div style="font-size:0.875rem;color:#f8fafc;font-weight:500;">{userInfo.username}</div>
        <span class="badge" class:badge-green={userInfo.role === 'admin'} class:badge-red={userInfo.role !== 'admin'} style="margin-top:0.25rem;">{userInfo.role}</span>
      </div>
    {/if}
  </nav>
  <main class="main">
    {#if currentPath === '/'}
      <Dashboard />
    {:else if currentPath === '/services'}
      <Services />
    {:else if currentPath === '/clients'}
      <Clients />
    {:else if currentPath === '/users'}
      {#if isAdmin}
        <Users />
      {:else}
        <h1>Forbidden</h1>
      {/if}
    {:else if currentPath === '/security'}
      {#if isAdmin}
        <Security />
      {:else}
        <h1>Forbidden</h1>
      {/if}
    {:else if currentPath === '/cliproxy'}
      {#if isAdmin}
        <Cliproxy />
      {:else}
        <h1>Forbidden</h1>
      {/if}
    {:else if currentPath === '/settings'}
      {#if isAdmin}
        <Settings />
      {:else}
        <h1>Forbidden</h1>
      {/if}
    {:else}
      <h1>Not Found</h1>
    {/if}
  </main>
</div>

<style>
  :global(*) { margin:0; padding:0; box-sizing:border-box; }
  :global(body) { font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif; background:#0f172a; color:#e2e8f0; min-height:100vh; }
  .layout { display:flex; min-height:100vh; }
  .nav { position:fixed; left:0; top:0; bottom:0; width:220px; background:#1e293b; border-right:1px solid #334155; display:flex; flex-direction:column; padding:1.5rem 0; z-index:10; }
  .brand { font-size:1.25rem; font-weight:700; color:#f8fafc; padding:0 1.5rem 2rem; letter-spacing:-0.02em; }
  .links { display:flex; flex-direction:column; gap:0.25rem; }
  .nav-link { display:flex; align-items:center; gap:0.75rem; padding:0.625rem 1.5rem; color:#94a3b8; text-decoration:none; font-size:0.875rem; transition:all 0.15s; border-left:3px solid transparent; background:none; border-right:none; border-top:none; border-bottom:none; width:100%; text-align:left; cursor:pointer; }
  .nav-link:hover { color:#f1f5f9; background:#334155; }
  .nav-link.active { color:#60a5fa !important; background:#1e3a5f; border-left-color:#60a5fa; }
  .icon { width:1.25rem; height:1.25rem; flex-shrink:0; }
  .main { flex:1; padding:2rem; margin-left:220px; max-width:1200px; }
  :global(.card) { background:#1e293b; border:1px solid #334155; border-radius:0.5rem; padding:1.25rem; margin-bottom:1rem; }
  :global(.card h2) { font-size:1rem; font-weight:600; margin-bottom:1rem; color:#f8fafc; }
  :global(table) { width:100%; border-collapse:collapse; }
  :global(th) { text-align:left; padding:0.5rem 0.75rem; font-size:0.75rem; text-transform:uppercase; color:#64748b; border-bottom:1px solid #334155; letter-spacing:0.05em; }
  :global(td) { padding:0.625rem 0.75rem; border-bottom:1px solid #1e293b; font-size:0.875rem; }
  :global(.badge) { display:inline-block; padding:0.125rem 0.5rem; border-radius:9999px; font-size:0.75rem; font-weight:500; }
  :global(.badge-green) { background:#064e3b; color:#6ee7b7; }
  :global(.badge-red) { background:#7f1d1d; color:#fca5a5; }
  :global(.btn) { display:inline-block; padding:0.375rem 0.75rem; border:none; border-radius:0.375rem; font-size:0.8rem; cursor:pointer; font-weight:500; transition:opacity 0.15s; }
  :global(.btn:hover) { opacity:0.85; }
  :global(.btn-blue) { background:#1d4ed8; color:#fff; }
  :global(.btn-red) { background:#b91c1c; color:#fff; }
  :global(.btn-green) { background:#15803d; color:#fff; }
  :global(.btn-gray) { background:#374151; color:#d1d5db; }
  :global(.btn-sm) { padding:0.25rem 0.5rem; font-size:0.75rem; }
  :global(input) { background:#0f172a; border:1px solid #334155; color:#e2e8f0; padding:0.5rem 0.75rem; border-radius:0.375rem; font-size:0.875rem; outline:none; }
  :global(input:focus) { border-color:#60a5fa; }
  :global(textarea) { background:#0f172a; border:1px solid #334155; color:#e2e8f0; padding:0.75rem; border-radius:0.375rem; font-family:monospace; font-size:0.75rem; width:100%; }
  :global(label) { display:block; font-size:0.75rem; color:#64748b; margin-bottom:0.25rem; }
  :global(h1) { font-size:1.5rem; font-weight:600; margin-bottom:1.5rem; color:#f8fafc; }
  :global(.stats) { display:grid; grid-template-columns:repeat(auto-fit,minmax(180px,1fr)); gap:1rem; margin-bottom:1.5rem; }
  :global(.stat) { background:#1e293b; border:1px solid #334155; border-radius:0.5rem; padding:1rem; }
  :global(.stat .label) { font-size:0.75rem; color:#64748b; text-transform:uppercase; letter-spacing:0.05em; }
  :global(.stat .value) { font-size:1.5rem; font-weight:700; color:#f8fafc; margin-top:0.25rem; }
  :global(.eye-btn) { position:absolute; right:2px; top:50%; transform:translateY(-4px); background:none; border:none; cursor:pointer; padding:4px; line-height:0; color:#64748b; }
  :global(.eye-btn:hover) { color:#e2e8f0; }
  :global(.eye-icon) { width:18px; height:18px; }
</style>
