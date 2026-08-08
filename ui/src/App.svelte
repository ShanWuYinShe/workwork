<script>
  import { onMount } from 'svelte';
  import { getCurrentUser } from './lib/api.js';
  import { t } from './lib/i18n.svelte.js';
  import { routes, routeTitles } from './lib/routes.js';
  import { nav, navigate, syncPath } from './lib/nav.svelte.js';
  import Sidebar from './lib/Sidebar.svelte';
  import ConfirmModal from './lib/ConfirmModal.svelte';
  import Toast from './lib/Toast.svelte';

  let userInfo = $state(null);
  let authError = $state(null);
  let authLoading = $state(true);
  let sidebarCollapsed = $state(false);
  let mobileMenuOpen = $state(false);
  let mainScroll = $state(null);

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
    window.addEventListener('popstate', syncPath);
    return () => window.removeEventListener('popstate', syncPath);
  });

  $effect(() => {
    document.title = 'WorkWork — ' + t(routeTitles[nav.path] || 'dashboard');
  });
  // 路由切换后回到顶部：避免从长页面进入新页面仍停留在旧滚动位置。
  $effect(() => {
    nav.path;
    mainScroll?.scrollTo(0, 0);
  });

  let isAdmin = $derived(userInfo?.role === 'admin');

  let currentRoute = $derived(routes.find((r) => r.path === nav.path));
</script>

<div class="layout {sidebarCollapsed ? 'collapsed' : ''}">
  <Sidebar
    currentPath={nav.path}
    {navigate}
    {sidebarCollapsed}
    {userInfo}
    {isAdmin}
    {mobileMenuOpen}
  />

  {#if mobileMenuOpen}
    <button
      class="mobile-overlay"
      type="button"
      aria-label={t('closeMenu')}
      onclick={() => (mobileMenuOpen = false)}
    ></button>
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
      <button
        class="hamburger-btn"
        onclick={() => (mobileMenuOpen = !mobileMenuOpen)}
        aria-label={t('menu')}
      >
        <svg
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          width="20"
          height="20"
        >
          {#if mobileMenuOpen}
            <line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" />
          {:else}
            <line x1="4" y1="6" x2="20" y2="6" /><line x1="4" y1="12" x2="20" y2="12" /><line
              x1="4"
              y1="18"
              x2="20"
              y2="18"
            />
          {/if}
        </svg>
      </button>
      <span class="topbar-title">WorkWork</span>
    </div>
    <div
      class="main-scroll"
      class:main-scroll-wide={nav.path === '/media-center'}
      bind:this={mainScroll}
    >
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
    /* 背景层次 */
    --c-bg-deep: #05080f;
    --c-bg: #0a0f1a;
    --c-bg-elevated: #0f1525;
    --c-bg-surface: #141c30;
    --c-bg-hover: #1a2440;
    --c-border: rgba(148, 163, 184, 0.09);
    --c-border-strong: rgba(148, 163, 184, 0.18);

    /* 文字层次 */
    --c-text: #cbd5e1;
    --c-text-secondary: #7d8da8;
    --c-text-muted: #556584;
    --c-text-heading: #f1f5f9;
    --c-text-info: #93c5fd;
    --c-text-success: #4ade80;
    --c-text-danger: #f87171;
    --c-text-warning: #fbbf24;
    --c-text-purple: #c4b5fd;

    /* 语义色 */
    --c-accent: #3b82f6;
    --c-accent-hover: #2563eb;
    --c-accent-bg: rgba(59, 130, 246, 0.12);
    --c-accent-border: rgba(59, 130, 246, 0.32);
    --c-success: #22c55e;
    --c-success-hover: #16a34a;
    --c-success-bg: rgba(34, 197, 94, 0.1);
    --c-danger: #ef4444;
    --c-danger-hover: #dc2626;
    --c-danger-bg: rgba(239, 68, 68, 0.1);
    --c-warning: #f59e0b;
    --c-warning-bg: rgba(245, 158, 11, 0.1);
    --c-purple: #8b5cf6;
    --c-purple-bg: rgba(139, 92, 246, 0.12);

    /* 渐变 */
    --gradient-brand: linear-gradient(135deg, #2563eb, #7c3aed);
    --gradient-accent: linear-gradient(135deg, #3b82f6, #60a5fa);
    --gradient-success: linear-gradient(135deg, #16a34a, #4ade80);

    /* 阴影 */
    --shadow-sm: 0 1px 2px rgba(0, 0, 0, 0.25);
    --shadow-md: 0 4px 16px rgba(0, 0, 0, 0.28);
    --shadow-lg: 0 12px 32px rgba(0, 0, 0, 0.4);
    --shadow-glow: 0 0 0 1px rgba(59, 130, 246, 0.25), 0 4px 20px rgba(59, 130, 246, 0.12);

    /* 圆角 */
    --radius-sm: 6px;
    --radius: 8px;
    --radius-lg: 12px;
    --radius-xl: 16px;

    /* 字体 */
    --font-sans:
      -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Hiragino Sans GB',
      'Microsoft YaHei', 'Noto Sans SC', system-ui, sans-serif;
    --font-mono: 'SF Mono', 'Fira Code', ui-monospace, 'Cascadia Code', Consolas, monospace;

    /* 布局与动效 */
    --sidebar-width: 248px;
    --sidebar-collapsed: 64px;
    --transition: 0.2s cubic-bezier(0.25, 0.46, 0.45, 0.94);
    --focus-ring: 0 0 0 2px var(--c-bg), 0 0 0 4px var(--c-accent);
  }

  :global(*) {
    margin: 0;
    padding: 0;
    box-sizing: border-box;
  }
  :global(body) {
    font-family: var(--font-sans);
    background:
      radial-gradient(1200px 500px at 75% -10%, rgba(59, 130, 246, 0.06), transparent 60%),
      radial-gradient(900px 400px at -10% 110%, rgba(124, 58, 237, 0.05), transparent 60%),
      var(--c-bg-deep);
    background-attachment: fixed;
    color: var(--c-text);
    min-height: 100vh;
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
  }

  :global(*::-webkit-scrollbar) {
    width: 8px;
    height: 8px;
  }
  :global(*::-webkit-scrollbar-track) {
    background: transparent;
  }
  :global(*::-webkit-scrollbar-thumb) {
    background: var(--c-bg-hover);
    border-radius: 4px;
    border: 2px solid transparent;
    background-clip: content-box;
  }
  :global(*::-webkit-scrollbar-thumb:hover) {
    background: var(--c-text-muted);
    border: 2px solid transparent;
    background-clip: content-box;
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
    background: linear-gradient(180deg, rgba(15, 21, 37, 0.9), rgba(10, 15, 26, 0.98)), var(--c-bg);
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
    background: var(--gradient-brand);
    border-radius: var(--radius);
    color: #fff;
    flex-shrink: 0;
    box-shadow: 0 2px 8px rgba(99, 102, 241, 0.35);
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
    transition:
      color var(--transition),
      background var(--transition);
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
    padding: 0.75rem 0.75rem 0.3rem;
    font-size: 0.625rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.1em;
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
    text-decoration: none;
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
    outline: none;
    box-shadow: var(--focus-ring);
  }
  :global(.nav-item.active) {
    color: var(--c-text-info);
    background: linear-gradient(90deg, var(--c-accent-bg), rgba(59, 130, 246, 0.04));
    box-shadow: inset 0 1px 0 rgba(148, 163, 184, 0.05);
  }
  :global(.nav-item.active::before) {
    content: '';
    position: absolute;
    left: 0;
    top: 50%;
    transform: translateY(-50%);
    width: 3px;
    height: 18px;
    background: var(--gradient-accent);
    border-radius: 0 2px 2px 0;
    box-shadow: 0 0 8px rgba(59, 130, 246, 0.6);
  }
  :global(.nav-item.active .nav-icon) {
    color: var(--c-accent);
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
    border: 1px solid var(--c-border);
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
    background: var(--gradient-brand);
    color: #fff;
    box-shadow: 0 2px 6px rgba(99, 102, 241, 0.3);
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
    color: var(--c-text-info);
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
    /* flex 子项默认 min-width:auto，宽表格内容会把整页撑出横向滚动；
       允许收缩后，溢出交由内部 .table-wrap 等滚动容器处理。 */
    min-width: 0;
  }
  .layout.collapsed .main {
    margin-left: var(--sidebar-collapsed);
  }

  .main-scroll {
    flex: 1;
    padding: 2rem 2.5rem 3rem;
    max-width: 1200px;
    width: 100%;
    min-width: 0;
  }
  .main-scroll.main-scroll-wide {
    margin: 0 auto;
    max-width: 1480px;
  }

  /* ── Page ── */
  :global(.page) {
    animation: pageIn 0.25s cubic-bezier(0.25, 0.46, 0.45, 0.94);
  }
  @keyframes pageIn {
    from {
      opacity: 0;
      transform: translateY(8px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  :global(.page-header) {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 1.5rem;
    flex-wrap: wrap;
  }
  :global(.page-title) {
    font-size: 1.4rem;
    font-weight: 700;
    color: var(--c-text-heading);
    letter-spacing: -0.02em;
  }
  :global(.page-subtitle) {
    color: var(--c-text-secondary);
    font-size: 0.8rem;
    margin-top: 0.3rem;
  }
  :global(.page-actions) {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
  }

  /* ── Global Cards ── */
  :global(.card) {
    background: var(--c-bg-elevated);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-lg);
    padding: 1.25rem 1.5rem;
    margin-bottom: 1rem;
    box-shadow: var(--shadow-sm);
    position: relative;
    transition:
      border-color var(--transition),
      box-shadow var(--transition),
      transform var(--transition);
  }
  :global(.card::before) {
    content: '';
    position: absolute;
    inset: 0 0 auto 0;
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(148, 163, 184, 0.14), transparent);
    border-radius: var(--radius-lg) var(--radius-lg) 0 0;
    pointer-events: none;
  }
  :global(.card:hover) {
    border-color: var(--c-border-strong);
    box-shadow: var(--shadow-md);
  }
  :global(.card-compact) {
    padding: 0.75rem 1rem;
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
    font-size: 0.9rem;
    font-weight: 600;
    color: var(--c-text-heading);
    display: flex;
    align-items: center;
    gap: 0.5rem;
    letter-spacing: 0.01em;
  }
  :global(.card-title-count) {
    font-size: 0.68rem;
    font-weight: 600;
    color: var(--c-text-muted);
    background: var(--c-bg-surface);
    padding: 0.1rem 0.5rem;
    border-radius: 999px;
    margin-left: 0.25rem;
    border: 1px solid var(--c-border);
  }
  :global(.card-actions) {
    display: flex;
    align-items: center;
    gap: 0.375rem;
  }
  :global(.card-footer-actions) {
    margin-top: 0.75rem;
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
  :global(.table-wrap) {
    overflow-x: auto;
    -webkit-overflow-scrolling: touch;
  }
  :global(th) {
    text-align: left;
    padding: 0.55rem 0.75rem;
    font-size: 0.65rem;
    text-transform: uppercase;
    color: var(--c-text-muted);
    border-bottom: 1px solid var(--c-border-strong);
    letter-spacing: 0.08em;
    font-weight: 700;
    white-space: nowrap;
  }
  :global(td) {
    padding: 0.65rem 0.75rem;
    border-bottom: 1px solid var(--c-border);
    font-size: 0.8rem;
    vertical-align: middle;
  }
  :global(tr:hover td) {
    background: rgba(148, 163, 184, 0.035);
  }
  :global(tr:last-child td) {
    border-bottom: none;
  }
  :global(.td-mono) {
    font-family: var(--font-mono);
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
  :global(.cell-strong) {
    color: var(--c-text);
    font-weight: 500;
  }
  :global(.cell-ellipsis) {
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  :global(.text-accent) {
    color: var(--c-text-info);
  }
  :global(.text-danger) {
    color: var(--c-danger);
  }
  :global(.link) {
    color: var(--c-text-info);
    text-decoration: none;
    transition: color var(--transition);
  }
  :global(.link:hover) {
    color: #bfdbfe;
    text-decoration: underline;
    text-underline-offset: 3px;
  }

  /* ── Badges ── */
  :global(.badge) {
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
    padding: 0.15rem 0.55rem;
    border-radius: 999px;
    font-size: 0.68rem;
    font-weight: 600;
    letter-spacing: 0.02em;
    border: 1px solid transparent;
    line-height: 1.35;
    white-space: nowrap;
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
    color: var(--c-text-success);
    border-color: rgba(74, 222, 128, 0.14);
  }
  :global(.badge-red) {
    background: var(--c-danger-bg);
    color: var(--c-text-danger);
    border-color: rgba(248, 113, 113, 0.14);
  }
  :global(.badge-blue) {
    background: var(--c-accent-bg);
    color: var(--c-text-info);
    border-color: rgba(147, 197, 253, 0.14);
  }
  :global(.badge-purple) {
    background: var(--c-purple-bg);
    color: var(--c-text-purple);
    border-color: rgba(196, 181, 253, 0.14);
  }
  :global(.badge-gray) {
    background: rgba(148, 163, 184, 0.08);
    color: var(--c-text-secondary);
    border-color: var(--c-border);
  }
  :global(.badge-yellow) {
    background: var(--c-warning-bg);
    color: var(--c-text-warning);
    border-color: rgba(251, 191, 36, 0.14);
  }
  :global(.badge-action) {
    border: 1px solid var(--c-accent-border);
    cursor: pointer;
    font-family: inherit;
    transition:
      background var(--transition),
      border-color var(--transition),
      color var(--transition),
      transform var(--transition);
  }
  :global(.badge-action:hover) {
    background: rgba(59, 130, 246, 0.18);
    border-color: rgba(59, 130, 246, 0.45);
    color: #bfdbfe;
  }
  :global(.badge-action:active) {
    transform: scale(0.97);
  }
  :global(.badge-action:disabled) {
    cursor: not-allowed;
    opacity: 0.5;
  }
  :global(.badge-list) {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
  }
  :global(.badge-code) {
    font-family: var(--font-mono);
    font-size: 0.65rem;
  }
  :global(.badge-inline) {
    margin-left: 0.25rem;
  }
  :global(.badge-progress) {
    justify-content: center;
    min-width: 60px;
  }

  /* ── Buttons ── */
  :global(.btn) {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 0.3rem;
    padding: 0.42rem 0.85rem;
    border: 1px solid transparent;
    border-radius: var(--radius);
    appearance: none;
    font-family: inherit;
    font-size: 0.78rem;
    font-weight: 500;
    cursor: pointer;
    text-decoration: none;
    transition:
      background var(--transition),
      border-color var(--transition),
      color var(--transition),
      transform var(--transition),
      box-shadow var(--transition);
    white-space: nowrap;
    line-height: 1.4;
  }
  :global(.btn:hover) {
    text-decoration: none;
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
    outline: none;
    box-shadow: var(--focus-ring);
  }

  :global(.btn-primary) {
    background: linear-gradient(180deg, #4a90f9, var(--c-accent));
    color: #fff;
    box-shadow:
      0 1px 2px rgba(0, 0, 0, 0.2),
      inset 0 1px 0 rgba(255, 255, 255, 0.12);
  }
  :global(.btn-primary:hover) {
    background: linear-gradient(180deg, #3d86f7, var(--c-accent-hover));
    box-shadow:
      0 2px 8px rgba(59, 130, 246, 0.35),
      inset 0 1px 0 rgba(255, 255, 255, 0.12);
  }
  :global(.btn-danger) {
    background: linear-gradient(180deg, #f9625f, var(--c-danger));
    color: #fff;
    box-shadow:
      0 1px 2px rgba(0, 0, 0, 0.2),
      inset 0 1px 0 rgba(255, 255, 255, 0.12);
  }
  :global(.btn-danger:hover) {
    background: linear-gradient(180deg, #f64f4b, var(--c-danger-hover));
    box-shadow:
      0 2px 8px rgba(239, 68, 68, 0.3),
      inset 0 1px 0 rgba(255, 255, 255, 0.12);
  }
  :global(.btn-success) {
    background: linear-gradient(180deg, #3bd571, var(--c-success));
    color: #fff;
    box-shadow:
      0 1px 2px rgba(0, 0, 0, 0.2),
      inset 0 1px 0 rgba(255, 255, 255, 0.12);
  }
  :global(.btn-success:hover) {
    background: linear-gradient(180deg, #2fc763, var(--c-success-hover));
    box-shadow:
      0 2px 8px rgba(34, 197, 94, 0.3),
      inset 0 1px 0 rgba(255, 255, 255, 0.12);
  }
  :global(.btn-secondary) {
    background: var(--c-bg-surface);
    border-color: var(--c-border-strong);
    color: var(--c-text);
    box-shadow: inset 0 1px 0 rgba(148, 163, 184, 0.05);
  }
  :global(.btn-secondary:hover) {
    background: var(--c-bg-hover);
    border-color: var(--c-border-strong);
    box-shadow: 0 1px 4px rgba(0, 0, 0, 0.2);
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
    padding: 0.25rem 0.6rem;
    font-size: 0.72rem;
    border-radius: var(--radius-sm);
  }
  :global(.btn-xs) {
    padding: 0.15rem 0.45rem;
    font-size: 0.7rem;
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
  :global(.btn .spinner) {
    width: 0.85rem;
    height: 0.85rem;
    border-width: 2px;
  }

  /* ── Tabs ── */
  :global(.tab-bar) {
    display: flex;
    gap: 0.25rem;
    margin-bottom: 1.5rem;
    border-bottom: 1px solid var(--c-border);
  }
  :global(.tab-btn) {
    appearance: none;
    background: transparent;
    border: 0;
    border-bottom: 2px solid transparent;
    color: var(--c-text-muted);
    cursor: pointer;
    font-family: inherit;
    font-size: 0.8rem;
    font-weight: 500;
    margin-bottom: -1px;
    padding: 0.5rem 0.9rem;
    border-radius: var(--radius-sm) var(--radius-sm) 0 0;
    transition:
      border-color var(--transition),
      color var(--transition),
      background var(--transition);
  }
  :global(.tab-btn:hover) {
    color: var(--c-text);
    background: var(--c-bg-hover);
  }
  :global(.tab-btn.active) {
    color: var(--c-text-info);
    border-bottom-color: var(--c-accent);
    background: linear-gradient(180deg, var(--c-accent-bg), transparent);
  }
  :global(.tab-btn:focus-visible) {
    outline: none;
    box-shadow: var(--focus-ring);
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
  :global(.inline-actions) {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    flex-wrap: wrap;
  }
  :global(.inline-actions-sm) {
    gap: 0.5rem;
  }
  :global(.inline-field) {
    display: flex;
    align-items: center;
    gap: 0.35rem;
  }
  :global(.inline-field input),
  :global(.inline-field select) {
    width: auto;
  }
  :global(.inline-label) {
    color: var(--c-text-muted);
    font-size: 0.75rem;
    margin: 0;
    text-transform: none;
    letter-spacing: 0;
    white-space: nowrap;
  }

  :global(label) {
    display: block;
    font-size: 0.65rem;
    color: var(--c-text-muted);
    margin-bottom: 0.3rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  :global(input),
  :global(select) {
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border-strong);
    color: var(--c-text);
    font-family: inherit;
    padding: 0.45rem 0.65rem;
    border-radius: var(--radius);
    font-size: 0.8rem;
    outline: none;
    transition:
      border-color var(--transition),
      box-shadow var(--transition),
      background var(--transition);
    width: 100%;
  }
  :global(input:hover),
  :global(select:hover) {
    border-color: var(--c-border-strong);
    background: var(--c-bg-hover);
  }
  :global(input:focus),
  :global(select:focus) {
    border-color: var(--c-accent);
    background: var(--c-bg-surface);
    box-shadow: 0 0 0 3px var(--c-accent-bg);
  }
  :global(input::placeholder) {
    color: var(--c-text-muted);
  }
  :global(select) {
    cursor: pointer;
  }
  :global(.input-compact) {
    font-size: 0.75rem;
    padding: 0.28rem 0.5rem;
  }
  :global(.input-w-sm) {
    max-width: 100%;
    width: 160px;
  }
  :global(.input-w-md) {
    max-width: 100%;
    width: 180px;
  }
  :global(.password-field) {
    position: relative;
  }
  :global(.password-input) {
    position: relative;
  }
  :global(.password-input input) {
    padding-right: 2rem;
  }
  :global(textarea) {
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border-strong);
    color: var(--c-text);
    padding: 0.65rem;
    border-radius: var(--radius);
    font-family: var(--font-mono);
    font-size: 0.75rem;
    width: 100%;
    resize: vertical;
    outline: none;
    transition:
      border-color var(--transition),
      box-shadow var(--transition);
  }
  :global(textarea:hover) {
    border-color: var(--c-border-strong);
    background: var(--c-bg-hover);
  }
  :global(textarea:focus) {
    border-color: var(--c-accent);
    background: var(--c-bg-surface);
    box-shadow: 0 0 0 3px var(--c-accent-bg);
  }

  /* ── Shared content utilities ── */
  :global(.helper-text),
  :global(.muted-text) {
    color: var(--c-text-muted);
    font-size: 0.75rem;
  }
  :global(.section-copy) {
    color: var(--c-text-secondary);
    font-size: 0.78rem;
    line-height: 1.6;
    margin-bottom: 1rem;
  }
  :global(.metric-row) {
    align-items: center;
    color: var(--c-text-muted);
    display: flex;
    font-size: 0.75rem;
    justify-content: space-between;
    margin-bottom: 0.35rem;
  }
  :global(.progress-bar) {
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border);
    border-radius: 999px;
    height: 6px;
    overflow: hidden;
  }
  :global(.progress-bar-thin) {
    height: 4px;
  }
  :global(.progress-bar-sm) {
    width: 80px;
  }
  :global(.progress-fill) {
    background: var(--gradient-accent);
    border-radius: inherit;
    height: 100%;
    transition: width 0.2s ease;
    box-shadow: 0 0 6px rgba(59, 130, 246, 0.4);
  }
  :global(.progress-caption) {
    color: var(--c-text-muted);
    font-size: 0.6rem;
    margin-top: 1px;
  }
  :global(.drop-zone) {
    border: 2px dashed var(--c-border-strong);
    border-radius: var(--radius-lg);
    transition:
      background var(--transition),
      border-color var(--transition),
      box-shadow var(--transition);
  }
  :global(.drop-zone:hover),
  :global(.drop-zone.drag-over) {
    border-color: var(--c-accent);
    background: var(--c-accent-bg);
    box-shadow: var(--shadow-glow);
  }
  :global(.drop-zone-content) {
    align-items: center;
    color: var(--c-text-muted);
    cursor: pointer;
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding: 1.5rem;
    text-transform: none;
    letter-spacing: 0;
    margin: 0;
  }
  :global(.drop-zone-icon) {
    color: var(--c-text-muted);
    height: 2rem;
    width: 2rem;
  }
  :global(.drop-zone-text) {
    color: var(--c-text-muted);
    font-size: 0.8rem;
    text-align: center;
  }
  :global(.file-input-hidden) {
    display: none;
  }
  :global(.user-line) {
    align-items: center;
    display: flex;
    gap: 0.5rem;
  }
  :global(.table-avatar) {
    align-items: center;
    background: var(--c-bg-surface);
    border-radius: var(--radius-sm);
    color: var(--c-text-secondary);
    display: flex;
    font-size: 0.7rem;
    font-weight: 700;
    height: 28px;
    justify-content: center;
    width: 28px;
  }
  :global(.table-avatar-admin) {
    background: var(--gradient-brand);
    color: #fff;
  }
  :global(.config-badges) {
    margin-bottom: 0.75rem;
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
    box-shadow: var(--shadow-sm);
    position: relative;
    overflow: hidden;
    transition:
      border-color var(--transition),
      box-shadow var(--transition),
      transform var(--transition);
  }
  :global(.stat:hover) {
    border-color: var(--c-border-strong);
    box-shadow: var(--shadow-md);
    transform: translateY(-1px);
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
    color: var(--c-text-info);
  }
  :global(.stat .stat-icon.green) {
    background: var(--c-success-bg);
    color: var(--c-text-success);
  }
  :global(.stat .stat-icon.purple) {
    background: var(--c-purple-bg);
    color: var(--c-text-purple);
  }
  :global(.stat .stat-icon.orange) {
    background: var(--c-warning-bg);
    color: var(--c-text-warning);
  }
  :global(.stat .stat-icon.red) {
    background: var(--c-danger-bg);
    color: var(--c-text-danger);
  }
  :global(.stat .label) {
    font-size: 0.65rem;
    color: var(--c-text-muted);
    text-transform: uppercase;
    letter-spacing: 0.07em;
    font-weight: 600;
    margin-bottom: 0.25rem;
  }
  :global(.stat .value) {
    font-size: 1.35rem;
    font-weight: 700;
    color: var(--c-text-heading);
    letter-spacing: -0.02em;
    font-variant-numeric: tabular-nums;
  }
  :global(.stat .value-sm) {
    font-size: 0.9rem;
  }
  :global(.stat .value-xs) {
    font-size: 0.85rem;
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
  :global(.spinner-xs) {
    width: 10px;
    height: 10px;
    border-width: 2px;
  }
  :global(.spinner-inline) {
    margin-left: 0.5rem;
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
    padding: 2.5rem 1rem;
    color: var(--c-text-muted);
    font-size: 0.85rem;
  }
  :global(.empty-state-title) {
    margin-bottom: 0.5rem;
    font-weight: 600;
    color: var(--c-text-secondary);
  }
  :global(.empty-state-copy) {
    color: var(--c-text-secondary);
    font-size: 0.78rem;
  }

  /* ── Mobile topbar ── */
  .mobile-topbar {
    display: none;
    align-items: center;
    gap: 0.75rem;
    padding: 0.5rem 0.75rem;
    background: rgba(10, 15, 26, 0.85);
    backdrop-filter: blur(8px);
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
  .hamburger-btn:hover {
    color: var(--c-text);
    background: var(--c-bg-hover);
  }
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
    border: 0;
    padding: 0;
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
    font-family: var(--font-mono);
    word-break: break-word;
  }
  .auth-error-actions {
    flex-shrink: 0;
  }

  /* ── Responsive ── */
  @media (max-width: 768px) {
    .mobile-topbar {
      display: flex;
    }
    .mobile-overlay {
      display: block;
    }

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
