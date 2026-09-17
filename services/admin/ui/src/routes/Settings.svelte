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
  let syncing = $state(false);
  let syncMsg = $state('');

  onMount(refresh);

  async function refresh() {
    loading = true;
    syncMsg = '';
    try {
      const ns = await getNetworkStatus();
      net = { ...net, ...ns };
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      loading = false;
    }
  }

  async function doSyncDNS() {
    syncing = true;
    syncMsg = t('syncing');
    try {
      const d = await syncDNS();
      syncMsg = d.results.join('; ');
      await refresh();
    } catch (e) {
      toast(t('syncFailed', { error: e.message }), 'error');
    } finally {
      syncing = false;
    }
  }

  async function doRotate() {
    if (!(await showConfirm(t('rotateConfirm'), { danger: true }))) return;
    rotating = true;
    try {
      const d = await rotateIPv6();
      toast(t('rotateResult', { xray: d.xray_ip, nginx: d.nginx_ip }), 'success');
      await refresh();
    } catch (e) {
      toast(e.message, 'error');
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
      <div class="table-wrap">
        <table>
          <thead
            ><tr
              ><th>{t('service')}</th><th>{t('staticIP')}</th><th>{t('rotatingIP')}</th><th
                >{t('roleCol')}</th
              ></tr
            ></thead
          >
          <tbody>
            <tr>
              <td class="cell-strong">nginx (xhttp)</td>
              <td class="td-mono">{net.nginx_ip}</td>
              <td class="td-mono text-accent">{net.nginx_rotating || '—'}</td>
              <td class="td-muted">{t('webGateway')}</td>
            </tr>
            <tr>
              <td class="cell-strong">xray (reality)</td>
              <td class="td-mono">{net.xray_static || '—'}</td>
              <td class="td-mono text-accent">{net.xray_ip}</td>
              <td class="td-muted">{t('realityDesc')}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <div class="card">
      <div class="card-header">
        <div class="card-title">{t('ipv6Allocation')}</div>
        <div class="card-subtitle">{net.subnet}</div>
      </div>
      <div class="table-wrap">
        <table>
          <thead
            ><tr><th>{t('range')}</th><th>{t('tablePrefix')}</th><th>{t('tableUsage')}</th></tr
            ></thead
          >
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
    </div>

    <div class="card">
      <div class="card-header">
        <div class="card-title">{t('internalServices')}</div>
        <div class="card-subtitle">{t('internalDocker')}</div>
      </div>
      <div class="table-wrap">
        <table>
          <thead><tr><th>{t('service')}</th><th>{t('port')}</th><th>{t('access')}</th></tr></thead>
          <tbody>
            <tr
              ><td class="cell-strong">admin</td><td class="td-mono">8318</td><td class="td-muted"
                >{t('internalDockerViaNginx')}</td
              ></tr
            >
            <tr
              ><td class="cell-strong">xray xhttp</td><td class="td-mono">8447</td><td
                class="td-muted">{t('internalDockerViaProxyQuery')}</td
              ></tr
            >
          </tbody>
        </table>
      </div>
    </div>
  {/if}

  <div class="card">
    <div class="card-header">
      <div class="card-title">{t('dnsSync')}</div>
    </div>
    {#if !loading}
      <div class="table-wrap">
        <table>
          <thead
            ><tr
              ><th>{t('domain')}</th><th>{t('dnsRecord')}</th><th>{t('expected')}</th><th
                >{t('status')}</th
              ></tr
            ></thead
          >
          <tbody>
            {#each net.dns_records as dns (dns.domain)}
              <tr>
                <td class="cell-strong">{dns.domain}</td>
                <td class="td-mono">{dns.record?.content || '—'}</td>
                <td class="td-mono">{dns.expected_ip || '—'}</td>
                <td>
                  {#if dns.error}
                    <span class="badge badge-red">{dns.error}</span>
                  {:else if dns.synced}
                    <span class="badge badge-green"
                      ><span class="badge-dot"></span>{t('synced')}</span
                    >
                  {:else}
                    <span class="badge badge-red"
                      ><span class="badge-dot"></span>{t('mismatch')}</span
                    >
                  {/if}
                </td>
              </tr>
            {:else}
              <tr><td colspan="4" class="td-muted">{t('noDNS')}</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
    <div class="inline-actions card-footer-actions">
      <button
        class="btn btn-sm btn-primary"
        onclick={doSyncDNS}
        disabled={syncing || loading || !net.cf_configured}
      >
        {#if syncing}<span class="spinner"></span>{/if}
        {t('syncAllDNS')}
      </button>
      {#if syncMsg}<span class="helper-text">{syncMsg}</span>{/if}
    </div>
  </div>

  <div class="card">
    <div class="card-header">
      <div class="card-title">{t('ipv6Rotation')}</div>
    </div>
    <p class="section-copy">
      {t('rotateDesc', { range: net.rotating_range })}
    </p>
    <div class="inline-actions">
      <button
        class="btn btn-primary"
        onclick={doRotate}
        disabled={rotating || loading || !net.cf_configured}
      >
        {#if rotating}<span class="spinner"></span>{/if}
        {rotating ? t('rotating') : t('rotateAll')}
      </button>
    </div>
  </div>
</div>
