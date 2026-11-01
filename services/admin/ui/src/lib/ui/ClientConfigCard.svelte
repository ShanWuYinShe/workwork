<script>
  import { t } from '../i18n.svelte.js';
  import { copyToClipboard } from '../clipboard.js';
  import { toast } from '../toast.svelte.js';

  let { json, uuid = '', loading = false, copyLabel = '' } = $props();

  async function copy() {
    if (await copyToClipboard(json)) {
      toast(t('configCopied'), 'success', 2000);
    } else {
      toast(t('copyFailedManual'), 'error');
    }
  }

  function download() {
    const blob = new Blob([json], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'xray-config.json';
    // 合成 click 前必须把 <a> 挂进文档:Firefox 对游离节点的点击不触发下载。
    // blob URL 也不能同步 revoke——部分浏览器在下载真正开始前就取不到数据。
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
</script>

{#if json}
  <div class="card">
    <div class="card-header">
      <div class="card-title">
        {t('clientConfig')}
        {#if uuid}
          <span class="badge badge-gray badge-code">{uuid.slice(0, 8)}...</span>
        {/if}
        {#if loading}<span class="spinner spinner-inline"></span>{/if}
      </div>
      <div class="card-actions">
        <button class="btn btn-sm btn-primary" onclick={copy} aria-label={t('copyConfiguration')}>
          {copyLabel || t('copyConfig')}
        </button>
        <button
          class="btn btn-sm btn-ghost"
          onclick={download}
          aria-label={t('downloadConfiguration')}>{t('download')}</button
        >
      </div>
    </div>
    <div class="badge-list config-badges">
      <span class="badge badge-gray">{t('socks5')}:10808</span>
      <span class="badge badge-gray">{t('http')}:10809</span>
      <span class="badge badge-gray">{t('protocolXhttp')}</span>
    </div>
    <textarea rows="16" readonly>{json}</textarea>
  </div>
{/if}
