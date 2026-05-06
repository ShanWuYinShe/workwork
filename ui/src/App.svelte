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
  <aside class="sidebar">
    <div class="sidebar-header">
      <div class="brand-icon">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M12 2L2 7l10 5 10-5-10-5z"/><path d="M2 17l10 5 10-5"/><path d="M2 12l10 5 10-5"/></svg>
      </div>
      <div class="brand-text">
        <span class="brand-title">WorkWork</span>
        <span class="brand-sub">Admin Panel</span>
      </div>
    </div>

    <nav class="nav-links">
      {#each navItems as item}
        {#if !item.adminOnly || isAdmin}
          <button class="nav-item" class:active={currentPath === item.path} onclick={() => navigate(item.path)}>
            <svg class="nav-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d={item.icon} stroke-linecap="round" stroke-linejoin="round"/></svg>
            <span class="nav-label">{item.label}</span>
          </button>
        {/if}
      {/each}
    </nav>

    <div class="sidebar-footer">
      {#if userInfo}
        <div class="user-card">
          <div class="user-avatar" class:admin-avatar={userInfo.role === 'admin'}>{userInfo.username[0].toUpperCase()}</div>
          <div class="user-info">
            <div class="user-name">{userInfo.username}</div>
            <span class="user-role" class:role-admin={userInfo.role === 'admin'}>{userInfo.role}</span>
          </div>
        </div>
      {/if}
    </div>
  </aside>

  <main class="main">
    {#if currentPath === '/'}
      <Dashboard />
    {:else if currentPath === '/services'}
      <Services />
    {:else if currentPath === '/clients'}
      <Clients />
    {:else if currentPath === '/users'}
      {#if isAdmin}<Users />{:else}<h1>Forbidden</h1>{/if}
    {:else if currentPath === '/security'}
      {#if isAdmin}<Security />{:else}<h1>Forbidden</h1>{/if}
    {:else if currentPath === '/cliproxy'}
      {#if isAdmin}<Cliproxy />{:else}<h1>Forbidden</h1>{/if}
    {:else if currentPath === '/settings'}
      {#if isAdmin}<Settings />{:else}<h1>Forbidden</h1>{/if}
    {:else}
      <h1>Not Found</h1>
    {/if}
  </main>
</div>

<style>
  :global(*) { margin:0; padding:0; box-sizing:border-box; }
  :global(body) { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif; background: #0b1121; color: #e2e8f0; min-height: 100vh; -webkit-font-smoothing: antialiased; }

  .layout { display: flex; min-height: 100vh; }

  /* ── Sidebar ── */
  .sidebar {
    position: fixed; left: 0; top: 0; bottom: 0;
    width: 240px;
    background: #0f1729;
    border-right: 1px solid rgba(255,255,255,0.06);
    display: flex; flex-direction: column;
    z-index: 20;
  }

  .sidebar-header {
    padding: 1.5rem 1.25rem 1.25rem;
    display: flex; align-items: center; gap: 0.75rem;
    border-bottom: 1px solid rgba(255,255,255,0.05);
  }

  .brand-icon {
    width: 36px; height: 36px;
    display: flex; align-items: center; justify-content: center;
    background: linear-gradient(135deg, #1d4ed8, #7c3aed);
    border-radius: 10px;
    color: #fff;
    flex-shrink: 0;
  }
  .brand-icon svg { width: 20px; height: 20px; }

  .brand-title { display: block; font-size: 0.95rem; font-weight: 700; color: #f1f5f9; letter-spacing: -0.01em; line-height: 1.3; }
  .brand-sub { display: block; font-size: 0.65rem; color: #475569; font-weight: 500; text-transform: uppercase; letter-spacing: 0.08em; }

  /* Navigation */
  .nav-links {
    flex: 1;
    padding: 0.75rem 0.75rem;
    display: flex; flex-direction: column;
    gap: 2px;
    overflow-y: auto;
  }

  .nav-item {
    display: flex; align-items: center; gap: 0.625rem;
    padding: 0.5rem 0.75rem;
    border-radius: 8px;
    color: #64748b;
    font-size: 0.8125rem;
    font-weight: 500;
    cursor: pointer;
    transition: all 0.12s ease;
    background: none;
    border: none;
    width: 100%;
    text-align: left;
    position: relative;
  }
  .nav-item:hover { color: #e2e8f0; background: rgba(255,255,255,0.04); }
  .nav-item.active {
    color: #93c5fd;
    background: rgba(59,130,246,0.12);
  }
  .nav-item.active::before {
    content: '';
    position: absolute; left: 0; top: 50%; transform: translateY(-50%);
    width: 3px; height: 18px;
    background: #3b82f6;
    border-radius: 0 2px 2px 0;
  }

  .nav-icon { width: 1.15rem; height: 1.15rem; flex-shrink: 0; opacity: 0.85; }
  .nav-item.active .nav-icon { opacity: 1; }
  .nav-label { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

  /* User footer */
  .sidebar-footer {
    padding: 0.75rem;
    border-top: 1px solid rgba(255,255,255,0.05);
  }

  .user-card {
    display: flex; align-items: center; gap: 0.625rem;
    padding: 0.625rem 0.75rem;
    border-radius: 10px;
    background: rgba(255,255,255,0.03);
  }

  .user-avatar {
    width: 32px; height: 32px;
    border-radius: 8px;
    display: flex; align-items: center; justify-content: center;
    font-size: 0.8rem; font-weight: 700;
    background: #334155;
    color: #94a3b8;
    flex-shrink: 0;
  }
  .admin-avatar { background: linear-gradient(135deg, #1d4ed8, #7c3aed); color: #fff; }

  .user-info { min-width: 0; }
  .user-name { font-size: 0.8rem; font-weight: 600; color: #f1f5f9; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .user-role { display: inline-block; font-size: 0.65rem; font-weight: 600; text-transform: uppercase; letter-spacing: 0.06em; padding: 0.1rem 0.4rem; border-radius: 4px; margin-top: 2px; background: #334155; color: #94a3b8; }
  .role-admin { background: rgba(59,130,246,0.15); color: #93c5fd; }

  /* ── Main ── */
  .main {
    flex: 1; padding: 2rem 2.5rem;
    margin-left: 240px;
    max-width: 1280px; width: 100%;
  }

  /* ── Global Components ── */
  :global(.card) {
    background: rgba(255,255,255,0.02);
    border: 1px solid rgba(255,255,255,0.06);
    border-radius: 12px;
    padding: 1.25rem 1.5rem;
    margin-bottom: 1rem;
  }
  :global(.card h2) {
    font-size: 0.9rem; font-weight: 600; margin-bottom: 1rem;
    color: #f1f5f9;
    display: flex; align-items: center; gap: 0.5rem;
  }

  :global(table) { width: 100%; border-collapse: collapse; }
  :global(th) { text-align: left; padding: 0.5rem 0.75rem; font-size: 0.7rem; text-transform: uppercase; color: #475569; border-bottom: 1px solid rgba(255,255,255,0.06); letter-spacing: 0.06em; font-weight: 600; }
  :global(td) { padding: 0.625rem 0.75rem; border-bottom: 1px solid rgba(255,255,255,0.03); font-size: 0.8125rem; }
  :global(tr:hover td) { background: rgba(255,255,255,0.015); }
  :global(tr:last-child td) { border-bottom: none; }

  :global(.badge) { display: inline-flex; align-items: center; padding: 0.15rem 0.5rem; border-radius: 6px; font-size: 0.7rem; font-weight: 600; letter-spacing: 0.02em; }
  :global(.badge-green) { background: rgba(34,197,94,0.12); color: #4ade80; }
  :global(.badge-red) { background: rgba(239,68,68,0.12); color: #f87171; }

  :global(.btn) { display: inline-flex; align-items: center; gap: 0.25rem; padding: 0.4rem 0.75rem; border: none; border-radius: 8px; font-size: 0.78rem; cursor: pointer; font-weight: 500; transition: all 0.12s ease; white-space: nowrap; }
  :global(.btn:hover) { filter: brightness(1.15); transform: translateY(-1px); }
  :global(.btn:active) { transform: translateY(0); }
  :global(.btn-blue) { background: #2563eb; color: #fff; }
  :global(.btn-red) { background: #dc2626; color: #fff; }
  :global(.btn-green) { background: #16a34a; color: #fff; }
  :global(.btn-gray) { background: rgba(255,255,255,0.08); color: #94a3b8; }
  :global(.btn-gray:hover) { background: rgba(255,255,255,0.12); color: #e2e8f0; }
  :global(.btn-sm) { padding: 0.28rem 0.5rem; font-size: 0.72rem; border-radius: 6px; }

  :global(input) { background: rgba(0,0,0,0.25); border: 1px solid rgba(255,255,255,0.08); color: #e2e8f0; padding: 0.45rem 0.65rem; border-radius: 8px; font-size: 0.8125rem; outline: none; transition: border-color 0.15s; width: 100%; }
  :global(input:focus) { border-color: #3b82f6; box-shadow: 0 0 0 3px rgba(59,130,246,0.15); }
  :global(textarea) { background: rgba(0,0,0,0.25); border: 1px solid rgba(255,255,255,0.08); color: #e2e8f0; padding: 0.65rem; border-radius: 8px; font-family: 'SF Mono', 'Fira Code', monospace; font-size: 0.75rem; width: 100%; resize: vertical; outline: none; }
  :global(textarea:focus) { border-color: #3b82f6; }
  :global(select) { background: rgba(0,0,0,0.25); border: 1px solid rgba(255,255,255,0.08); color: #e2e8f0; padding: 0.45rem 0.65rem; border-radius: 8px; font-size: 0.8125rem; outline: none; }
  :global(select:focus) { border-color: #3b82f6; }

  :global(label) { display: block; font-size: 0.7rem; color: #475569; margin-bottom: 0.3rem; font-weight: 600; text-transform: uppercase; letter-spacing: 0.04em; }

  :global(h1) { font-size: 1.35rem; font-weight: 700; margin-bottom: 1.5rem; color: #f1f5f9; letter-spacing: -0.02em; }

  :global(.stats) { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 0.75rem; margin-bottom: 1.5rem; }
  :global(.stat) { background: rgba(255,255,255,0.02); border: 1px solid rgba(255,255,255,0.06); border-radius: 12px; padding: 1.125rem 1.25rem; transition: border-color 0.15s; }
  :global(.stat:hover) { border-color: rgba(255,255,255,0.12); }
  :global(.stat .label) { font-size: 0.68rem; color: #475569; text-transform: uppercase; letter-spacing: 0.06em; font-weight: 600; margin-bottom: 0.35rem; }
  :global(.stat .value) { font-size: 1.35rem; font-weight: 700; color: #f1f5f9; letter-spacing: -0.02em; }

  :global(.eye-btn) { position: absolute; right: 2px; top: 50%; transform: translateY(-8px); background: none; border: none; cursor: pointer; padding: 4px; line-height: 0; color: #475569; border-radius: 4px; }
  :global(.eye-btn:hover) { color: #e2e8f0; }
  :global(.eye-icon) { width: 17px; height: 17px; }
</style>
