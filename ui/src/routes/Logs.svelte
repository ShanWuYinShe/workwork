<script>
  import { onMount } from 'svelte';
  import { getServices, getServiceLogs } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import Toast from '../lib/Toast.svelte';

  let services = $state([]);
  let selectedService = $state('');
  let logLines = $state('');
  let loading = $state(true);
  let error = $state('');

  onMount(refresh);

  async function refresh() {
    loading = true;
    error = '';
    try {
      const s = await getServices();
      services = s.services || [];
      if (services.length && !selectedService) selectedService = services[0].name;
      if (selectedService) await loadLogs();
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  async function loadLogs() {
    if (!selectedService) return;
    error = '';
    try {
      const d = await getServiceLogs(selectedService, 200);
      logLines = d.logs || '';
    } catch (e) {
      error = e.message;
    }
  }

  function selectService(name) {
    selectedService = name;
    loadLogs();
  }

  let parsed = $derived(logLines.split('\n').filter(Boolean));
</script>

<div class="page">
  <div class="page-header">
    <h1 class="page-title">{t('serviceLogs')}</h1>
    <div class="page-actions">
      <button class="btn btn-sm btn-ghost" onclick={loadLogs}>{t('refresh')}</button>
    </div>
  </div>

  <Toast type="error" visible={!!error} onclose={() => error = ''}>{error}</Toast>

  {#if loading}
    <div class="card"><div class="loading-skeleton" style="height:300px;"></div></div>
  {:else}
    <div class="card">
      <div class="card-header">
        <div class="card-title">{t('service')}</div>
      </div>
      <div style="display:flex;gap:0.5rem;flex-wrap:wrap;margin-bottom:1rem;">
        {#each services as s}
          <button
            class="btn btn-sm"
            style={selectedService === s.name ? 'background:var(--c-accent);color:#fff;' : ''}
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

      {#if selectedService}
        <div class="card-header" style="margin-top:0;">
          <div class="card-title">{selectedService} — {t('logs')}</div>
          <div class="card-subtitle">{t('lastLines', { n: parsed.length })}</div>
        </div>
        <div class="log-container">
          {#if parsed.length === 0}
            <span style="color:var(--c-text-muted);">{t('noLogEntries')}</span>
          {:else}
            {#each parsed as line}
              <div class="log-line">{line}</div>
            {/each}
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .log-container {
    background: var(--c-bg-deep);
    border: 1px solid var(--c-border);
    border-radius: var(--radius);
    padding: 0.75rem;
    max-height: 500px;
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
  .log-line:has(:global(.timestamp)) {
    color: var(--c-text);
  }
</style>
