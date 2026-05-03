<script>
import { link } from 'svelte-spa-router';

const nav = [
  { path: '/', label: 'Dashboard', icon: 'M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6' },
  { path: '/services', label: 'Services', icon: 'M4 6a2 2 0 012-2h12a2 2 0 012 2v12a2 2 0 01-2 2H6a2 2 0 01-2-2V6z' },
  { path: '/config', label: 'Config', icon: 'M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z' },
  { path: '/users', label: 'Users', icon: 'M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z' },
];
</script>

<nav class="nav">
  <div class="brand">Admin</div>
  <div class="links">
    {#each nav as item}
      <a href={item.path} use:link class="nav-link" class:active={$location.pathname === item.path}>
        <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d={item.icon} stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
        {item.label}
      </a>
    {/each}
  </div>
</nav>

<style>
  .nav { position: fixed; left: 0; top: 0; bottom: 0; width: 220px; background: #1e293b; border-right: 1px solid #334155; display: flex; flex-direction: column; padding: 1.5rem 0; z-index: 10; }
  .brand { font-size: 1.25rem; font-weight: 700; color: #f8fafc; padding: 0 1.5rem 2rem; letter-spacing: -0.02em; }
  .links { display: flex; flex-direction: column; gap: 0.25rem; }
  .nav-link { display: flex; align-items: center; gap: 0.75rem; padding: 0.625rem 1.5rem; color: #94a3b8; text-decoration: none; font-size: 0.875rem; transition: all 0.15s; border-left: 3px solid transparent; }
  .nav-link:hover { color: #f1f5f9; background: #334155; }
  .nav-link.active { color: #60a5fa; background: #1e3a5f; border-left-color: #60a5fa; }
  .icon { width: 1.25rem; height: 1.25rem; flex-shrink: 0; }
</style>
