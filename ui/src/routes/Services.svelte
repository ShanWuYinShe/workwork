<script>
  import { onMount } from 'svelte';
  import { getServices, serviceAction, getCurrentUser } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import { toast } from '../lib/toast.svelte.js';

  let services = $state([]);
  let loading = $state(true);
  let isAdmin = $state(false);
  let actingService = $state('');

  onMount(() => {
    refresh();
  });

  async function refresh() {
    loading = true;
    try {
      const [d, user] = await Promise.all([getServices(), getCurrentUser()]);
      services = d.services || [];
      isAdmin = user.role === 'admin';
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      loading = false;
    }
  }

  function confirmKey(act) {
    const map = { stop: 'confirmStopService', start: 'confirmStartService', restart: 'confirmRestartService' };
    return map[act] || 'confirmStopService';
  }

  async function act(name, action) {
    if (!(await showConfirm(t(confirmKey(action), { name })))) return;
    actingService = name;
    try {
      const d = await serviceAction(name, action);
      services = d.services || services;
      toast(t('serviceActionDone', { name, action: t(action) }), 'success', 2000);
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      actingService = '';
    }
  }

  let runningCount = $derived(services.filter((s) => s.state === 'running').length);
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('services')}</h1>
    <div class="page-actions">
      <button class="btn btn-sm btn-ghost" onclick={refresh} disabled={loading}>{t('refresh')}</button>
    </div>
  </div>

  {#if loading}
    <div class="loading-skeleton" style="height:200px;"></div>
  {:else}
    <div class="stats">
      <div class="stat">
        <div class="stat-icon blue">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"
            ><path
              d="M20.25 7.5l-.625 10.632a2.25 2.25 0 01-2.247 2.118H6.622a2.25 2.25 0 01-2.247-2.118L3.75 7.5M10 11.25h4M3.375 7.5h17.25c.621 0 1.125-.504 1.125-1.125v-1.5c0-.621-.504-1.125-1.125-1.125H3.375c-.621 0-1.125.504-1.125 1.125v1.5c0 .621.504 1.125 1.125 1.125Z"
            /></svg
          >
        </div>
        <div class="label">{t('total')}</div>
        <div class="value">{services.length}</div>
      </div>
      <div class="stat">
        <div class="stat-icon green">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"
            ><path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="M9 12.75L11.25 15 15 9.75M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
            /></svg
          >
        </div>
        <div class="label">{t('running')}</div>
        <div class="value">{runningCount}</div>
      </div>
      <div class="stat">
        <div class="stat-icon" style="background:rgba(239,68,68,0.1);color:#f87171;">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"
            ><path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126ZM12 15.75h.007v.008H12v-.008Z"
            /></svg
          >
        </div>
        <div class="label">{t('stopped')}</div>
        <div class="value">{services.length - runningCount}</div>
      </div>
    </div>

    <div class="card">
      <div class="card-header">
        <div class="card-title">{t('allServices')}</div>
      </div>
      {#if services.length}
        <table>
          <thead
            ><tr
              ><th>{t('service')}</th><th>{t('state')}</th><th>{t('ports')}</th><th>{t('image')}</th>{#if isAdmin}<th
                  >{t('serviceActions')}</th
                >{/if}</tr
            ></thead
          >
          <tbody>
            {#each services as s}
              <tr>
                <td style="font-weight:500;">{s.name}</td>
                <td>
                  <span class="badge badge-{s.state === 'running' ? 'green' : 'red'}">
                    <span class="badge-dot"></span>{s.state}
                  </span>
                </td>
                <td class="td-mono">{s.ports || '—'}</td>
                <td class="td-muted">{s.image}</td>
                {#if isAdmin}
                  <td>
                    <div class="td-actions">
                      {#if actingService === s.name}
                        <span class="spinner"></span>
                      {:else}
                        <button
                          class="btn btn-primary btn-sm"
                          onclick={() => act(s.name, 'restart')}>{t('restart')}</button
                        >
                        {#if s.state === 'running'}
                          <button class="btn btn-danger btn-sm" onclick={() => act(s.name, 'stop')}
                            >{t('stop')}</button
                          >
                        {:else}
                          <button
                            class="btn btn-success btn-sm"
                            onclick={() => act(s.name, 'start')}>{t('start')}</button
                          >
                        {/if}
                      {/if}
                    </div>
                  </td>
                {/if}
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}
        <div class="empty-state">{t('noServices')}</div>
      {/if}
    </div>
  {/if}
</div>
