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
