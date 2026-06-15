package main

import (
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
	Service    mediaCenterServiceConfig `json:"service"`
	Sources    []mediaCenterSource      `json:"sources"`
	Categories []mediaCenterCategory    `json:"categories"`
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

func apiMediaCenterConfig() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		cfg, err := readMediaCenterConfig()
		if err != nil {
			log.Printf("read media center config: %v", err)
			writeInternalError(w, err)
			return
		}
		defaultCfg, err := readDefaultMediaCenterConfig()
		if err != nil {
			log.Printf("read media center defaults: %v", err)
			writeInternalError(w, err)
			return
		}
		writeJSON(w, map[string]any{
			"config":             cfg,
			"default_sources":    defaultCfg.Sources,
			"default_categories": defaultCfg.Categories,
			"files": map[string]string{
				"admin_config":   mediaCenterConfigPath,
				"runtime_config": mediaCenterCategoriesPath,
				"defaults":       mediaCenterDefaultCategoriesPath,
			},
			"limits": map[string]int{
				"max_categories":              maxMediaCenterCategories,
				"max_sources":                 maxMediaCenterSources,
				"max_keywords_per_category":   maxCategoryKeywords,
				"max_source_queries_per_rule": maxCategorySourceQueries,
			},
			"runtime_file": mediaCenterCategoriesPath,
		})
	}
}

func apiSaveMediaCenterConfig(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var incoming mediaCenterConfig
		if err := decodeStrictJSON(r.Body, &incoming); err != nil {
			writeError(w, "Invalid media center config JSON", http.StatusBadRequest)
			return
		}

		cfg, err := normalizeMediaCenterConfig(incoming)
		if err != nil {
			writeError(w, err.Error(), http.StatusBadRequest)
			return
		}
		restartCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 60*time.Second)
		defer cancel()
		restarted, warnings, err := applyMediaCenterConfig(restartCtx, cli, cfg)
		if err != nil {
			writeInternalError(w, err)
			return
		}

		writeJSON(w, map[string]any{
			"config":    cfg,
			"restarted": restarted,
			"warnings":  warnings,
		})
	}
}

func applyMediaCenterConfig(ctx context.Context, cli *client.Client, cfg mediaCenterConfig) ([]string, []string, error) {
	mediaCenterApplyMu.Lock()
	defer mediaCenterApplyMu.Unlock()

	if err := persistMediaCenterConfig(cfg); err != nil {
		return nil, nil, fmt.Errorf("persist media center config: %w", err)
	}
	if err := writeMediaCenterCategoriesFile(cfg); err != nil {
		return nil, nil, fmt.Errorf("write media center categories: %w", err)
	}
	if err := genNginxConfigForMediaCenter(cfg); err != nil {
		return nil, nil, fmt.Errorf("regenerate nginx config after media center save: %w", err)
	}
	restarted, warnings, err := restartMediaCenterServices(ctx, cli)
	if err != nil {
		return restarted, warnings, fmt.Errorf("restart media center services: %w", err)
	}
	return restarted, warnings, nil
}

func ensureMediaCenterConfig() error {
	mediaCenterConfigMu.Lock()
	defer mediaCenterConfigMu.Unlock()

	if _, err := os.Stat(mediaCenterConfigPath); err == nil {
		cfg, err := readMediaCenterConfigNoLock()
		if err != nil {
			return err
		}
		return writeMediaCenterCategoriesFile(cfg)
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
	return writeMediaCenterCategoriesFile(normalized)
}

func genMediaCenterCategories() error {
	cfg, err := readMediaCenterConfig()
	if err != nil {
		return err
	}
	return writeMediaCenterCategoriesFile(cfg)
}

func readMediaCenterConfig() (mediaCenterConfig, error) {
	mediaCenterConfigMu.Lock()
	defer mediaCenterConfigMu.Unlock()
	return readMediaCenterConfigNoLock()
}

func readMediaCenterConfigNoLock() (mediaCenterConfig, error) {
	data, err := os.ReadFile(mediaCenterConfigPath)
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

func saveMediaCenterConfig(input mediaCenterConfig) (mediaCenterConfig, error) {
	normalized, err := normalizeMediaCenterConfig(input)
	if err != nil {
		return mediaCenterConfig{}, err
	}
	if err := persistMediaCenterConfig(normalized); err != nil {
		return mediaCenterConfig{}, err
	}
	return normalized, nil
}

// persistMediaCenterConfig writes an already-normalized config to disk.
// Errors here are internal I/O failures and must not be surfaced to clients.
func persistMediaCenterConfig(normalized mediaCenterConfig) error {
	mediaCenterConfigMu.Lock()
	defer mediaCenterConfigMu.Unlock()
	return writeMediaCenterConfigFile(normalized)
}

func writeMediaCenterConfigFile(cfg mediaCenterConfig) error {
	if err := os.MkdirAll(filepath.Dir(mediaCenterConfigPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(mediaCenterConfigPath, data, 0644)
}

func writeMediaCenterCategoriesFile(cfg mediaCenterConfig) error {
	if err := os.MkdirAll(filepath.Dir(mediaCenterCategoriesPath), 0755); err != nil {
		return err
	}
	payload := struct {
		Sources    []mediaCenterRuntimeSource `json:"sources"`
		Categories []mediaCenterCategory      `json:"categories"`
	}{Sources: runtimeMediaCenterSources(cfg.Sources), Categories: cfg.Categories}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(mediaCenterCategoriesPath, data, 0644)
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
	data, err := os.ReadFile(mediaCenterDefaultCategoriesPath)
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

func normalizeMediaCenterConfig(input mediaCenterConfig) (mediaCenterConfig, error) {
	service, err := normalizeMediaCenterServiceConfig(input.Service)
	if err != nil {
		return mediaCenterConfig{}, err
	}
	sources, err := normalizeMediaCenterSources(input.Sources)
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
		Service:    service,
		Sources:    sources,
		Categories: categories,
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
		if normalized.ID == "youtube" && normalized.Enabled && strings.TrimSpace(getEnv("YOUTUBE_API_KEY")) == "" {
			return nil, errors.New("youtube source requires YOUTUBE_API_KEY")
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
	value := strings.TrimSpace(getEnv(key))
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

func restartMediaCenterServices(ctx context.Context, cli *client.Client) ([]string, []string, error) {
	var restarted []string
	var warnings []string
	for _, service := range []string{"media_server", "nginx"} {
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
		Filters: composeProjectFilters(filters.KeyValuePair{
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
	// 容器实际未运行时 addContainerIP 会静默失败，不影响正确性。
	if service == "xray" || service == "nginx" {
		restoreServiceRotatingIP(ctx, cli, id, service)
	}
	if restartErr != nil {
		return false, restartErr
	}
	return true, nil
}
