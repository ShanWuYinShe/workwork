<script>
  import { onMount } from 'svelte';
  import {
    getMediaCatalogOverview,
    getMediaCatalogItems,
    reviewMediaCatalogItem,
    deleteMediaCatalogItem,
    exportMediaCatalog,
    importMediaCatalog,
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
  let itemsLoadingMore = $state(false);
  let hasMore = $state(false);
  let rankDraft = $state({});

  let importText = $state('');
  let importApproved = $state(false);
  let importBusy = $state(false);
  let importOpen = $state(false);
  let importResult = $state(null);

  let exportBusy = $state(false);

  const statusOptions = ['pending', 'approved', 'rejected', ''];
  const CATALOG_PAGE_SIZE = 300;

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

  async function loadItems(categoryId, title, append = false) {
    activeCategory = categoryId;
    activeTitle = title;
    if (append) {
      itemsLoadingMore = true;
    } else {
      itemsLoading = true;
    }
    try {
      const offset = append ? items.length : 0;
      const data = await getMediaCatalogItems(categoryId, statusFilter, CATALOG_PAGE_SIZE, offset);
      const incoming = data.items || [];
      hasMore = incoming.length >= CATALOG_PAGE_SIZE;
      if (append) {
        const seen = new Set(items.map(itemKey));
        items = [...items, ...incoming.filter((it) => !seen.has(itemKey(it)))];
        const drafts = { ...rankDraft };
        for (const it of incoming) {
          if (drafts[itemKey(it)] === undefined) {
            drafts[itemKey(it)] = it.rank == null ? '' : String(it.rank);
          }
        }
        rankDraft = drafts;
      } else {
        items = incoming;
        const drafts = {};
        for (const it of items) {
          drafts[itemKey(it)] = it.rank == null ? '' : String(it.rank);
        }
        rankDraft = drafts;
      }
    } catch (e) {
      toast(e?.message || String(e), 'error');
      if (!append) items = [];
      hasMore = false;
    } finally {
      itemsLoading = false;
      itemsLoadingMore = false;
    }
  }

  async function loadMore() {
    if (!hasMore || itemsLoadingMore) return;
    await loadItems(activeCategory, activeTitle, true);
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
      // 原地应用结果，不重拉列表：保留分页位置和滚动位置。
      applyLocalChange(it, change);
      await refreshOverview();
    } catch (e) {
      toast(e?.message || String(e), 'error');
    }
  }

  // 把审校结果就地写回当前列表；状态变化后不再匹配当前筛选的条目直接移除。
  function applyLocalChange(it, change) {
    const key = itemKey(it);
    let updated = null;
    items = items
      .map((x) => (itemKey(x) === key ? { ...x, ...change } : x))
      .filter((x) => {
        if (itemKey(x) !== key) return true;
        updated = x;
        if (change.status && statusFilter && x.status !== statusFilter) return false;
        return true;
      });
    if (updated) {
      if (change.rank !== undefined) rankDraft[key] = String(change.rank);
      if (change.clearRank) rankDraft[key] = '';
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
      const key = itemKey(it);
      items = items.filter((x) => itemKey(x) !== key);
      delete rankDraft[key];
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
      toast(
        t('catalogExportDone', {
          count: data?.count ?? 0,
          scope: label || category || t('catalogExportAll'),
        }),
        'success',
      );
    } catch (e) {
      toast(e?.message || String(e), 'error');
    } finally {
      exportBusy = false;
    }
  }

  function toggleImport() {
    importOpen = !importOpen;
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
      const data = await importMediaCatalog(parsed, importApproved);
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
      <div class="table-wrap">
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
          {#if hasMore}
            <div class="load-more-row">
              <button class="btn btn-sm btn-ghost" onclick={loadMore} disabled={itemsLoadingMore}>
                {#if itemsLoadingMore}<span class="spinner"></span>{/if}
                {t('catalogLoadMore')}
              </button>
            </div>
          {/if}
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
    font-family: var(--font-mono);
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
  .load-more-row {
    display: flex;
    justify-content: center;
    padding: 0.75rem 0 0.25rem;
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
    font-family: var(--font-mono);
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
</style>
