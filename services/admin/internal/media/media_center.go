package media

import (
	"admin/internal/auth"
	"admin/internal/platform"
	"admin/internal/proxy"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

const (
	defaultMediaCenterUpstream   = "media_server:8787"
	defaultMediaCenterPublicPath = "/api/media"

	maxMediaCenterCategories = 50
	maxMediaCenterSources    = 10
	maxCategoryKeywords      = 100
	maxCategorySourceQueries = 10
)

var (
	mediaCenterConfigMu        sync.Mutex
	mediaCenterApplyMu         sync.Mutex
	mediaCenterIDPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)
	mediaCenterUpstreamPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+:[0-9]{1,5}$`)
	mediaCenterPathPattern     = regexp.MustCompile(`^/[A-Za-z0-9._~/-]*$`)
)

var (
	mediaCenterReservedPublicPathPrefixes = []string{
		"/admin",
		"/api/admin",
		"/api/transfer",
		"/proxy",
		"/assets",
	}
	mediaCenterReservedPublicPaths = map[string]struct{}{
		"/favicon.svg": {},
		"/icons.svg":   {},
	}
)

type mediaCenterConfig struct {
	Service     mediaCenterServiceConfig `json:"service"`
	Sources     []mediaCenterSource      `json:"sources"`
	Categories  []mediaCenterCategory    `json:"categories"`
	Credentials []providerCredential     `json:"credentials,omitempty"`
	// Revision 是乐观锁版本号:每次成功保存 +1。PUT 时前端回传已读版本,
	// 与持久化版本不一致返回 409——两个管理员并发编辑不再后写静默覆盖。
	// 0 表示旧客户端未携带(跳过检查,兼容过渡)。
	Revision int64 `json:"revision,omitempty"`
}

type mediaCenterServiceConfig struct {
	Upstream   string `json:"upstream"`
	PublicPath string `json:"public_path"`
}

type mediaCenterSource struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	HomeURL     string `json:"homeUrl,omitempty"`
	RiskLevel   string `json:"riskLevel"`
	Enabled     bool   `json:"enabled"`
}

type mediaCenterCategory struct {
	ID            string            `json:"id"`
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	SearchQuery   string            `json:"searchQuery,omitempty"`
	SearchQueries []string          `json:"searchQueries,omitempty"`
	SourceQueries map[string]string `json:"sourceQueries,omitempty"`
	Keywords      []string          `json:"keywords"`
}

// providerCredential 存储 provider 的认证凭证（如 bilibili 的 SESSDATA/bili_jct）。
// Fields 是任意 key-value 对，不同 provider 可有不同的字段集。
// 这些凭证通过 providerCredentials.json 注入 media_server，由 App 端拉取后
// 用于请求高分辨率内容。设计为通用 map 以便未来扩展其他 provider。
type providerCredential struct {
	Provider string            `json:"provider"`
	Fields   map[string]string `json:"fields"`
}

func ApiMediaCenterConfig() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		cfg, err := ReadMediaCenterConfig()
		if err != nil {
			log.Printf("read media center config: %v", err)
			platform.WriteInternalError(w, err)
			return
		}
		defaultCfg, err := readDefaultMediaCenterConfig()
		if err != nil {
			log.Printf("read media center defaults: %v", err)
			platform.WriteInternalError(w, err)
			return
		}
		// files 只返回文件名(不泄露服务端绝对路径);路径仅用于 UI 展示配置流,
		// 绝对路径对管理员排查无增益反而泄露容器内部布局。
		// credentials 从响应剥离(明文 SESSDATA):配置读取面没必要携带凭据,
		// 独立的 GET /credentials 端点已覆盖凭据查看需求。
		cfgView := cfg
		cfgView.Credentials = nil
		platform.WriteJSON(w, map[string]any{
			"config":             cfgView,
			"default_sources":    defaultCfg.Sources,
			"default_categories": defaultCfg.Categories,
			"files": map[string]string{
				"admin_config":   filepath.Base(ConfigPath),
				"runtime_config": filepath.Base(CategoriesPath),
				"defaults":       filepath.Base(DefaultCategoriesPath),
			},
			"limits": map[string]int{
				"max_categories":              maxMediaCenterCategories,
				"max_sources":                 maxMediaCenterSources,
				"max_keywords_per_category":   maxCategoryKeywords,
				"max_source_queries_per_rule": maxCategorySourceQueries,
			},
			"runtime_file": filepath.Base(CategoriesPath),
		})
	}
}

func ApiSaveMediaCenterConfig(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var incoming mediaCenterConfig
		if err := platform.DecodeStrictJSON(r.Body, &incoming); err != nil {
			platform.WriteError(w, "媒体中心配置不是合法 JSON", http.StatusBadRequest)
			return
		}

		cfg, err := normalizeMediaCenterConfig(incoming)
		if err != nil {
			platform.WriteError(w, err.Error(), http.StatusBadRequest)
			return
		}
		restartCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 60*time.Second)
		defer cancel()
		// 持 applyMu 跨越 read-modify-apply，避免与并发保存竞态导致 credentials 被覆盖。
		mediaCenterApplyMu.Lock()
		existingCfg, existingErr := ReadMediaCenterConfig()
		// 乐观锁:前端回传的 revision 与持久化不一致 = 配置在编辑期间被他人
		// 修改,拒绝保存(409)而不是后写静默覆盖。旧前端不携带 revision(0)
		// 时跳过检查,兼容灰度窗口。
		if incoming.Revision != 0 && existingErr == nil && existingCfg.Revision != incoming.Revision {
			mediaCenterApplyMu.Unlock()
			platform.WriteErrorCode(w, "配置已被他人修改，请刷新页面后重新编辑再保存", http.StatusConflict, "CONFIG_CONFLICT")
			return
		}
		// 如果前端未提交 credentials（保存来源/分类时不涉及），保留现有配置，
		// 避免无意清空已配置的 provider 凭证。
		if len(incoming.Credentials) == 0 && len(cfg.Credentials) == 0 {
			if existingErr == nil && len(existingCfg.Credentials) > 0 {
				cfg.Credentials = existingCfg.Credentials
			}
		}
		if existingErr == nil {
			cfg.Revision = existingCfg.Revision + 1
		} else {
			cfg.Revision = 1
		}
		restarted, warnings, err := applyMediaCenterConfigLocked(restartCtx, cli, cfg)
		mediaCenterApplyMu.Unlock()
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}

		_, actor := auth.GetUserFromContext(r)
		log.Printf("[security] media-center config save sources=%d categories=%d by=%s",
			len(cfg.Sources), len(cfg.Categories), actor)
		platform.WriteJSON(w, map[string]any{
			"config":    cfg,
			"restarted": restarted,
			"warnings":  warnings,
		})
	}
}

func ApiMediaCenterCredentials() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		cfg, err := ReadMediaCenterConfig()
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		platform.WriteJSON(w, map[string]any{
			"credentials": cfg.Credentials,
		})
	}
}

func ApiSaveMediaCenterCredentials(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var incoming struct {
			Credentials []providerCredential `json:"credentials"`
		}
		if err := platform.DecodeStrictJSON(r.Body, &incoming); err != nil {
			platform.WriteError(w, "凭证不是合法 JSON", http.StatusBadRequest)
			return
		}

		// 持 applyMu 跨越 read-modify-apply，避免与并发保存竞态导致 sources/categories 被覆盖。
		mediaCenterApplyMu.Lock()
		cfg, err := ReadMediaCenterConfig()
		if err != nil {
			mediaCenterApplyMu.Unlock()
			platform.WriteInternalError(w, err)
			return
		}
		cfg.Credentials = incoming.Credentials

		normalized, err := normalizeMediaCenterConfig(cfg)
		if err != nil {
			mediaCenterApplyMu.Unlock()
			platform.WriteError(w, err.Error(), http.StatusBadRequest)
			return
		}
		restartCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 60*time.Second)
		defer cancel()
		restarted, warnings, err := applyMediaCenterConfigLocked(restartCtx, cli, normalized)
		mediaCenterApplyMu.Unlock()
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}

		_, actor := auth.GetUserFromContext(r)
		log.Printf("[security] media-center credentials save providers=%d by=%s",
			len(normalized.Credentials), actor)
		platform.WriteJSON(w, map[string]any{
			"credentials": normalized.Credentials,
			"restarted":   restarted,
			"warnings":    warnings,
		})
	}
}

// applyMediaCenterConfigLocked 执行实际的持久化与重启，调用者必须持有 mediaCenterApplyMu。
// 拆出无锁版本是为了让需要先读取现有配置再修改的端点（如 credentials 保存）
// 能在单次持锁内完成 read-modify-write，避免与并发保存竞态。
func applyMediaCenterConfigLocked(ctx context.Context, cli *client.Client, cfg mediaCenterConfig) ([]string, []string, error) {
	if err := persistMediaCenterConfig(cfg); err != nil {
		return nil, nil, fmt.Errorf("persist media center config: %w", err)
	}
	categoriesChanged, err := writeMediaCenterCategoriesFileIfChanged(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("write media center categories: %w", err)
	}
	credentialsChanged, err := writeMediaCenterCredentialsFileIfChanged(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("write media center credentials: %w", err)
	}
	nginxChanged, err := RegenNginxForMediaCenter(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("regenerate nginx config after media center save: %w", err)
	}
	// 只重启配置实际变化的下游服务：改分类/凭证只动 media_server，
	// 只有 public_path/upstream 等 nginx 配置变化才重启 nginx，避免每次
	// 保存都打断全部代理与 admin 连接。
	restarted, warnings, err := restartMediaCenterServices(ctx, cli, categoriesChanged || credentialsChanged, nginxChanged)
	if err != nil {
		return restarted, warnings, fmt.Errorf("restart media center services: %w", err)
	}
	return restarted, warnings, nil
}

func EnsureMediaCenterConfig() error {
	mediaCenterConfigMu.Lock()
	defer mediaCenterConfigMu.Unlock()

	if _, err := os.Stat(ConfigPath); err == nil {
		cfg, err := readMediaCenterConfigNoLock()
		if err != nil {
			return err
		}
		if err := writeMediaCenterCategoriesFile(cfg); err != nil {
			return err
		}
		return writeMediaCenterCredentialsFile(cfg)
	} else if !os.IsNotExist(err) {
		return err
	}

	categories, err := readDefaultMediaCenterCategories()
	if err != nil {
		return err
	}
	cfg := defaultMediaCenterConfig(categories)
	normalized, err := normalizeMediaCenterConfig(cfg)
	if err != nil {
		return err
	}
	if err := writeMediaCenterConfigFile(normalized); err != nil {
		return err
	}
	if err := writeMediaCenterCategoriesFile(normalized); err != nil {
		return err
	}
	return writeMediaCenterCredentialsFile(normalized)
}

func GenMediaCenterCategories() error {
	cfg, err := ReadMediaCenterConfig()
	if err != nil {
		return err
	}
	if err := writeMediaCenterCategoriesFile(cfg); err != nil {
		return err
	}
	return writeMediaCenterCredentialsFile(cfg)
}

func ReadMediaCenterConfig() (mediaCenterConfig, error) {
	mediaCenterConfigMu.Lock()
	defer mediaCenterConfigMu.Unlock()
	return readMediaCenterConfigNoLock()
}

func readMediaCenterConfigNoLock() (mediaCenterConfig, error) {
	data, err := os.ReadFile(ConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			categories, defaultsErr := readDefaultMediaCenterCategories()
			if defaultsErr != nil {
				return mediaCenterConfig{}, defaultsErr
			}
			return normalizeMediaCenterConfig(defaultMediaCenterConfig(categories))
		}
		return mediaCenterConfig{}, err
	}

	var cfg mediaCenterConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return mediaCenterConfig{}, fmt.Errorf("parse media center config: %w", err)
	}
	return normalizeMediaCenterConfig(cfg)
}

// persistMediaCenterConfig writes an already-normalized config to disk.
// Errors here are internal I/O failures and must not be surfaced to clients.
func persistMediaCenterConfig(normalized mediaCenterConfig) error {
	mediaCenterConfigMu.Lock()
	defer mediaCenterConfigMu.Unlock()
	return writeMediaCenterConfigFile(normalized)
}

func writeMediaCenterConfigFile(cfg mediaCenterConfig) error {
	if err := os.MkdirAll(filepath.Dir(ConfigPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	// 0600：config.json 内嵌 provider 凭证（SESSDATA 等）且无任何容器消费
	// （下游只读 categories/credentials 两个派生文件），不应全局可读。
	return platform.AtomicWriteFile(ConfigPath, data, 0600)
}

func writeMediaCenterCategoriesFile(cfg mediaCenterConfig) error {
	_, err := writeMediaCenterCategoriesFileIfChanged(cfg)
	return err
}

// writeMediaCenterCategoriesFileIfChanged 写 contentCategories.json；内容未变化时
// 跳过写盘并返回 false，供保存流程判断是否需要重启 media_server。
func writeMediaCenterCategoriesFileIfChanged(cfg mediaCenterConfig) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(CategoriesPath), 0755); err != nil {
		return false, err
	}
	payload := struct {
		Sources    []mediaCenterRuntimeSource `json:"sources"`
		Categories []mediaCenterCategory      `json:"categories"`
	}{Sources: runtimeMediaCenterSources(cfg.Sources), Categories: cfg.Categories}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return false, err
	}
	data = append(data, '\n')
	if sameFileContent(CategoriesPath, data) {
		return false, nil
	}
	if err := platform.AtomicWriteFile(CategoriesPath, data, 0644); err != nil {
		return false, err
	}
	return true, nil
}

// writeMediaCenterCredentialsFile 生成注入 media_server 的 providerCredentials.json。
// 权限与 contentCategories.json 一致(0644)：media_server 容器 cap_drop:ALL
// 无 DAC_OVERRIDE，0640 会导致读取失败。
func writeMediaCenterCredentialsFile(cfg mediaCenterConfig) error {
	_, err := writeMediaCenterCredentialsFileIfChanged(cfg)
	return err
}

// writeMediaCenterCredentialsFileIfChanged 写 providerCredentials.json；内容未变化时
// 跳过写盘并返回 false，供保存流程判断是否需要重启 media_server。
func writeMediaCenterCredentialsFileIfChanged(cfg mediaCenterConfig) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(CredentialsPath), 0755); err != nil {
		return false, err
	}
	payload := struct {
		Credentials []providerCredential `json:"credentials"`
	}{Credentials: cfg.Credentials}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return false, err
	}
	data = append(data, '\n')
	if sameFileContent(CredentialsPath, data) {
		return false, nil
	}
	if err := platform.AtomicWriteFile(CredentialsPath, data, 0644); err != nil {
		return false, err
	}
	return true, nil
}

// sameFileContent 判断 path 现有内容是否与 data 完全一致（用于跳过无变化写盘）。
func sameFileContent(path string, data []byte) bool {
	existing, err := os.ReadFile(path)
	return err == nil && bytes.Equal(existing, data)
}

type mediaCenterRuntimeSource struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

func readDefaultMediaCenterCategories() ([]mediaCenterCategory, error) {
	cfg, err := readDefaultMediaCenterConfig()
	if err != nil {
		return nil, err
	}
	return cfg.Categories, nil
}

func readDefaultMediaCenterConfig() (mediaCenterConfig, error) {
	data, err := os.ReadFile(DefaultCategoriesPath)
	if err != nil {
		return mediaCenterConfig{}, err
	}
	var payload mediaCenterConfig
	if err := json.Unmarshal(data, &payload); err != nil {
		return mediaCenterConfig{}, fmt.Errorf("parse default media center config: %w", err)
	}
	cfg, err := normalizeMediaCenterConfig(mediaCenterConfig{
		Service:    defaultMediaCenterServiceConfig(),
		Sources:    payload.Sources,
		Categories: payload.Categories,
	})
	if err != nil {
		return mediaCenterConfig{}, err
	}
	return cfg, nil
}

func defaultMediaCenterConfig(categories []mediaCenterCategory) mediaCenterConfig {
	return mediaCenterConfig{
		Service:    defaultMediaCenterServiceConfig(),
		Sources:    defaultMediaCenterSources(),
		Categories: categories,
	}
}

func defaultMediaCenterServiceConfig() mediaCenterServiceConfig {
	return mediaCenterServiceConfig{
		Upstream:   envOrDefault("MEDIA_CENTER_UPSTREAM", defaultMediaCenterUpstream),
		PublicPath: envOrDefault("MEDIA_CENTER_PUBLIC_PATH", defaultMediaCenterPublicPath),
	}
}

func defaultMediaCenterSources() []mediaCenterSource {
	return []mediaCenterSource{
		{
			ID:          "bilibili",
			Title:       "Bilibili",
			Description: "中文覆盖最好，适合 Dota、中文纪录片和用户上传内容；播放地址偶发 v_voucher 风控。",
			HomeURL:     "https://www.bilibili.com",
			RiskLevel:   "high",
			Enabled:     true,
		},
		{
			ID:          "youtube",
			Title:       "YouTube",
			Description: "中文公开视频覆盖一般，可作补源；搜索/yt-dlp 解析可能遇到反 bot 或登录校验。",
			HomeURL:     "https://www.youtube.com",
			RiskLevel:   "high",
			Enabled:     false,
		},
		{
			ID:          "internet-archive",
			Title:       "Internet Archive",
			Description: "低风控、官方 API 和直链稳定，但中文内容覆盖弱，适合作为公版老片/历史影像补充而非主源。",
			HomeURL:     "https://archive.org/details/movies",
			RiskLevel:   "low",
			Enabled:     false,
		},
	}
}

func mediaCenterSourceDefaultsByID() map[string]mediaCenterSource {
	out := make(map[string]mediaCenterSource)
	for _, source := range defaultMediaCenterSources() {
		out[source.ID] = source
	}
	return out
}

func runtimeMediaCenterSources(sources []mediaCenterSource) []mediaCenterRuntimeSource {
	out := make([]mediaCenterRuntimeSource, 0, len(sources))
	for _, source := range sources {
		out = append(out, mediaCenterRuntimeSource{
			ID:      source.ID,
			Enabled: source.Enabled,
		})
	}
	return out
}

func normalizeMediaCenterCredentials(input []providerCredential, validSources []mediaCenterSource) ([]providerCredential, error) {
	if len(input) == 0 {
		return nil, nil
	}
	validIDs := make(map[string]bool, len(validSources))
	for _, s := range validSources {
		validIDs[s.ID] = true
	}
	seen := make(map[string]bool, len(input))
	out := make([]providerCredential, 0, len(input))
	for i, cred := range input {
		provider := strings.TrimSpace(cred.Provider)
		if !mediaCenterIDPattern.MatchString(provider) {
			return nil, fmt.Errorf("credential %d: provider id must be lowercase letters, numbers and dashes", i+1)
		}
		if !validIDs[provider] {
			return nil, fmt.Errorf("credential %d: unknown provider: %s", i+1, provider)
		}
		if seen[provider] {
			return nil, fmt.Errorf("credential %d: duplicate provider: %s", i+1, provider)
		}
		seen[provider] = true
		fields := make(map[string]string, len(cred.Fields))
		for k, v := range cred.Fields {
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if k != "" && v != "" {
				fields[k] = v
			}
		}
		if len(fields) == 0 {
			return nil, fmt.Errorf("credential %d: at least one non-empty field required", i+1)
		}
		out = append(out, providerCredential{Provider: provider, Fields: fields})
	}
	return out, nil
}

func normalizeMediaCenterConfig(input mediaCenterConfig) (mediaCenterConfig, error) {
	service, err := normalizeMediaCenterServiceConfig(input.Service)
	if err != nil {
		return mediaCenterConfig{}, err
	}
	sources, err := normalizeMediaCenterSources(input.Sources)
	if err != nil {
		return mediaCenterConfig{}, err
	}
	credentials, err := normalizeMediaCenterCredentials(input.Credentials, sources)
	if err != nil {
		return mediaCenterConfig{}, err
	}
	if len(input.Categories) == 0 {
		return mediaCenterConfig{}, errors.New("at least one category is required")
	}
	if len(input.Categories) > maxMediaCenterCategories {
		return mediaCenterConfig{}, fmt.Errorf("too many categories: max %d", maxMediaCenterCategories)
	}

	seen := make(map[string]bool, len(input.Categories))
	categories := make([]mediaCenterCategory, 0, len(input.Categories))
	for i, category := range input.Categories {
		normalized, err := normalizeMediaCenterCategory(category)
		if err != nil {
			return mediaCenterConfig{}, fmt.Errorf("category %d: %w", i+1, err)
		}
		if seen[normalized.ID] {
			return mediaCenterConfig{}, fmt.Errorf("duplicate category id: %s", normalized.ID)
		}
		seen[normalized.ID] = true
		categories = append(categories, normalized)
	}

	return mediaCenterConfig{
		Service:     service,
		Sources:     sources,
		Categories:  categories,
		Credentials: credentials,
		Revision:    input.Revision,
	}, nil
}

func normalizeMediaCenterServiceConfig(input mediaCenterServiceConfig) (mediaCenterServiceConfig, error) {
	defaults := defaultMediaCenterServiceConfig()
	upstream := strings.TrimSpace(input.Upstream)
	if upstream == "" {
		upstream = defaults.Upstream
	}
	if err := validateMediaCenterUpstream(upstream); err != nil {
		return mediaCenterServiceConfig{}, err
	}

	publicPath := strings.TrimSpace(input.PublicPath)
	if publicPath == "" {
		publicPath = defaults.PublicPath
	}
	publicPath, err := normalizeMediaCenterPublicPath(publicPath)
	if err != nil {
		return mediaCenterServiceConfig{}, err
	}

	return mediaCenterServiceConfig{
		Upstream:   upstream,
		PublicPath: publicPath,
	}, nil
}

func normalizeMediaCenterSources(input []mediaCenterSource) ([]mediaCenterSource, error) {
	sources := input
	if len(sources) == 0 {
		sources = defaultMediaCenterSources()
	}
	if len(sources) > maxMediaCenterSources {
		return nil, fmt.Errorf("too many sources: max %d", maxMediaCenterSources)
	}

	defaults := mediaCenterSourceDefaultsByID()
	seen := make(map[string]bool, len(sources))
	enabled := false
	out := make([]mediaCenterSource, 0, len(sources))
	for i, source := range sources {
		normalized, err := normalizeMediaCenterSource(source, defaults)
		if err != nil {
			return nil, fmt.Errorf("source %d: %w", i+1, err)
		}
		if seen[normalized.ID] {
			return nil, fmt.Errorf("duplicate source id: %s", normalized.ID)
		}
		seen[normalized.ID] = true
		enabled = enabled || normalized.Enabled
		out = append(out, normalized)
	}
	for _, source := range defaultMediaCenterSources() {
		if !seen[source.ID] {
			source.Enabled = false
			out = append(out, source)
			enabled = enabled || source.Enabled
		}
	}
	if !enabled {
		return nil, errors.New("at least one media source must be enabled")
	}
	return out, nil
}

func normalizeMediaCenterSource(input mediaCenterSource, defaults map[string]mediaCenterSource) (mediaCenterSource, error) {
	id := strings.TrimSpace(input.ID)
	if !mediaCenterIDPattern.MatchString(id) {
		return mediaCenterSource{}, errors.New("id must be lowercase letters, numbers and dashes, max 80 chars")
	}
	defaultSource, ok := defaults[id]
	if !ok {
		return mediaCenterSource{}, fmt.Errorf("unsupported media source: %s", id)
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = defaultSource.Title
	}
	description := strings.TrimSpace(input.Description)
	if description == "" {
		description = defaultSource.Description
	}
	homeURL := strings.TrimSpace(input.HomeURL)
	if homeURL == "" {
		homeURL = defaultSource.HomeURL
	} else if !strings.HasPrefix(homeURL, "https://") && !strings.HasPrefix(homeURL, "http://") {
		return mediaCenterSource{}, fmt.Errorf("homeUrl must be an http(s) URL, got: %s", homeURL)
	}
	riskLevel := strings.TrimSpace(input.RiskLevel)
	if riskLevel == "" {
		riskLevel = defaultSource.RiskLevel
	}
	if riskLevel != "low" && riskLevel != "medium" && riskLevel != "high" {
		return mediaCenterSource{}, fmt.Errorf("%s riskLevel must be low, medium or high", id)
	}
	return mediaCenterSource{
		ID:          id,
		Title:       title,
		Description: description,
		HomeURL:     homeURL,
		RiskLevel:   riskLevel,
		Enabled:     input.Enabled,
	}, nil
}

func normalizeMediaCenterCategory(input mediaCenterCategory) (mediaCenterCategory, error) {
	id := strings.TrimSpace(input.ID)
	if !mediaCenterIDPattern.MatchString(id) {
		return mediaCenterCategory{}, errors.New("id must be lowercase letters, numbers and dashes, max 80 chars")
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return mediaCenterCategory{}, fmt.Errorf("%s title is required", id)
	}
	description := strings.TrimSpace(input.Description)
	if description == "" {
		return mediaCenterCategory{}, fmt.Errorf("%s description is required", id)
	}
	searchQuery := strings.TrimSpace(input.SearchQuery)
	searchQueries := normalizeStringList(input.SearchQueries)
	sourceQueries, err := normalizeMediaCenterSourceQueries(input.SourceQueries)
	if err != nil {
		return mediaCenterCategory{}, fmt.Errorf("%s sourceQueries: %w", id, err)
	}
	keywords := normalizeStringList(input.Keywords)
	if len(keywords) == 0 {
		return mediaCenterCategory{}, fmt.Errorf("%s keywords are required", id)
	}
	if len(keywords) > maxCategoryKeywords {
		return mediaCenterCategory{}, fmt.Errorf("%s has too many keywords: max %d", id, maxCategoryKeywords)
	}
	return mediaCenterCategory{
		ID:            id,
		Title:         title,
		Description:   description,
		SearchQuery:   searchQuery,
		SearchQueries: searchQueries,
		SourceQueries: sourceQueries,
		Keywords:      keywords,
	}, nil
}

func normalizeMediaCenterSourceQueries(values map[string]string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	defaults := mediaCenterSourceDefaultsByID()
	out := make(map[string]string, len(values))
	for id, value := range values {
		normalizedID := strings.TrimSpace(id)
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := defaults[normalizedID]; !ok {
			return nil, fmt.Errorf("unsupported source: %s", normalizedID)
		}
		out[normalizedID] = trimmed
		if len(out) > maxCategorySourceQueries {
			return nil, fmt.Errorf("too many source queries: max %d", maxCategorySourceQueries)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func validateMediaCenterUpstream(upstream string) error {
	if !mediaCenterUpstreamPattern.MatchString(upstream) {
		return errors.New("media center upstream must be host:port, for example media_server:8787")
	}
	host, portText, _ := strings.Cut(upstream, ":")
	if host == "" {
		return errors.New("media center upstream host is required")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("media center upstream port must be 1-65535")
	}
	return nil
}

func normalizeMediaCenterPublicPath(path string) (string, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimRight(path, "/")
	if path == "" || path == "/" {
		return "", errors.New("media center public path must not be root")
	}
	if strings.Contains(path, "//") || !mediaCenterPathPattern.MatchString(path) {
		return "", errors.New("media center public path contains invalid characters")
	}
	// 逐段拒绝 ".."：字符白名单挡住了空格/分号/换行（nginx 指令注入不可能），
	// 但 /foo/../admin 这类点段会造成 location 语义混淆、绕过保留路由的
	// 字面前缀匹配。前端已同步实现同一校验。
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return "", errors.New("media center public path must not contain '..'")
		}
	}
	if isReservedMediaCenterPublicPath(path) {
		return "", fmt.Errorf("media center public path conflicts with reserved route: %s", path)
	}
	return path, nil
}

func isReservedMediaCenterPublicPath(path string) bool {
	if _, ok := mediaCenterReservedPublicPaths[path]; ok {
		return true
	}
	for _, prefix := range mediaCenterReservedPublicPathPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(platform.GetEnv(key))
	if value == "" {
		return fallback
	}
	return value
}

func renderNginxConfigWithMediaCenter(tmpl string, cfg mediaCenterConfig) string {
	out := strings.ReplaceAll(tmpl, "__MEDIA_CENTER_UPSTREAM__", cfg.Service.Upstream)
	out = strings.ReplaceAll(out, "__MEDIA_CENTER_PUBLIC_PATH__", cfg.Service.PublicPath)
	out = strings.ReplaceAll(out, "__MEDIA_CENTER_PUBLIC_PATH_REGEX__", regexp.QuoteMeta(cfg.Service.PublicPath))
	return out
}

// restartMediaCenterServices 按需重启下游服务：media_server 在分类/凭证变化时重启，
// nginx 仅在 nginx 配置变化时重启。两个开关均为 false 时直接返回，不做任何容器操作。
func restartMediaCenterServices(ctx context.Context, cli *client.Client, restartMedia, restartNginx bool) ([]string, []string, error) {
	var restarted []string
	var warnings []string
	var services []string
	if restartMedia {
		services = append(services, "media_server")
	}
	if restartNginx {
		services = append(services, "nginx")
	}
	for _, service := range services {
		ok, err := restartComposeService(ctx, cli, service)
		if err != nil {
			return restarted, warnings, err
		}
		if ok {
			restarted = append(restarted, service)
		} else {
			warnings = append(warnings, fmt.Sprintf("%s container not found; config will apply on next compose start", service))
		}
	}
	return restarted, warnings, nil
}

func restartComposeService(ctx context.Context, cli *client.Client, service string) (bool, error) {
	containers, err := cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: platform.ComposeProjectFilters(filters.KeyValuePair{
			Key: "label", Value: fmt.Sprintf("com.docker.compose.service=%s", service),
		}),
	})
	if err != nil {
		return false, fmt.Errorf("list %s container: %w", service, err)
	}
	if len(containers) == 0 {
		return false, nil
	}
	id := containers[0].ID
	var restartErr error
	if containers[0].State == "running" {
		t := 10
		if err := cli.ContainerRestart(ctx, id, container.StopOptions{Timeout: &t}); err != nil {
			restartErr = fmt.Errorf("restart %s: %w", service, err)
		}
	} else if err := cli.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		return false, fmt.Errorf("start %s: %w", service, err)
	}

	// 无论 restart/start 是否返回错误，都尝试恢复轮换 IP。
	// 容器实际未运行时 platform.AddContainerIP 会静默失败，不影响正确性。
	if service == "xray" || service == "nginx" {
		proxy.RestoreServiceRotatingIP(ctx, cli, id, service)
	}
	if restartErr != nil {
		return false, restartErr
	}
	return true, nil
}
