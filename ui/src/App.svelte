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
let sidebarCollapsed = $state(false);

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

const navSections = [
  {
    label: 'Overview',
    items: [
      { path: '/', label: 'Dashboard', icon: 'grid' },
    ]
  },
  {
    label: 'Manage',
    items: [
      { path: '/services', label: 'Services', icon: 'box' },
      { path: '/clients', label: 'Clients', icon: 'users' },
    ]
  },
  {
    label: 'System',
    items: [
      { path: '/users', label: 'Users', icon: 'user-circle', adminOnly: true },
      { path: '/security', label: 'Security', icon: 'shield', adminOnly: true },
      { path: '/cliproxy', label: 'Cliproxy', icon: 'zap', adminOnly: true },
      { path: '/settings', label: 'Settings', icon: 'settings', adminOnly: true },
    ]
  }
];

const icons = {
  grid: '<path stroke-linecap="round" stroke-linejoin="round" d="M3.75 6A2.25 2.25 0 0 1 6 3.75h2.25A2.25 2.25 0 0 1 10.5 6v2.25a2.25 2.25 0 0 1-2.25 2.25H6a2.25 2.25 0 0 1-2.25-2.25V6ZM3.75 15.75A2.25 2.25 0 0 1 6 13.5h2.25a2.25 2.25 0 0 1 2.25 2.25V18a2.25 2.25 0 0 1-2.25 2.25H6A2.25 2.25 0 0 1 3.75 18v-2.25ZM13.5 6a2.25 2.25 0 0 1 2.25-2.25H18A2.25 2.25 0 0 1 20.25 6v2.25A2.25 2.25 0 0 1 18 10.5h-2.25a2.25 2.25 0 0 1-2.25-2.25V6ZM13.5 15.75a2.25 2.25 0 0 1 2.25-2.25H18a2.25 2.25 0 0 1 2.25 2.25V18A2.25 2.25 0 0 1 18 20.25h-2.25a2.25 2.25 0 0 1-2.25-2.25v-2.25Z"/>',
  box: '<path stroke-linecap="round" stroke-linejoin="round" d="M20.25 7.5l-.625 10.632a2.25 2.25 0 01-2.247 2.118H6.622a2.25 2.25 0 01-2.247-2.118L3.75 7.5M10 11.25h4M3.375 7.5h17.25c.621 0 1.125-.504 1.125-1.125v-1.5c0-.621-.504-1.125-1.125-1.125H3.375c-.621 0-1.125.504-1.125 1.125v1.5c0 .621.504 1.125 1.125 1.125Z"/>',
  users: '<path stroke-linecap="round" stroke-linejoin="round" d="M18 18.72a9.094 9.094 0 0 0 3.741-.479 3 3 0 0 0-4.682-2.72m.94 3.198.001.031c0 .225-.012.447-.037.666A11.944 11.944 0 0 1 12 21c-2.17 0-4.207-.576-5.963-1.584A6.062 6.062 0 0 1 6 18.719m12 0a5.971 5.971 0 0 0-.941-3.197m0 0A5.995 5.995 0 0 0 12 12.75a5.995 5.995 0 0 0-5.058 2.772m0 0a3 3 0 0 0-4.681 2.72 8.986 8.986 0 0 0 3.74.477m.94-3.197a5.971 5.971 0 0 0-.94 3.197M15 6.75a3 3 0 1 1-6 0 3 3 0 0 1 6 0Zm6 3a2.25 2.25 0 1 1-4.5 0 2.25 2.25 0 0 1 4.5 0Zm-13.5 0a2.25 2.25 0 1 1-4.5 0 2.25 2.25 0 0 1 4.5 0Z"/>',
  'user-circle': '<path stroke-linecap="round" stroke-linejoin="round" d="M17.982 18.725A7.488 7.488 0 0 0 12 15.75a7.488 7.488 0 0 0-5.982 2.975m11.963 0a9 9 0 1 0-11.963 0m11.963 0A8.966 8.966 0 0 1 12 21a8.966 8.966 0 0 1-5.982-2.275M15 9.75a3 3 0 1 1-6 0 3 3 0 0 1 6 0Z"/>',
  shield: '<path stroke-linecap="round" stroke-linejoin="round" d="M9 12.75L11.25 15 15 9.75m-3-7.036A11.959 11.959 0 0 1 3.598 6 11.99 11.99 0 0 0 3 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285Z"/>',
  zap: '<path stroke-linecap="round" stroke-linejoin="round" d="m3.75 13.5 10.5-11.25L12 10.5h8.25L9.75 21.75 12 13.5H3.75Z"/>',
  settings: '<path stroke-linecap="round" stroke-linejoin="round" d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.325.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 0 1 1.37.49l1.296 2.247a1.125 1.125 0 0 1-.26 1.431l-1.003.827c-.293.241-.438.613-.43.992a7.723 7.723 0 0 1 0 .255c-.008.378.137.75.43.991l1.004.827c.424.35.534.955.26 1.43l-1.298 2.247a1.125 1.125 0 0 1-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.47 6.47 0 0 1-.22.128c-.331.183-.581.495-.644.869l-.213 1.281c-.09.543-.56.94-1.11.94h-2.594c-.55 0-1.019-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 0 1-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 0 1-1.369-.49l-1.297-2.247a1.125 1.125 0 0 1 .26-1.431l1.004-.827c.292-.24.437-.613.43-.991a6.932 6.932 0 0 1 0-.255c.007-.38-.138-.751-.43-.992l-1.004-.827a1.125 1.125 0 0 1-.26-1.43l1.297-2.247a1.125 1.125 0 0 1 1.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.086.22-.128.332-.183.582-.495.644-.869l.214-1.28Z"/><path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 1 1-6 0 3 3 0 0 1 6 0Z"/>',
};

function getIcon(name) {
  return icons[name] || '';
}
</script>

<div class="layout" class:collapsed={sidebarCollapsed}>
  <aside class="sidebar">
    <div class="sidebar-header">
      <div class="brand-icon">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M12 2L2 7l10 5 10-5-10-5z"/><path d="M2 17l10 5 10-5"/><path d="M2 12l10 5 10-5"/></svg>
      </div>
      <div class="brand-text">
        <span class="brand-title">WorkWork</span>
        <span class="brand-sub">Admin Panel</span>
      </div>
      <button class="sidebar-toggle" onclick={() => sidebarCollapsed = !sidebarCollapsed} title={sidebarCollapsed ? 'Expand' : 'Collapse'} aria-label={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          {#if sidebarCollapsed}
            <polyline points="9 18 15 12 9 6"/>
          {:else}
            <polyline points="15 18 9 12 15 6"/>
          {/if}
        </svg>
      </button>
    </div>

    <nav class="sidebar-nav">
      {#each navSections as section}
        {#if section.items.filter(item => !item.adminOnly || isAdmin).length > 0}
          <div class="nav-section">
            {#if !sidebarCollapsed}
              <div class="nav-section-label">{section.label}</div>
            {/if}
            {#each section.items as item}
              {#if !item.adminOnly || isAdmin}
                <button
                  class="nav-item"
                  class:active={currentPath === item.path}
                  onclick={() => navigate(item.path)}
                  title={sidebarCollapsed ? item.label : ''}
                >
                  <svg class="nav-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">{@html getIcon(item.icon)}</svg>
                  {#if !sidebarCollapsed}
                    <span class="nav-label">{item.label}</span>
                  {/if}
                </button>
              {/if}
            {/each}
          </div>
        {/if}
      {/each}
    </nav>

    <div class="sidebar-footer">
      {#if userInfo}
        <div class="user-card">
          <div class="user-avatar" class:admin-avatar={userInfo.role === 'admin'}>
            {userInfo.username[0].toUpperCase()}
          </div>
          {#if !sidebarCollapsed}
            <div class="user-info">
              <div class="user-name">{userInfo.username}</div>
              <span class="user-role" class:role-admin={userInfo.role === 'admin'}>{userInfo.role}</span>
            </div>
          {/if}
        </div>
      {/if}
    </div>
  </aside>

  <main class="main">
    <div class="main-scroll">
      {#if currentPath === '/'}
        <Dashboard />
      {:else if currentPath === '/services'}
        <Services />
      {:else if currentPath === '/clients'}
        <Clients />
      {:else if currentPath === '/users'}
        {#if isAdmin}<Users />{:else}<div class="page"><h1>Forbidden</h1></div>{/if}
      {:else if currentPath === '/security'}
        {#if isAdmin}<Security />{:else}<div class="page"><h1>Forbidden</h1></div>{/if}
      {:else if currentPath === '/cliproxy'}
        {#if isAdmin}<Cliproxy />{:else}<div class="page"><h1>Forbidden</h1></div>{/if}
      {:else if currentPath === '/settings'}
        {#if isAdmin}<Settings />{:else}<div class="page"><h1>Forbidden</h1></div>{/if}
      {:else}
        <div class="page"><h1>Not Found</h1></div>
      {/if}
    </div>
  </main>
</div>


<style>
  /* ── CSS Design Tokens ── */
  :global(:root) {
    --c-bg-deep: #060a13;
    --c-bg: #0a0f1a;
    --c-bg-elevated: #0f1525;
    --c-bg-surface: #131b2e;
    --c-bg-hover: #182240;
    --c-border: rgba(148, 163, 184, 0.08);
    --c-border-strong: rgba(148, 163, 184, 0.15);
    --c-text: #cbd5e1;
    --c-text-secondary: #64748b;
    --c-text-muted: #475569;
    --c-text-heading: #f1f5f9;
    --c-accent: #3b82f6;
    --c-accent-bg: rgba(59, 130, 246, 0.12);
    --c-accent-border: rgba(59, 130, 246, 0.3);
    --c-success: #22c55e;
    --c-success-bg: rgba(34, 197, 94, 0.1);
    --c-danger: #ef4444;
    --c-danger-bg: rgba(239, 68, 68, 0.1);
    --c-warning: #f59e0b;
    --c-warning-bg: rgba(245, 158, 11, 0.1);
    --c-purple: #8b5cf6;
    --c-purple-bg: rgba(139, 92, 246, 0.12);
    --radius-sm: 6px;
    --radius: 8px;
    --radius-lg: 12px;
    --radius-xl: 16px;
    --sidebar-width: 248px;
    --sidebar-collapsed: 64px;
    --transition: 0.15s ease;
  }

  :global(*) { margin: 0; padding: 0; box-sizing: border-box; }
  :global(body) {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif;
    background: var(--c-bg-deep);
    color: var(--c-text);
    min-height: 100vh;
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
  }

  /* ── Layout ── */
  .layout { display: flex; min-height: 100vh; }

  /* ── Sidebar ── */
  .sidebar {
    position: fixed; left: 0; top: 0; bottom: 0;
    width: var(--sidebar-width);
    background: var(--c-bg);
    border-right: 1px solid var(--c-border);
    display: flex; flex-direction: column;
    z-index: 20;
    transition: width 0.2s ease;
    overflow: hidden;
  }
  .collapsed .sidebar { width: var(--sidebar-collapsed); }

  .sidebar-header {
    padding: 1.25rem 1rem;
    display: flex; align-items: center; gap: 0.75rem;
    border-bottom: 1px solid var(--c-border);
    min-height: 60px;
  }

  .brand-icon {
    width: 34px; height: 34px;
    display: flex; align-items: center; justify-content: center;
    background: linear-gradient(135deg, #2563eb, #7c3aed);
    border-radius: var(--radius);
    color: #fff;
    flex-shrink: 0;
  }
  .brand-icon svg { width: 18px; height: 18px; }

  .brand-text { flex: 1; min-width: 0; }
  .brand-title { display: block; font-size: 0.9rem; font-weight: 700; color: var(--c-text-heading); letter-spacing: -0.01em; line-height: 1.3; }
  .brand-sub { display: block; font-size: 0.625rem; color: var(--c-text-muted); font-weight: 500; text-transform: uppercase; letter-spacing: 0.08em; }

  .sidebar-toggle {
    width: 24px; height: 24px;
    display: flex; align-items: center; justify-content: center;
    background: none; border: none; color: var(--c-text-muted);
    cursor: pointer; border-radius: var(--radius-sm);
    transition: color var(--transition), background var(--transition);
    flex-shrink: 0;
  }
  .sidebar-toggle svg { width: 16px; height: 16px; }
  .sidebar-toggle:hover { color: var(--c-text); background: var(--c-bg-hover); }
  .sidebar-toggle:focus-visible { outline: 2px solid var(--c-accent); outline-offset: 2px; }
  .collapsed .sidebar-toggle { display: none; }

  /* ── Navigation ── */
  .sidebar-nav {
    flex: 1;
    padding: 0.5rem 0.5rem;
    display: flex; flex-direction: column;
    gap: 0.25rem;
    overflow-y: auto;
    overflow-x: hidden;
  }

  .nav-section { margin-bottom: 0.25rem; }
  .nav-section-label {
    padding: 0.625rem 0.75rem 0.25rem;
    font-size: 0.625rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--c-text-muted);
    white-space: nowrap;
  }

  .nav-item {
    display: flex; align-items: center; gap: 0.625rem;
    padding: 0.5rem 0.75rem;
    border-radius: var(--radius);
    color: var(--c-text-secondary);
    font-size: 0.8rem;
    font-weight: 500;
    cursor: pointer;
    transition: all var(--transition);
    background: none;
    border: none;
    width: 100%;
    text-align: left;
    position: relative;
    white-space: nowrap;
  }
  .nav-item:hover { color: var(--c-text); background: var(--c-bg-hover); }
  .nav-item:focus-visible { outline: 2px solid var(--c-accent); outline-offset: -2px; }
  .nav-item.active {
    color: #93c5fd;
    background: var(--c-accent-bg);
  }
  .nav-item.active::before {
    content: '';
    position: absolute; left: 0; top: 50%; transform: translateY(-50%);
    width: 3px; height: 16px;
    background: var(--c-accent);
    border-radius: 0 2px 2px 0;
  }
  .collapsed .nav-item { justify-content: center; padding: 0.5rem; }

  .nav-icon { width: 1.15rem; height: 1.15rem; flex-shrink: 0; }
  .nav-label { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

  /* ── User footer ── */
  .sidebar-footer {
    padding: 0.75rem;
    border-top: 1px solid var(--c-border);
  }
  .user-card {
    display: flex; align-items: center; gap: 0.625rem;
    padding: 0.5rem 0.625rem;
    border-radius: var(--radius-lg);
    background: var(--c-bg-elevated);
  }
  .user-avatar {
    width: 32px; height: 32px;
    border-radius: var(--radius);
    display: flex; align-items: center; justify-content: center;
    font-size: 0.75rem; font-weight: 700;
    background: var(--c-bg-surface);
    color: var(--c-text-secondary);
    flex-shrink: 0;
  }
  .admin-avatar { background: linear-gradient(135deg, #2563eb, #7c3aed); color: #fff; }

  .user-info { min-width: 0; }
  .user-name { font-size: 0.78rem; font-weight: 600; color: var(--c-text-heading); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .user-role {
    display: inline-block; font-size: 0.6rem; font-weight: 600;
    text-transform: uppercase; letter-spacing: 0.06em;
    padding: 0.1rem 0.35rem; border-radius: 4px; margin-top: 2px;
    background: var(--c-bg-surface); color: var(--c-text-secondary);
  }
  .role-admin { background: var(--c-accent-bg); color: #93c5fd; }

  /* ── Main content ── */
  .main {
    flex: 1;
    margin-left: var(--sidebar-width);
    transition: margin-left 0.2s ease;
    min-height: 100vh;
    display: flex;
    flex-direction: column;
  }
  .collapsed .main { margin-left: var(--sidebar-collapsed); }

  .main-scroll {
    flex: 1;
    padding: 2rem 2.5rem 3rem;
    max-width: 1200px;
    width: 100%;
  }

  /* ── Page ── */
  :global(.page) { animation: pageIn 0.2s ease; }
  @keyframes pageIn { from { opacity: 0; transform: translateY(6px); } to { opacity: 1; transform: translateY(0); } }

  :global(.page-header) {
    display: flex; align-items: center; justify-content: space-between;
    gap: 1rem; margin-bottom: 1.5rem; flex-wrap: wrap;
  }
  :global(.page-title) {
    font-size: 1.25rem; font-weight: 700; color: var(--c-text-heading);
    letter-spacing: -0.02em;
  }
  :global(.page-actions) {
    display: flex; align-items: center; gap: 0.5rem;
  }

  /* ── Global Cards ── */
  :global(.card) {
    background: var(--c-bg-elevated);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-lg);
    padding: 1.25rem 1.5rem;
    margin-bottom: 1rem;
    transition: border-color var(--transition);
  }
  :global(.card:hover) { border-color: var(--c-border-strong); }
  :global(.card-header) {
    display: flex; align-items: center; justify-content: space-between;
    margin-bottom: 1rem; gap: 0.75rem; flex-wrap: wrap;
  }
  :global(.card-title) {
    font-size: 0.85rem; font-weight: 600;
    color: var(--c-text-heading);
    display: flex; align-items: center; gap: 0.5rem;
  }
  :global(.card-title-count) {
    font-size: 0.7rem; font-weight: 600;
    color: var(--c-text-muted);
    background: var(--c-bg-surface);
    padding: 0.1rem 0.45rem;
    border-radius: 10px;
    margin-left: 0.25rem;
  }
  :global(.card-actions) {
    display: flex; align-items: center; gap: 0.375rem;
  }
  :global(.card-subtitle) {
    font-size: 0.7rem; color: var(--c-text-muted); font-weight: 400;
  }

  /* ── Tables ── */
  :global(table) { width: 100%; border-collapse: collapse; }
  :global(th) {
    text-align: left; padding: 0.5rem 0.75rem;
    font-size: 0.65rem; text-transform: uppercase;
    color: var(--c-text-muted);
    border-bottom: 1px solid var(--c-border);
    letter-spacing: 0.06em; font-weight: 600;
  }
  :global(td) {
    padding: 0.625rem 0.75rem;
    border-bottom: 1px solid var(--c-border);
    font-size: 0.8rem;
    vertical-align: middle;
  }
  :global(tr:hover td) { background: rgba(148, 163, 184, 0.02); }
  :global(tr:last-child td) { border-bottom: none; }
  :global(.td-mono) { font-family: 'SF Mono', 'Fira Code', ui-monospace, monospace; font-size: 0.75rem; }
  :global(.td-muted) { color: var(--c-text-secondary); font-size: 0.75rem; }
  :global(.td-actions) { display: flex; gap: 0.25rem; flex-wrap: wrap; }

  /* ── Badges ── */
  :global(.badge) {
    display: inline-flex; align-items: center; gap: 0.35rem;
    padding: 0.15rem 0.5rem; border-radius: var(--radius-sm);
    font-size: 0.68rem; font-weight: 600; letter-spacing: 0.02em;
  }
  :global(.badge-dot) {
    width: 6px; height: 6px; border-radius: 50%;
    background: currentColor; flex-shrink: 0;
  }
  :global(.badge-green) { background: var(--c-success-bg); color: #4ade80; }
  :global(.badge-red) { background: var(--c-danger-bg); color: #f87171; }
  :global(.badge-blue) { background: var(--c-accent-bg); color: #93c5fd; }
  :global(.badge-purple) { background: var(--c-purple-bg); color: #c4b5fd; }
  :global(.badge-gray) { background: rgba(148, 163, 184, 0.08); color: var(--c-text-secondary); }

  /* ── Buttons ── */
  :global(.btn) {
    display: inline-flex; align-items: center; gap: 0.3rem;
    padding: 0.4rem 0.8rem; border: none;
    border-radius: var(--radius);
    font-size: 0.78rem; font-weight: 500;
    cursor: pointer;
    transition: all var(--transition);
    white-space: nowrap;
    line-height: 1.4;
  }
  :global(.btn:hover) { filter: brightness(1.1); }
  :global(.btn:active) { transform: scale(0.98); }
  :global(.btn:disabled) { opacity: 0.5; cursor: not-allowed; pointer-events: none; }
  :global(.btn:focus-visible) { outline: 2px solid var(--c-accent); outline-offset: 2px; }

  :global(.btn-primary) { background: var(--c-accent); color: #fff; }
  :global(.btn-primary:hover) { background: #2563eb; }
  :global(.btn-danger) { background: var(--c-danger); color: #fff; }
  :global(.btn-danger:hover) { background: #dc2626; }
  :global(.btn-success) { background: var(--c-success); color: #fff; }
  :global(.btn-success:hover) { background: #16a34a; }
  :global(.btn-ghost) {
    background: transparent; color: var(--c-text-secondary);
    border: 1px solid var(--c-border);
  }
  :global(.btn-ghost:hover) {
    background: var(--c-bg-hover); color: var(--c-text);
    border-color: var(--c-border-strong);
  }
  :global(.btn-sm) { padding: 0.25rem 0.55rem; font-size: 0.72rem; border-radius: var(--radius-sm); }
  :global(.btn-icon) {
    width: 32px; height: 32px;
    padding: 0; justify-content: center;
    border-radius: var(--radius);
    background: transparent;
    color: var(--c-text-secondary);
    border: 1px solid transparent;
  }
  :global(.btn-icon:hover) {
    background: var(--c-bg-hover);
    color: var(--c-text);
    border-color: var(--c-border);
  }

  /* ── Form elements ── */
  :global(.form-row) {
    display: flex; gap: 0.75rem; align-items: flex-end; flex-wrap: wrap;
  }
  :global(.form-group) { display: flex; flex-direction: column; }
  :global(.form-group-grow) { flex: 1; min-width: 160px; }

  :global(label) {
    display: block; font-size: 0.65rem; color: var(--c-text-muted);
    margin-bottom: 0.3rem; font-weight: 600;
    text-transform: uppercase; letter-spacing: 0.04em;
  }
  :global(input), :global(select) {
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border);
    color: var(--c-text);
    padding: 0.45rem 0.65rem;
    border-radius: var(--radius);
    font-size: 0.8rem;
    outline: none;
    transition: border-color var(--transition), box-shadow var(--transition);
    width: 100%;
  }
  :global(input:focus), :global(select:focus) {
    border-color: var(--c-accent);
    box-shadow: 0 0 0 3px var(--c-accent-bg);
  }
  :global(input::placeholder) { color: var(--c-text-muted); }
  :global(select) { cursor: pointer; }
  :global(textarea) {
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border);
    color: var(--c-text);
    padding: 0.65rem;
    border-radius: var(--radius);
    font-family: 'SF Mono', 'Fira Code', ui-monospace, monospace;
    font-size: 0.75rem;
    width: 100%; resize: vertical; outline: none;
    transition: border-color var(--transition);
  }
  :global(textarea:focus) { border-color: var(--c-accent); }

  /* ── Stats grid ── */
  :global(.stats) {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
    gap: 0.75rem;
    margin-bottom: 1.5rem;
  }
  :global(.stat) {
    background: var(--c-bg-elevated);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-lg);
    padding: 1rem 1.25rem;
    transition: border-color var(--transition);
  }
  :global(.stat:hover) { border-color: var(--c-border-strong); }
  :global(.stat .stat-icon) {
    width: 32px; height: 32px;
    display: flex; align-items: center; justify-content: center;
    border-radius: var(--radius);
    margin-bottom: 0.75rem;
  }
  :global(.stat .stat-icon svg) { width: 16px; height: 16px; }
  :global(.stat .stat-icon.blue) { background: var(--c-accent-bg); color: #93c5fd; }
  :global(.stat .stat-icon.green) { background: var(--c-success-bg); color: #4ade80; }
  :global(.stat .stat-icon.purple) { background: var(--c-purple-bg); color: #c4b5fd; }
  :global(.stat .stat-icon.orange) { background: var(--c-warning-bg); color: #fbbf24; }
  :global(.stat .label) {
    font-size: 0.65rem; color: var(--c-text-muted);
    text-transform: uppercase; letter-spacing: 0.06em;
    font-weight: 600; margin-bottom: 0.25rem;
  }
  :global(.stat .value) {
    font-size: 1.25rem; font-weight: 700;
    color: var(--c-text-heading); letter-spacing: -0.02em;
  }

  /* ── Toast ── */
  :global(.toast) {
    padding: 0.6rem 1rem;
    border-radius: var(--radius);
    font-size: 0.8rem; font-weight: 500;
    animation: toastIn 0.25s ease;
    pointer-events: auto;
    max-width: 400px;
    box-shadow: 0 4px 24px rgba(0,0,0,0.4);
  }
  :global(.toast-error) { background: #7f1d1d; color: #fca5a5; border: 1px solid rgba(239,68,68,0.2); }
  :global(.toast-success) { background: #064e3b; color: #6ee7b7; border: 1px solid rgba(34,197,94,0.2); }
  @keyframes toastIn { from { opacity: 0; transform: translateY(-8px) scale(0.96); } to { opacity: 1; transform: translateY(0) scale(1); } }

  /* ── Loading ── */
  :global(.loading-skeleton) {
    background: linear-gradient(90deg, var(--c-bg-surface) 25%, var(--c-bg-hover) 50%, var(--c-bg-surface) 75%);
    background-size: 200% 100%;
    animation: shimmer 1.5s infinite;
    border-radius: var(--radius);
  }
  @keyframes shimmer { 0% { background-position: 200% 0; } 100% { background-position: -200% 0; } }

  :global(.spinner) {
    width: 18px; height: 18px;
    border: 2px solid var(--c-border);
    border-top-color: var(--c-accent);
    border-radius: 50%;
    animation: spin 0.6s linear infinite;
    display: inline-block;
  }
  @keyframes spin { to { transform: rotate(360deg); } }

  /* ── Password eye toggle ── */
  :global(.eye-btn) {
    position: absolute; right: 4px; top: 50%; transform: translateY(-50%);
    background: none; border: none; cursor: pointer; padding: 4px;
    line-height: 0; color: var(--c-text-muted); border-radius: 4px;
  }
  :global(.eye-btn:hover) { color: var(--c-text); }
  :global(.eye-icon) { width: 16px; height: 16px; }

  /* ── Empty state ── */
  :global(.empty-state) {
    text-align: center; padding: 2rem 1rem;
    color: var(--c-text-muted); font-size: 0.85rem;
  }

  /* ── Responsive ── */
  @media (max-width: 768px) {
    .sidebar { width: var(--sidebar-collapsed); }
    .sidebar .brand-text, .sidebar .nav-label, .sidebar .nav-section-label,
    .sidebar .sidebar-toggle, .sidebar .user-info { display: none; }
    .sidebar .nav-item { justify-content: center; padding: 0.5rem; }
    .main { margin-left: var(--sidebar-collapsed) !important; }
    .main-scroll { padding: 1.25rem 1rem 2rem; }
  }

  @media (prefers-reduced-motion: reduce) {
    :global(.page) { animation: none; }
    :global(.toast) { animation: none; }
    :global(.loading-skeleton) { animation: none; }
    :global(.spinner) { animation: none; }
    .sidebar { transition: none; }
    .main { transition: none; }
  }
</style>
