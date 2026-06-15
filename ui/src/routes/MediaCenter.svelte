<script>
  import { onMount } from 'svelte';
  import {
    getMediaAppRelease,
    getMediaCenterConfig,
    saveMediaCenterConfig,
    uploadMediaAppRelease,
  } from '../lib/api.js';
  import { t } from '../lib/i18n.svelte.js';
  import { showConfirm } from '../lib/confirm.svelte.js';
  import { toast } from '../lib/toast.svelte.js';
  import MediaCatalog from './MediaCatalog.svelte';

  const defaultLimits = {
    max_categories: 50,
    max_sources: 10,
    max_keywords_per_category: 100,
    max_excludes_per_category: 200,
    max_source_queries_per_rule: 10,
  };

  const emptyConfig = {
    service: {
      upstream: 'media_server:8787',
      public_path: '/api/media',
    },
    sources: [],
    categories: [],
  };

  const categoryIdPattern = /^[a-z0-9][a-z0-9-]{0,79}$/;
  const publicPathPattern = /^\/[A-Za-z0-9._~/-]*$/;
  const upstreamPattern = /^[A-Za-z0-9._-]+:[0-9]{1,5}$/;
  const reservedPublicPathPrefixes = ['/admin', '/api/admin', '/api/transfer', '/proxy', '/assets'];
  const reservedPublicPaths = new Set(['/favicon.svg', '/icons.svg']);

  let uidCounter = 0;
  const nextUid = () => `c${(uidCounter += 1)}`;

  let config = $state(clone(emptyConfig));
  let defaultSources = $state([]);
  let defaultCategories = $state([]);
  let configFiles = $state({ admin_config: '', runtime_config: '', defaults: '' });
  let limits = $state({ ...defaultLimits });
  let warnings = $state([]);
  let loading = $state(true);
  let saving = $state(false);
  let expandedIds = $state([]);
  let appRelease = $state(null);
  let appReleaseLoading = $state(false);
  let appReleaseUploading = $state(false);
  let appReleaseForm = $state({ versionCode: '', versionName: '', releaseNotes: '' });
  let appReleaseFile = $state(null);
  let activeTab = $state('runtime');

  const mediaTabs = [
    { id: 'runtime', labelKey: 'mediaTabRuntime' },
    { id: 'app', labelKey: 'mediaTabApp' },
    { id: 'rules', labelKey: 'mediaTabRules' },
    { id: 'catalog', labelKey: 'mediaTabCatalog' },
  ];

  let appPublicURL = $derived(`${window.location.origin}${publicPathForDisplay()}`);
  let enabledSourceCount = $derived((config.sources || []).filter((s) => s.enabled).length);
  let allExpanded = $derived(
    config.categories.length > 0 && expandedIds.length >= config.categories.length,
  );
  let validationErrors = $derived(validateConfig(config));

  onMount(load);

  async function load() {
    loading = true;
    warnings = [];
    try {
      const data = await getMediaCenterConfig();
      config = withUids(normalizeConfigShape(clone(data.config || emptyConfig)));
      defaultSources = clone(data.default_sources || []);
      defaultCategories = clone(data.default_categories || []);
      configFiles = {
        admin_config: data.files?.admin_config || '',
        runtime_config: data.files?.runtime_config || data.runtime_file || '',
        defaults: data.files?.defaults || '',
      };
      limits = { ...defaultLimits, ...(data.limits || {}) };
      expandedIds = [];
      await loadAppRelease();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      loading = false;
    }
  }

  async function loadAppRelease() {
    appReleaseLoading = true;
    try {
      appRelease = await getMediaAppRelease();
      const manifest = appRelease?.manifest || null;
      if (manifest?.versionCode) {
        appReleaseForm = {
          versionCode: String(manifest.versionCode + 1),
          versionName: '',
          releaseNotes: '',
        };
      }
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      appReleaseLoading = false;
    }
  }

  async function publishAppRelease() {
    if (!appReleaseFile) {
      toast('请选择 APK 文件', 'error');
      return;
    }
    const versionCode = Number(appReleaseForm.versionCode);
    if (!Number.isInteger(versionCode) || versionCode <= 0) {
      toast('versionCode 必须是正整数', 'error');
      return;
    }
    if (!appReleaseForm.versionName.trim()) {
      toast('versionName 不能为空', 'error');
      return;
    }
    const form = new FormData();
    form.set('versionCode', String(versionCode));
    form.set('versionName', appReleaseForm.versionName.trim());
    form.set('releaseNotes', appReleaseForm.releaseNotes.trim());
    form.set('apk', appReleaseFile);
    appReleaseUploading = true;
    try {
      appRelease = await uploadMediaAppRelease(form);
      appReleaseFile = null;
      toast('App 更新已发布', 'success');
      await loadAppRelease();
    } catch (e) {
      toast(e.message, 'error');
    } finally {
      appReleaseUploading = false;
    }
  }

  function formatBytes(bytes) {
    const n = Number(bytes || 0);
    if (!Number.isFinite(n) || n <= 0) return '0 B';
    if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MiB`;
    if (n >= 1024) return `${(n / 1024).toFixed(1)} KiB`;
    return `${n} B`;
  }

  async function save() {
    const errors = validateConfig(config);
    if (errors.length > 0) {
      expandInvalidCategories();
      activeTab = validationTargetTab(config);
      toast(t('mediaCenterValidationFailed'), 'error');
      return;
    }

    saving = true;
    warnings = [];
    try {
      const openCategoryIDs = new Set(
        config.categories.filter((category) => isExpanded(category._uid)).map((category) => category.id),
      );
      const result = await saveMediaCenterConfig(toPayload(config));
      config = withUids(normalizeConfigShape(clone(result.config || config)));
      expandedIds = config.categories
        .filter((category) => openCategoryIDs.has(category.id))
        .map((category) => category._uid);
      warnings = result.warnings || [];
      if (warnings.length > 0) {
        toast(t('mediaCenterSavedWithWarnings'), 'warning', 5000);
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

  function normalizeConfigShape(cfg) {
    return {
      service: { ...emptyConfig.service, ...(cfg.service || {}) },
      sources: cfg.sources || [],
      categories: cfg.categories || [],
    };
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
      service: {
        upstream: cfg.service?.upstream || emptyConfig.service.upstream,
        public_path: cfg.service?.public_path || emptyConfig.service.public_path,
      },
      categories: (cfg.categories || []).map(({ _uid, ...rest }) => rest),
    };
  }

  function publicPathForDisplay() {
    const path = config.service?.public_path || '/api/media';
    const normalized = path.startsWith('/') ? path : `/${path}`;
    return normalized.replace(/\/+$/, '') || '/';
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

  function isExpanded(uid) {
    return expandedIds.includes(uid);
  }

  function toggleExpanded(uid) {
    expandedIds = isExpanded(uid)
      ? expandedIds.filter((id) => id !== uid)
      : [...expandedIds, uid];
  }

  function setAllExpanded(open) {
    expandedIds = open ? config.categories.map((c) => c._uid) : [];
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
    expandedIds = [...expandedIds, uid];
  }

  async function removeCategory(index) {
    const category = config.categories[index];
    if (
      !(await showConfirm(
        t('confirmDeleteCategory', { title: category?.title || category?.id || '' }),
      ))
    )
      return;
    if (category?._uid) expandedIds = expandedIds.filter((id) => id !== category._uid);
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
    expandedIds = [];
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

  function sourceStatusLabel(source) {
    return source.enabled ? t('enabledLabel') : t('disabledLabel');
  }

  function validateConfig(cfg) {
    const errors = [];
    const service = cfg.service || {};
    const publicPath = `${service.public_path || ''}`.trim();
    const normalizedPath = publicPath.startsWith('/') ? publicPath : `/${publicPath}`;
    const trimmedPath = normalizedPath.replace(/\/+$/, '');
    if (!publicPath || trimmedPath === '' || trimmedPath === '/') {
      errors.push(t('invalidPublicPath'));
    } else if (normalizedPath.includes('//') || !publicPathPattern.test(normalizedPath)) {
      errors.push(t('invalidPublicPath'));
    } else if (isReservedPublicPath(trimmedPath)) {
      errors.push(t('reservedPublicPath', { path: trimmedPath }));
    }

    const upstream = `${service.upstream || ''}`.trim();
    if (!upstreamPattern.test(upstream)) {
      errors.push(t('invalidUpstream'));
    } else {
      const port = Number(upstream.split(':').pop());
      if (!Number.isInteger(port) || port < 1 || port > 65535) {
        errors.push(t('invalidUpstream'));
      }
    }

    const sources = cfg.sources || [];
    const sourceIDs = new Set(sources.map((source) => `${source.id || ''}`.trim()).filter(Boolean));
    if (!sources.some((source) => source.enabled)) {
      errors.push(t('noEnabledSources'));
    }
    if (sources.length > limits.max_sources) {
      errors.push(t('tooManySources', { max: limits.max_sources }));
    }

    const categories = cfg.categories || [];
    if (categories.length === 0) {
      errors.push(t('categoriesRequired'));
    }
    if (categories.length > limits.max_categories) {
      errors.push(t('tooManyCategories', { max: limits.max_categories }));
    }

    const seenIDs = new Map();
    categories.forEach((category, index) => {
      const number = index + 1;
      const label = category.title || category.id || `#${number}`;
      const id = `${category.id || ''}`.trim();
      if (!categoryIdPattern.test(id)) {
        errors.push(t('invalidCategoryId', { label }));
      } else if (seenIDs.has(id)) {
        errors.push(t('duplicateCategoryId', { id }));
      }
      seenIDs.set(id, true);

      if (!`${category.title || ''}`.trim()) {
        errors.push(t('categoryTitleRequired', { label }));
      }
      if (!`${category.description || ''}`.trim()) {
        errors.push(t('categoryDescRequired', { label }));
      }
      if ((category.keywords || []).length === 0) {
        errors.push(t('categoryKeywordsRequired', { label }));
      }
      if ((category.keywords || []).length > limits.max_keywords_per_category) {
        errors.push(
          t('categoryTooManyKeywords', {
            label,
            max: limits.max_keywords_per_category,
          }),
        );
      }
      if ((category.excludes || []).length > limits.max_excludes_per_category) {
        errors.push(
          t('categoryTooManyExcludes', {
            label,
            max: limits.max_excludes_per_category,
          }),
        );
      }
      const sourceQueries = category.sourceQueries || {};
      const sourceQueryIDs = Object.keys(sourceQueries);
      if (sourceQueryIDs.length > limits.max_source_queries_per_rule) {
        errors.push(
          t('categoryTooManySourceQueries', {
            label,
            max: limits.max_source_queries_per_rule,
          }),
        );
      }
      sourceQueryIDs.forEach((sourceID) => {
        if (!sourceIDs.has(sourceID)) {
          errors.push(t('categoryUnknownSourceQuery', { label, source: sourceID }));
        }
      });
    });
    return errors;
  }

  function validationTargetTab(cfg) {
    const service = cfg.service || {};
    const publicPath = `${service.public_path || ''}`.trim();
    const normalizedPath = publicPath.startsWith('/') ? publicPath : `/${publicPath}`;
    const trimmedPath = normalizedPath.replace(/\/+$/, '');
    const upstream = `${service.upstream || ''}`.trim();
    const upstreamPort = Number(upstream.split(':').pop());
    if (
      !publicPath ||
      trimmedPath === '' ||
      trimmedPath === '/' ||
      normalizedPath.includes('//') ||
      !publicPathPattern.test(normalizedPath) ||
      isReservedPublicPath(trimmedPath) ||
      !upstreamPattern.test(upstream) ||
      !Number.isInteger(upstreamPort) ||
      upstreamPort < 1 ||
      upstreamPort > 65535
    ) {
      return 'runtime';
    }
    return 'rules';
  }

  function isReservedPublicPath(path) {
    if (reservedPublicPaths.has(path)) return true;
    return reservedPublicPathPrefixes.some(
      (prefix) => path === prefix || path.startsWith(`${prefix}/`),
    );
  }

  function expandInvalidCategories() {
    const sourceIDs = new Set(
      (config.sources || []).map((source) => `${source.id || ''}`.trim()).filter(Boolean),
    );
    const duplicateIDs = new Set();
    const seenIDs = new Set();
    for (const category of config.categories || []) {
      const id = `${category.id || ''}`.trim();
      if (seenIDs.has(id)) duplicateIDs.add(id);
      seenIDs.add(id);
    }
    const invalidUids = (config.categories || [])
      .filter((category) => {
        const id = `${category.id || ''}`.trim();
        const sourceQueries = category.sourceQueries || {};
        const sourceQueryIDs = Object.keys(sourceQueries);
        const hasInvalidSourceQuery =
          sourceQueryIDs.length > limits.max_source_queries_per_rule ||
          sourceQueryIDs.some((sourceID) => !sourceIDs.has(sourceID));
        return (
          !categoryIdPattern.test(id) ||
          duplicateIDs.has(id) ||
          !`${category.title || ''}`.trim() ||
          !`${category.description || ''}`.trim() ||
          (category.keywords || []).length === 0 ||
          (category.keywords || []).length > limits.max_keywords_per_category ||
          (category.excludes || []).length > limits.max_excludes_per_category ||
          hasInvalidSourceQuery
        );
      })
      .map((category) => category._uid);
    expandedIds = [...new Set([...expandedIds, ...invalidUids])];
  }
</script>

<div class="page media-center-page">
  <div class="page-header">
    <div>
      <h1 class="page-title">{t('mediaCenter')}</h1>
      <div class="page-subtitle">{t('mediaCenterSubtitle')}</div>
    </div>
    <div class="page-actions">
      <button class="btn btn-sm btn-ghost" onclick={load} disabled={loading || saving}>
        {t('refresh')}
      </button>
      <button
        class="btn btn-sm btn-primary"
        onclick={save}
        disabled={loading || saving}
      >
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

    {#if validationErrors.length}
      <div class="card validation-card" role="alert">
        <div class="card-title">{t('validationErrors')}</div>
        <ul>
          {#each validationErrors as error}
            <li>{error}</li>
          {/each}
        </ul>
      </div>
    {/if}

    <nav class="media-tab-bar tab-bar" aria-label={t('mediaCenter')}>
      {#each mediaTabs as tab}
        <button
          class="tab-btn media-tab-btn"
          class:active={activeTab === tab.id}
          type="button"
          aria-current={activeTab === tab.id ? 'page' : undefined}
          onclick={() => (activeTab = tab.id)}
        >
          {t(tab.labelKey)}
        </button>
      {/each}
    </nav>

    {#if activeTab === 'runtime'}
      <!-- Public access and internal runtime wiring -->
      <div class="card media-card">
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

        <details class="advanced-panel">
          <summary>{t('advancedRuntimeSettings')}</summary>
          <div class="form-grid advanced-panel-body">
            <div class="form-group">
              <label for="media-upstream">{t('upstreamTarget')}</label>
              <input
                id="media-upstream"
                value={config.service.upstream}
                oninput={(e) => updateService('upstream', e.currentTarget.value)}
                placeholder="media_server:8787"
              />
              <div class="helper-text">{t('upstreamTargetHelp')}</div>
            </div>
          </div>
          <div class="config-flow runtime-config-flow">
            <div class="config-step">
              <span class="config-step-label">{t('editableConfigFile')}</span>
              <code>{configFiles.admin_config || t('configPathUnavailable')}</code>
            </div>
            <div class="config-flow-arrow">→</div>
            <div class="config-step">
              <span class="config-step-label">{t('generatedRuntimeFile')}</span>
              <code>{configFiles.runtime_config || t('configPathUnavailable')}</code>
            </div>
            <div class="config-flow-arrow">→</div>
            <div class="config-step">
              <span class="config-step-label">{t('mediaServerMount')}</span>
              <code>/config/contentCategories.json</code>
            </div>
          </div>
          <div class="helper-text runtime-hint">{t('runtimeApplyHint')}</div>
          {#if configFiles.defaults}
            <div class="helper-text runtime-hint">
              {t('bundledDefaultsFile')}: <code>{configFiles.defaults}</code>
            </div>
          {/if}
        </details>
      </div>
    {:else if activeTab === 'app'}
      <!-- Android TV App release: admin publishes reviewed APK, app consumes /api/media/app/update -->
      <div class="card media-card">
        <div class="card-header">
          <div>
            <div class="card-title">Android TV App 更新发布</div>
            <div class="card-subtitle">
              上传审核后的 APK；客户端启动时会自动检查、下载、校验并拉起安装器。
            </div>
          </div>
          <button class="btn btn-sm btn-ghost" onclick={loadAppRelease} disabled={appReleaseLoading}>
            {appReleaseLoading ? '刷新中…' : t('refresh')}
          </button>
        </div>

        <div class="release-status">
          {#if appRelease?.published}
            {@const manifest = appRelease.manifest}
            <div class="release-current">
              <span class="badge badge-green">已发布</span>
              <strong>{manifest.versionName}</strong>
              <span>versionCode {manifest.versionCode}</span>
              <span>{formatBytes(manifest.sizeBytes)}</span>
              {#if manifest.publishedAt}<span>{manifest.publishedAt}</span>{/if}
            </div>
            <code class="release-sha">{manifest.sha256}</code>
          {:else}
            <div class="helper-text">尚未发布 App 更新；客户端会继续使用当前版本。</div>
          {/if}
        </div>

        <div class="form-grid app-release-form">
          <div class="form-group">
            <label for="app-version-code">versionCode</label>
            <input
              id="app-version-code"
              inputmode="numeric"
              value={appReleaseForm.versionCode}
              oninput={(e) =>
                (appReleaseForm = { ...appReleaseForm, versionCode: e.currentTarget.value })}
              placeholder="2"
            />
          </div>
          <div class="form-group">
            <label for="app-version-name">versionName</label>
            <input
              id="app-version-name"
              value={appReleaseForm.versionName}
              oninput={(e) =>
                (appReleaseForm = { ...appReleaseForm, versionName: e.currentTarget.value })}
              placeholder="1.1.0"
            />
          </div>
          <div class="form-group">
            <label for="app-apk-file">APK 文件</label>
            <input
              id="app-apk-file"
              type="file"
              accept=".apk,application/vnd.android.package-archive"
              onchange={(e) => (appReleaseFile = e.currentTarget.files?.[0] || null)}
            />
          </div>
        </div>
        <div class="form-group app-release-notes">
          <label for="app-release-notes">更新说明</label>
          <textarea
            id="app-release-notes"
            rows="4"
            value={appReleaseForm.releaseNotes}
            oninput={(e) =>
              (appReleaseForm = { ...appReleaseForm, releaseNotes: e.currentTarget.value })}
          ></textarea>
        </div>
        <div class="card-actions">
          <button
            class="btn btn-sm btn-primary"
            onclick={publishAppRelease}
            disabled={appReleaseUploading}
          >
            {#if appReleaseUploading}<span class="spinner"></span>{/if}
            {appReleaseUploading ? '发布中…' : '发布 App 更新'}
          </button>
        </div>
      </div>
    {:else if activeTab === 'rules'}
      <!-- Sources — known sources, only the on/off switch is editable -->
      <div class="card media-card">
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
                  <span class={`badge ${source.enabled ? 'badge-green' : 'badge-gray'}`}>
                    {sourceStatusLabel(source)}
                  </span>
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
      <div class="card media-card">
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
            {@const open = isExpanded(category._uid)}
            <section class="category-card" class:is-open={open}>
              <div class="category-summary">
                <button
                  type="button"
                  class="category-toggle"
                  aria-expanded={open}
                  aria-label={`${open ? t('collapseLabel') : t('expandLabel')} ${category.title || category.id}`}
                  onclick={() => toggleExpanded(category._uid)}
                >
                  <span class="chevron" class:chevron-open={open} aria-hidden="true">›</span>
                  <span class="category-main">
                    <span class="category-heading">
                      <span class="category-title">{category.title || category.id}</span>
                      <span class="category-id-tag">{category.id}</span>
                    </span>
                    {#if category.description}
                      <span class="category-summary-desc">{category.description}</span>
                    {/if}
                  </span>
                  <span class="category-meta">
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
                  </span>
                  <span class="category-toggle-label">
                    {open ? t('collapseLabel') : t('expandLabel')}
                  </span>
                </button>

                <details class="category-menu">
                  <summary class="btn btn-sm btn-ghost">{t('actions')}</summary>
                  <div class="category-menu-body">
                    <button
                      class="btn btn-sm btn-ghost"
                      onclick={() => moveCategory(index, -1)}
                      disabled={index === 0}
                    >
                      {t('moveUp')}
                    </button>
                    <button
                      class="btn btn-sm btn-ghost"
                      onclick={() => moveCategory(index, 1)}
                      disabled={index === config.categories.length - 1}
                    >
                      {t('moveDown')}
                    </button>
                    <button class="btn btn-sm btn-danger" onclick={() => removeCategory(index)}>
                      {t('delete')}
                    </button>
                  </div>
                </details>
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
                      <div class="helper-text">{t('searchQueryHelp')}</div>
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
                    <details class="field-section advanced-panel">
                      <summary>{t('sourceSpecificQueries')}</summary>
                      <div class="helper-text source-query-help">{t('sourceSpecificQueriesDesc')}</div>
                      <div class="source-query-grid advanced-panel-body">
                        {#each config.sources as source}
                          <div class="form-group">
                            <label for={`category-${category._uid}-source-${source.id}`}>
                              {t('sourceSearchQuery', { source: source.title || source.id })}
                              {#if !source.enabled}
                                <span class="inline-muted">({t('disabledLabel')})</span>
                              {/if}
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
                    </details>
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
                      <div class="helper-text">{t('keywordsHelp')}</div>
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
                      <div class="helper-text">{t('excludesHelp')}</div>
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
    {:else if activeTab === 'catalog'}
      <!-- Pre-collected category management: catalog review + snapshots + curation -->
      <MediaCatalog />
    {/if}
  {/if}
</div>

<style>
  .media-center-page {
    --c-text-secondary: #94a3b8;
    --c-text-muted: #64748b;
    max-width: 100%;
  }
  .media-center-page :global(.card-subtitle),
  .media-center-page :global(.helper-text),
  .media-center-page :global(label) {
    line-height: 1.55;
  }
  .media-center-page :global(.card-subtitle),
  .media-center-page :global(.helper-text) {
    color: #7f8ea3;
  }
  .media-tab-bar {
    background: rgba(6, 10, 19, 0.86);
    backdrop-filter: blur(12px);
    margin-bottom: 1rem;
    overflow-x: auto;
    position: sticky;
    top: 0;
    z-index: 5;
  }
  .media-tab-btn {
    color: var(--c-text-secondary);
    flex: 0 0 auto;
  }
  .media-card {
    margin-bottom: 1rem;
  }

  .form-grid,
  .textarea-grid,
  .source-query-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    gap: 0.75rem;
  }

  .warning-card {
    border-color: var(--c-warning);
    color: #fbbf24;
  }
  .warning-card ul,
  .validation-card ul {
    margin: 0.75rem 0 0 1rem;
    font-size: 0.78rem;
    line-height: 1.6;
  }
  .validation-card {
    border-color: var(--c-danger);
    color: #fca5a5;
  }

  /* ── Config flow ── */
  .config-flow {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr) auto minmax(0, 1fr);
    gap: 0.75rem;
    align-items: stretch;
  }
  .config-step {
    min-width: 0;
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-lg);
    padding: 0.75rem;
  }
  .config-step-label {
    display: block;
    color: var(--c-text-secondary);
    font-size: 0.68rem;
    font-weight: 600;
    letter-spacing: 0.04em;
    margin-bottom: 0.45rem;
    text-transform: uppercase;
  }
  .config-step code,
  .runtime-hint code {
    color: var(--c-accent);
    font-family: 'SF Mono', 'Fira Code', ui-monospace, monospace;
    font-size: 0.72rem;
    overflow-wrap: anywhere;
  }
  .config-flow-arrow {
    align-self: center;
    color: var(--c-text-muted);
    font-weight: 700;
  }
  .runtime-hint {
    margin-top: 0.65rem;
    line-height: 1.5;
  }
  .runtime-config-flow {
    margin-top: 1rem;
  }

  .release-status {
    background: var(--c-bg-surface);
    border: 1px solid var(--c-border);
    border-radius: var(--radius-lg);
    margin-bottom: 1rem;
    padding: 0.85rem;
  }
  .release-current {
    align-items: center;
    color: var(--c-text-secondary);
    display: flex;
    flex-wrap: wrap;
    gap: 0.65rem;
    font-size: 0.78rem;
  }
  .release-current strong {
    color: var(--c-text);
    font-size: 0.95rem;
  }
  .release-sha {
    color: var(--c-text-muted);
    display: block;
    font-family: 'SF Mono', 'Fira Code', ui-monospace, monospace;
    font-size: 0.7rem;
    margin-top: 0.65rem;
    overflow-wrap: anywhere;
  }
  .app-release-form,
  .app-release-notes {
    margin-top: 0.75rem;
  }

  .advanced-panel {
    margin-top: 0.85rem;
  }
  .advanced-panel summary {
    color: var(--c-text-secondary);
    cursor: pointer;
    font-size: 0.75rem;
    font-weight: 600;
  }
  .advanced-panel summary:hover {
    color: var(--c-text);
  }
  .advanced-panel-body {
    margin-top: 0.75rem;
  }
  .source-query-help {
    margin-top: 0.45rem;
  }
  .inline-muted {
    color: var(--c-text-muted);
    font-weight: 500;
    text-transform: none;
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
    overflow: visible;
    transition: border-color var(--transition);
  }
  .category-card.is-open {
    border-color: var(--c-border-strong);
  }
  .category-summary {
    display: flex;
    align-items: center;
    gap: 0.65rem;
    padding: 0.65rem 0.75rem;
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
    flex: 1 1 auto;
    border-radius: var(--radius);
    min-height: 48px;
    padding: 0.25rem 0.35rem;
    transition:
      background var(--transition),
      color var(--transition);
  }
  .category-toggle:hover {
    background: rgba(59, 130, 246, 0.08);
  }
  .category-toggle:focus-visible {
    outline: 2px solid var(--c-accent);
    outline-offset: 2px;
  }
  .chevron {
    color: var(--c-text-muted);
    font-size: 1.1rem;
    line-height: 1;
    transition: transform var(--transition);
    display: inline-block;
    flex-shrink: 0;
  }
  .chevron-open {
    transform: rotate(90deg);
    color: var(--c-accent);
  }
  .category-main {
    display: flex;
    flex-direction: column;
    gap: 0.22rem;
    flex: 1 1 auto;
    min-width: 0;
  }
  .category-heading {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    min-width: 0;
  }
  .category-title {
    color: var(--c-text-heading);
    font-size: 0.85rem;
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .category-summary-desc {
    color: var(--c-text-secondary);
    font-size: 0.72rem;
    line-height: 1.4;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
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
    justify-content: flex-end;
    flex: 0 1 42%;
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
  .category-toggle-label {
    color: var(--c-accent);
    background: var(--c-accent-bg);
    border: 1px solid var(--c-accent-border);
    border-radius: 999px;
    flex-shrink: 0;
    font-size: 0.65rem;
    font-weight: 600;
    padding: 0.12rem 0.45rem;
  }
  .category-menu {
    flex-shrink: 0;
    position: relative;
  }
  .category-menu summary {
    cursor: pointer;
    list-style: none;
  }
  .category-menu summary::-webkit-details-marker {
    display: none;
  }
  .category-menu-body {
    background: var(--c-bg-elevated);
    border: 1px solid var(--c-border-strong);
    border-radius: var(--radius);
    box-shadow: 0 18px 35px rgba(0, 0, 0, 0.28);
    display: grid;
    gap: 0.35rem;
    min-width: 128px;
    padding: 0.45rem;
    position: absolute;
    right: 0;
    top: calc(100% + 0.35rem);
    z-index: 20;
  }
  .category-menu-body :global(.btn) {
    justify-content: flex-start;
    width: 100%;
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
    .config-flow {
      grid-template-columns: 1fr;
    }
    .config-flow-arrow {
      display: none;
    }
    .category-summary {
      flex-wrap: wrap;
    }
    .category-toggle {
      flex-basis: 100%;
    }
    .category-meta {
      display: none;
    }
    .category-menu {
      margin-left: 1.8rem;
    }
    .category-menu-body {
      left: 0;
      right: auto;
    }
  }
</style>
