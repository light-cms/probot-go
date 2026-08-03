package probot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ServerOptions configure a Server.
type ServerOptions struct {
	// Probot is the framework instance to serve. Required.
	Probot *Probot
	// Host to bind; defaults to the Probot option.
	Host string
	// Port to listen on; defaults to the Probot option (3000).
	Port int
	// WebhookPath to mount the webhook handler on; defaults to Probot option.
	WebhookPath string
	// EnvFile is where the setup wizard persists credentials. Defaults to
	// ".env" in the process working directory.
	EnvFile string
}

// Server runs a Probot instance over HTTP: it mounts the webhook handler and a
// setup wizard (when no app is configured) at "/". This is the Go equivalent of
// probot's Server.
type Server struct {
	probot  *Probot
	host    string
	port    int
	path    string
	envFile string
	httpS   *http.Server
}

// NewServer creates a Server.
func NewServer(o ServerOptions) *Server {
	p := o.Probot
	host := o.Host
	if host == "" {
		host = p.opts.Host
	}
	port := o.Port
	if port == 0 {
		port = p.opts.Port
	}
	path := o.WebhookPath
	if path == "" {
		path = p.opts.WebhookPath
	}
	envFile := o.EnvFile
	if envFile == "" {
		envFile = ".env"
	}
	return &Server{probot: p, host: host, port: port, path: path, envFile: envFile}
}

// Handler returns the full HTTP handler for this server: the setup wizard at
// "/", its callback at "/setup/callback", and the webhook handler at the
// configured path.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(s.path, s.probot.WebhookHandler())
	mux.HandleFunc("/setup/callback", s.handleSetupCallback)
	if s.path != "" && s.path != "/" {
		p := strings.TrimRight(s.path, "/")
		mux.HandleFunc(p+"/setup/callback", s.handleSetupCallback)
	}
	mux.HandleFunc("/", s.setupWizard)
	return mux
}

// Start begins serving and blocks until the server stops or fails.
func (s *Server) Start() error {
	s.httpS = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", s.host, s.port),
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.probot.log.Info().Str("addr", s.httpS.Addr).Str("webhook", s.path).Msg("starting probot server")
	err := s.httpS.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// Stop shuts the server down gracefully.
func (s *Server) Stop() error {
	if s.httpS == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.httpS.Shutdown(ctx)
}

// setupWizard serves a landing page. When no app is configured it offers the
// GitHub App creation flow, persisting credentials to .env.
func (s *Server) setupWizard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	if s.probot.opts.GithubToken != "" || s.probot.AppID() != 0 {
		s.renderStatus(w)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.renderSetupForm(w)
	case http.MethodPost:
		s.handleSetupForm(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// renderStatus shows the configured app's status, including the webhook URL.
func (s *Server) renderStatus(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<html><body>
<h1>Probot (Go)</h1>
<p>App configured.</p>
<ul>
<li>App ID: <code>%d</code></li>
<li>Webhook URL: <code>%s</code></li>
</ul>
<p>Point your GitHub App webhooks at the webhook URL.</p>
</body></html>`, s.probot.AppID(), s.path)
}

// setupPermissions and setupEvents are exposed on the setup form.
var setupPermissions = []struct {
	Name string
	Perm string
}{
	{"checks", "write"},
	{"contents", "write"},
	{"issues", "write"},
	{"metadata", "read"},
	{"pull_requests", "write"},
}

var setupEvents = []string{
	"check_run", "issues", "issue_comment", "pull_request", "push",
}

func (s *Server) renderSetupForm(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	permRows := ""
	for _, p := range setupPermissions {
		selected := "selected"
		permRows += fmt.Sprintf(`<tr><td>%s</td><td><select name="perm_%s"><option value="read">read</option><option value="write" %s>write</option></select></td></tr>`,
			p.Name, p.Name, selected)
	}
	eventRows := ""
	for _, e := range setupEvents {
		eventRows += fmt.Sprintf(`<tr><td><input type="checkbox" name="event_%s" value="%s" checked></td><td>%s</td></tr>`, e, e, e)
	}

	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><title>Register a GitHub App</title></head>
<body style="font-family:system-ui;max-width:640px;margin:40px auto">
<h1>probot (Go)</h1>
<p>This Probot is not configured yet. Register a GitHub App to get started. Credentials
are written to a <code>.env</code> file in the current directory.</p>
<form method="POST" action="/">
<h2>Configuration</h2>
<label>App name: <input name="name" placeholder="my-probot-app" required></label>
<label>Webhook URL (public): <input type="url" name="url" placeholder="https://your-host.example" required></label>
<h2>Permissions</h2>
<table>%s</table>
<h2>Events</h2>
<table>%s</table>
<button type="submit">Register GitHub App</button>
</form>
</body></html>`, permRows, eventRows)
}

func (s *Server) handleSetupForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	name := sanitizeName(r.FormValue("name"))
	baseURL := strings.TrimRight(r.FormValue("url"), "/")
	if name == "" || baseURL == "" {
		http.Error(w, "name and url are required", http.StatusBadRequest)
		return
	}

	if s.path != "" && s.path != "/" {
		baseURL = strings.TrimSuffix(baseURL, s.path)
		baseURL = strings.TrimRight(baseURL, "/")
	}

	state := base64.RawURLEncoding.EncodeToString([]byte(name))

	permissions := map[string]string{}
	for _, p := range setupPermissions {
		if per := r.FormValue("perm_" + p.Name); per != "" {
			permissions[p.Name] = per
		}
	}
	events := []string{}
	for _, e := range setupEvents {
		if r.FormValue("event_"+e) != "" {
			events = append(events, e)
		}
	}

	manifest := map[string]any{
		"name":                name,
		"url":                 baseURL,
		"hook_attributes":     map[string]any{"url": baseURL + s.path},
		"redirect_url":        baseURL + "/setup/callback",
		"public":              false,
		"default_permissions": permissions,
		"default_events":      events,
	}

	// Redirect to GitHub's app creation page via an auto-submitted form.
	_, _ = fmt.Fprintf(w, `<!DOCTYPE html>
<html><body><form id=f method=post action="https://github.com/settings/apps/new?state=%s">
<input name="manifest" value='%s'>
</form><script>f.submit()</script></body></html>`, state, mustJSON(manifest))
}

// manifestApp is the GitHub App manifest conversion response.
type manifestApp struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	PEM           string `json:"pem"`
	WebhookSecret string `json:"webhook_secret"`
	HTMLURL       string `json:"html_url"`
}

// handleSetupCallback receives the manifest code after the user creates the
// app on GitHub, exchanges it for credentials and persists them to .env.
func (s *Server) handleSetupCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	resp, err := http.Post(
		fmt.Sprintf("https://api.github.com/app-manifests/%s/conversions", code),
		"application/json", nil,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		http.Error(w, fmt.Sprintf("GitHub returned status %d", resp.StatusCode), resp.StatusCode)
		return
	}

	var app manifestApp
	if err := json.NewDecoder(resp.Body).Decode(&app); err != nil {
		http.Error(w, "failed to decode app response", http.StatusInternalServerError)
		return
	}

	envPath := s.writeEnv(app)

	// Apply the new credentials to the running instance so incoming webhooks
	// validate immediately — no restart required.
	applyErr := s.probot.applyCredentials(app.ID, app.PEM, app.WebhookSecret)
	if applyErr != nil {
		s.probot.log.Warn().Err(applyErr).Msg("could not activate credentials; restart required")
	} else {
		s.probot.log.Info().Int("app_id", app.ID).Str("name", app.Name).
			Msg("GitHub App registered and activated")
	}

	note := "They are active now — no restart needed."
	if applyErr != nil {
		note = "Restart the server to use them."
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<html><body><h1>App registered: %s</h1>
<p>App ID <code>%d</code>. Credentials were written to <code>%s</code>. %s</p>
<p>Install the app on a repository, then send it an event.</p></body></html>`,
		app.Name, app.ID, envPath, note)
}

// writeEnv persists credentials to the env file and returns the path written.
// The private key is stored as a single quoted line with escaped newlines;
// LoadEnvFile restores it.
func (s *Server) writeEnv(app manifestApp) string {
	path := s.envFile
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		s.probot.log.Error().Err(err).Str("path", path).Msg("failed to write .env")
		return path
	}
	defer func() { _ = f.Close() }()

	if _, err := fmt.Fprintf(f, "\nAPP_ID=%d\nWEBHOOK_SECRET=%s\nPRIVATE_KEY=%s\n",
		app.ID, app.WebhookSecret, strconv.Quote(app.PEM)); err != nil {
		s.probot.log.Error().Err(err).Str("path", path).Msg("failed to write .env")
		return path
	}

	s.probot.log.Info().Str("path", path).Msg("wrote credentials to .env")
	return path
}

func sanitizeName(s string) string {
	var b []rune
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b = append(b, r)
		}
	}
	return string(b)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
