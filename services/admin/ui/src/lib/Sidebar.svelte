<script>
  import { onMount } from 'svelte';
  import { navSections } from './routes.js';
  import { t, toggleLang, getLang } from './i18n.svelte.js';
  import { authProbeURL, getHealth } from './api/index.js';
  import { getIcon } from './icons.js';
  import { LOGOUT_PENDING_KEY, adminHref } from './constants.js';

  // sidebarCollapsed / mobileMenuOpen 由本组件写回父级（折叠切换、移动端
  // 导航后收起），必须 $bindable：普通 prop 的本地赋值不会同步到 App.svelte，
  // 导致布局 collapsed 类与全屏遮罩残留。
  let {
    currentPath,
    navigate,
    sidebarCollapsed = $bindable(false),
    userInfo,
    isAdmin,
    mobileMenuOpen = $bindable(false),
  } = $props();

  // 后端构建版本(workwork git describe):页脚展示,排障时报版本用。
  // 必须 $state:onMount 异步赋值发生在首次渲染之后,普通变量不触发更新,
  // 页脚会一直是空串。
  let backendVersion = $state('');
  onMount(async () => {
    try {
      const health = await getHealth();
      backendVersion = health?.version || '';
    } catch {
      backendVersion = '';
    }
  });

  function handleNav(path) {
    navigate(path);
    if (mobileMenuOpen !== undefined) mobileMenuOpen = false;
  }

  async function doLogout() {
    // Basic Auth 没有服务端会话，"登出"只能是尽力覆盖浏览器的凭证缓存：
    // 发一个携带错误凭证的同源请求，覆盖成功的浏览器在下一次导航时会重新
    // 要求登录。覆盖是否生效因浏览器而异——导航后 App.svelte 会检查
    // ww_logout_pending 标记，仍持有有效凭证时如实提示用户，而不是假装
    // 已登出（旧的 logout:logout@host userinfo 导航在现行浏览器上会被
    // 拦截而静默失效，且假凭证会写进历史记录）。
    sessionStorage.setItem(LOGOUT_PENDING_KEY, '1');
    try {
      await fetch(authProbeURL(), {
        headers: { Authorization: 'Basic ' + btoa(`logout:${Date.now()}`) },
        cache: 'no-store',
      });
    } catch {
      // 覆盖请求失败也继续导航
    }
    window.location.href = adminHref('/');
  }
</script>

<aside class="sidebar" class:collapsed={sidebarCollapsed} class:mobile-open={mobileMenuOpen}>
  <div class="sidebar-header">
    <!-- 折叠态 logo 即「展开」入口：hover 浮现箭头覆盖层，2 合 1；
         展开态退化为纯 logo（点击无操作）。 -->
    <button
      class="brand-launcher"
      class:clickable={sidebarCollapsed}
      onclick={() => {
        if (sidebarCollapsed) sidebarCollapsed = false;
      }}
      title={sidebarCollapsed ? t('expandSidebar') : 'WorkWork'}
      aria-label={sidebarCollapsed ? t('expandSidebar') : 'WorkWork'}
    >
      <span class="brand-icon">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"
          ><path d="M12 2L2 7l10 5 10-5-10-5z" /><path d="M2 17l10 5 10-5" /><path
            d="M2 12l10 5 10-5"
          /></svg
        >
      </span>
      <span class="brand-launcher-hover" aria-hidden="true">
        <svg
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2.2"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <polyline points="9 18 15 12 9 6" />
        </svg>
      </span>
    </button>
    <div class="brand-text">
      <span class="brand-title">WorkWork</span>
      <span class="brand-sub">{t('adminPanel')}</span>
    </div>
    <button
      class="lang-toggle"
      onclick={toggleLang}
      title={getLang() === 'zh' ? 'Switch to English' : '切换到中文'}
      >{getLang() === 'zh' ? 'EN' : '中'}</button
    ><button
      class="sidebar-toggle"
      onclick={() => (sidebarCollapsed = !sidebarCollapsed)}
      title={sidebarCollapsed ? t('expandSidebar') : t('collapseSidebar')}
      aria-label={sidebarCollapsed ? t('expandSidebar') : t('collapseSidebar')}
    >
      <svg
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      >
        {#if sidebarCollapsed}
          <polyline points="9 18 15 12 9 6" />
        {:else}
          <polyline points="15 18 9 12 15 6" />
        {/if}
      </svg>
    </button>
  </div>

  <nav class="sidebar-nav">
    {#each navSections as section (section.key)}
      {#if section.items.filter((item) => !item.adminOnly || isAdmin).length > 0}
        <div class="nav-section">
          {#if !sidebarCollapsed}
            <div class="nav-section-label">{t(section.key)}</div>
          {/if}
          {#each section.items as item (item.path)}
            {#if !item.adminOnly || isAdmin}
              <button
                class="nav-item"
                class:active={currentPath === item.path}
                onclick={() => handleNav(item.path)}
                title={sidebarCollapsed ? t(item.key) : ''}
                aria-label={t(item.key)}
                aria-current={currentPath === item.path ? 'page' : undefined}
              >
                <svg
                  class="nav-icon"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="1.5"
                  ><!-- eslint-disable-next-line svelte/no-at-html-tags -- 内容为代码内常量(图标 SVG 路径表),非用户输入 -->
                  {@html getIcon(item.icon)}</svg
                >
                {#if !sidebarCollapsed}
                  <span class="nav-label">{t(item.key)}</span>
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
        <!-- 头像即「退出」入口：hover 浮现红色覆盖按钮，2 合 1 -->
        <span class="avatar-wrap">
          <span class="user-avatar" class:admin-avatar={userInfo.role === 'admin'}>
            {(userInfo.username || '?')[0].toUpperCase()}
          </span>
          <button
            class="logout-overlay"
            onclick={doLogout}
            title={t('logout')}
            aria-label={t('logout')}
          >
            <svg
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              width="15"
              height="15"
              ><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" /><polyline
                points="16 17 21 12 16 7"
              /><line x1="21" y1="12" x2="9" y2="12" /></svg
            >
          </button>
        </span>
        {#if !sidebarCollapsed}
          <div class="user-info">
            <div class="user-name">{userInfo.username}</div>
            <span class="user-role" class:role-admin={userInfo.role === 'admin'}
              >{userInfo.role}</span
            >
          </div>
        {/if}
      </div>
    {/if}
    {#if !sidebarCollapsed && backendVersion}
      <div class="build-version" title="后端构建版本">build {backendVersion}</div>
    {/if}
  </div>
</aside>
