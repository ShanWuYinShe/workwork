<script>
  import { onMount } from 'svelte';
  import { getMediaCenterConfig, saveMediaCenterConfig } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import { toast } from '../lib/toast.svelte.js';

  const emptyConfig = {
    service: {
      upstream: 'media_server:8787',
      public_path: '/api/media',
    },
    sources: [],
    categories: [],
  };

  let uidCounter = 0;
  const nextUid = () => `c${(uidCounter += 1)}`;

  let config = $state(clone(emptyConfig));
  let defaultSources = $state([]);
  let defaultCategories = $state([]);
  let warnings = $state([]);
  let loading = $state(true);
  let saving = $state(false);
  let expanded = $state(new Set());

  let appPublicURL = $derived(`${window.location.origin}${publicPathForDisplay()}`);
  let enabledSourceCount = $derived((config.sources || []).filter((s) => s.enabled).length);
  let allExpanded = $derived(
    config.categories.length > 0 && expanded.size >= config.categories.length,
  );

  onMount(load);

  async function load() {
    loading = true;
    warnings = [];
    try {
      const data = await getMediaCenterConfig();
      config = withUids(clone(data.config || emptyConfig));
      defaultSources = clone(data.default_sources || []);
      defaultCategories = clone(data.default_categories || []);
      expanded = new Set();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      loading = false;
    }
  }

  async function save() {
    saving = true;
    warnings = [];
    try {
      const result = await saveMediaCenterConfig(toPayload(config));
      config = withUids(clone(result.config || config));
      expanded = new Set();
      warnings = result.warnings || [];
      if (warnings.length > 0) {
        toast(t('mediaCenterSavedWithWarnings'), 'error', 4000);
      } else {
        toast(t('mediaCenterSaved'), 'success');
      }
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      saving = false;
    }
  }

  function clone(value) {
    return JSON.parse(JSON.stringify(value));
  }

  function withUids(cfg) {
    return {
      ...cfg,
      categories: (cfg.categories || []).map((category) => ({
        ...category,
        _uid: nextUid(),
      })),
    };
  }

  function toPayload(cfg) {
    return {
      ...cfg,
      categories: (cfg.categories || []).map(({ _uid, ...rest }) => rest),
    };
  }

  function publicPathForDisplay() {
    const path = config.service?.public_path || '/api/media';
    return path.startsWith('/') ? path : `/${path}`;
  }

  function updateService(field, value) {
    config = {
      ...config,
      service: { ...config.service, [field]: value },
    };
  }

  function updateSource(index, patch) {
    const sources = [...(config.sources || [])];
    sources[index] = { ...sources[index], ...patch };
    config = { ...config, sources };
  }

  function updateCategory(index, patch) {
    const categories = [...config.categories];
    categories[index] = { ...categories[index], ...patch };
    config = { ...config, categories };
  }

  function updateCategorySourceQuery(index, sourceId, value) {
    const category = config.categories[index];
    const sourceQueries = { ...(category.sourceQueries || {}) };
    if (value.trim()) {
      sourceQueries[sourceId] = value;
    } else {
      delete sourceQueries[sourceId];
    }
    updateCategory(index, {
      sourceQueries: Object.keys(sourceQueries).length > 0 ? sourceQueries : undefined,
    });
  }

  function listFromText(value) {
    return value
      .split('\n')
      .map((item) => item.trim())
      .filter(Boolean);
  }

  function updateCategoryList(index, field, value) {
    updateCategory(index, { [field]: listFromText(value) });
  }

  function toggleExpanded(uid) {
    if (expanded.has(uid)) {
      expanded.delete(uid);
    } else {
      expanded.add(uid);
    }
  }

  function setAllExpanded(open) {
    expanded = open ? new Set(config.categories.map((c) => c._uid)) : new Set();
  }

  function addCategory() {
    const existing = new Set(config.categories.map((category) => category.id));
    let n = config.categories.length + 1;
    let id = `custom-${n}`;
    while (existing.has(id)) {
      n += 1;
      id = `custom-${n}`;
    }
    const uid = nextUid();
    config = {
      ...config,
      categories: [
        ...config.categories,
        {
          _uid: uid,
          id,
          title: t('newCategory'),
          description: t('newCategoryDesc'),
          searchQuery: '',
          sourceQueries: {},
          keywords: [t('newCategoryKeyword')],
          excludes: [],
        },
      ],
    };
    expanded.add(uid);
  }

  async function removeCategory(index) {
    const category = config.categories[index];
    if (
      !(await showConfirm(
        t('confirmDeleteCategory', { title: category?.title || category?.id || '' }),
      ))
    )
      return;
    if (category?._uid) expanded.delete(category._uid);
    config = {
      ...config,
      categories: config.categories.filter((_, i) => i !== index),
    };
  }

  function moveCategory(index, delta) {
    const nextIndex = index + delta;
    if (nextIndex < 0 || nextIndex >= config.categories.length) return;
    const categories = [...config.categories];
    const [category] = categories.splice(index, 1);
    categories.splice(nextIndex, 0, category);
    config = { ...config, categories };
  }

  async function resetCategories() {
    if (!(await showConfirm(t('confirmResetCategories')))) return;
    config = {
      ...config,
      categories: withUids({ categories: clone(defaultCategories) }).categories,
    };
    expanded = new Set();
  }

  async function resetSources() {
    if (!(await showConfirm(t('confirmResetSources')))) return;
    config = { ...config, sources: clone(defaultSources) };
  }

  function sourceRiskLabel(level) {
    if (level === 'low') return t('riskLow');
    if (level === 'medium') return t('riskMedium');
    return t('riskHigh');
  }

  function sourceRiskBadge(level) {
    if (level === 'low') return 'badge-green';
    if (level === 'medium') return 'badge-yellow';
    return 'badge-red';
  }

  function categoryKeywordCount(category) {
    return (category.keywords || []).length;
  }

  function categoryExcludeCount(category) {
    return (category.excludes || []).length;
  }

  function categorySourceTuned(category) {
    return Object.keys(category.sourceQueries || {}).length > 0;
  }
</script>

<div class="page">
  <div class="page-header">
    <div>
      <h1 class="page-title">{t('mediaCenter')}</h1>
      <div class="page-subtitle">{t('mediaCenterSubtitle')}</div>
    </div>
    <div class="page-actions">
      <button class="btn btn-sm btn-ghost" onclick={load} disabled={loading || saving}>
        {t('refresh')}
      </button>
      <button class="btn btn-sm btn-primary" onclick={save} disabled={loading || saving}>
        {#if saving}<span class="spinner"></span>{/if}
        {saving ? t('saving') : t('saveAndApply')}
      </button>
    </div>
  </div>

  {#if loading}
    <div class="card"><div class="loading-skeleton" style="height:120px;"></div></div>
    <div class="card"><div class="loading-skeleton" style="height:240px;"></div></div>
  {:else}
    {#if warnings.length}
      <div class="card warning-card">
        <div class="card-title">{t('warnings')}</div>
        <ul>
          {#each warnings as warning}
            <li>{warning}</li>
          {/each}
        </ul>
      </div>
    {/if}

    <!-- Public access — the only editable runtime knob -->
    <div class="card">
      <div class="card-header">
        <div>
          <div class="card-title">{t('mediaServiceRuntime')}</div>
          <div class="card-subtitle">{t('mediaServiceRuntimeDesc')}</div>
        </div>
      </div>

      <div class="access-row">
        <div class="form-group access-prefix">
          <label for="media-public-path">{t('publicApiPrefix')}</label>
          <input
            id="media-public-path"
            value={config.service.public_path}
            oninput={(e) => updateService('public_path', e.currentTarget.value)}
            placeholder="/api/media"
          />
        </div>
        <div class="access-url">
          <span class="access-url-label">{t('mediaPublicUrl')}</span>
          <code>{appPublicURL}</code>
        </div>
      </div>
    </div>

    <!-- Sources — known sources, only the on/off switch is editable -->
    <div class="card">
      <div class="card-header">
        <div>
          <div class="card-title">
            {t('mediaSources')}
            <span class="card-title-count">
              {t('sourcesEnabledCount', {
                count: enabledSourceCount,
                total: (config.sources || []).length,
              })}
            </span>
          </div>
          <div class="card-subtitle">{t('mediaSourcesDesc')}</div>
        </div>
        <div class="card-actions">
          <button
            class="btn btn-sm btn-ghost"
            onclick={resetSources}
            disabled={defaultSources.length === 0}
          >
            {t('resetDefaultSources')}
          </button>
        </div>
      </div>

      <div class="source-grid">
        {#each config.sources || [] as source, index}
          <section class="source-card" class:source-on={source.enabled}>
            <div class="source-top">
              <div class="source-id-row">
                <span class="source-name">{source.title || source.id}</span>
                <span class={`badge ${sourceRiskBadge(source.riskLevel)}`}>
                  {sourceRiskLabel(source.riskLevel)}
                </span>
              </div>
              <button
                type="button"
                class="switch"
                class:switch-on={source.enabled}
                role="switch"
                aria-checked={source.enabled}
                aria-label={source.title || source.id}
                onclick={() => updateSource(index, { enabled: !source.enabled })}
              >
                <span class="switch-knob"></span>
              </button>
            </div>
            <div class="source-desc">{source.description}</div>
            <div class="source-foot">
              <span class="source-id-tag">{source.id}</span>
              {#if source.homeUrl}
                <a class="source-link" href={source.homeUrl} target="_blank" rel="noreferrer">
                  {t('sourceHomepage')} ↗
                </a>
              {/if}
            </div>
          </section>
        {/each}
      </div>
    </div>

    <!-- Categories — the main editable content -->
    <div class="card">
      <div class="card-header">
        <div>
          <div class="card-title">
            {t('mediaCategories')}
            <span class="card-title-count">{config.categories.length}</span>
          </div>
          <div class="card-subtitle">{t('mediaCategoriesDesc')}</div>
        </div>
        <div class="card-actions">
          {#if config.categories.length > 0}
            <button class="btn btn-sm btn-ghost" onclick={() => setAllExpanded(!allExpanded)}>
              {allExpanded ? t('collapseAll') : t('expandAll')}
            </button>
          {/if}
          <button
            class="btn btn-sm btn-ghost"
            onclick={resetCategories}
            disabled={defaultCategories.length === 0}
          >
            {t('resetDefaults')}
          </button>
          <button class="btn btn-sm btn-success" onclick={addCategory}>{t('addCategory')}</button>
        </div>
      </div>

      <div class="category-list">
        {#each config.categories as category, index (category._uid)}
          {@const open = expanded.has(category._uid)}
          <section class="category-card" class:is-open={open}>
            <div class="category-summary">
              <button
                type="button"
                class="category-toggle"
                aria-expanded={open}
                onclick={() => toggleExpanded(category._uid)}
              >
                <span class="chevron" class:chevron-open={open}>›</span>
                <span class="category-title">{category.title || category.id}</span>
                <span class="category-id-tag">{category.id}</span>
              </button>

              <div class="category-meta">
                {#if !open}
                  <span class="meta-pill"
                    >{t('categoryKeywordsCount', { count: categoryKeywordCount(category) })}</span
                  >
                  {#if categoryExcludeCount(category) > 0}
                    <span class="meta-pill"
                      >{t('categoryExcludesCount', { count: categoryExcludeCount(category) })}</span
                    >
                  {/if}
                  {#if categorySourceTuned(category)}
                    <span class="meta-pill meta-pill-accent">{t('categorySourceTuned')}</span>
                  {/if}
                {/if}
              </div>

              <div class="category-controls">
                <button
                  class="btn btn-icon btn-ghost"
                  title="↑"
                  onclick={() => moveCategory(index, -1)}
                  disabled={index === 0}>↑</button
                >
                <button
                  class="btn btn-icon btn-ghost"
                  title="↓"
                  onclick={() => moveCategory(index, 1)}
                  disabled={index === config.categories.length - 1}>↓</button
                >
                <button class="btn btn-sm btn-danger" onclick={() => removeCategory(index)}>
                  {t('delete')}
                </button>
              </div>
            </div>

            {#if open}
              <div class="category-body">
                <div class="form-grid">
                  <div class="form-group">
                    <label for={`category-${category._uid}-id`}>{t('categoryId')}</label>
                    <input
                      id={`category-${category._uid}-id`}
                      value={category.id}
                      oninput={(e) => updateCategory(index, { id: e.currentTarget.value })}
                    />
                  </div>
                  <div class="form-group">
                    <label for={`category-${category._uid}-title`}>{t('categoryTitle')}</label>
                    <input
                      id={`category-${category._uid}-title`}
                      value={category.title}
                      oninput={(e) => updateCategory(index, { title: e.currentTarget.value })}
                    />
                  </div>
                  <div class="form-group">
                    <label for={`category-${category._uid}-query`}>{t('searchQuery')}</label>
                    <input
                      id={`category-${category._uid}-query`}
                      value={category.searchQuery || ''}
                      oninput={(e) => updateCategory(index, { searchQuery: e.currentTarget.value })}
                      placeholder={category.keywords?.[0] || ''}
                    />
                  </div>
                </div>

                <div class="form-group">
                  <label for={`category-${category._uid}-description`}
                    >{t('categoryDescription')}</label
                  >
                  <input
                    id={`category-${category._uid}-description`}
                    value={category.description}
                    oninput={(e) => updateCategory(index, { description: e.currentTarget.value })}
                  />
                </div>

                {#if config.sources?.length}
                  <div class="field-section">
                    <div class="source-query-grid">
                      {#each config.sources as source}
                        <div class="form-group">
                          <label for={`category-${category._uid}-source-${source.id}`}>
                            {t('sourceSearchQuery', { source: source.title || source.id })}
                          </label>
                          <input
                            id={`category-${category._uid}-source-${source.id}`}
                            value={category.sourceQueries?.[source.id] || ''}
                            oninput={(e) =>
                              updateCategorySourceQuery(index, source.id, e.currentTarget.value)}
                            placeholder={category.searchQuery || category.keywords?.[0] || ''}
                          />
                        </div>
                      {/each}
                    </div>
                  </div>
                {/if}

                <div class="textarea-grid">
                  <div class="form-group">
                    <label for={`category-${category._uid}-keywords`}
                      >{t('keywordsOnePerLine')}</label
                    >
                    <textarea
                      id={`category-${category._uid}-keywords`}
                      rows="6"
                      value={(category.keywords || []).join('\n')}
                      oninput={(e) => updateCategoryList(index, 'keywords', e.currentTarget.value)}
                    ></textarea>
                  </div>
                  <div class="form-group">
                    <label for={`category-${category._uid}-excludes`}
                      >{t('excludesOnePerLine')}</label
                    >
                    <textarea
                      id={`category-${category._uid}-excludes`}
                      rows="6"
                      value={(category.excludes || []).join('\n')}
                      oninput={(e) => updateCategoryList(index, 'excludes', e.currentTarget.value)}
                    ></textarea>
                  </div>
                </div>
              </div>
            {/if}
          </section>
        {:else}
          <div class="empty-state">{t('noMediaCategories')}</div>
        {/each}
      </div>
    </div>
  {/if}
</div>

<style>
  .form-grid,
  .textarea-grid,
  .source-query-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: 0.75rem;
  }

  .warning-card {
    border-color: var(--c-warning);
    color: #fbbf24;
  }
  .warning-card ul {
    margin: 0.75rem 0 0 1rem;
    font-size: 0.78rem;
    line-height: 1.6;
  }

  /* ── Public access ── */
  .access-row {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-end;
    gap: 1rem;
  }
  .access-prefix {
    flex: 0 0 240px;
    max-width: 100%;
  }
  .access-url {
    flex: 1 1 260px;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .access-url-label {
    font-size: 0.65rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--c-text-muted);
  }
  .access-url code {
    display: block;
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border);
    border-radius: var(--radius);
    color: var(--c-accent);
    font-family: 'SF Mono', 'Fira Code', ui-monospace, monospace;
    font-size: 0.75rem;
    padding: 0.5rem 0.65rem;
    overflow-x: auto;
    white-space: nowrap;
  }

  /* ── Sources ── */
  .source-grid {
    display: grid;
    gap: 0.75rem;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  }
  .source-card {
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-lg);
    padding: 0.9rem 1rem;
    transition: border-color var(--transition);
  }
  .source-card.source-on {
    border-color: var(--c-accent-border);
  }
  .source-top {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 0.75rem;
  }
  .source-id-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
  }
  .source-name {
    color: var(--c-text-heading);
    font-weight: 600;
    font-size: 0.85rem;
  }
  .source-desc {
    color: var(--c-text-secondary);
    font-size: 0.75rem;
    line-height: 1.5;
    margin-top: 0.6rem;
  }
  .source-foot {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.5rem;
    margin-top: 0.7rem;
  }
  .source-id-tag {
    font-family: 'SF Mono', 'Fira Code', ui-monospace, monospace;
    font-size: 0.68rem;
    color: var(--c-text-muted);
  }
  .source-link {
    color: var(--c-accent);
    font-size: 0.72rem;
    text-decoration: none;
  }
  .source-link:hover {
    text-decoration: underline;
  }

  /* ── Toggle switch ── */
  .switch {
    flex-shrink: 0;
    width: 38px;
    height: 22px;
    border-radius: 999px;
    background: var(--c-bg-hover);
    border: 1px solid var(--c-border-strong);
    padding: 0;
    cursor: pointer;
    position: relative;
    transition:
      background var(--transition),
      border-color var(--transition);
  }
  .switch-knob {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: var(--c-text-secondary);
    transition:
      transform var(--transition),
      background var(--transition);
  }
  .switch-on {
    background: var(--c-accent);
    border-color: var(--c-accent);
  }
  .switch-on .switch-knob {
    transform: translateX(16px);
    background: #fff;
  }
  .switch:focus-visible {
    outline: none;
    box-shadow: 0 0 0 3px var(--c-accent-bg);
  }

  /* ── Categories ── */
  .category-list {
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .category-card {
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-lg);
    overflow: hidden;
    transition: border-color var(--transition);
  }
  .category-card.is-open {
    border-color: var(--c-border-strong);
  }
  .category-summary {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.7rem 0.85rem;
  }
  .category-toggle {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    background: none;
    border: none;
    cursor: pointer;
    padding: 0;
    color: inherit;
    font-family: inherit;
    text-align: left;
    min-width: 0;
    flex-shrink: 1;
  }
  .chevron {
    color: var(--c-text-muted);
    font-size: 1.1rem;
    line-height: 1;
    transition: transform var(--transition);
    display: inline-block;
  }
  .chevron-open {
    transform: rotate(90deg);
    color: var(--c-accent);
  }
  .category-title {
    color: var(--c-text-heading);
    font-size: 0.85rem;
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .category-id-tag {
    font-family: 'SF Mono', 'Fira Code', ui-monospace, monospace;
    font-size: 0.65rem;
    color: var(--c-text-muted);
    background: var(--c-bg-elevated);
    padding: 0.1rem 0.4rem;
    border-radius: var(--radius-sm);
    flex-shrink: 0;
  }
  .category-meta {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    flex-wrap: wrap;
    margin-left: auto;
  }
  .meta-pill {
    font-size: 0.65rem;
    color: var(--c-text-secondary);
    background: var(--c-bg-elevated);
    padding: 0.12rem 0.45rem;
    border-radius: 999px;
    white-space: nowrap;
  }
  .meta-pill-accent {
    color: var(--c-accent);
    background: var(--c-accent-bg);
  }
  .category-controls {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    flex-shrink: 0;
    margin-left: auto;
  }
  .category-body {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    padding: 0.25rem 0.85rem 0.95rem;
    border-top: 1px solid var(--c-border);
  }
  .field-section {
    border-top: 1px dashed var(--c-border);
    padding-top: 0.75rem;
  }

  @media (max-width: 760px) {
    .category-meta {
      display: none;
    }
    .category-summary {
      flex-wrap: wrap;
    }
  }
</style>
