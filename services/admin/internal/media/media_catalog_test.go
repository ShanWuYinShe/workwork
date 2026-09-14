package media

import (
	"admin/internal/auth"
	"admin/internal/platform"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func decodeAdminEnvelope(t *testing.T, body io.Reader) platform.JSONResp {
	t.Helper()
	var resp platform.JSONResp
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		t.Fatalf("decode admin envelope: %v", err)
	}
	return resp
}

func TestMediaCenterInternalErrorMessage(t *testing.T) {
	msg := mediaCenterInternalErrorMessage([]byte(`{"error":{"code":"BAD_REQUEST","message":"未知分类:foo"}}`), 400)
	if msg != "未知分类:foo" {
		t.Errorf("expected extracted message, got %q", msg)
	}
	fallback := mediaCenterInternalErrorMessage([]byte("not json"), http.StatusBadGateway)
	if fallback != http.StatusText(http.StatusBadGateway) {
		t.Errorf("expected status text fallback, got %q", fallback)
	}
}

func TestProxyMediaCenterInternalSuccess(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"categories":[{"id":"movies"}],"catalogEnabled":true}`))
	}))
	defer backend.Close()
	t.Setenv("MEDIA_SERVER_INTERNAL_TOKEN", "tok")
	t.Setenv("MEDIA_CENTER_INTERNAL_URL", backend.URL)

	rec := httptest.NewRecorder()
	proxyMediaCenterInternal(rec, context.Background(), http.MethodGet, "/internal/catalog/overview", nil, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	resp := decodeAdminEnvelope(t, rec.Body)
	if !resp.Success {
		t.Fatalf("expected success envelope, got %#v", resp)
	}
	data, _ := json.Marshal(resp.Data)
	if !strings.Contains(string(data), `"catalogEnabled":true`) {
		t.Errorf("expected upstream body passed through, got %s", data)
	}
}

func TestProxyMediaCenterInternalUpstreamError(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"BAD_REQUEST","message":"status 取值不合法。"}}`))
	}))
	defer backend.Close()
	t.Setenv("MEDIA_SERVER_INTERNAL_TOKEN", "tok")
	t.Setenv("MEDIA_CENTER_INTERNAL_URL", backend.URL)

	rec := httptest.NewRecorder()
	proxyMediaCenterInternal(rec, context.Background(), http.MethodGet, "/internal/catalog/items", url.Values{"status": {"bogus"}}, nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	resp := decodeAdminEnvelope(t, rec.Body)
	if resp.Success || resp.Error != "status 取值不合法。" {
		t.Errorf("expected mapped upstream error, got %#v", resp)
	}
}

func TestProxyMediaCenterInternalDisabled(t *testing.T) {
	t.Setenv("MEDIA_SERVER_INTERNAL_TOKEN", "")
	rec := httptest.NewRecorder()
	proxyMediaCenterInternal(rec, context.Background(), http.MethodGet, "/internal/catalog/overview", nil, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	resp := decodeAdminEnvelope(t, rec.Body)
	if resp.Success {
		t.Errorf("expected failure envelope when token unset")
	}
}

func TestApiMediaCatalogReviewInjectsReviewer(t *testing.T) {
	var gotReviewer string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reviewer string `json:"reviewer"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotReviewer = body.Reviewer
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"updated":1}`))
	}))
	defer backend.Close()
	t.Setenv("MEDIA_SERVER_INTERNAL_TOKEN", "tok")
	t.Setenv("MEDIA_CENTER_INTERNAL_URL", backend.URL)

	reqBody := `{"categoryId":"movies","provider":"bilibili","nativeId":"BV1","status":"approved","reviewer":"attacker"}`
	r := httptest.NewRequest(http.MethodPost, "/api/admin/media-center/catalog/review", strings.NewReader(reqBody))
	r = auth.WithTestUser(r, &auth.UserInfo{}, "alice")
	rec := httptest.NewRecorder()

	ApiMediaCatalogReview()(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if gotReviewer != "alice" {
		t.Errorf("reviewer = %q, want injected login user 'alice'", gotReviewer)
	}
}

func TestApiMediaCatalogImportProxiesJSON(t *testing.T) {
	var gotPath string
	var gotBody string
	var gotReviewer string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		var parsed struct {
			Reviewer string `json:"reviewer"`
		}
		_ = json.Unmarshal(body, &parsed)
		gotReviewer = parsed.Reviewer
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"imported":1,"approved":1,"skipped":0}`))
	}))
	defer backend.Close()
	t.Setenv("MEDIA_SERVER_INTERNAL_TOKEN", "tok")
	t.Setenv("MEDIA_CENTER_INTERNAL_URL", backend.URL)

	reqBody := `{"reviewer":"attacker","items":[{"categoryId":"movies","provider":"bilibili","nativeId":"BV1","title":"T","status":"approved"}]}`
	r := httptest.NewRequest(http.MethodPost, "/api/admin/media-center/catalog/import", strings.NewReader(reqBody))
	r = auth.WithTestUser(r, &auth.UserInfo{}, "alice")
	rec := httptest.NewRecorder()
	ApiMediaCatalogImport()(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/internal/catalog/import" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"nativeId":"BV1"`) {
		t.Fatalf("unexpected proxied body: %s", gotBody)
	}
	if gotReviewer != "alice" {
		t.Errorf("reviewer = %q, want injected login user 'alice'", gotReviewer)
	}
}

func TestApiMediaCatalogExportProxies(t *testing.T) {
	var gotPath, gotQuery string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[{"categoryId":"movies","nativeId":"BV1"}],"count":1,"category":"movies"}`))
	}))
	defer backend.Close()
	t.Setenv("MEDIA_SERVER_INTERNAL_TOKEN", "tok")
	t.Setenv("MEDIA_CENTER_INTERNAL_URL", backend.URL)

	r := httptest.NewRequest(http.MethodGet, "/api/admin/media-center/catalog/export?category=movies&status=approved", nil)
	rec := httptest.NewRecorder()
	ApiMediaCatalogExport()(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/internal/catalog/export" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "category=movies") || !strings.Contains(gotQuery, "status=approved") {
		t.Fatalf("query not forwarded: %q", gotQuery)
	}
	resp := decodeAdminEnvelope(t, rec.Body)
	if !resp.Success {
		t.Fatalf("envelope not success: %+v", resp)
	}
}

func TestApiMediaCatalogDeleteProxies(t *testing.T) {
	var gotPath, gotBody string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"deleted":1,"videoDeleted":true}`))
	}))
	defer backend.Close()
	t.Setenv("MEDIA_SERVER_INTERNAL_TOKEN", "tok")
	t.Setenv("MEDIA_CENTER_INTERNAL_URL", backend.URL)

	reqBody := `{"categoryId":"movies","provider":"bilibili","nativeId":"BV1"}`
	r := httptest.NewRequest(http.MethodPost, "/api/admin/media-center/catalog/delete", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	ApiMediaCatalogDelete()(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/internal/catalog/delete" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"nativeId":"BV1"`) {
		t.Fatalf("body not forwarded: %s", gotBody)
	}
}

func TestApiMediaCatalogDeleteRejectsBadJSON(t *testing.T) {
	t.Setenv("MEDIA_SERVER_INTERNAL_TOKEN", "tok")
	r := httptest.NewRequest(http.MethodPost, "/api/admin/media-center/catalog/delete", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	ApiMediaCatalogDelete()(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: want 400, got %d", rec.Code)
	}
}

func TestApiMediaCatalogImportRejectsTopLevelArray(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/admin/media-center/catalog/import", strings.NewReader(`[]`))
	rec := httptest.NewRecorder()
	ApiMediaCatalogImport()(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}
