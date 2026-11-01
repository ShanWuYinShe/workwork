package auth

import (
	"admin/internal/requestctx"
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuditMiddlewareLogsSuccessfulMutationWithActor(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	h := AuditMiddleware("/api/admin/users/admin/add", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "http://admin/api/admin/users/admin/add", nil)
	req = req.WithContext(requestctx.WithActor(req.Context(), requestctx.Actor{Username: "alice", Role: "admin"}))
	h(httptest.NewRecorder(), req)

	out := buf.String()
	for _, want := range []string{"[security] audit", "/api/admin/users/admin/add", "by=alice"} {
		if !strings.Contains(out, want) {
			t.Fatalf("audit log missing %q: %s", want, out)
		}
	}
}

func TestAuditMiddlewareSkipsFailedRequests(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	h := AuditMiddleware("/api/admin/users/admin/add", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	h(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "http://admin/x", nil))

	if strings.Contains(buf.String(), "[security] audit") {
		t.Fatalf("failed request must not produce success audit line: %s", buf.String())
	}
}
