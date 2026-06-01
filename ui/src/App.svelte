<script>
  import { onMount } from 'svelte';
  import { getCurrentUser } from './lib/api.js';
  import { t } from './lib/i18n.svelte.js';
  import { routes, routeTitles } from './lib/routes.js';
  import Sidebar from './lib/Sidebar.svelte';
  import ConfirmModal from './lib/ConfirmModal.svelte';
  import Toast from './lib/Toast.svelte';

  let currentPath = $state(window.location.pathname.replace('/admin', '') || '/');
  let userInfo = $state(null);
  let authError = $state(null);
  let authLoading = $state(true);
  let sidebarCollapsed = $state(false);
  let mobileMenuOpen = $state(false);

  async function loadUser() {
    authLoading = true;
    authError = null;
    try {
      userInfo = await getCurrentUser();
    } catch (e) {
      // Don't silently swallow — surface the failure so the user can
      // retry or report a broken auth backend. Without this, an admin
      // who hits one bad response is permanently locked on the
      // forbidden screen.
      authError = e?.message || String(e);
      userInfo = null;
    } finally {
      authLoading = false;
    }
  }

  onMount(() => {
    loadUser();
    function onPopstate() {
      currentPath = window.location.pathname.replace('/admin', '') || '/';
    }
    window.addEventListener('popstate', onPopstate);
    return () => window.removeEventListener('popstate', onPopstate);
  });

  function navigate(path) {
    currentPath = path;
    window.history.pushState({}, '', '/admin' + path);
  }

  $effect(() => {
    document.title = 'WorkWork — ' + t(routeTitles[currentPath] || 'dashboard');
  });

  let isAdmin = $derived(userInfo?.role === 'admin');

  let currentRoute = $derived(routes.find((r) => r.path === currentPath));
</script>

<div class="layout {sidebarCollapsed ? 'collapsed' : ''}">
  <Sidebar {currentPath} {navigate} {sidebarCollapsed} {userInfo} {isAdmin} {mobileMenuOpen} />

  {#if mobileMenuOpen}
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
    <div class="mobile-overlay" onclick={() => mobileMenuOpen = false}></div>
  {/if}

  <main class="main">
    {#if authError}
      <div class="auth-error-banner" role="alert">
        <div class="auth-error-text">
          <strong>{t('authFailedTitle')}</strong>
          <span>{authError}</span>
        </div>
        <div class="auth-error-actions">
          <button class="btn btn-ghost btn-sm" onclick={() => loadUser()} disabled={authLoading}>
            {authLoading ? '…' : t('retry')}
          </button>
        </div>
      </div>
    {/if}
    <div class="mobile-topbar">
      <button class="hamburger-btn" onclick={() => mobileMenuOpen = !mobileMenuOpen} aria-label="Menu">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="20" height="20">
          {#if mobileMenuOpen}
            <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
          {:else}
            <line x1="4" y1="6" x2="20" y2="6" /><line x1="4" y1="12" x2="20" y2="12" /><line x1="4" y1="18" x2="20" y2="18" />
          {/if}
        </svg>
      </button>
      <span class="topbar-title">WorkWork</span>
    </div>
    <div class="main-scroll">
      {#if currentRoute}
        {#if !currentRoute.adminOnly || isAdmin}
          <currentRoute.component />
        {:else}
          <div class="page"><h1>{t('forbidden')}</h1></div>
        {/if}
      {:else}
        <div class="page"><h1>{t('notFound')}</h1></div>
      {/if}
    </div>
  </main>
</div>

<ConfirmModal />
<Toast />

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

  :global(*) {
    margin: 0;
    padding: 0;
    box-sizing: border-box;
  }
  :global(body) {
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif;
    background: var(--c-bg-deep);
    color: var(--c-text);
    min-height: 100vh;
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
  }

  :global(*::-webkit-scrollbar) {
    width: 6px;
    height: 6px;
  }
  :global(*::-webkit-scrollbar-track) {
    background: var(--c-bg-deep);
  }
  :global(*::-webkit-scrollbar-thumb) {
    background: var(--c-bg-hover);
    border-radius: 3px;
  }
  :global(*::-webkit-scrollbar-thumb:hover) {
    background: var(--c-text-muted);
  }

  /* ── Layout ── */
  .layout {
    display: flex;
    min-height: 100vh;
  }

  /* ── Sidebar (global — rendered in Sidebar.svelte) ── */
  :global(.sidebar) {
    position: fixed;
    left: 0;
    top: 0;
    bottom: 0;
    width: var(--sidebar-width);
    background: var(--c-bg);
    border-right: 1px solid var(--c-border);
    display: flex;
    flex-direction: column;
    z-index: 20;
    transition: width 0.2s ease;
    overflow: hidden;
  }
:global(.sidebar.collapsed) {
     width: var(--sidebar-collapsed);
   }
   :global(.sidebar.collapsed .sidebar-header) {
    flex-direction: column;
    justify-content: center;
    padding: 0.75rem 0;
    gap: 0.5rem;
  }
  :global(.sidebar-header) {
    padding: 1.25rem 1rem;
    display: flex;
    align-items: center;
    gap: 0.75rem;
    border-bottom: 1px solid var(--c-border);
    min-height: 60px;
    position: relative;
  }
  :global(.brand-icon) {
    width: 34px;
    height: 34px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: linear-gradient(135deg, #2563eb, #7c3aed);
    border-radius: var(--radius);
    color: #fff;
    flex-shrink: 0;
  }
  :global(.brand-icon svg) {
    width: 18px;
    height: 18px;
  }
  :global(.brand-text) {
    flex: 1;
    min-width: 0;
    transition: opacity 0.15s ease;
  }
  :global(.sidebar.collapsed .brand-text) {
    display: none;
  }
  :global(.brand-title) {
    display: block;
    font-size: 0.9rem;
    font-weight: 700;
    color: var(--c-text-heading);
    letter-spacing: -0.01em;
    line-height: 1.3;
  }
  :global(.brand-sub) {
    display: block;
    font-size: 0.625rem;
    color: var(--c-text-muted);
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }
  :global(.sidebar-toggle) {
    width: 24px;
    height: 24px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: none;
    border: none;
    color: var(--c-text-muted);
    cursor: pointer;
    border-radius: var(--radius-sm);
    transition: color var(--transition), background var(--transition);
    flex-shrink: 0;
  }
  :global(.sidebar-toggle svg) {
    width: 16px;
    height: 16px;
  }
  :global(.sidebar-toggle:hover) {
    color: var(--c-text);
    background: var(--c-bg-hover);
  }
  :global(.sidebar-toggle:focus-visible) {
    outline: 2px solid var(--c-accent);
    outline-offset: 2px;
  }
  :global(.lang-toggle) {
    width: 28px;
    height: 24px;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border);
    color: var(--c-text-muted);
    cursor: pointer;
    border-radius: var(--radius-sm);
    font-size: 0.6rem;
    font-weight: 700;
    flex-shrink: 0;
    transition: all var(--transition);
  }
  :global(.lang-toggle:hover) {
    color: var(--c-text);
    border-color: var(--c-border-strong);
  }
  :global(.sidebar.collapsed .lang-toggle) {
    display: none;
  }
  :global(.sidebar.collapsed .sidebar-toggle) {
    position: static;
    transform: none;
  }
  :global(.sidebar-nav) {
    flex: 1;
    padding: 0.5rem 0.5rem;
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    overflow-y: auto;
    overflow-x: hidden;
  }
  :global(.nav-section) {
    margin-bottom: 0.25rem;
  }
  :global(.nav-section-label) {
    padding: 0.625rem 0.75rem 0.25rem;
    font-size: 0.625rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--c-text-muted);
    white-space: nowrap;
  }
  :global(.nav-item) {
    display: flex;
    align-items: center;
    gap: 0.625rem;
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
  :global(.nav-item:hover) {
    color: var(--c-text);
    background: var(--c-bg-hover);
  }
  :global(.nav-item:focus-visible) {
    outline: 2px solid var(--c-accent);
    outline-offset: -2px;
  }
  :global(.nav-item.active) {
    color: #93c5fd;
    background: var(--c-accent-bg);
  }
  :global(.nav-item.active::before) {
    content: '';
    position: absolute;
    left: 0;
    top: 50%;
    transform: translateY(-50%);
    width: 3px;
    height: 16px;
    background: var(--c-accent);
    border-radius: 0 2px 2px 0;
  }
  :global(.sidebar.collapsed .nav-item) {
    justify-content: center;
    padding: 0.5rem;
  }
  :global(.nav-icon) {
    width: 1.15rem;
    height: 1.15rem;
    flex-shrink: 0;
  }
  :global(.nav-label) {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  :global(.sidebar-footer) {
    padding: 0.75rem;
    border-top: 1px solid var(--c-border);
  }
  :global(.user-card) {
    display: flex;
    align-items: center;
    gap: 0.625rem;
    padding: 0.5rem 0.625rem;
    border-radius: var(--radius-lg);
    background: var(--c-bg-elevated);
  }
  :global(.user-avatar) {
    width: 32px;
    height: 32px;
    border-radius: var(--radius);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 0.75rem;
    font-weight: 700;
    background: var(--c-bg-surface);
    color: var(--c-text-secondary);
    flex-shrink: 0;
  }
  :global(.admin-avatar) {
    background: linear-gradient(135deg, #2563eb, #7c3aed);
    color: #fff;
  }
  :global(.user-info) {
    min-width: 0;
  }
  :global(.user-name) {
    font-size: 0.78rem;
    font-weight: 600;
    color: var(--c-text-heading);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  :global(.user-role) {
    display: inline-block;
    font-size: 0.6rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    padding: 0.1rem 0.35rem;
    border-radius: 4px;
    margin-top: 2px;
    background: var(--c-bg-surface);
    color: var(--c-text-secondary);
  }
  :global(.role-admin) {
    background: var(--c-accent-bg);
    color: #93c5fd;
  }
  :global(.sidebar.collapsed .sidebar-footer) {
    padding: 0.5rem;
  }
  :global(.sidebar.collapsed .user-card) {
    justify-content: center;
    padding: 0.4rem;
  }
  :global(.logout-btn) {
    background: none;
    border: none;
    cursor: pointer;
    padding: 4px;
    color: var(--c-text-muted);
    border-radius: 4px;
    display: flex;
    margin-left: auto;
    opacity: 0;
    transition: opacity 0.15s;
  }
  :global(.user-card:hover .logout-btn) {
    opacity: 1;
  }
  :global(.logout-btn:hover) {
    color: var(--c-danger);
    background: var(--c-danger-bg);
  }
  :global(.sidebar.collapsed .logout-btn) {
    display: none;
  }

  /* ── Main content ── */
  .main {
    flex: 1;
    margin-left: var(--sidebar-width);
    transition: margin-left 0.2s ease;
    min-height: 100vh;
    display: flex;
    flex-direction: column;
  }
  .sidebar.collapsed ~ .main {
    margin-left: var(--sidebar-collapsed);
  }

  .main-scroll {
    flex: 1;
    padding: 2rem 2.5rem 3rem;
    max-width: 1200px;
    width: 100%;
  }

  /* ── Page ── */
  :global(.page) {
    animation: pageIn 0.2s ease;
  }
  @keyframes pageIn {
    from {
      opacity: 0;
      transform: translateY(6px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  :global(.page-header) {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 1.5rem;
    flex-wrap: wrap;
  }
  :global(.page-title) {
    font-size: 1.25rem;
    font-weight: 700;
    color: var(--c-text-heading);
    letter-spacing: -0.02em;
  }
  :global(.page-actions) {
    display: flex;
    align-items: center;
    gap: 0.5rem;
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
  :global(.card:hover) {
    border-color: var(--c-border-strong);
  }
  :global(.card-header) {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 1rem;
    gap: 0.75rem;
    flex-wrap: wrap;
  }
  :global(.card-title) {
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--c-text-heading);
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  :global(.card-title-count) {
    font-size: 0.7rem;
    font-weight: 600;
    color: var(--c-text-muted);
    background: var(--c-bg-surface);
    padding: 0.1rem 0.45rem;
    border-radius: 10px;
    margin-left: 0.25rem;
  }
  :global(.card-actions) {
    display: flex;
    align-items: center;
    gap: 0.375rem;
  }
  :global(.card-subtitle) {
    font-size: 0.7rem;
    color: var(--c-text-muted);
    font-weight: 400;
  }

  /* ── Tables ── */
  :global(table) {
    width: 100%;
    border-collapse: collapse;
  }
  :global(th) {
    text-align: left;
    padding: 0.5rem 0.75rem;
    font-size: 0.65rem;
    text-transform: uppercase;
    color: var(--c-text-muted);
    border-bottom: 1px solid var(--c-border);
    letter-spacing: 0.06em;
    font-weight: 600;
  }
  :global(td) {
    padding: 0.625rem 0.75rem;
    border-bottom: 1px solid var(--c-border);
    font-size: 0.8rem;
    vertical-align: middle;
  }
  :global(tr:hover td) {
    background: rgba(148, 163, 184, 0.02);
  }
  :global(tr:last-child td) {
    border-bottom: none;
  }
  :global(.td-mono) {
    font-family: 'SF Mono', 'Fira Code', ui-monospace, monospace;
    font-size: 0.75rem;
  }
  :global(.td-muted) {
    color: var(--c-text-secondary);
    font-size: 0.75rem;
  }
  :global(.td-actions) {
    display: flex;
    gap: 0.25rem;
    flex-wrap: wrap;
  }

  /* ── Badges ── */
  :global(.badge) {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.15rem 0.5rem;
    border-radius: var(--radius-sm);
    font-size: 0.68rem;
    font-weight: 600;
    letter-spacing: 0.02em;
  }
  :global(.badge-dot) {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: currentColor;
    flex-shrink: 0;
  }
  :global(.badge-green) {
    background: var(--c-success-bg);
    color: #4ade80;
  }
  :global(.badge-red) {
    background: var(--c-danger-bg);
    color: #f87171;
  }
  :global(.badge-blue) {
    background: var(--c-accent-bg);
    color: #93c5fd;
  }
  :global(.badge-purple) {
    background: var(--c-purple-bg);
    color: #c4b5fd;
  }
  :global(.badge-gray) {
    background: rgba(148, 163, 184, 0.08);
    color: var(--c-text-secondary);
  }
  :global(.badge-yellow) {
    background: rgba(245, 158, 11, 0.1);
    color: #fbbf24;
  }

  /* ── Buttons ── */
  :global(.btn) {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    padding: 0.4rem 0.8rem;
    border: none;
    border-radius: var(--radius);
    font-size: 0.78rem;
    font-weight: 500;
    cursor: pointer;
    transition: all var(--transition);
    white-space: nowrap;
    line-height: 1.4;
  }
  :global(.btn:hover) {
    filter: brightness(1.1);
  }
  :global(.btn:active) {
    transform: scale(0.98);
  }
  :global(.btn:disabled) {
    opacity: 0.5;
    cursor: not-allowed;
    pointer-events: none;
  }
  :global(.btn:focus-visible) {
    outline: 2px solid var(--c-accent);
    outline-offset: 2px;
  }

  :global(.btn-primary) {
    background: var(--c-accent);
    color: #fff;
  }
  :global(.btn-primary:hover) {
    background: #2563eb;
  }
  :global(.btn-danger) {
    background: var(--c-danger);
    color: #fff;
  }
  :global(.btn-danger:hover) {
    background: #dc2626;
  }
  :global(.btn-success) {
    background: var(--c-success);
    color: #fff;
  }
  :global(.btn-success:hover) {
    background: #16a34a;
  }
  :global(.btn-ghost) {
    background: transparent;
    color: var(--c-text-secondary);
    border: 1px solid var(--c-border);
  }
  :global(.btn-ghost:hover) {
    background: var(--c-bg-hover);
    color: var(--c-text);
    border-color: var(--c-border-strong);
  }
  :global(.btn-sm) {
    padding: 0.25rem 0.55rem;
    font-size: 0.72rem;
    border-radius: var(--radius-sm);
  }
  :global(.btn-icon) {
    width: 32px;
    height: 32px;
    padding: 0;
    justify-content: center;
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
    display: flex;
    gap: 0.75rem;
    align-items: flex-end;
    flex-wrap: wrap;
  }
  :global(.form-group) {
    display: flex;
    flex-direction: column;
  }
  :global(.form-group-grow) {
    flex: 1;
    min-width: 160px;
  }

  :global(label) {
    display: block;
    font-size: 0.65rem;
    color: var(--c-text-muted);
    margin-bottom: 0.3rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  :global(input),
  :global(select) {
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border);
    color: var(--c-text);
    padding: 0.45rem 0.65rem;
    border-radius: var(--radius);
    font-size: 0.8rem;
    outline: none;
    transition:
      border-color var(--transition),
      box-shadow var(--transition);
    width: 100%;
  }
  :global(input:focus),
  :global(select:focus) {
    border-color: var(--c-accent);
    box-shadow: 0 0 0 3px var(--c-accent-bg);
  }
  :global(input::placeholder) {
    color: var(--c-text-muted);
  }
  :global(select) {
    cursor: pointer;
  }
  :global(textarea) {
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border);
    color: var(--c-text);
    padding: 0.65rem;
    border-radius: var(--radius);
    font-family: 'SF Mono', 'Fira Code', ui-monospace, monospace;
    font-size: 0.75rem;
    width: 100%;
    resize: vertical;
    outline: none;
    transition: border-color var(--transition);
  }
  :global(textarea:focus) {
    border-color: var(--c-accent);
  }

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
  :global(.stat:hover) {
    border-color: var(--c-border-strong);
  }
  :global(.stat .stat-icon) {
    width: 32px;
    height: 32px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: var(--radius);
    margin-bottom: 0.75rem;
  }
  :global(.stat .stat-icon svg) {
    width: 16px;
    height: 16px;
  }
  :global(.stat .stat-icon.blue) {
    background: var(--c-accent-bg);
    color: #93c5fd;
  }
  :global(.stat .stat-icon.green) {
    background: var(--c-success-bg);
    color: #4ade80;
  }
  :global(.stat .stat-icon.purple) {
    background: var(--c-purple-bg);
    color: #c4b5fd;
  }
  :global(.stat .stat-icon.orange) {
    background: var(--c-warning-bg);
    color: #fbbf24;
  }
  :global(.stat .label) {
    font-size: 0.65rem;
    color: var(--c-text-muted);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-weight: 600;
    margin-bottom: 0.25rem;
  }
  :global(.stat .value) {
    font-size: 1.25rem;
    font-weight: 700;
    color: var(--c-text-heading);
    letter-spacing: -0.02em;
  }

  /* ── Loading ── */
  :global(.loading-skeleton) {
    background: linear-gradient(
      90deg,
      var(--c-bg-surface) 25%,
      var(--c-bg-hover) 50%,
      var(--c-bg-surface) 75%
    );
    background-size: 200% 100%;
    animation: shimmer 1.5s infinite;
    border-radius: var(--radius);
  }
  @keyframes shimmer {
    0% {
      background-position: 200% 0;
    }
    100% {
      background-position: -200% 0;
    }
  }

  :global(.spinner) {
    width: 18px;
    height: 18px;
    border: 2px solid var(--c-border);
    border-top-color: var(--c-accent);
    border-radius: 50%;
    animation: spin 0.6s linear infinite;
    display: inline-block;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  /* ── Password eye toggle ── */
  :global(.eye-btn) {
    position: absolute;
    right: 4px;
    top: 50%;
    transform: translateY(-50%);
    background: none;
    border: none;
    cursor: pointer;
    padding: 4px;
    line-height: 0;
    color: var(--c-text-muted);
    border-radius: 4px;
  }
  :global(.eye-btn:hover) {
    color: var(--c-text);
  }
  :global(.eye-icon) {
    width: 16px;
    height: 16px;
  }

  /* ── Empty state ── */
  :global(.empty-state) {
    text-align: center;
    padding: 2rem 1rem;
    color: var(--c-text-muted);
    font-size: 0.85rem;
  }

  /* ── Mobile topbar ── */
  .mobile-topbar {
    display: none;
    align-items: center;
    gap: 0.75rem;
    padding: 0.5rem 0.75rem;
    background: var(--c-bg);
    border-bottom: 1px solid var(--c-border);
    position: sticky;
    top: 0;
    z-index: 15;
  }
  .hamburger-btn {
    background: none;
    border: none;
    color: var(--c-text-secondary);
    cursor: pointer;
    padding: 0.25rem;
    border-radius: var(--radius-sm);
    display: flex;
  }
  .hamburger-btn:hover { color: var(--c-text); background: var(--c-bg-hover); }
  .topbar-title {
    font-size: 0.9rem;
    font-weight: 700;
    color: var(--c-text-heading);
  }
  .mobile-overlay {
    display: none;
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: 19;
  }

  /* ── Auth error banner ── */
  .auth-error-banner {
    display: flex;
    align-items: center;
    gap: 1rem;
    padding: 0.75rem 1.25rem;
    background: var(--c-danger-bg);
    border-bottom: 1px solid var(--c-danger);
    color: var(--c-danger);
    font-size: 0.8rem;
  }
  .auth-error-text {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    flex: 1;
    min-width: 0;
  }
  .auth-error-text strong {
    color: var(--c-text-heading);
    font-weight: 600;
  }
  .auth-error-text span {
    color: var(--c-text-secondary);
    font-size: 0.75rem;
    font-family: 'SF Mono', 'Fira Code', ui-monospace, monospace;
    word-break: break-word;
  }
  .auth-error-actions {
    flex-shrink: 0;
  }

  /* ── Responsive ── */
  @media (max-width: 768px) {
    .mobile-topbar { display: flex; }
    .mobile-overlay { display: block; }

    :global(.sidebar) {
      width: var(--sidebar-width);
      transform: translateX(-100%);
      transition: transform 0.2s ease;
      z-index: 25;
    }
    :global(.sidebar.mobile-open) {
      transform: translateX(0);
    }
    :global(.sidebar) {
      width: var(--sidebar-width) !important;
    }
    :global(.sidebar .nav-item) {
      justify-content: flex-start;
      padding: 0.5rem 0.75rem;
    }
    :global(.sidebar .brand-text),
    :global(.sidebar .nav-label),
    :global(.sidebar .nav-section-label),
    :global(.sidebar .user-info) {
      display: block;
    }
    :global(.sidebar .sidebar-toggle) {
      display: flex;
    }
    :global(.sidebar .lang-toggle) {
      display: flex;
    }
    :global(.sidebar.collapsed) {
      width: var(--sidebar-width) !important;
    }
    :global(.sidebar.collapsed .brand-text),
    :global(.sidebar.collapsed .nav-label),
    :global(.sidebar.collapsed .nav-section-label),
    :global(.sidebar.collapsed .user-info) {
      display: block;
    }
    :global(.sidebar.collapsed .nav-item) {
      justify-content: flex-start;
      padding: 0.5rem 0.75rem;
    }
    :global(.sidebar.collapsed .sidebar-toggle) {
      display: flex;
    }
    :global(.sidebar.collapsed .lang-toggle) {
      display: flex;
    }

    .main {
      margin-left: 0 !important;
    }
    .main-scroll {
      padding: 1rem 0.75rem 2rem;
    }
  }

  @media (max-width: 1024px) and (min-width: 769px) {
    :global(.sidebar) {
      width: var(--sidebar-collapsed);
    }
    :global(.sidebar .brand-text),
    :global(.sidebar .nav-label),
    :global(.sidebar .nav-section-label),
    :global(.sidebar .user-info) {
      display: none;
    }
    :global(.sidebar .nav-item) {
      justify-content: center;
      padding: 0.5rem;
    }
    :global(.sidebar .lang-toggle) {
      display: none;
    }
    :global(.sidebar.collapsed) {
      width: var(--sidebar-width) !important;
    }
    :global(.sidebar.collapsed .brand-text),
    :global(.sidebar.collapsed .nav-label),
    :global(.sidebar.collapsed .nav-section-label),
    :global(.sidebar.collapsed .user-info) {
      display: block;
    }
    :global(.sidebar.collapsed .nav-item) {
      justify-content: flex-start;
      padding: 0.5rem 0.75rem;
    }
    :global(.sidebar.collapsed .lang-toggle) {
      display: flex;
    }
    .main {
      margin-left: var(--sidebar-collapsed) !important;
    }
    .main-scroll {
      padding: 1.5rem 1.5rem 2.5rem;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    :global(.page) {
      animation: none;
    }
    :global(.loading-skeleton) {
      animation: none;
    }
    :global(.spinner) {
      animation: none;
    }
    :global(.sidebar) {
      transition: none;
    }
    .main {
      transition: none;
    }
  }
</style>
