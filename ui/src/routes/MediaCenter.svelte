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

  let config = $state(clone(emptyConfig));
  let defaultSources = $state([]);
  let defaultCategories = $state([]);
  let runtimeFile = $state('');
  let warnings = $state([]);
  let loading = $state(true);
  let saving = $state(false);

  let appApiBaseURL = $derived(`${window.location.origin}${publicPathForDisplay()}`);

  onMount(load);

  async function load() {
    loading = true;
    warnings = [];
    try {
      const data = await getMediaCenterConfig();
      config = clone(data.config || emptyConfig);
      defaultSources = clone(data.default_sources || []);
      defaultCategories = clone(data.default_categories || []);
      runtimeFile = data.runtime_file || '';
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
      const result = await saveMediaCenterConfig(config);
      config = clone(result.config || config);
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

  function publicPathForDisplay() {
    const path = config.service?.public_path || '/api/media';
    return path.startsWith('/') ? path : `/${path}`;
  }

  function updateService(field, value) {
    config = {
      ...config,
      service: {
        ...config.service,
        [field]: value,
      },
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
    const trimmed = value.trim();
    if (trimmed) {
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

  function addCategory() {
    const existing = new Set(config.categories.map((category) => category.id));
    let n = config.categories.length + 1;
    let id = `custom-${n}`;
    while (existing.has(id)) {
      n += 1;
      id = `custom-${n}`;
    }
    config = {
      ...config,
      categories: [
        ...config.categories,
        {
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
  }

  async function removeCategory(index) {
    const category = config.categories[index];
    if (
      !(await showConfirm(
        t('confirmDeleteCategory', { title: category?.title || category?.id || '' }),
      ))
    )
      return;
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
      categories: clone(defaultCategories),
    };
  }

  async function resetSources() {
    if (!(await showConfirm(t('confirmResetSources')))) return;
    config = {
      ...config,
      sources: clone(defaultSources),
    };
  }

  function sourceRiskLabel(level) {
    if (level === 'low') return t('riskLow');
    if (level === 'medium') return t('riskMedium');
    return t('riskHigh');
  }

  function sourceRiskClass(level) {
    if (level === 'low') return 'risk-low';
    if (level === 'medium') return 'risk-medium';
    return 'risk-high';
  }
</script>

<div class="page">
  <div class="page-header">
    <div>
      <h1 class="page-title">{t('mediaCenter')}</h1>
      <div class="page-subtitle">{t('mediaCenterSubtitle')}</div>
    </div>
    <div class="page-actions">
      <button class="btn btn-sm btn-ghost" onclick={load} disabled={loading || saving}
        >{t('refresh')}</button
      >
      <button class="btn btn-sm btn-primary" onclick={save} disabled={loading || saving}>
        {#if saving}<span class="spinner"></span>{/if}
        {saving ? t('saving') : t('saveAndApply')}
      </button>
    </div>
  </div>

  {#if loading}
    <div class="card"><div class="loading-skeleton" style="height:180px;"></div></div>
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

    <div class="card">
      <div class="card-header">
        <div>
          <div class="card-title">{t('mediaServiceRuntime')}</div>
          <div class="card-subtitle">{t('mediaServiceRuntimeDesc')}</div>
        </div>
      </div>

      <div class="form-grid">
        <div class="form-group">
          <label for="media-upstream">{t('nginxUpstream')}</label>
          <input
            id="media-upstream"
            value={config.service.upstream}
            oninput={(e) => updateService('upstream', e.currentTarget.value)}
            placeholder="media_server:8787"
          />
        </div>
        <div class="form-group">
          <label for="media-public-path">{t('publicApiPrefix')}</label>
          <input
            id="media-public-path"
            value={config.service.public_path}
            oninput={(e) => updateService('public_path', e.currentTarget.value)}
            placeholder="/api/media"
          />
        </div>
      </div>

      <div class="hint-grid">
        <div>
          <div class="hint-label">{t('appApiBaseUrl')}</div>
          <code>./gradlew :app:assembleDebug -PMEDIA_SERVER_URL={appApiBaseURL}</code>
        </div>
      </div>

      {#if runtimeFile}
        <div class="runtime-file">
          {t('runtimeCategoryFile')}: <span class="td-mono">{runtimeFile}</span>
        </div>
      {/if}
    </div>

    <div class="card">
      <div class="card-header">
        <div>
          <div class="card-title">{t('mediaSources')}</div>
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
          <section class="source-card" class:source-disabled={!source.enabled}>
            <div class="source-header">
              <label class="source-toggle">
                <input
                  type="checkbox"
                  checked={source.enabled}
                  onchange={(e) => updateSource(index, { enabled: e.currentTarget.checked })}
                />
                <span>{source.title || source.id}</span>
              </label>
              <span class={`risk-pill ${sourceRiskClass(source.riskLevel)}`}>
                {sourceRiskLabel(source.riskLevel)}
              </span>
            </div>
            <div class="source-id">{source.id}</div>
            <div class="source-desc">{source.description}</div>
            {#if source.homeUrl}
              <a class="source-link" href={source.homeUrl} target="_blank" rel="noreferrer">
                {t('sourceHomepage')}
              </a>
            {/if}
          </section>
        {/each}
      </div>
    </div>

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
        {#each config.categories as category, index}
          <section class="category-card">
            <div class="category-header">
              <div>
                <div class="category-title">{category.title || category.id}</div>
                <div class="td-muted">{category.id}</div>
              </div>
              <div class="td-actions">
                <button
                  class="btn btn-sm btn-ghost"
                  onclick={() => moveCategory(index, -1)}
                  disabled={index === 0}
                >
                  ↑
                </button>
                <button
                  class="btn btn-sm btn-ghost"
                  onclick={() => moveCategory(index, 1)}
                  disabled={index === config.categories.length - 1}
                >
                  ↓
                </button>
                <button class="btn btn-sm btn-danger" onclick={() => removeCategory(index)}
                  >{t('delete')}</button
                >
              </div>
            </div>

            <div class="form-grid category-fields">
              <div class="form-group">
                <label for={`category-${index}-id`}>{t('categoryId')}</label>
                <input
                  id={`category-${index}-id`}
                  value={category.id}
                  oninput={(e) => updateCategory(index, { id: e.currentTarget.value })}
                />
              </div>
              <div class="form-group">
                <label for={`category-${index}-title`}>{t('categoryTitle')}</label>
                <input
                  id={`category-${index}-title`}
                  value={category.title}
                  oninput={(e) => updateCategory(index, { title: e.currentTarget.value })}
                />
              </div>
              <div class="form-group">
                <label for={`category-${index}-query`}>{t('searchQuery')}</label>
                <input
                  id={`category-${index}-query`}
                  value={category.searchQuery || ''}
                  oninput={(e) => updateCategory(index, { searchQuery: e.currentTarget.value })}
                  placeholder={category.keywords?.[0] || ''}
                />
              </div>
            </div>

            {#if config.sources?.length}
              <div class="source-query-grid">
                {#each config.sources as source}
                  <div class="form-group">
                    <label for={`category-${index}-source-${source.id}`}>
                      {t('sourceSearchQuery', { source: source.title || source.id })}
                    </label>
                    <input
                      id={`category-${index}-source-${source.id}`}
                      value={category.sourceQueries?.[source.id] || ''}
                      oninput={(e) =>
                        updateCategorySourceQuery(index, source.id, e.currentTarget.value)}
                      placeholder={category.searchQuery || category.keywords?.[0] || ''}
                    />
                  </div>
                {/each}
              </div>
            {/if}

            <div class="form-group">
              <label for={`category-${index}-description`}>{t('categoryDescription')}</label>
              <input
                id={`category-${index}-description`}
                value={category.description}
                oninput={(e) => updateCategory(index, { description: e.currentTarget.value })}
              />
            </div>

            <div class="textarea-grid">
              <div class="form-group">
                <label for={`category-${index}-keywords`}>{t('keywordsOnePerLine')}</label>
                <textarea
                  id={`category-${index}-keywords`}
                  rows="7"
                  value={(category.keywords || []).join('\n')}
                  oninput={(e) => updateCategoryList(index, 'keywords', e.currentTarget.value)}
                ></textarea>
              </div>
              <div class="form-group">
                <label for={`category-${index}-excludes`}>{t('excludesOnePerLine')}</label>
                <textarea
                  id={`category-${index}-excludes`}
                  rows="7"
                  value={(category.excludes || []).join('\n')}
                  oninput={(e) => updateCategoryList(index, 'excludes', e.currentTarget.value)}
                ></textarea>
              </div>
            </div>
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
  .hint-grid,
  .textarea-grid,
  .source-query-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    gap: 0.75rem;
  }

  .hint-grid {
    margin-top: 1rem;
  }

  .hint-grid code {
    display: block;
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border);
    border-radius: var(--radius);
    color: var(--c-text);
    font-family: 'SF Mono', 'Fira Code', ui-monospace, monospace;
    font-size: 0.72rem;
    line-height: 1.5;
    margin-top: 0.35rem;
    overflow-x: auto;
    padding: 0.65rem;
    white-space: nowrap;
  }

  .hint-label,
  .runtime-file {
    color: var(--c-text-muted);
    font-size: 0.72rem;
  }

  .runtime-file {
    margin-top: 1rem;
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

  .source-grid {
    display: grid;
    gap: 0.75rem;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  }

  .source-card {
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-lg);
    padding: 1rem;
  }

  .source-disabled {
    opacity: 0.65;
  }

  .source-header {
    align-items: center;
    display: flex;
    gap: 0.75rem;
    justify-content: space-between;
  }

  .source-toggle {
    align-items: center;
    color: var(--c-text-heading);
    display: inline-flex;
    font-weight: 600;
    gap: 0.5rem;
  }

  .source-id,
  .source-desc,
  .source-link {
    color: var(--c-text-muted);
    font-size: 0.75rem;
    line-height: 1.5;
    margin-top: 0.5rem;
  }

  .source-link {
    color: var(--c-accent);
    display: inline-block;
    text-decoration: none;
  }

  .risk-pill {
    border-radius: 999px;
    font-size: 0.68rem;
    font-weight: 700;
    padding: 0.2rem 0.5rem;
  }

  .risk-low {
    background: rgba(34, 197, 94, 0.12);
    color: #22c55e;
  }

  .risk-medium {
    background: rgba(245, 158, 11, 0.14);
    color: #f59e0b;
  }

  .risk-high {
    background: rgba(239, 68, 68, 0.14);
    color: #ef4444;
  }

  .category-list {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .category-card {
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-lg);
    padding: 1rem;
  }

  .category-header {
    align-items: flex-start;
    display: flex;
    gap: 0.75rem;
    justify-content: space-between;
    margin-bottom: 1rem;
  }

  .category-title {
    color: var(--c-text-heading);
    font-size: 0.9rem;
    font-weight: 600;
  }

  .category-fields {
    margin-bottom: 0.75rem;
  }

  .source-query-grid {
    margin-bottom: 0.75rem;
  }

  @media (max-width: 760px) {
    .category-header {
      flex-direction: column;
    }
  }
</style>
