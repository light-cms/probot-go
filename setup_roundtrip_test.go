package probot

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Simulates the full wizard flow: writeEnv persists creds, a new process reads
// them back with LoadEnvFile+OptionsFromEnv, and a signed webhook validates.
func TestSetupRoundTrip(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	unconfigured, err := New(Options{LogLevel: "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(ServerOptions{Probot: unconfigured, EnvFile: envPath})

	app := manifestApp{ID: 4475737, Name: "test-app", PEM: testPrivateKey + "\n", WebhookSecret: "s3cr3t-webhook"}
	got := s.writeEnv(app)
	if got != envPath {
		t.Fatalf("writeEnv path = %q want %q", got, envPath)
	}

	// Fresh "process": clear env, load the file the wizard wrote.
	for _, k := range []string{"APP_ID", "WEBHOOK_SECRET", "PRIVATE_KEY"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	if err := LoadEnvFile(envPath); err != nil {
		t.Fatal(err)
	}
	o, err := OptionsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	o.LogLevel = "disabled"

	if o.AppID != "4475737" {
		t.Errorf("AppID = %q", o.AppID)
	}
	if o.Secret != "s3cr3t-webhook" {
		t.Errorf("Secret = %q", o.Secret)
	}
	if o.PrivateKey != app.PEM {
		t.Errorf("PrivateKey round-trip broken:\ngot  %q\nwant %q", o.PrivateKey, app.PEM)
	}

	// The reloaded options must actually construct a working Probot.
	p, err := New(o)
	if err != nil {
		t.Fatalf("New with reloaded creds: %v", err)
	}
	if p.AppID() != 4475737 {
		t.Errorf("AppID() = %d", p.AppID())
	}

	// And a webhook signed with the persisted secret must validate.
	body := []byte(`{"action":"opened","issue":{"number":7},"repository":{"name":"r","owner":{"login":"o"}}}`)
	fired := false
	p.On("issues.opened", func(ctx *Context) error { fired = true; return nil })

	req := httptest.NewRequest(http.MethodPost, p.WebhookPath(), bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-Hub-Signature-256", sign("s3cr3t-webhook", body))
	rr := httptest.NewRecorder()
	p.WebhookHandler()(rr, req)
	if rr.Code != http.StatusOK || !fired {
		t.Fatalf("signed webhook rejected: %d body=%s fired=%v", rr.Code, rr.Body.String(), fired)
	}
}

// Credentials applied at runtime take effect without a restart.
func TestApplyCredentialsLive(t *testing.T) {
	p, err := New(Options{LogLevel: "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"action":"opened"}`)

	// Before: default secret "development", so a wizard-secret signature fails.
	req := httptest.NewRequest(http.MethodPost, p.WebhookPath(), bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-Hub-Signature-256", sign("new-secret", body))
	rr := httptest.NewRecorder()
	p.WebhookHandler()(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 before applying creds, got %d", rr.Code)
	}

	if err := p.applyCredentials(4475737, testPrivateKey, "new-secret"); err != nil {
		t.Fatalf("applyCredentials: %v", err)
	}
	if p.AppID() != 4475737 {
		t.Errorf("AppID() = %d", p.AppID())
	}

	// After: same signature now validates, no restart.
	req2 := httptest.NewRequest(http.MethodPost, p.WebhookPath(), bytes.NewReader(body))
	req2.Header.Set("X-GitHub-Event", "issues")
	req2.Header.Set("X-Hub-Signature-256", sign("new-secret", body))
	rr2 := httptest.NewRecorder()
	p.WebhookHandler()(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200 after applying creds, got %d body=%s", rr2.Code, rr2.Body.String())
	}

	// The wizard must stop being served once configured.
	rr3 := httptest.NewRecorder()
	NewServer(ServerOptions{Probot: p}).Handler().ServeHTTP(rr3, httptest.NewRequest(http.MethodGet, "/", nil))
	if !bytes.Contains(rr3.Body.Bytes(), []byte("App configured")) {
		t.Fatalf("expected status page after setup, got %s", rr3.Body.String())
	}
}

func TestApplyCredentialsRejectsBadKey(t *testing.T) {
	p, err := New(Options{LogLevel: "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.applyCredentials(123, "not-a-pem", "s"); err == nil {
		t.Fatal("expected error for invalid private key")
	}
	if p.AppID() != 0 {
		t.Fatalf("AppID mutated after failed apply: %d", p.AppID())
	}
}

func TestParseEnvValue(t *testing.T) {
	cases := map[string]string{
		`bare`:              "bare",
		`"double"`:          "double",
		`'single'`:          "single",
		`"a\nb"`:            "a\nb",
		`'a\nb'`:            `a\nb`,
		`"-----BEGIN\nX\n"`: "-----BEGIN\nX\n",
	}
	for in, want := range cases {
		if got := parseEnvValue(in); got != want {
			t.Errorf("parseEnvValue(%s) = %q want %q", in, got, want)
		}
	}
}

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}
