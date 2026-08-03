package probot

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeGitHub starts an httptest server that serves repo contents for the
// config loader. Returns the base URL and a serve function for contents.
func fakeGitHub(t *testing.T, contents map[string]string) string {
	t.Helper()
	mux := http.NewServeMux()
	for path, content := range contents {
		path := path
		content := content
		mux.HandleFunc("/api/v3/repos/"+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"type":"file","content":%q,"encoding":"base64"}`,
				base64.StdEncoding.EncodeToString([]byte(content)))
		})
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL
}

func newConfigProbot(t *testing.T, baseURL string) *Probot {
	t.Helper()
	p, err := New(Options{
		GithubToken: "ghp_test",
		BaseURL:     baseURL,
		LogLevel:    "disabled",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestContextConfig(t *testing.T) {
	baseURL := fakeGitHub(t, map[string]string{
		"octocat/hello-world/contents/.github/config.yml": "autoClose: true\ngreeting: hi\n",
	})
	p := newConfigProbot(t, baseURL)

	var cfg map[string]any
	p.On("issues.opened", func(ctx *Context) error {
		var err error
		cfg, err = ctx.Config("config.yml")
		return err
	})

	payload := issueOpenedPayload()
	payload["repository"] = map[string]any{"name": "hello-world", "owner": map[string]any{"login": "octocat"}}
	if err := p.Receive(WebhookEvent{Name: "issues", ID: "id", Payload: payload}); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if cfg["autoClose"] != true || cfg["greeting"] != "hi" {
		t.Fatalf("config: %+v", cfg)
	}
}

func TestContextConfigWithDefaultsWhenMissing(t *testing.T) {
	baseURL := fakeGitHub(t, map[string]string{})
	p := newConfigProbot(t, baseURL)

	var cfg map[string]any
	p.On("issues.opened", func(ctx *Context) error {
		var err error
		cfg, err = ctx.Config("config.yml", map[string]any{"greeting": "default"})
		return err
	})

	payload := issueOpenedPayload()
	payload["repository"] = map[string]any{"name": "hello-world", "owner": map[string]any{"login": "octocat"}}
	if err := p.Receive(WebhookEvent{Name: "issues", ID: "id", Payload: payload}); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if cfg["greeting"] != "default" {
		t.Fatalf("expected defaults on missing config, got %+v", cfg)
	}
}

func TestContextConfigExtends(t *testing.T) {
	baseURL := fakeGitHub(t, map[string]string{
		"octocat/hello-world/contents/.github/config.yml": "_extends: my-org/base\nautoClose: true\n",
		"my-org/base/contents/.github/config.yml":         "greeting: from-base\nautoClose: false\n",
	})
	p := newConfigProbot(t, baseURL)

	var cfg map[string]any
	p.On("issues.opened", func(ctx *Context) error {
		var err error
		cfg, err = ctx.Config("config.yml")
		return err
	})

	payload := issueOpenedPayload()
	payload["repository"] = map[string]any{"name": "hello-world", "owner": map[string]any{"login": "octocat"}}
	if err := p.Receive(WebhookEvent{Name: "issues", ID: "id", Payload: payload}); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if cfg["greeting"] != "from-base" {
		t.Fatalf("expected inherited greeting, got %+v", cfg)
	}
	if cfg["autoClose"] != true {
		t.Fatalf("expected local autoClose to win, got %+v", cfg)
	}
}
