<script>
  import { onMount } from 'svelte';
  import {
    getMediaCatalogOverview,
    getMediaCatalogItems,
    reviewMediaCatalogItem,
    deleteMediaCatalogItem,
    exportMediaCatalog,
    importMediaCatalog,
    analyzeMediaCatalog,
  } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import { toast } from '../lib/toast.svelte.js';

  let loading = $state(true);
  let loadError = $state('');
  let overview = $state(null);

  let activeCategory = $state(null);
  let activeTitle = $state('');
  let statusFilter = $state('pending');
  let items = $state([]);
  let itemsLoading = $state(false);
  let rankDraft = $state({});

  let importText = $state('');
  let importApproved = $state(false);
  let importBusy = $state(false);
  let importOpen = $state(false);
  let importResult = $state(null);

  // AI 分析状态：analyzeRows 是 [{item, verdict, reason, score, removed}]，
  // null 表示尚未分析（按钮未点）。
  let analyzeBusy = $state(false);
  let analyzeRows = $state(null);
  let analyzeError = $state('');

  let exportBusy = $state(false);

  const statusOptions = ['pending', 'approved', 'rejected', ''];

  onMount(load);

  async function load() {
    loading = true;
    loadError = '';
    try {
      overview = await getMediaCatalogOverview();
    } catch (e) {
      loadError = e?.message || String(e);
      overview = null;
    } finally {
      loading = false;
    }
  }

  async function refreshOverview() {
    try {
      overview = await getMediaCatalogOverview();
    } catch {
      /* ignore refresh errors */
    }
  }

  function itemKey(it) {
    return `${it.provider}:${it.nativeId}`;
  }

  async function loadItems(categoryId, title) {
    activeCategory = categoryId;
    activeTitle = title;
    itemsLoading = true;
    try {
      const data = await getMediaCatalogItems(categoryId, statusFilter, 300);
      items = data.items || [];
      const drafts = {};
      for (const it of items) {
        drafts[itemKey(it)] = it.rank == null ? '' : String(it.rank);
      }
      rankDraft = drafts;
    } catch (e) {
      toast(e?.message || String(e), 'error');
      items = [];
    } finally {
      itemsLoading = false;
    }
  }

  function closeReview() {
    activeCategory = null;
    items = [];
  }

  async function changeStatusFilter(s) {
    statusFilter = s;
    if (activeCategory) await loadItems(activeCategory, activeTitle);
  }

  async function applyReview(it, change) {
    const payload = {
      categoryId: activeCategory,
      provider: it.provider,
      nativeId: it.nativeId,
      ...change,
    };
    try {
      await reviewMediaCatalogItem(payload);
      toast(t('catalogReviewSaved'), 'success');
      await loadItems(activeCategory, activeTitle);
      await refreshOverview();
    } catch (e) {
      toast(e?.message || String(e), 'error');
    }
  }

  function approve(it) {
    applyReview(it, { status: 'approved' });
  }
  function reject(it) {
    applyReview(it, { status: 'rejected' });
  }
  function togglePin(it) {
    applyReview(it, { pinned: !it.pinned });
  }
  function setRank(it) {
    const raw = (rankDraft[itemKey(it)] || '').trim();
    if (raw === '') {
      applyReview(it, { clearRank: true });
      return;
    }
    const n = Number(raw);
    if (!Number.isInteger(n) || n < 0) {
      toast(t('catalogRankPlaceholder'), 'error');
      return;
    }
    applyReview(it, { rank: n });
  }

  async function removeItem(it) {
    if (!(await showConfirm(t('catalogDeleteConfirm', { title: it.title })))) return;
    try {
      await deleteMediaCatalogItem({
        categoryId: activeCategory,
        provider: it.provider,
        nativeId: it.nativeId,
      });
      toast(t('catalogDeleteDone'), 'success');
      await loadItems(activeCategory, activeTitle);
      await refreshOverview();
    } catch (e) {
      toast(e?.message || String(e), 'error');
    }
  }

  async function runExport(category = '', label = '') {
    exportBusy = true;
    try {
      const data = await exportMediaCatalog(category, '');
      const json = JSON.stringify(data?.items ?? [], null, 2);
      const blob = new Blob([json], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      const ts = new Date().toISOString().slice(0, 19).replace(/[:T]/g, '');
      a.href = url;
      a.download = `catalog-${category || 'all'}-${ts}.json`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
      toast(t('catalogExportDone', { count: data?.count ?? 0, scope: label || category || t('catalogExportAll') }), 'success');
    } catch (e) {
      toast(e?.message || String(e), 'error');
    } finally {
      exportBusy = false;
    }
  }

  function toggleImport() {
    importOpen = !importOpen;
    if (!importOpen) resetAnalyze();
  }

  async function loadImportFile(event) {
    const input = event.currentTarget;
    const file = input.files?.[0];
    if (!file) return;
    try {
      const text = await file.text();
      const parsed = JSON.parse(text);
      if (!Array.isArray(parsed?.items) || parsed.items.length === 0) {
        toast(t('catalogImportEmpty'), 'error');
        return;
      }
      importText = JSON.stringify(parsed, null, 2);
      importResult = null;
      resetAnalyze();
      toast(
        t('catalogImportFileLoaded', {
          name: file.name,
          count: parsed.items.length,
        }),
        'success',
      );
    } catch {
      toast(t('catalogImportInvalidJson'), 'error');
    } finally {
      input.value = '';
    }
  }

  async function runImport() {
    let parsed;
    try {
      parsed = JSON.parse(importText);
    } catch {
      toast(t('catalogImportInvalidJson'), 'error');
      return;
    }
    if (!Array.isArray(parsed?.items) || parsed.items.length === 0) {
      toast(t('catalogImportEmpty'), 'error');
      return;
    }
    importBusy = true;
    try {
      // 若已做 AI 分析，按分析结果裁剪：剔除被移除的行，保留行带上 reason。
      let doc = parsed;
      if (analyzeRows) {
        const kept = analyzeRows
          .filter((r) => !r.removed)
          .map((r) => ({ ...r.item, reason: r.reason || undefined }));
        doc = { ...parsed, items: kept };
      }
      const data = await importMediaCatalog(doc, importApproved);
      importResult = data;
      toast(
        t('catalogImportDone', {
          imported: data.imported ?? 0,
          approved: data.approved ?? 0,
          skipped: data.skipped ?? 0,
        }),
        'success',
      );
      await refreshOverview();
    } catch (e) {
      toast(e?.message || String(e), 'error');
    } finally {
      importBusy = false;
    }
  }

  // runAnalyze 把当前 importText 的候选送 AI 分析，结果填进 analyzeRows。
  async function runAnalyze() {
    let parsed;
    try {
      parsed = JSON.parse(importText);
    } catch {
      toast(t('catalogImportInvalidJson'), 'error');
      return;
    }
    if (!Array.isArray(parsed?.items) || parsed.items.length === 0) {
      toast(t('catalogImportEmpty'), 'error');
      return;
    }
    analyzeBusy = true;
    analyzeError = '';
    try {
      const data = await analyzeMediaCatalog(parsed);
      const results = Array.isArray(data?.results) ? data.results : [];
      const byIndex = new Map(results.map((r) => [r.index, r]));
      analyzeRows = parsed.items.map((item, i) => {
        const v = byIndex.get(i) || { verdict: 'fail', reason: t('catalogAnalyzeMissing'), score: 0 };
        return { item, verdict: v.verdict, reason: v.reason || '', score: v.score ?? 0, removed: false };
      });
      const fails = analyzeRows.filter((r) => r.verdict === 'fail').length;
      toast(t('catalogAnalyzeDone', { total: analyzeRows.length, fails }), 'success');
    } catch (e) {
      analyzeError = e?.message || String(e);
      toast(analyzeError, 'error');
    } finally {
      analyzeBusy = false;
    }
  }

  // removeFailedRows 一键剔除所有 verdict=fail 的行（仍可选恢复，因仅标记 removed）。
  function removeFailedRows() {
    if (!analyzeRows) return;
    analyzeRows = analyzeRows.map((r) => (r.verdict === 'fail' ? { ...r, removed: true } : r));
  }

  function toggleRowRemoved(idx) {
    if (!analyzeRows || !analyzeRows[idx]) return;
    analyzeRows = analyzeRows.map((r, i) => (i === idx ? { ...r, removed: !r.removed } : r));
  }

  function resetAnalyze() {
    analyzeRows = null;
    analyzeError = '';
  }

  function fmtDuration(sec) {
    const total = Math.max(0, Math.floor(Number(sec) || 0));
    const h = Math.floor(total / 3600);
    const m = Math.floor((total % 3600) / 60);
    const s = total % 60;
    const pad = (n) => String(n).padStart(2, '0');
    return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
  }

  function statusBadgeClass(status) {
    if (status === 'approved') return 'badge badge-green';
    if (status === 'rejected') return 'badge badge-red';
    return 'badge badge-yellow';
  }

  function statusLabel(status) {
    if (status === 'approved') return t('catalogStatusApproved');
    if (status === 'rejected') return t('catalogStatusRejected');
    return t('catalogStatusPending');
  }

  function statusFilterLabel(s) {
    if (s === 'pending') return t('catalogStatusPending');
    if (s === 'approved') return t('catalogStatusApproved');
    if (s === 'rejected') return t('catalogStatusRejected');
    return t('catalogStatusAll');
  }

</script>

<div class="card">
  <div class="card-header">
    <div>
      <div class="card-title">{t('catalogManagement')}</div>
      <div class="card-subtitle">{t('catalogManagementDesc')}</div>
    </div>
    <div class="card-actions">
      <button class="btn btn-sm btn-ghost" onclick={load} disabled={loading}>{t('refresh')}</button>
    </div>
  </div>

  {#if loading}
    <div class="loading-skeleton" style="height:160px;"></div>
  {:else if loadError}
    <div class="empty-state">{loadError}</div>
  {:else if overview}
    {#if !overview.catalogEnabled}
      <div class="cat-note">{t('catalogDisabledNotice')}</div>
    {/if}

    <div class="cat-actions">
      <button
        class="btn btn-sm btn-secondary"
        onclick={() => runExport('', t('catalogExportAll'))}
        disabled={exportBusy || !overview.catalogEnabled}
        >{#if exportBusy}<span class="spinner"></span>{/if}{t('catalogExportAll')}</button
      >
    </div>

    <div class="cat-table-wrap">
      <table class="cat-table">
        <thead>
          <tr>
            <th>{t('catalogColCategory')}</th>
            <th class="num">{t('catalogColPending')}</th>
            <th class="num">{t('catalogColApproved')}</th>
            <th class="num">{t('catalogColRejected')}</th>
            <th class="actions-col">{t('catalogColActions')}</th>
          </tr>
        </thead>
        <tbody>
          {#each overview.categories as cat (cat.id)}
            <tr class:active-row={activeCategory === cat.id}>
              <td>
                <div class="cat-name">{cat.title || cat.id}</div>
                <div class="cat-id">{cat.id}</div>
              </td>
              <td class="num">{cat.pending}</td>
              <td class="num">{cat.approved}</td>
              <td class="num">{cat.rejected}</td>
              <td class="actions-col">
                <button
                  class="btn btn-xs btn-ghost"
                  onclick={() => loadItems(cat.id, cat.title || cat.id)}
                  disabled={!overview.catalogEnabled}>{t('catalogReview')}</button
                >
                <button
                  class="btn btn-xs btn-ghost"
                  onclick={() => runExport(cat.id, cat.title || cat.id)}
                  disabled={exportBusy || !overview.catalogEnabled}>{t('catalogExport')}</button
                >
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>

    {#if activeCategory}
      <div class="review-panel">
        <div class="review-head">
          <div class="review-title">{t('catalogItemsFor', { title: activeTitle })}</div>
          <button class="btn btn-xs btn-ghost" onclick={closeReview}>{t('catalogClose')}</button>
        </div>
        <div class="status-tabs">
          {#each statusOptions as s}
            <button
              class="chip"
              class:chip-on={statusFilter === s}
              onclick={() => changeStatusFilter(s)}>{statusFilterLabel(s)}</button
            >
          {/each}
        </div>
        {#if itemsLoading}
          <div class="loading-skeleton" style="height:120px;"></div>
        {:else if items.length === 0}
          <div class="empty-state">{t('catalogNoItems')}</div>
        {:else}
          <div class="item-list">
            {#each items as it (itemKey(it))}
              <div class="item-row">
                <div class="item-main">
                  <div class="item-title-line">
                    <span class={statusBadgeClass(it.status)}>{statusLabel(it.status)}</span>
                    {#if it.pinned}<span class="badge badge-purple">★</span>{/if}
                    <span class="item-title">{it.title}</span>
                  </div>
                  <div class="item-meta">
                    {#if it.creators?.length}<span>{it.creators.join(', ')}</span>{/if}
                    <span>{fmtDuration(it.durationSeconds)}</span>
                    {#if it.year}<span>{it.year}</span>{/if}
                    <span>{t('catalogScore', { score: it.score })}</span>
                    {#if it.reason}<span class="item-reason" title={it.reason}
                      >{t('catalogReason')}：{it.reason}</span
                    >{/if}
                    {#if it.sourceUrl}<a
                        href={it.sourceUrl}
                        target="_blank"
                        rel="noopener noreferrer">{t('catalogOpenSource')}</a
                      >{/if}
                  </div>
                </div>
                <div class="item-actions">
                  <button class="btn btn-xs btn-success" onclick={() => approve(it)}
                    >{t('catalogApprove')}</button
                  >
                  <button class="btn btn-xs btn-danger" onclick={() => reject(it)}
                    >{t('catalogReject')}</button
                  >
                  <button class="btn btn-xs btn-ghost" onclick={() => togglePin(it)}
                    >{it.pinned ? t('catalogUnpin') : t('catalogPin')}</button
                  >
                  <input
                    class="rank-input"
                    type="number"
                    min="0"
                    placeholder={t('catalogRankPlaceholder')}
                    bind:value={rankDraft[itemKey(it)]}
                  />
                  <button class="btn btn-xs btn-ghost" onclick={() => setRank(it)}
                    >{t('catalogSetRank')}</button
                  >
                  <button class="btn btn-xs btn-danger-outline" onclick={() => removeItem(it)}
                    >{t('catalogDelete')}</button
                  >
                </div>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}

    <div class="curation-block">
      <button class="curation-toggle" onclick={toggleImport}>
        <span>{t('catalogImportTitle')}</span>
        <span class="curation-sign">{importOpen ? '−' : '+'}</span>
      </button>
      {#if importOpen}
        <div class="curation-body">
          <div class="helper-text">{t('catalogImportDesc')}</div>
          <div class="import-file-row">
            <label class="btn btn-sm btn-secondary" for="catalog-import-file">
              {t('catalogImportSelectFile')}
            </label>
            <input
              id="catalog-import-file"
              class="import-file-input"
              type="file"
              accept=".json,application/json"
              onchange={loadImportFile}
            />
            <span class="helper-text">{t('catalogImportFileHelp')}</span>
          </div>
          <textarea
            class="curation-text"
            bind:value={importText}
            spellcheck="false"
            placeholder={t('catalogImportPlaceholder')}
          ></textarea>
          <div class="analyze-actions">
            <button
              class="btn btn-sm btn-secondary"
              onclick={runAnalyze}
              disabled={analyzeBusy || !overview.catalogEnabled}
              title={t('catalogAnalyzeHelp')}
            >
              {#if analyzeBusy}<span class="spinner"></span>{/if}
              {t('catalogAnalyze')}
            </button>
            {#if analyzeRows}
              <button class="btn btn-sm btn-ghost" onclick={removeFailedRows}>
                {t('catalogAnalyzeRemoveFailed')}
              </button>
              <button class="btn btn-sm btn-ghost" onclick={resetAnalyze}>{t('catalogAnalyzeReset')}</button>
              <span class="helper-text">
                {t('catalogAnalyzeSummary', {
                  total: analyzeRows.length,
                  pass: analyzeRows.filter((r) => r.verdict === 'pass').length,
                  fail: analyzeRows.filter((r) => r.verdict === 'fail').length,
                })}
              </span>
            {/if}
          </div>
          {#if analyzeError}
            <div class="analyze-error">{analyzeError}</div>
          {/if}
          {#if analyzeRows}
            <div class="analyze-table-wrap">
              <table class="analyze-table">
                <thead>
                  <tr>
                    <th></th>
                    <th>{t('catalogAnalyzeVerdict')}</th>
                    <th>{t('catalogColCategory')}</th>
                    <th>{t('catalogAnalyzeTitle')}</th>
                    <th>{t('catalogScore', { score: '' }).replace(/\s*$/, '')}</th>
                    <th>{t('catalogAnalyzeReason')}</th>
                  </tr>
                </thead>
                <tbody>
                  {#each analyzeRows as row, idx}
                    <tr class:row-removed={row.removed}>
                      <td class="cell-toggle">
                        <button
                          class="btn btn-xs btn-ghost"
                          onclick={() => toggleRowRemoved(idx)}
                          title={row.removed ? t('catalogAnalyzeRestore') : t('catalogAnalyzeRemove')}
                        >
                          {row.removed ? '↩' : '✕'}
                        </button>
                      </td>
                      <td>
                        <span class={row.verdict === 'pass' ? 'badge badge-green' : 'badge badge-red'}>
                          {row.verdict === 'pass' ? t('catalogAnalyzePass') : t('catalogAnalyzeFail')}
                        </span>
                      </td>
                      <td class="cell-muted">{row.item.categoryId}</td>
                      <td class="cell-strong">{row.item.title}</td>
                      <td>{row.score}</td>
                      <td class="cell-reason">{row.reason}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
          <label class="import-approve">
            <input type="checkbox" bind:checked={importApproved} />
            <span>{t('catalogImportApproved')}</span>
          </label>
          <div class="curation-actions">
            <button
              class="btn btn-sm btn-primary"
              onclick={runImport}
              disabled={importBusy || !overview.catalogEnabled}
            >
              {#if importBusy}<span class="spinner"></span>{/if}
              {t('catalogImportRun')}
            </button>
            {#if importResult}
              <span class="import-result"
                >{t('catalogImportResult', {
                  imported: importResult.imported ?? 0,
                  approved: importResult.approved ?? 0,
                  skipped: importResult.skipped ?? 0,
                })}</span
              >
            {/if}
          </div>
        </div>
      {/if}
    </div>

  {/if}
</div>

<style>
  .cat-note {
    padding: 0.7rem 0.9rem;
    margin-bottom: 0.9rem;
    border: 1px solid var(--c-warning);
    background: var(--c-warning-bg);
    color: var(--c-text-heading);
    border-radius: var(--radius);
    font-size: 0.85rem;
  }

  .cat-actions {
    display: flex;
    gap: 0.5rem;
    flex-wrap: wrap;
    margin-bottom: 0.9rem;
  }

  .cat-table-wrap {
    overflow-x: auto;
  }
  .cat-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.85rem;
  }
  .cat-table th,
  .cat-table td {
    text-align: left;
    padding: 0.55rem 0.6rem;
    border-bottom: 1px solid var(--c-border);
    vertical-align: middle;
  }
  .cat-table th {
    color: var(--c-text-secondary);
    font-weight: 600;
    white-space: nowrap;
  }
  .cat-table th.num,
  .cat-table td.num {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .cat-table tr.active-row {
    background: var(--c-accent-bg);
  }
  .cat-name {
    color: var(--c-text-heading);
    font-weight: 600;
  }
  .cat-id {
    color: var(--c-text-muted);
    font-size: 0.72rem;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    word-break: break-all;
  }
  .actions-col {
    white-space: nowrap;
  }
  .actions-col .btn {
    margin-right: 0.3rem;
  }

  .review-panel {
    margin-top: 1rem;
    padding: 0.9rem;
    border: 1px solid var(--c-border-strong);
    border-radius: var(--radius);
    background: var(--c-bg-elevated);
  }
  .review-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 0.7rem;
  }
  .review-title {
    color: var(--c-text-heading);
    font-weight: 600;
  }
  .status-tabs {
    display: flex;
    gap: 0.4rem;
    flex-wrap: wrap;
    margin-bottom: 0.8rem;
  }
  .chip {
    padding: 0.25rem 0.7rem;
    border-radius: 999px;
    border: 1px solid var(--c-border-strong);
    background: var(--c-bg-surface);
    color: var(--c-text-secondary);
    font-size: 0.78rem;
    cursor: pointer;
    transition: var(--transition);
  }
  .chip:hover {
    color: var(--c-text);
  }
  .chip-on {
    background: var(--c-accent-bg);
    border-color: var(--c-accent-border);
    color: var(--c-text-heading);
  }

  .item-list {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .item-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.8rem;
    padding: 0.6rem 0.7rem;
    border: 1px solid var(--c-border);
    border-radius: var(--radius-sm);
    background: var(--c-bg-surface);
    flex-wrap: wrap;
  }
  .item-main {
    min-width: 0;
    flex: 1 1 320px;
  }
  .item-title-line {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    flex-wrap: wrap;
  }
  .item-title {
    color: var(--c-text-heading);
    font-weight: 500;
    word-break: break-word;
  }
  .item-meta {
    display: flex;
    gap: 0.6rem;
    flex-wrap: wrap;
    margin-top: 0.25rem;
    color: var(--c-text-secondary);
    font-size: 0.76rem;
  }
  .item-meta a {
    color: var(--c-accent);
    text-decoration: none;
  }
  .item-meta a:hover {
    text-decoration: underline;
  }
  .item-actions {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    flex-wrap: wrap;
  }
  .rank-input {
    width: 4.5rem;
    padding: 0.25rem 0.4rem;
    border: 1px solid var(--c-border-strong);
    border-radius: var(--radius-sm);
    background: var(--c-bg);
    color: var(--c-text);
    font-size: 0.78rem;
  }
  .item-actions :global(.btn-danger-outline) {
    background: transparent;
    color: var(--c-danger);
    border: 1px solid var(--c-danger);
  }
  .item-actions :global(.btn-danger-outline:hover) {
    background: var(--c-danger);
    color: #fff;
  }

  .curation-block {
    margin-top: 1rem;
    border-top: 1px solid var(--c-border);
    padding-top: 0.8rem;
  }
  .curation-toggle {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    padding: 0.5rem 0.2rem;
    background: transparent;
    border: none;
    color: var(--c-text-heading);
    font-weight: 600;
    font-size: 0.9rem;
    cursor: pointer;
  }
  .curation-sign {
    color: var(--c-text-secondary);
    font-size: 1.1rem;
  }
  .curation-body {
    margin-top: 0.5rem;
  }
  .curation-text {
    width: 100%;
    min-height: 220px;
    margin-top: 0.5rem;
    padding: 0.6rem;
    border: 1px solid var(--c-border-strong);
    border-radius: var(--radius-sm);
    background: var(--c-bg);
    color: var(--c-text);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 0.8rem;
    resize: vertical;
  }
  .import-file-row {
    align-items: center;
    display: flex;
    flex-wrap: wrap;
    gap: 0.6rem;
    margin-top: 0.7rem;
  }
  .import-file-input {
    height: 1px;
    opacity: 0;
    overflow: hidden;
    position: absolute;
    width: 1px;
  }
  .curation-actions {
    margin-top: 0.6rem;
    display: flex;
    align-items: center;
    gap: 0.6rem;
    flex-wrap: wrap;
  }
  .import-approve {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin-top: 0.6rem;
    font-size: 0.85rem;
    color: var(--c-text);
    cursor: pointer;
  }
  .import-result {
    font-size: 0.82rem;
    color: var(--c-text-muted);
  }
  .analyze-actions {
    display: flex;
    gap: 0.5rem;
    flex-wrap: wrap;
    align-items: center;
    margin: 0.5rem 0;
  }
  .analyze-error {
    padding: 0.5rem 0.7rem;
    margin-bottom: 0.5rem;
    border: 1px solid var(--c-danger);
    background: var(--c-danger-bg, rgba(220, 38, 38, 0.08));
    color: var(--c-danger);
    border-radius: var(--radius);
    font-size: 0.85rem;
  }
  .analyze-table-wrap {
    overflow-x: auto;
    max-height: 360px;
    overflow-y: auto;
    border: 1px solid var(--c-border);
    border-radius: var(--radius);
    margin-bottom: 0.7rem;
  }
  .analyze-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.84rem;
  }
  .analyze-table th,
  .analyze-table td {
    padding: 0.4rem 0.55rem;
    text-align: left;
    border-bottom: 1px solid var(--c-border);
    vertical-align: top;
  }
  .analyze-table th {
    position: sticky;
    top: 0;
    background: var(--c-bg-elevated);
    font-weight: 600;
  }
  .analyze-table tr.row-removed {
    opacity: 0.45;
    text-decoration: line-through;
  }
  .cell-toggle {
    width: 2.2rem;
    text-align: center;
  }
  .cell-reason {
    max-width: 22rem;
    color: var(--c-text-muted);
  }
</style>
