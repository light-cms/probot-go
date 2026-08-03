package probot

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerHandlerRoutesWebhook(t *testing.T) {
	p := testProbot(t)
	received := false
	p.On("issues.opened", func(ctx *Context) error { received = true; return nil })

	s := NewServer(ServerOptions{Probot: p})
	h := s.Handler()

	body := []byte(`{"action":"opened","issue":{"number":1},"repository":{"name":"r","owner":{"login":"o"}}}`)
	req := httptest.NewRequest(http.MethodPost, p.WebhookPath(), bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-GitHub-Delivery", "d-1")
	req.Header.Set("X-Hub-Signature-256", signature("test-secret", string(body)))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rr.Code, rr.Body.String())
	}
	if !received {
		t.Fatal("webhook not dispatched through server handler")
	}
}

func TestServerSetupWizardWhenUnconfigured(t *testing.T) {
	p, err := New(Options{LogLevel: "disabled"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s := NewServer(ServerOptions{Probot: p})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("Register a GitHub App")) {
		t.Fatalf("expected setup wizard, got: %s", rr.Body.String())
	}
}

func TestServerStatusWhenConfigured(t *testing.T) {
	p := testProbot(t)
	s := NewServer(ServerOptions{Probot: p})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if !bytes.Contains(rr.Body.Bytes(), []byte("App configured")) {
		t.Fatalf("expected status page, got: %s", rr.Body.String())
	}
}

func TestWebhookMiddleware(t *testing.T) {
	p := testProbot(t)
	received := false
	p.On("push", func(ctx *Context) error { received = true; return nil })

	mw := p.WebhookMiddleware()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })

	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest(http.MethodPost, p.WebhookPath(), bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", signature("test-secret", string(body)))
	rr := httptest.NewRecorder()
	mw(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !received {
		t.Fatalf("webhook through middleware failed: %d received=%v", rr.Code, received)
	}

	other := httptest.NewRequest(http.MethodGet, "/other", nil)
	rr2 := httptest.NewRecorder()
	mw(next).ServeHTTP(rr2, other)
	if rr2.Code != http.StatusTeapot {
		t.Fatalf("non-webhook path should pass through, got %d", rr2.Code)
	}
}
