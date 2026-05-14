<script>
  import { onMount } from 'svelte';
  import { getBlockedIPs, blockIP, unblockIP } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import Toast from '../lib/Toast.svelte';

  let entries = $state([]);
  let loading = $state(true);
  let error = $state('');
  let message = $state('');
  let newIP = $state(''),
    newReason = $state(''),
    newDuration = $state('86400');

  onMount(refresh);

  async function refresh() {
    loading = true;
    error = '';
    try {
      const d = await getBlockedIPs();
      entries = d.entries || [];
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  async function doBlock() {
    if (!newIP) return;
    error = '';
    message = '';
    try {
      await blockIP(newIP, newReason, parseInt(newDuration));
      message = t('ipBlocked', { ip: newIP });
      newIP = '';
      newReason = '';
      await refresh();
    } catch (e) {
      error = e.message;
    }
  }

  async function doUnblock(ip) {
    if (!(await showConfirm(t('confirmUnblock', { ip })))) return;
    error = '';
    message = '';
    try {
      await unblockIP(ip);
      message = t('ipUnblocked', { ip });
      await refresh();
    } catch (e) {
      error = e.message;
    }
  }
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('security')}</h1>
  </div>

  <Toast type="error" visible={!!error} onclose={() => error = ''}>{error}</Toast>
  <Toast type="success" visible={!!message} onclose={() => message = ''}>{message}</Toast>

  <div class="card">
    <div class="card-header">
      <div class="card-title">{t('blockIpTitle')}</div>
      <div class="card-subtitle">{t('blockIpDesc')}</div>
    </div>
    <div class="form-row">
      <div class="form-group">
        <label for="block-ip">{t('labelIPAddress')}</label>
        <input
          id="block-ip"
          type="text"
          bind:value={newIP}
          placeholder="1.2.3.4"
          style="width:160px;"
        />
      </div>
      <div class="form-group form-group-grow">
        <label for="block-reason">{t('reason')}</label>
        <input id="block-reason" type="text" bind:value={newReason} placeholder={t('reasonPlaceholder')} />
      </div>
      <div class="form-group">
        <label for="block-duration">{t('duration')}</label>
        <select id="block-duration" bind:value={newDuration}>
          <option value="3600">{t('oneHour')}</option>
          <option value="86400">{t('twentyFourHours')}</option>
          <option value="604800">{t('sevenDays')}</option>
          <option value="0">{t('forever')}</option>
        </select>
      </div>
      <button class="btn btn-danger" onclick={doBlock}>{t('blockIP')}</button>
    </div>
  </div>

  <div class="card">
    <div class="card-header">
      <div class="card-title">
        {t('blockedIPs')}
        {#if entries.length}<span class="card-title-count">{entries.length}</span>{/if}
      </div>
      <div class="card-actions">
        <button class="btn btn-sm btn-ghost" onclick={refresh}>{t('refresh')}</button>
      </div>
    </div>
    {#if loading}
      <div class="loading-skeleton" style="height:100px;"></div>
    {:else if entries.length}
      <table>
        <thead
          ><tr><th>{t('ipAddress')}</th><th>{t('reason')}</th><th>{t('blockedAt')}</th><th>{t('expiresAt')}</th><th>{t('actions')}</th></tr
          ></thead
        >
        <tbody>
          {#each entries as e}
            <tr>
              <td class="td-mono">{e.ip}</td>
              <td class="td-muted">{e.reason || '—'}</td>
              <td class="td-muted">{e.blocked_at?.slice(0, 16) || '—'}</td>
              <td>
                {#if e.expires_at}
                  <span class="td-muted">{e.expires_at.slice(0, 16)}</span>
                {:else}
                  <span class="badge badge-red">{t('permanent')}</span>
                {/if}
              </td>
              <td>
                <button class="btn btn-ghost btn-sm" onclick={() => doUnblock(e.ip)}>{t('unblock')}</button
                >
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {:else}
      <div class="empty-state">{t('noIpsBlocked')}</div>
    {/if}
  </div>
</div>
