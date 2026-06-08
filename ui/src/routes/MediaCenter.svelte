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
    categories: [],
  };

  let config = $state(clone(emptyConfig));
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

  function updateCategory(index, patch) {
    const categories = [...config.categories];
    categories[index] = { ...categories[index], ...patch };
    config = { ...config, categories };
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
  .page-subtitle {
    color: var(--c-text-secondary);
    font-size: 0.78rem;
    margin-top: 0.25rem;
  }

  .form-grid,
  .hint-grid,
  .textarea-grid {
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
    border-color: rgba(245, 158, 11, 0.3);
    color: #fbbf24;
  }

  .warning-card ul {
    margin: 0.75rem 0 0 1rem;
    font-size: 0.78rem;
    line-height: 1.6;
  }

  .category-list {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .category-card {
    background: rgba(15, 23, 42, 0.5);
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

  @media (max-width: 760px) {
    .category-header {
      flex-direction: column;
    }
  }
</style>
