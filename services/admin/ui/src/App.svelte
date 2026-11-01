<script>
  import { onMount } from 'svelte';
  import { getCurrentUser } from './lib/api.js';
  import { t } from './lib/i18n.svelte.js';
  import { toast } from './lib/toast.svelte.js';
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
      // 登出标记仍在且 /me 成功 = 浏览器没有清除凭证（覆盖失败），如实告知
      // 而不是假装已登出；认证失败的分支会清掉标记（用户即将重新登录）。
      if (sessionStorage.getItem('ww_logout_pending')) {
        sessionStorage.removeItem('ww_logout_pending');
        toast(t('logoutBlockedWarning'), 'error', 10000);
      }
    } catch (e) {
      // Don't silently swallow — surface the failure so the user can
      // retry or report a broken auth backend. Without this, an admin
      // who hits one bad response is permanently locked on the
      // forbidden screen.
      authError = e?.message || String(e);
      userInfo = null;
      sessionStorage.removeItem('ww_logout_pending');
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

  import './styles/app.css';
</script>

<div class="layout {sidebarCollapsed ? 'collapsed' : ''}">
  <Sidebar
    currentPath={nav.path}
    {navigate}
    bind:sidebarCollapsed
    {userInfo}
    {isAdmin}
    bind:mobileMenuOpen
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
    <div class="main-scroll" bind:this={mainScroll}>
      {#if authLoading}
        <!-- 认证未返回前不要按 isAdmin=false 渲染：管理员会先看到"无权限"
             或 viewer 版页面，随后又跳回正常内容。 -->
        <div class="page"><div class="loading-skeleton" style="height:120px;"></div></div>
      {:else if currentRoute}
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
