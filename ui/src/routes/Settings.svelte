<script>
  import { onMount } from 'svelte';
  import { getNetworkStatus, rotateIPv6, syncDNS } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import { toast } from '../lib/toast.svelte.js';

  let net = $state({
    nginx_ip: '-',
    xray_ip: '-',
    nginx_rotating: null,
    subnet: '-',
    static_range: '-',
    rotating_range: '-',
    cf_configured: false,
    dns_records: [],
  });
  let loading = $state(true);
  let rotating = $state(false);
  let syncMsg = $state('');
  let result = $state({ ok: false, msg: '' });

  onMount(refresh);

  async function refresh() {
    loading = true;
    syncMsg = '';
    try {
      const ns = await getNetworkStatus();
      net = { ...net, ...ns };
    } catch (e) {
      result = { ok: false, msg: e.message };
    } finally {
      loading = false;
    }
  }

  async function doSyncDNS() {
    syncMsg = t('syncing');
    try {
      const d = await syncDNS();
      syncMsg = d.results.join('; ');
      await refresh();
    } catch (e) {
      toast(t('syncFailed', { error: e.message }), 'error');
    }
  }

  async function doRotate() {
    if (!(await showConfirm(t('rotateConfirm')))) return;
    rotating = true;
    result = { ok: false, msg: '' };
    try {
      const d = await rotateIPv6();
      result = { ok: true, msg: t('rotateResult', { xray: d.xray_ip, nginx: d.nginx_ip }) };
      await refresh();
    } catch (e) {
      result = { ok: false, msg: e.message };
    } finally {
      rotating = false;
    }
  }
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('settings')}</h1>
    <div class="page-actions">
      <button class="btn btn-sm btn-ghost" onclick={refresh}>{t('refresh')}</button>
    </div>
  </div>

  {#if loading}
    <div class="card"><div class="loading-skeleton" style="height:150px;"></div></div>
    <div class="card"><div class="loading-skeleton" style="height:100px;"></div></div>
    <div class="card"><div class="loading-skeleton" style="height:80px;"></div></div>
  {:else}
    <div class="card">
      <div class="card-header">
        <div class="card-title">{t('externalServices')}</div>
        <div class="card-subtitle">{t('publicIPv6')}</div>
      </div>
      <table>
        <thead><tr><th>{t('service')}</th><th>{t('staticIP')}</th><th>{t('rotatingIP')}</th><th>{t('roleCol')}</th></tr></thead>
        <tbody>
          <tr>
            <td style="font-weight:500;">nginx (xhttp)</td>
            <td class="td-mono">{net.nginx_ip}</td>
            <td class="td-mono" style="color:#93c5fd;">{net.nginx_rotating || '—'}</td>
            <td class="td-muted">{t('webGateway')}</td>
          </tr>
          <tr>
            <td style="font-weight:500;">xray (reality)</td>
            <td class="td-mono">{net.xray_ip}</td>
            <td class="td-mono" style="color:#93c5fd;">{net.xray_ip}</td>
            <td class="td-muted">{t('realityDesc')}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="card">
      <div class="card-header">
        <div class="card-title">{t('ipv6Allocation')}</div>
        <div class="card-subtitle">{net.subnet}</div>
      </div>
      <table>
        <thead><tr><th>{t('range')}</th><th>{t('tablePrefix')}</th><th>{t('tableUsage')}</th></tr></thead>
        <tbody>
          <tr>
            <td><span class="badge badge-green">{t('staticRange')}</span></td>
            <td class="td-mono">{net.static_range}</td>
            <td class="td-muted">{t('staticRangeUsage')}</td>
          </tr>
          <tr>
            <td><span class="badge badge-blue">{t('rotatingRange')}</span></td>
            <td class="td-mono">{net.rotating_range}</td>
            <td class="td-muted">{t('rotatingRangeUsage')}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="card">
      <div class="card-header">
        <div class="card-title">{t('internalServices')}</div>
        <div class="card-subtitle">{t('internalDocker')}</div>
      </div>
      <table>
        <thead><tr><th>{t('service')}</th><th>{t('port')}</th><th>{t('access')}</th></tr></thead>
        <tbody>
          <tr
            ><td style="font-weight:500;">admin</td><td class="td-mono">8318</td><td
              class="td-muted">{t('internalDockerViaNginx')}</td
            ></tr
          >
          <tr
            ><td style="font-weight:500;">xray xhttp</td><td class="td-mono">8447</td><td
              class="td-muted">{t('internalDockerViaProxyQuery')}</td
            ></tr
          >
        </tbody>
      </table>
    </div>
  {/if}

  <div class="card">
    <div class="card-header">
      <div class="card-title">{t('dnsSync')}</div>
    </div>
    {#if !loading}
      <table>
        <thead><tr><th>{t('domain')}</th><th>{t('dnsRecord')}</th><th>{t('expected')}</th><th>{t('status')}</th></tr></thead>
        <tbody>
          {#each net.dns_records as dns}
            <tr>
              <td style="font-weight:500;">{dns.domain}</td>
              <td class="td-mono">{dns.record?.content || '—'}</td>
              <td class="td-mono">{dns.record?.content || '—'}</td>
              <td>
                {#if dns.error}
                  <span class="badge badge-red">{dns.error}</span>
                {:else if dns.synced}
                  <span class="badge badge-green"><span class="badge-dot"></span>{t('synced')}</span>
                {:else}
                  <span class="badge badge-red"><span class="badge-dot"></span>{t('mismatch')}</span>
                {/if}
              </td>
            </tr>
          {:else}
            <tr><td colspan="4" class="td-muted">{t('noDNS')}</td></tr>
          {/each}
        </tbody>
      </table>
    {/if}
    <div style="display:flex;align-items:center;gap:0.75rem;margin-top:0.75rem;flex-wrap:wrap;">
      <button class="btn btn-sm btn-primary" onclick={doSyncDNS}>{t('syncAllDNS')}</button>
      {#if syncMsg}<span style="font-size:0.78rem;color:var(--c-text-secondary);">{syncMsg}</span
        >{/if}
    </div>
  </div>

  <div class="card">
    <div class="card-header">
      <div class="card-title">{t('ipv6Rotation')}</div>
    </div>
    <p style="color:var(--c-text-secondary);font-size:0.78rem;margin-bottom:1rem;line-height:1.6;">
      {t('rotateDesc', { range: net.rotating_range })}
    </p>
    <div style="display:flex;align-items:center;gap:0.75rem;flex-wrap:wrap;">
      <button class="btn btn-primary" onclick={doRotate} disabled={rotating}>
        {#if rotating}<span class="spinner" style="margin-right:0.35rem;"></span>{/if}
        {rotating ? t('rotating') : t('rotateAll')}
      </button>
      {#if result.msg}
        <Toast type={result.ok ? 'success' : 'error'} visible={!!result.msg} onclose={() => result = { ok: false, msg: '' }}>
          {result.msg}
        </Toast>
      {/if}
    </div>
  </div>
</div>
