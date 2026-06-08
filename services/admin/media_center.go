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

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

const (
	defaultMediaCenterUpstream   = "media_server:8787"
	defaultMediaCenterPublicPath = "/api/media"

	maxMediaCenterCategories = 50
	maxCategoryKeywords      = 100
	maxCategoryExcludes      = 200
)

var (
	mediaCenterConfigMu        sync.Mutex
	mediaCenterIDPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)
	mediaCenterUpstreamPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+:[0-9]{1,5}$`)
	mediaCenterPathPattern     = regexp.MustCompile(`^/[A-Za-z0-9._~/-]*$`)
)

type mediaCenterConfig struct {
	Service    mediaCenterServiceConfig `json:"service"`
	Categories []mediaCenterCategory    `json:"categories"`
}

type mediaCenterServiceConfig struct {
	Upstream   string `json:"upstream"`
	PublicPath string `json:"public_path"`
}

type mediaCenterCategory struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	SearchQuery string   `json:"searchQuery,omitempty"`
	Keywords    []string `json:"keywords"`
	Excludes    []string `json:"excludes,omitempty"`
}

func apiMediaCenterConfig() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		cfg, err := readMediaCenterConfig()
		if err != nil {
			log.Printf("read media center config: %v", err)
			writeInternalError(w, err)
			return
		}
		defaultCategories, err := readDefaultMediaCenterCategories()
		if err != nil {
			log.Printf("read media center defaults: %v", err)
			writeInternalError(w, err)
			return
		}
		writeJSON(w, map[string]any{
			"config":             cfg,
			"default_categories": defaultCategories,
			"runtime_file":       mediaCenterCategoriesPath,
		})
	}
}

func apiSaveMediaCenterConfig(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var incoming mediaCenterConfig
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&incoming); err != nil {
			writeError(w, "Invalid media center config JSON", http.StatusBadRequest)
			return
		}

		cfg, err := saveMediaCenterConfig(incoming)
		if err != nil {
			writeError(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := writeMediaCenterCategoriesFile(cfg); err != nil {
			log.Printf("write media center categories: %v", err)
			writeInternalError(w, err)
			return
		}
		if err := genNginxConfig(); err != nil {
			log.Printf("regenerate nginx config after media center save: %v", err)
			writeInternalError(w, err)
			return
		}

		restarted, warnings, err := restartMediaCenterServices(r.Context(), cli)
		if err != nil {
			log.Printf("restart media center services: %v", err)
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

	mediaCenterConfigMu.Lock()
	defer mediaCenterConfigMu.Unlock()
	if err := writeMediaCenterConfigFile(normalized); err != nil {
		return mediaCenterConfig{}, err
	}
	return normalized, nil
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
		Categories []mediaCenterCategory `json:"categories"`
	}{Categories: cfg.Categories}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(mediaCenterCategoriesPath, data, 0644)
}

func readDefaultMediaCenterCategories() ([]mediaCenterCategory, error) {
	data, err := os.ReadFile(mediaCenterDefaultCategoriesPath)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Categories []mediaCenterCategory `json:"categories"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse default media center categories: %w", err)
	}
	cfg, err := normalizeMediaCenterConfig(mediaCenterConfig{
		Service:    defaultMediaCenterServiceConfig(),
		Categories: payload.Categories,
	})
	if err != nil {
		return nil, err
	}
	return cfg.Categories, nil
}

func defaultMediaCenterConfig(categories []mediaCenterCategory) mediaCenterConfig {
	return mediaCenterConfig{
		Service:    defaultMediaCenterServiceConfig(),
		Categories: categories,
	}
}

func defaultMediaCenterServiceConfig() mediaCenterServiceConfig {
	return mediaCenterServiceConfig{
		Upstream:   envOrDefault("MEDIA_CENTER_UPSTREAM", defaultMediaCenterUpstream),
		PublicPath: envOrDefault("MEDIA_CENTER_PUBLIC_PATH", defaultMediaCenterPublicPath),
	}
}

func normalizeMediaCenterConfig(input mediaCenterConfig) (mediaCenterConfig, error) {
	service, err := normalizeMediaCenterServiceConfig(input.Service)
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
	keywords := normalizeStringList(input.Keywords)
	if len(keywords) == 0 {
		return mediaCenterCategory{}, fmt.Errorf("%s keywords are required", id)
	}
	if len(keywords) > maxCategoryKeywords {
		return mediaCenterCategory{}, fmt.Errorf("%s has too many keywords: max %d", id, maxCategoryKeywords)
	}
	excludes := normalizeStringList(input.Excludes)
	if len(excludes) > maxCategoryExcludes {
		return mediaCenterCategory{}, fmt.Errorf("%s has too many excludes: max %d", id, maxCategoryExcludes)
	}

	return mediaCenterCategory{
		ID:          id,
		Title:       title,
		Description: description,
		SearchQuery: searchQuery,
		Keywords:    keywords,
		Excludes:    excludes,
	}, nil
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
	return path, nil
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
		Filters: filters.NewArgs(filters.KeyValuePair{
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
	if containers[0].State == "running" {
		t := 10
		if err := cli.ContainerRestart(ctx, id, container.StopOptions{Timeout: &t}); err != nil {
			return false, fmt.Errorf("restart %s: %w", service, err)
		}
	} else if err := cli.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		return false, fmt.Errorf("start %s: %w", service, err)
	}

	if service == "xray" || service == "nginx" {
		restoreServiceRotatingIP(ctx, cli, id, service)
	}
	return true, nil
}
