<script>
  import { onMount } from 'svelte';
  import { getBlockedIPs, blockIP, unblockIP } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import { toast } from '../lib/toast.svelte.js';
  import { formatDateTime } from '../lib/format.js';
  let entries = $state([]);
  let loading = $state(true);
  let newIP = $state(''),
    newReason = $state(''),
    newDuration = $state('86400');
  let blocking = $state(false);
  let ipError = $state('');

  // IPv4/IPv6 粗校验：提交前给出字段旁错误，而不是静默不响应或等后端报错
  function validateIP(value) {
    if (!value.trim()) return t('errIpRequired');
    if (!/^[0-9a-fA-F:.]+$/.test(value.trim())) return t('errIpFormat');
    return '';
  }

  onMount(refresh);

  async function refresh() {
    loading = true;
    try {
      const d = await getBlockedIPs();
      entries = d.entries || [];
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      loading = false;
    }
  }

  async function doBlock() {
    ipError = validateIP(newIP);
    if (ipError || blocking) return;
    blocking = true;
    try {
      const data = await blockIP(newIP.trim(), newReason, parseInt(newDuration, 10));
      // 用服务端回读的封禁详情展示真实状态（add 对已封禁 IP 是覆盖语义，
      // 响应反映的是实际生效的 kind/expires_at，而非请求参数）。
      const b = data?.block;
      if (b?.kind === 'permanent') {
        toast(t('ipBlockedPermanent', { ip: b.ip }), 'success');
      } else {
        toast(
          t('ipBlockedUntil', {
            ip: b.ip,
            until: b?.expires_at ? formatDateTime(b.expires_at) : '—',
          }),
          'success',
        );
      }
      newIP = '';
      newReason = '';
      await refresh();
    } catch (e) {
      // 按机器可读错误码分支提示；未知错误回退展示原始 message（含服务端文案）
      if (e.code === 'INVALID_DURATION') {
        toast(t('errInvalidDuration'), 'error');
      } else if (e.code === 'INVALID_IP') {
        toast(t('errIpFormat'), 'error');
      } else {
        toast(e.message, 'error');
      }
    } finally {
      blocking = false;
    }
  }

  async function doUnblock(ip) {
    if (!(await showConfirm(t('confirmUnblock', { ip })))) return;
    try {
      await unblockIP(ip);
      toast(t('ipUnblocked', { ip }), 'success');
      await refresh();
    } catch (e) {
      if (e.code === 'BLOCK_NOT_FOUND') {
        // 幂等场景（如 fail2ban 已自动解封 / 条目被清理）：提示而非报错，并刷新列表
        toast(t('errNotBlocked', { ip }), 'info');
        await refresh();
      } else {
        toast(e.message, 'error');
      }
    }
  }
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('security')}</h1>
  </div>

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
          class="input-w-sm"
          aria-invalid={ipError ? 'true' : undefined}
          aria-describedby={ipError ? 'block-ip-error' : undefined}
        />
        {#if ipError}
          <p class="field-error" id="block-ip-error" role="alert">{ipError}</p>
        {/if}
      </div>
      <div class="form-group form-group-grow">
        <label for="block-reason">{t('reason')}</label>
        <input
          id="block-reason"
          type="text"
          bind:value={newReason}
          placeholder={t('reasonPlaceholder')}
        />
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
      <button class="btn btn-danger" onclick={doBlock} disabled={blocking}>
        {blocking ? t('blocking') : t('blockIP')}
      </button>
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
      <div class="table-wrap">
        <table>
          <thead
            ><tr
              ><th>{t('ipAddress')}</th><th>{t('reason')}</th><th>{t('blockedAt')}</th><th
                >{t('expiresAt')}</th
              ><th>{t('actions')}</th></tr
            ></thead
          >
          <tbody>
            {#each entries as e (e.ip)}
              <tr>
                <td class="td-mono">{e.ip}</td>
                <td class="td-muted">{e.reason || '—'}</td>
                <td class="td-muted">{e.blocked_at ? formatDateTime(e.blocked_at) : '—'}</td>
                <td>
                  {#if e.expires_at}
                    <span class="td-muted">{formatDateTime(e.expires_at)}</span>
                  {:else}
                    <span class="badge badge-red">{t('permanent')}</span>
                  {/if}
                </td>
                <td>
                  <button class="btn btn-ghost btn-sm" onclick={() => doUnblock(e.ip)}
                    >{t('unblock')}</button
                  >
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else}
      <div class="empty-state">{t('noIpsBlocked')}</div>
    {/if}
  </div>
</div>
