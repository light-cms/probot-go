package probot

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Options configures a Probot instance. Either AppID + PrivateKey or
// GithubToken must be provided; all other options are optional.
type Options struct {
	// AppID is the numeric ID of the GitHub App. Required unless GithubToken
	// is set.
	AppID string

	// PrivateKey is the full contents of the .pem private key downloaded from
	// your GitHub App's settings page. Required unless GithubToken is set.
	PrivateKey string

	// Secret is the webhook secret used to validate incoming payloads from
	// GitHub. Defaults to "development" when not set.
	Secret string

	// GithubToken is a personal access token or installation token. Use it as
	// an alternative to AppID + PrivateKey for simple single-repository
	// integrations.
	GithubToken string

	// LogLevel sets the minimum log level. One of trace, debug, info, warn,
	// error, fatal, panic. Defaults to "info".
	LogLevel string

	// LogFormat is "pretty" (human readable, default) or "json" (newline
	// delimited JSON).
	LogFormat string

	// Port is the TCP port the HTTP server listens on. Defaults to 3000.
	Port int

	// Host is the hostname or IP the HTTP server binds to. Defaults to
	// "localhost".
	Host string

	// BaseURL overrides the GitHub API base URL, e.g. for GitHub Enterprise
	// Server: "https://github.example.com/api/v3".
	BaseURL string

	// WebhookPath is the URL path the webhook handler is mounted on. Defaults
	// to "/api/github/webhooks".
	WebhookPath string

	// RequestTimeout limits each GitHub API request. Defaults to 30s.
	RequestTimeout time.Duration
}

func (o Options) validate() error {
	switch {
	case o.GithubToken != "":
		return nil
	case o.AppID == "" && o.PrivateKey == "":
		// Unconfigured: a Probot with no credentials is valid and is used to
		// serve the setup wizard. It has no authenticated GitHub client.
		return nil
	case o.AppID == "":
		return errors.New("probot: AppID is required when PrivateKey is set")
	case o.PrivateKey == "":
		return errors.New("probot: PrivateKey is required when AppID is set")
	default:
		return nil
	}
}

// applyDefaults fills zero-valued fields in opt from the merged defaults.
func applyDefaults(opt *Options, defaults []func(*Options)) {
	for _, fn := range defaults {
		fn(opt)
	}
	if opt.Secret == "" {
		opt.Secret = "development"
	}
	if opt.LogLevel == "" {
		opt.LogLevel = "info"
	}
	if opt.LogFormat == "" {
		opt.LogFormat = "pretty"
	}
	if opt.Host == "" {
		opt.Host = "localhost"
	}
	if opt.Port == 0 {
		opt.Port = 3000
	}
	if opt.WebhookPath == "" {
		opt.WebhookPath = "/api/github/webhooks"
	}
	if opt.RequestTimeout == 0 {
		opt.RequestTimeout = 30 * time.Second
	}
}

// Defaults returns a functional option that pre-applies the given options as
// defaults. Options passed directly to New still override these. Mirrors
// Probot.defaults().
func Defaults(o Options) func(*Options) {
	return func(opt *Options) {
		if opt.AppID == "" {
			opt.AppID = o.AppID
		}
		if opt.PrivateKey == "" {
			opt.PrivateKey = o.PrivateKey
		}
		if opt.Secret == "" {
			opt.Secret = o.Secret
		}
		if opt.GithubToken == "" {
			opt.GithubToken = o.GithubToken
		}
		if opt.LogLevel == "" {
			opt.LogLevel = o.LogLevel
		}
		if opt.LogFormat == "" {
			opt.LogFormat = o.LogFormat
		}
		if opt.Port == 0 {
			opt.Port = o.Port
		}
		if opt.Host == "" {
			opt.Host = o.Host
		}
		if opt.BaseURL == "" {
			opt.BaseURL = o.BaseURL
		}
		if opt.WebhookPath == "" {
			opt.WebhookPath = o.WebhookPath
		}
		if opt.RequestTimeout == 0 {
			opt.RequestTimeout = o.RequestTimeout
		}
	}
}

// OptionsFromEnv builds Options from environment variables. It understands the
// standard names used by probot apps:
//
//	APP_ID, PRIVATE_KEY, PRIVATE_KEY_PATH, WEBHOOK_SECRET, GITHUB_TOKEN,
//	LOG_LEVEL, LOG_FORMAT, PORT, HOST, GHE_HOST, WEBHOOK_PATH
//
// A PROBOT_ prefix is accepted as an alternative for every variable
// (e.g. PROBOT_APP_ID).
func OptionsFromEnv() (Options, error) {
	o := Options{
		AppID:       env("APP_ID"),
		PrivateKey:  env("PRIVATE_KEY"),
		Secret:      env("WEBHOOK_SECRET"),
		GithubToken: env("GITHUB_TOKEN"),
		LogLevel:    env("LOG_LEVEL"),
		LogFormat:   env("LOG_FORMAT"),
		Host:        env("HOST"),
		WebhookPath: env("WEBHOOK_PATH"),
	}
	if key := env("PRIVATE_KEY_PATH"); key != "" && o.PrivateKey == "" {
		b, err := os.ReadFile(key)
		if err != nil {
			return o, fmt.Errorf("probot: reading PRIVATE_KEY_PATH: %w", err)
		}
		o.PrivateKey = string(b)
	}
	if ghe := env("GHE_HOST"); ghe != "" {
		o.BaseURL = "https://" + ghe + "/api/v3"
	}
	if p := env("PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return o, fmt.Errorf("probot: invalid PORT %q: %w", p, err)
		}
		o.Port = n
	}
	return o, nil
}

// env reads an environment variable, checking for a PROBOT_ prefixed alias
// first and falling back to the bare name.
func env(name string) string {
	if v := os.Getenv("PROBOT_" + name); v != "" {
		return v
	}
	return os.Getenv(name)
}

// LoadEnvFile parses a .env style file (KEY=VALUE lines, # comments) into the
// process environment. It does not override variables that are already set.
//
// Values may be bare, single quoted or double quoted. Double quoted values are
// unescaped, so a PEM private key stored on a single line as
// "-----BEGIN...\nMIIE...\n-----END...\n" is restored with real newlines —
// which is how the setup wizard writes PRIVATE_KEY.
func LoadEnvFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, parseEnvValue(strings.TrimSpace(val)))
		}
	}
	return nil
}

// parseEnvValue unquotes a .env value, expanding escape sequences inside
// double quotes the way dotenv implementations do.
func parseEnvValue(v string) string {
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		// Single quotes are literal.
		return v[1 : len(v)-1]
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		if unquoted, err := strconv.Unquote(v); err == nil {
			return unquoted
		}
		return expandEscapes(v[1 : len(v)-1])
	}
	return v
}

// expandEscapes replaces the escape sequences dotenv files commonly contain.
func expandEscapes(s string) string {
	return strings.NewReplacer(
		`\n`, "\n",
		`\r`, "\r",
		`\t`, "\t",
		`\"`, `"`,
		`\\`, `\`,
	).Replace(s)
}
