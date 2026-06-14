package main

import (
	"strings"
	"testing"
)

func TestMediaCenterConfigNormalization(t *testing.T) {
	cfg, err := normalizeMediaCenterConfig(mediaCenterConfig{
		Service: mediaCenterServiceConfig{
			Upstream:   "media_server:8787",
			PublicPath: "api/media/",
		},
		Categories: []mediaCenterCategory{
			{
				ID:          "custom-zone",
				Title:       " 自定义 ",
				Description: " 测试分类 ",
				SearchQuery: " 公开视频 ",
				Keywords:    []string{" 公开 ", "", "视频"},
				Excludes:    []string{" 预告 ", ""},
			},
		},
	})
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}

	if cfg.Service.PublicPath != "/api/media" {
		t.Fatalf("unexpected public path: %q", cfg.Service.PublicPath)
	}
	if len(cfg.Sources) != 3 || cfg.Sources[0].ID != "bilibili" || !cfg.Sources[0].Enabled {
		t.Fatalf("unexpected default sources: %#v", cfg.Sources)
	}
	category := cfg.Categories[0]
	if category.Title != "自定义" || category.Description != "测试分类" || category.SearchQuery != "公开视频" {
		t.Fatalf("category fields were not trimmed: %#v", category)
	}
	if got := strings.Join(category.Keywords, ","); got != "公开,视频" {
		t.Fatalf("unexpected keywords: %q", got)
	}
	if got := strings.Join(category.Excludes, ","); got != "预告" {
		t.Fatalf("unexpected excludes: %q", got)
	}
}

func TestMediaCenterConfigRejectsAllSourcesDisabled(t *testing.T) {
	_, err := normalizeMediaCenterConfig(mediaCenterConfig{
		Service: mediaCenterServiceConfig{
			Upstream:   "media_server:8787",
			PublicPath: "/api/media",
		},
		Sources: []mediaCenterSource{
			{ID: "bilibili", Enabled: false},
			{ID: "youtube", Enabled: false},
			{ID: "internet-archive", Enabled: false},
		},
		Categories: []mediaCenterCategory{
			{ID: "movies", Title: "电影", Description: "电影", Keywords: []string{"电影"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "at least one media source") {
		t.Fatalf("expected all sources disabled error, got %v", err)
	}
}

func TestMediaCenterConfigNormalizesSourceQueries(t *testing.T) {
	cfg, err := normalizeMediaCenterConfig(mediaCenterConfig{
		Service: mediaCenterServiceConfig{
			Upstream:   "media_server:8787",
			PublicPath: "/api/media",
		},
		Categories: []mediaCenterCategory{
			{
				ID:            "documentaries",
				Title:         "纪录片",
				Description:   "纪录片",
				SearchQuery:   "纪录片 完整版",
				SourceQueries: map[string]string{"internet-archive": " documentary ", "youtube": ""},
				Keywords:      []string{"纪录片"},
			},
		},
	})
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	got := cfg.Categories[0].SourceQueries
	if len(got) != 1 || got["internet-archive"] != "documentary" {
		t.Fatalf("unexpected source queries: %#v", got)
	}
}

func TestMediaCenterConfigIgnoresBlankSourceQueriesBeforeValidation(t *testing.T) {
	cfg, err := normalizeMediaCenterConfig(mediaCenterConfig{
		Service: mediaCenterServiceConfig{
			Upstream:   "media_server:8787",
			PublicPath: "/api/media",
		},
		Categories: []mediaCenterCategory{
			{
				ID:          "documentaries",
				Title:       "纪录片",
				Description: "纪录片",
				SourceQueries: map[string]string{
					"youtube":       "",
					"unknown-empty": " ",
				},
				Keywords: []string{"纪录片"},
			},
		},
	})
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	if got := cfg.Categories[0].SourceQueries; got != nil {
		t.Fatalf("expected blank source queries to be omitted, got %#v", got)
	}
}

func TestMediaCenterConfigRejectsDuplicateCategoryIDs(t *testing.T) {
	_, err := normalizeMediaCenterConfig(mediaCenterConfig{
		Service: mediaCenterServiceConfig{
			Upstream:   "media_server:8787",
			PublicPath: "/api/media",
		},
		Categories: []mediaCenterCategory{
			{ID: "movies", Title: "电影", Description: "电影", Keywords: []string{"电影"}},
			{ID: "movies", Title: "重复", Description: "重复", Keywords: []string{"重复"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate category id") {
		t.Fatalf("expected duplicate id error, got %v", err)
	}
}

func TestMediaCenterConfigDecodeRejectsTrailingJSON(t *testing.T) {
	var cfg mediaCenterConfig
	err := decodeStrictJSON(strings.NewReader(`{"service":{"upstream":"media_server:8787","public_path":"/api/media"},"categories":[{"id":"movies","title":"电影","description":"电影","keywords":["电影"]}]} {"extra":true}`), &cfg)
	if err == nil {
		t.Fatal("expected trailing JSON to be rejected")
	}
}

func TestMediaCenterConfigRejectsReservedPublicPaths(t *testing.T) {
	for _, publicPath := range []string{"/admin", "/admin/media", "/api/admin", "/api/admin/media", "/api/transfer", "/proxy/query", "/assets/media", "/favicon.svg"} {
		_, err := normalizeMediaCenterConfig(mediaCenterConfig{
			Service: mediaCenterServiceConfig{
				Upstream:   "media_server:8787",
				PublicPath: publicPath,
			},
			Categories: []mediaCenterCategory{
				{ID: "movies", Title: "电影", Description: "电影", Keywords: []string{"电影"}},
			},
		})
		if err == nil || !strings.Contains(err.Error(), "reserved route") {
			t.Fatalf("expected reserved path %q to be rejected, got %v", publicPath, err)
		}
	}
}

func TestMediaCenterConfigPreservesSearchQueriesAndMinDuration(t *testing.T) {
	cfg, err := normalizeMediaCenterConfig(mediaCenterConfig{
		Service: mediaCenterServiceConfig{
			Upstream:   "media_server:8787",
			PublicPath: "/api/media",
		},
		Categories: []mediaCenterCategory{
			{
				ID:                 "dota-zone",
				Title:              "Dota 专区",
				Description:        "Dota 内容",
				SearchQueries:      []string{" Dota1 录像 ", "Dota2 比赛", ""},
				Keywords:           []string{"Dota1", "Dota2"},
				MinDurationSeconds: 180,
			},
		},
	})
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	cat := cfg.Categories[0]
	if len(cat.SearchQueries) != 2 || cat.SearchQueries[0] != "Dota1 录像" || cat.SearchQueries[1] != "Dota2 比赛" {
		t.Fatalf("searchQueries not normalized correctly: %v", cat.SearchQueries)
	}
	if cat.MinDurationSeconds != 180 {
		t.Fatalf("minDurationSeconds lost: got %d, want 180", cat.MinDurationSeconds)
	}
}

func TestRenderNginxConfigWithMediaCenter(t *testing.T) {
	rendered := renderNginxConfigWithMediaCenter(
		"set $media_upstream __MEDIA_CENTER_UPSTREAM__; location = __MEDIA_CENTER_PUBLIC_PATH__ { rewrite ^__MEDIA_CENTER_PUBLIC_PATH_REGEX__(/.*)$ /api$1 break; }",
		mediaCenterConfig{
			Service: mediaCenterServiceConfig{
				Upstream:   "media_server:8787",
				PublicPath: "/api/media",
			},
		},
	)

	for _, want := range []string{
		"set $media_upstream media_server:8787;",
		"location = /api/media",
		"rewrite ^/api/media(/.*)$ /api$1 break;",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered config missing %q: %s", want, rendered)
		}
	}
}
