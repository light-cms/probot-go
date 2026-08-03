package probot

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func signature(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func testProbot(t *testing.T) *Probot {
	t.Helper()
	p, err := New(Options{
		AppID:      "12345",
		PrivateKey: testPrivateKey,
		Secret:     "test-secret",
		LogLevel:   "disabled",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func issueOpenedPayload() map[string]any {
	return map[string]any{
		"action": "opened",
		"issue":  map[string]any{"number": 42.0, "title": "Bug report"},
		"repository": map[string]any{
			"name":  "hello-world",
			"owner": map[string]any{"login": "octocat"},
		},
		"sender":       map[string]any{"type": "User"},
		"installation": map[string]any{"id": 12345678.0},
	}
}

func TestNewValidation(t *testing.T) {
	// Completely empty is allowed: an unconfigured Probot used to serve the
	// setup wizard.
	if _, err := New(Options{}); err != nil {
		t.Fatalf("unconfigured Probot should succeed: %v", err)
	}
	// Partial credentials are a config error.
	if _, err := New(Options{AppID: "1"}); err == nil {
		t.Fatal("expected error when private key missing")
	}
	if _, err := New(Options{PrivateKey: "key"}); err == nil {
		t.Fatal("expected error when app id missing")
	}
	p, err := New(Options{GithubToken: "ghp_abc", LogLevel: "disabled"})
	if err != nil {
		t.Fatalf("expected token-only mode to work: %v", err)
	}
	if p.appClient == nil {
		t.Fatal("expected app client to be created")
	}
}

func TestDefaultsMerge(t *testing.T) {
	d := Defaults(Options{AppID: "1", PrivateKey: testPrivateKey, Secret: "s", Port: 9999})
	p, err := New(Options{}, d)
	if err != nil {
		t.Fatalf("New with defaults: %v", err)
	}
	if p.opts.AppID != "1" || p.opts.PrivateKey != testPrivateKey || p.opts.Secret != "s" || p.opts.Port != 9999 {
		t.Fatalf("defaults not applied: %+v", p.opts)
	}
}

func TestReceiveMatchesEventAndAction(t *testing.T) {
	p := testProbot(t)
	var got string
	p.On("issues.opened", func(ctx *Context) error {
		got = "opened"
		return nil
	})
	p.On("issues", func(ctx *Context) error {
		got += "+all"
		return nil
	})
	if err := p.Receive(WebhookEvent{Name: "issues", ID: "id-1", Payload: issueOpenedPayload()}); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if got != "opened+all" {
		t.Fatalf("expected both handlers, got %q", got)
	}
}

func TestReceiveDoesNotMatchOtherAction(t *testing.T) {
	p := testProbot(t)
	called := false
	p.On("issues.closed", func(ctx *Context) error { called = true; return nil })
	payload := issueOpenedPayload()
	if err := p.Receive(WebhookEvent{Name: "issues", ID: "id-1", Payload: payload}); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if called {
		t.Fatal("handler for issues.closed should not run on opened")
	}
}

func TestOnAny(t *testing.T) {
	p := testProbot(t)
	var names []string
	p.OnAny(func(ctx *Context) error {
		names = append(names, ctx.Name())
		return nil
	})
	payload := map[string]any{"action": "created", "repository": map[string]any{"name": "r", "owner": map[string]any{"login": "o"}}}
	if err := p.Receive(WebhookEvent{Name: "star", ID: "id-1", Payload: payload}); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if len(names) != 1 || names[0] != "star" {
		t.Fatalf("onAny: got %v", names)
	}
}

func TestOnErrorReceivesHandlerErrors(t *testing.T) {
	p := testProbot(t)
	sentinel := errors.New("boom")
	var reported error
	p.On("issues.opened", func(ctx *Context) error { return sentinel })
	p.OnError(func(ctx *Context, err error) { reported = err })

	payload := issueOpenedPayload()
	err := p.Receive(WebhookEvent{Name: "issues", ID: "id-1", Payload: payload})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if !errors.Is(reported, sentinel) {
		t.Fatalf("error handler should receive sentinel, got %v", reported)
	}
}

func TestContextHelpers(t *testing.T) {
	p := testProbot(t)
	var repo Repo
	var issue IssueParams
	var pr PullRequestParams
	var isBot bool
	p.On("issues.opened", func(ctx *Context) error {
		var err error
		repo, err = ctx.Repo()
		if err != nil {
			return err
		}
		issue, err = ctx.Issue()
		if err != nil {
			return err
		}
		pr, err = ctx.PullRequest()
		if err != nil {
			return err
		}
		isBot = ctx.IsBot()
		if ctx.Name() != "issues" || ctx.ID() != "id-1" {
			return errors.New("name/id mismatch")
		}
		return nil
	})
	if err := p.Receive(WebhookEvent{Name: "issues", ID: "id-1", Payload: issueOpenedPayload()}); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if repo != (Repo{Owner: "octocat", Repo: "hello-world"}) {
		t.Fatalf("repo: %+v", repo)
	}
	if issue != (IssueParams{Owner: "octocat", Repo: "hello-world", Number: 42}) {
		t.Fatalf("issue: %+v", issue)
	}
	if pr.Number != 42 {
		t.Fatalf("pr number: %+v", pr)
	}
	if isBot {
		t.Fatal("sender is a user, isBot should be false")
	}
}

func TestParseInto(t *testing.T) {
	p := testProbot(t)
	type pushPayload struct {
		Repository struct {
			Name string `json:"name"`
		} `json:"repository"`
		Sender struct {
			Type string `json:"type"`
		} `json:"sender"`
	}
	var parsed pushPayload
	p.On("push", func(ctx *Context) error {
		return ctx.ParseInto(&parsed)
	})
	payload := map[string]any{
		"repository": map[string]any{"name": "hello-world"},
		"sender":     map[string]any{"type": "Bot"},
	}
	if err := p.Receive(WebhookEvent{Name: "push", ID: "id-1", Payload: payload}); err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if parsed.Repository.Name != "hello-world" || parsed.Sender.Type != "Bot" {
		t.Fatalf("ParseInto: %+v", parsed)
	}
}

func TestVerifyGitHubSignature(t *testing.T) {
	body := `{"action":"opened"}`
	if err := VerifyGitHubSignature("secret", body, signature("secret", body)); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := VerifyGitHubSignature("secret", body, signature("wrong", body)); err == nil {
		t.Fatal("invalid signature accepted")
	}
	if err := VerifyGitHubSignature("secret", body, ""); err == nil {
		t.Fatal("missing signature accepted")
	}
}

func TestWebhookHandler(t *testing.T) {
	p := testProbot(t)
	received := false
	p.On("push", func(ctx *Context) error { received = true; return nil })

	body, _ := json.Marshal(map[string]any{"ref": "refs/heads/main"})
	req := httptest.NewRequest(http.MethodPost, "/api/github/webhooks", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "delivery-1")
	req.Header.Set("X-Hub-Signature-256", signature("test-secret", string(body)))

	rr := httptest.NewRecorder()
	p.WebhookHandler()(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rr.Code, rr.Body.String())
	}
	if !received {
		t.Fatal("handler did not run")
	}
}

func TestWebhookHandlerRejectsBadSignature(t *testing.T) {
	p := testProbot(t)
	body := `{"ref":"refs/heads/main"}`
	req := httptest.NewRequest(http.MethodPost, "/api/github/webhooks", bytes.NewBufferString(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", signature("nope", body))

	rr := httptest.NewRecorder()
	p.WebhookHandler()(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/.env"
	if err := writeFile(path, "APP_ID=999\nWEBHOOK_SECRET=shh\n# comment\nGITHUB_TOKEN=\"tok123\"\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("APP_ID", "")
	t.Setenv("WEBHOOK_SECRET", "")
	t.Setenv("GITHUB_TOKEN", "")
	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}
	if getenv("APP_ID") != "999" || getenv("WEBHOOK_SECRET") != "shh" || getenv("GITHUB_TOKEN") != "tok123" {
		t.Fatalf("env not loaded: %v %v %v", getenv("APP_ID"), getenv("WEBHOOK_SECRET"), getenv("GITHUB_TOKEN"))
	}
}

// writeFile helper keeps the test file independent of os.WriteFile nuances.
func writeFile(path, content string) error {
	return osWriteFile(path, []byte(content), 0600)
}
