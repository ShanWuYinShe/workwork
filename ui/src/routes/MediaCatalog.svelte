<script>
  import { onMount, onDestroy } from 'svelte';
  import {
    getMediaCatalogOverview,
    getMediaCatalogItems,
    reviewMediaCatalogItem,
    seedMediaCatalog,
    rebuildMediaSnapshots,
    getMediaCuration,
    saveMediaCuration,
  } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import { toast } from '../lib/toast.svelte.js';

  let loading = $state(true);
  let loadError = $state('');
  let overview = $state(null);
  let busy = $state(false);
  let pollTimer = null;

  let activeCategory = $state(null);
  let activeTitle = $state('');
  let statusFilter = $state('pending');
  let items = $state([]);
  let itemsLoading = $state(false);
  let rankDraft = $state({});

  let curationText = $state('');
  let curationFile = $state('');
  let curationLoaded = $state(false);
  let curationLoading = $state(false);
  let curationSaving = $state(false);
  let curationOpen = $state(false);

  const statusOptions = ['pending', 'approved', 'rejected', ''];

  let op = $derived(overview?.op || null);
  let opRunning = $derived(!!op?.running);

  onMount(load);
  onDestroy(stopPolling);

  function stopPolling() {
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  function syncPolling() {
    if (opRunning && !pollTimer) {
      pollTimer = setInterval(refreshOverview, 3000);
    } else if (!opRunning) {
      stopPolling();
    }
  }

  async function load() {
    loading = true;
    loadError = '';
    try {
      overview = await getMediaCatalogOverview();
      syncPolling();
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
      syncPolling();
    } catch {
      stopPolling();
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

  async function seed(categoryId) {
    busy = true;
    try {
      await seedMediaCatalog(categoryId || '');
      toast(t('catalogSeedStarted'), 'success');
      await refreshOverview();
    } catch (e) {
      toast(e?.message || String(e), 'error');
    } finally {
      busy = false;
    }
  }

  async function rebuild(categoryId) {
    busy = true;
    try {
      await rebuildMediaSnapshots(categoryId || '');
      toast(t('catalogRebuildStarted'), 'success');
      await refreshOverview();
    } catch (e) {
      toast(e?.message || String(e), 'error');
    } finally {
      busy = false;
    }
  }

  async function seedAll() {
    if (!(await showConfirm(t('confirmSeedAll')))) return;
    await seed('');
  }
  async function rebuildAll() {
    if (!(await showConfirm(t('confirmRebuildAll')))) return;
    await rebuild('');
  }

  async function loadCuration() {
    curationLoading = true;
    try {
      const data = await getMediaCuration();
      curationFile = data.file || '';
      curationText = JSON.stringify(data.curation || { categories: {} }, null, 2);
      curationLoaded = true;
    } catch (e) {
      toast(e?.message || String(e), 'error');
    } finally {
      curationLoading = false;
    }
  }

  async function toggleCuration() {
    curationOpen = !curationOpen;
    if (curationOpen && !curationLoaded) await loadCuration();
  }

  async function saveCuration() {
    let parsed;
    try {
      parsed = JSON.parse(curationText);
    } catch {
      toast(t('curationInvalidJson'), 'error');
      return;
    }
    curationSaving = true;
    try {
      const data = await saveMediaCuration(parsed);
      curationText = JSON.stringify(data.curation || { categories: {} }, null, 2);
      toast(t('curationSaved'), 'success');
    } catch (e) {
      toast(e?.message || String(e), 'error');
    } finally {
      curationSaving = false;
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

  function fmtTime(iso) {
    if (!iso) return '';
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    return d.toLocaleString();
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

  function opKindLabel(kind) {
    if (kind === 'seed') return t('catalogOpSeed');
    if (kind === 'rebuild') return t('catalogOpRebuild');
    return kind;
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

    {#if op && (op.running || op.message || op.error)}
      <div class="op-bar" class:op-running={op.running} class:op-error={!op.running && op.error}>
        {#if op.running}
          <span class="spinner"></span>
          <span
            >{t('catalogOpRunning', {
              kind: opKindLabel(op.kind),
              category: op.category || 'all',
            })}</span
          >
        {:else if op.error}
          <span>{t('catalogOpFailed', { error: op.error })}</span>
        {:else}
          <span>{t('catalogOpDone', { message: op.message })}</span>
        {/if}
      </div>
    {/if}

    <div class="cat-actions">
      <button
        class="btn btn-sm btn-secondary"
        onclick={seedAll}
        disabled={busy || opRunning || !overview.catalogEnabled}>{t('catalogSeedAll')}</button
      >
      <button class="btn btn-sm btn-secondary" onclick={rebuildAll} disabled={busy || opRunning}
        >{t('catalogRebuildAll')}</button
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
            <th>{t('catalogColSnapshot')}</th>
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
              <td>
                {#if cat.snapshot}
                  <span class="badge badge-blue"
                    >{t('catalogSnapshotItems', {
                      count: cat.snapshot.items,
                      time: fmtTime(cat.snapshot.updatedAt),
                    })}</span
                  >
                {:else}
                  <span class="badge badge-gray">{t('catalogSnapshotNone')}</span>
                {/if}
              </td>
              <td class="actions-col">
                <button
                  class="btn btn-xs btn-ghost"
                  onclick={() => loadItems(cat.id, cat.title || cat.id)}
                  disabled={!overview.catalogEnabled}>{t('catalogReview')}</button
                >
                <button
                  class="btn btn-xs btn-ghost"
                  onclick={() => seed(cat.id)}
                  disabled={busy || opRunning || !overview.catalogEnabled}
                  >{t('catalogSeed')}</button
                >
                <button
                  class="btn btn-xs btn-ghost"
                  onclick={() => rebuild(cat.id)}
                  disabled={busy || opRunning}>{t('catalogRebuild')}</button
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
                </div>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}

    <div class="curation-block">
      <button class="curation-toggle" onclick={toggleCuration}>
        <span>{t('curationTitle')}</span>
        <span class="curation-sign">{curationOpen ? '−' : '+'}</span>
      </button>
      {#if curationOpen}
        <div class="curation-body">
          <div class="helper-text">{t('curationDesc')}</div>
          {#if curationFile}<div class="cat-id">{curationFile}</div>{/if}
          {#if curationLoading}
            <div class="loading-skeleton" style="height:120px;"></div>
          {:else}
            <textarea class="curation-text" bind:value={curationText} spellcheck="false"></textarea>
            <div class="curation-actions">
              <button
                class="btn btn-sm btn-primary"
                onclick={saveCuration}
                disabled={curationSaving}
              >
                {#if curationSaving}<span class="spinner"></span>{/if}
                {t('curationSave')}
              </button>
            </div>
          {/if}
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

  .op-bar {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.6rem 0.9rem;
    margin-bottom: 0.9rem;
    border-radius: var(--radius);
    font-size: 0.85rem;
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border-strong);
    color: var(--c-text);
  }
  .op-bar.op-running {
    background: var(--c-accent-bg);
    border-color: var(--c-accent-border);
  }
  .op-bar.op-error {
    background: var(--c-danger-bg);
    border-color: var(--c-danger);
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
  .curation-actions {
    margin-top: 0.6rem;
  }
</style>
