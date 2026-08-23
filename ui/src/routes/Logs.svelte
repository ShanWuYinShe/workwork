<script>
  import { onMount } from 'svelte';
  import { getServices } from '../lib/api.js';
  import { copyToClipboard } from '../lib/clipboard.js';
  import { t } from '../lib/i18n.svelte.js';
  import { serviceDisplayName } from '../lib/services.js';
  import { toast } from '../lib/toast.svelte.js';

  let services = $state([]);
  let selectedService = $state('');
  let logLines = $state([]);
  let loading = $state(true);
  let following = $state(true);
  let search = $state('');
  let es = $state(null);
  let reconnectTimer = $state(null);
  let reconnectDelay = 1000;
  let reconnected = $state(false);
  let logContainer = $state(null);

  onMount(() => {
    refresh();
    return () => {
      closeStream();
      clearReconnect();
    };
  });

  async function refresh() {
    loading = true;
    try {
      const s = await getServices();
      services = s.services || [];
      if (
        services.length &&
        (!selectedService || !services.some((svc) => svc.name === selectedService))
      ) {
        selectedService = services[0].name;
      }
      if (selectedService && following) startStream();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      loading = false;
    }
  }

  function startStream(clear = true) {
    closeStream();
    if (!selectedService) return;
    if (clear) logLines = [];
    es = new EventSource(
      `/api/admin/services/${encodeURIComponent(selectedService)}/logs/stream?tail=200`,
    );
    es.onopen = () => {
      reconnectDelay = 1000;
      // 重连成功后服务端会重新推送尾部 tail=200 行；清掉断线前的旧行，
      // 避免同一批日志重复累积（列表有 5000 行上限，重复会挤掉更多历史）。
      if (reconnected) {
        logLines = [];
        reconnected = false;
      }
    };
    es.onmessage = (e) => {
      logLines.push(e.data);
      if (logLines.length > 5000) logLines.splice(0, logLines.length - 5000);
    };
    es.onerror = () => {
      closeStream();
      reconnected = true;
      if (following) {
        scheduleReconnect();
      }
    };
  }

  function closeStream() {
    clearReconnect();
    if (es) {
      es.close();
      es = null;
    }
  }

  function clearReconnect() {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
  }

  function scheduleReconnect() {
    reconnectTimer = setTimeout(() => {
      reconnectDelay = Math.min(reconnectDelay * 2, 30000);
      startStream(false);
    }, reconnectDelay);
  }

  function selectService(name) {
    selectedService = name;
    following = true;
    reconnectDelay = 1000;
    startStream();
  }

  function toggleFollow() {
    following = !following;
    if (following) {
      reconnectDelay = 1000;
      startStream(false);
    } else {
      closeStream();
    }
  }

  async function copyLogs() {
    const text = logLines.join('\n');
    if (await copyToClipboard(text)) {
      toast(t('copied'), 'success', 2000);
    } else {
      toast(t('copyFailed'), 'error');
    }
  }

  function downloadLogs() {
    const text = logLines.join('\n');
    const blob = new Blob([text], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${selectedService}-logs.txt`;
    a.click();
    URL.revokeObjectURL(url);
  }

  function levelClass(line) {
    if (/\b(error|fatal|panic)\b/i.test(line) || /\bERR\]/i.test(line)) return 'log-error';
    if (/\b(warn|warning)\b/i.test(line) || /\bWRN\]/i.test(line)) return 'log-warn';
    if (/\binfo\b/i.test(line) || /\bINF\]/i.test(line)) return 'log-info';
    return '';
  }

  $effect(() => {
    if (following && logContainer && logLines.length) {
      requestAnimationFrame(() => {
        // 仅在用户已在底部附近时自动跟随，避免打断向上查看历史日志。
        const nearBottom =
          logContainer.scrollTop + logContainer.clientHeight >= logContainer.scrollHeight - 60;
        if (nearBottom) logContainer.scrollTop = logContainer.scrollHeight;
      });
    }
  });

  let filtered = $derived(
    search ? logLines.filter((l) => l.toLowerCase().includes(search.toLowerCase())) : logLines,
  );
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('serviceLogs')}</h1>
    <div class="page-actions">
      <button class="btn btn-sm btn-ghost" onclick={refresh} disabled={loading}>
        {t('refresh')}
      </button>
      <button class="btn btn-sm btn-ghost" onclick={copyLogs} disabled={!logLines.length}>
        {t('copy')}
      </button>
      <button class="btn btn-sm btn-ghost" onclick={downloadLogs} disabled={!logLines.length}>
        {t('download')}
      </button>
    </div>
  </div>

  {#if loading}
    <div class="card"><div class="loading-skeleton" style="height:80px;"></div></div>
  {:else}
    <div class="card">
      <div class="log-toolbar">
        <div class="log-services">
          {#each services as s}
            <button
              class="btn btn-sm"
              class:btn-primary={selectedService === s.name}
              class:btn-ghost={selectedService !== s.name}
              onclick={() => selectService(s.name)}
            >
              {serviceDisplayName(s.name)}
              <span class="badge badge-{s.state === 'running' ? 'green' : 'red'} badge-inline">
                <span class="badge-dot"></span>{s.state === 'running' ? t('stateRunning') : t('stateStopped')}
              </span>
            </button>
          {/each}
        </div>
        <div class="log-controls">
          <input
            class="log-search input-compact"
            type="text"
            placeholder={t('searchLogs')}
            aria-label={t('searchLogs')}
            bind:value={search}
          />
          <button
            class="btn btn-sm"
            class:btn-primary={following}
            class:btn-ghost={!following}
            onclick={toggleFollow}
            title={following ? t('live') : t('paused')}
          >
            {following ? '⊡ ' + t('live') : '⊡ ' + t('paused')}
          </button>
        </div>
      </div>

      <div class="log-container" bind:this={logContainer}>
        {#if filtered.length === 0}
          <span class="log-empty">
            {search ? t('noMatchingLogs') : t('noLogEntries')}
          </span>
        {:else}
          {#each filtered as line}
            <div class="log-line {levelClass(line)}">{line}</div>
          {/each}
        {/if}
        {#if !es && logLines.length > 0}
          <div class="log-stream-ended">
            {#if following}
              — {t('reconnecting')}... —
            {:else}
              — {t('streamPaused')} ·
              <button
                class="btn btn-sm btn-ghost"
                onclick={() => {
                  following = true;
                  reconnectDelay = 1000;
                  startStream(false);
                }}>{t('resume')}</button
              > —
            {/if}
          </div>
        {/if}
      </div>
      <div class="log-status">
        <span class="badge badge-{es ? 'green' : following ? 'yellow' : 'red'}">
          <span class="badge-dot"></span>{es
            ? t('live')
            : following
              ? t('reconnecting')
              : t('paused')}
        </span>
        <span class="log-count"
          >{t('linesCount', { filtered: filtered.length, total: logLines.length })}</span
        >
      </div>
    </div>
  {/if}
</div>

<style>
  .log-toolbar {
    display: flex;
    flex-wrap: wrap;
    gap: 0.75rem;
    align-items: center;
    margin-bottom: 0.75rem;
  }
  .log-services {
    display: flex;
    gap: 0.375rem;
    flex-wrap: wrap;
  }
  .log-controls {
    display: flex;
    gap: 0.375rem;
    margin-left: auto;
    align-items: center;
  }
  .log-search {
    width: 140px;
  }
  .log-container {
    background: var(--c-bg-deep);
    border: 1px solid var(--c-border);
    border-radius: var(--radius);
    padding: 0.75rem;
    height: 500px;
    overflow-y: auto;
    font-family: var(--font-mono);
    font-size: 0.7rem;
    line-height: 1.6;
  }
  .log-line {
    white-space: pre-wrap;
    word-break: break-all;
    color: var(--c-text-secondary);
  }
  .log-line.log-error {
    color: var(--c-text-danger);
    background: var(--c-danger-bg);
  }
  .log-line.log-warn {
    color: var(--c-text-warning);
    background: var(--c-warning-bg);
  }
  .log-line.log-info {
    color: var(--c-text-info);
  }
  .log-empty {
    color: var(--c-text-muted);
    font-size: 0.8rem;
  }
  .log-stream-ended {
    text-align: center;
    padding: 1rem;
    color: var(--c-text-muted);
    font-size: 0.75rem;
  }
  .log-status {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-top: 0.5rem;
    font-size: 0.7rem;
    color: var(--c-text-muted);
  }
  .log-count {
    font-family: var(--font-mono);
  }

  @media (max-width: 768px) {
    .log-controls {
      margin-left: 0;
      width: 100%;
    }
    .log-search {
      flex: 1;
    }
    .log-container {
      height: 350px;
    }
  }
</style>
