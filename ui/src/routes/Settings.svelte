<script>
  import { onMount } from 'svelte';
  import { getNetworkStatus, rotateIPv6, syncDNS } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';

  let net = $state({
    nginx_ip: '-',
    xray_ip: '-',
    subnet: '-',
    static_range: '-',
    rotating_range: '-',
    cf_configured: false,
    dns_reality: null,
    dns_nginx: null,
  });
  let loading = $state(true);
  let rotating = $state(false);
  let syncMsg = $state('');
  let result = $state({ ok: false, msg: '' });

  onMount(refresh);

  async function refresh() {
    loading = true;
    try {
      const ns = await getNetworkStatus();
      net = { ...net, ...ns };
    } catch (e) {
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
      syncMsg = t('syncFailed', { error: e.message });
    }
  }

  async function doRotate() {
    if (!confirm(t('rotateConfirm'))) return;
    rotating = true;
    result = { ok: false, msg: '' };
    try {
      const d = await rotateIPv6();
      result = { ok: true, msg: t('newIp', { ip: d.new_ip }) };
      await refresh();
    } catch (e) {
      result = { ok: false, msg: e.message };
    } finally {
      rotating = false;
    }
  }

  let realitySynced = $derived(net.dns_reality?.synced);
  let nginxSynced = $derived(net.dns_nginx?.synced);
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
        <thead><tr><th>{t('service')}</th><th>{t('ipv6Address')}</th><th>{t('roleCol')}</th><th>{t('dns')}</th></tr></thead>
        <tbody>
          <tr>
            <td style="font-weight:500;">nginx</td>
            <td class="td-mono">{net.nginx_ip}</td>
            <td class="td-muted">{t('webGateway')}</td>
            <td>
              {#if net.dns_nginx}
                <span class="badge badge-{nginxSynced ? 'green' : 'red'}">
                  <span class="badge-dot"></span>{nginxSynced ? t('synced') : t('mismatch')}
                </span>
              {:else}
                <span class="badge badge-red">{t('noDNS')}</span>
              {/if}
            </td>
          </tr>
          <tr>
            <td style="font-weight:500;">xray reality</td>
            <td class="td-mono" style="color:#93c5fd;">{net.xray_ip}</td>
            <td class="td-muted">{t('realityDesc')}</td>
            <td>
              {#if net.dns_reality}
                <span class="badge badge-{realitySynced ? 'green' : 'red'}">
                  <span class="badge-dot"></span>{realitySynced ? t('synced') : t('mismatch')}
                </span>
              {:else}
                <span class="badge badge-red">{t('noDNS')}</span>
              {/if}
            </td>
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
          {#if net.dns_nginx}
            <tr>
              <td style="font-weight:500;">{net.dns_nginx.domain}</td>
              <td class="td-mono">{net.dns_nginx.record?.content || '—'}</td>
              <td class="td-mono">{net.nginx_ip}</td>
              <td>
                <span class="badge badge-{nginxSynced ? 'green' : 'red'}">
                  <span class="badge-dot"></span>{nginxSynced ? t('synced') : t('mismatch')}
                </span>
              </td>
            </tr>
          {/if}
          {#if net.dns_reality}
            <tr>
              <td style="font-weight:500;">{net.dns_reality.domain}</td>
              <td class="td-mono">{net.dns_reality.record?.content || '—'}</td>
              <td class="td-mono" style="color:#93c5fd;">{net.xray_ip}</td>
              <td>
                <span class="badge badge-{realitySynced ? 'green' : 'red'}">
                  <span class="badge-dot"></span>{realitySynced ? t('synced') : t('mismatch')}
                </span>
              </td>
            </tr>
          {/if}
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
        {rotating ? t('rotating') : t('rotateReality')}
      </button>
      {#if result.msg}
        <div class="toast toast-{result.ok ? 'success' : 'error'}" style="margin:0;">
          {result.msg}
        </div>
      {/if}
    </div>
  </div>
</div>
