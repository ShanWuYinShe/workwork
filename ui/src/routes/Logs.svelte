<script>
  import { onMount, onDestroy } from 'svelte';
  import { getServices } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { toast } from '../lib/toast.svelte.js';

  let services = $state([]);
  let selectedService = $state('');
  let logLines = $state([]);
  let loading = $state(true);
  let following = $state(true);
  let search = $state('');
  let es = null;
  let logContainer = $state(null);

  onMount(() => {
    refresh();
    return () => closeStream();
  });

  async function refresh() {
    loading = true;
    try {
      const s = await getServices();
      services = s.services || [];
      if (services.length && !selectedService) selectedService = services[0].name;
      if (selectedService) startStream();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      loading = false;
    }
  }

  function startStream() {
    closeStream();
    if (!selectedService) return;
    logLines = [];
    es = new EventSource(`/api/admin/services/${encodeURIComponent(selectedService)}/logs/stream?tail=200`);
    es.onmessage = (e) => {
      logLines = [...logLines, e.data];
      // Keep max 5000 lines in memory
      if (logLines.length > 5000) logLines = logLines.slice(-5000);
    };
    es.onerror = () => {
      closeStream();
    };
  }

  function closeStream() {
    if (es) {
      es.close();
      es = null;
    }
  }

  function selectService(name) {
    selectedService = name;
    startStream();
  }

  function toggleFollow() {
    following = !following;
  }

  function copyLogs() {
    const text = logLines.join('\n');
    navigator.clipboard.writeText(text).then(() => {
      toast(t('copied'), 'success', 2000);
    }).catch(() => {
      toast(t('copyFailed'), 'error');
    });
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
    const lower = line.toLowerCase();
    if (/error|fatal|panic|err\]/.test(lower)) return 'log-error';
    if (/warn|warning/.test(lower)) return 'log-warn';
    if (/info/.test(lower)) return 'log-info';
    return '';
  }

  $effect(() => {
    if (following && logContainer && logLines.length) {
      requestAnimationFrame(() => {
        logContainer.scrollTop = logContainer.scrollHeight;
      });
    }
  });

  let filtered = $derived(
    search
      ? logLines.filter((l) => l.toLowerCase().includes(search.toLowerCase()))
      : logLines,
  );
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('serviceLogs')}</h1>
    <div class="page-actions">
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
              {s.name}
              <span
                class="badge badge-{s.state === 'running' ? 'green' : 'red'}"
                style="margin-left:4px;"
              >
                <span class="badge-dot"></span>{s.state}
              </span>
            </button>
          {/each}
        </div>
        <div class="log-controls">
          <input
            class="log-search"
            type="text"
            placeholder="搜索..."
            bind:value={search}
          />
          <button
            class="btn btn-sm"
            class:btn-primary={following}
            class:btn-ghost={!following}
            onclick={toggleFollow}
            title={following ? '自动滚动中' : '已暂停滚动'}
          >
            {following ? '⊡ 实时' : '⊡ 暂停'}
          </button>
        </div>
      </div>

      <div class="log-container" bind:this={logContainer}>
        {#if filtered.length === 0}
          <span class="log-empty">
            {search ? '无匹配日志' : t('noLogEntries')}
          </span>
        {:else}
          {#each filtered as line}
            <div class="log-line {levelClass(line)}">{line}</div>
          {/each}
        {/if}
        {#if !es && logLines.length > 0}
          <div class="log-stream-ended">— 流已断开 · <button class="btn btn-sm btn-ghost" onclick={startStream}>重新连接</button> —</div>
        {/if}
      </div>
      <div class="log-status">
        <span class="badge badge-{es ? 'green' : 'red'}">
          <span class="badge-dot"></span>{es ? '实时' : '已断开'}
        </span>
        <span class="log-count">{filtered.length} / {logLines.length} 行</span>
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
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border);
    color: var(--c-text);
    padding: 0.3rem 0.5rem;
    border-radius: var(--radius-sm);
    font-size: 0.75rem;
    width: 140px;
    outline: none;
  }
  .log-search:focus {
    border-color: var(--c-accent);
    box-shadow: 0 0 0 3px var(--c-accent-bg);
  }
  .log-container {
    background: var(--c-bg-deep);
    border: 1px solid var(--c-border);
    border-radius: var(--radius);
    padding: 0.75rem;
    height: 500px;
    overflow-y: auto;
    font-family: 'SF Mono', 'Fira Code', ui-monospace, monospace;
    font-size: 0.7rem;
    line-height: 1.6;
  }
  .log-line {
    white-space: pre-wrap;
    word-break: break-all;
    color: var(--c-text-secondary);
  }
  .log-line.log-error {
    color: #fca5a5;
    background: rgba(239, 68, 68, 0.06);
  }
  .log-line.log-warn {
    color: #fde68a;
    background: rgba(245, 158, 11, 0.04);
  }
  .log-line.log-info {
    color: #93c5fd;
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
    font-family: 'SF Mono', 'Fira Code', ui-monospace, monospace;
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
