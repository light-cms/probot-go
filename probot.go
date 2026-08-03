package probot

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v88/github"
	"github.com/rs/zerolog"
)

// Version is the current version of this port.
const Version = "0.1.0"

// HandlerFunc is a webhook event handler. It receives a *Context and may
// return an error that will be routed to the registered error handler.
type HandlerFunc func(ctx *Context) error

// ApplicationFunc is a function loaded into a Probot instance via Load. It
// typically registers webhook handlers.
type ApplicationFunc func(app *Probot, opts ApplicationFunctionOptions) error

// ApplicationFunctionOptions are options forwarded to an ApplicationFunc.
type ApplicationFunctionOptions struct {
	Cwd string
}

// ErrorHandlerFunc handles errors raised inside webhook handlers.
type ErrorHandlerFunc func(ctx *Context, err error)

// registeredHandler is a single On() subscription. name may be "issues" or
// "issues.opened"; an empty name means "any" (see OnAny).
type registeredHandler struct {
	name string
	fn   HandlerFunc
}

// Probot is the core framework. It manages authentication, webhook routing and
// lifecycle behind a simple event-driven API.
//
// Probot is safe for concurrent use: register handlers with On / OnAny / OnError
// and dispatch events with Receive.
type Probot struct {
	opts       Options
	log        zerolog.Logger
	appID      int64
	privateKey []byte

	// credMu guards opts.Secret, appID, privateKey, appClient and
	// appTransport, which the setup wizard can replace at runtime.
	credMu sync.RWMutex

	appClient    *github.Client
	appTransport *ghinstallation.AppsTransport

	installClients map[int64]*github.Client
	installMu      sync.Mutex

	handlers     []registeredHandler
	anyHandlers  []HandlerFunc
	errorHandler ErrorHandlerFunc

	readyCh chan struct{}
}

// New creates a Probot instance. Either AppID + PrivateKey or GithubToken must
// be present (via Options or the supplied defaults). Initialization is
// synchronous — register handlers with On/OnAny/OnError after New.
func New(o Options, defaults ...func(*Options)) (*Probot, error) {
	applyDefaults(&o, defaults)
	if err := o.validate(); err != nil {
		return nil, err
	}

	p := &Probot{
		opts:           o,
		log:            newLogger(o.LogLevel, o.LogFormat),
		installClients: make(map[int64]*github.Client),
		readyCh:        make(chan struct{}),
	}

	if o.GithubToken == "" && o.AppID != "" {
		id, err := strconv.ParseInt(strings.TrimSpace(o.AppID), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("probot: invalid AppID %q: %w", o.AppID, err)
		}
		p.appID = id
		p.privateKey = []byte(o.PrivateKey)
	}

	if o.GithubToken != "" || o.AppID != "" {
		appClient, err := p.newAppClient()
		if err != nil {
			return nil, err
		}
		p.appClient = appClient
	} else {
		p.log.Warn().Msg("probot created without credentials — only the setup wizard will be served")
	}

	close(p.readyCh)
	return p, nil
}

// Ready returns the Probot. Provided for API parity with probot's async
// ready(); in this Go port initialization is synchronous so Ready requires no
// waiting.
func (p *Probot) Ready() *Probot { return p }

// Done returns a channel closed once Probot has been initialized.
func (p *Probot) Done() <-chan struct{} { return p.readyCh }

// Log returns the Probot logger.
func (p *Probot) Log() zerolog.Logger { return p.log }

// Version returns the version of this port.
func (p *Probot) Version() string { return Version }

// WebhookPath returns the URL path webhook events are expected on.
func (p *Probot) WebhookPath() string { return p.opts.WebhookPath }

// Options returns a copy of the Probot's options.
func (p *Probot) Options() Options {
	p.credMu.RLock()
	defer p.credMu.RUnlock()
	return p.opts
}

// AppID returns the numeric GitHub App ID, or 0 when the instance is
// unconfigured or uses a GithubToken.
func (p *Probot) AppID() int64 {
	p.credMu.RLock()
	defer p.credMu.RUnlock()
	return p.appID
}

// secret returns the webhook secret under the credentials lock.
func (p *Probot) secret() string {
	p.credMu.RLock()
	defer p.credMu.RUnlock()
	return p.opts.Secret
}

// applyCredentials swaps in App credentials on a running instance. It is used
// by the setup wizard so a freshly registered app starts validating webhooks
// without a restart.
func (p *Probot) applyCredentials(appID int, privateKey, secret string) error {
	if appID == 0 || privateKey == "" {
		return errors.New("probot: appID and privateKey are required")
	}

	tr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, int64(appID), []byte(privateKey))
	if err != nil {
		return fmt.Errorf("probot: invalid private key: %w", err)
	}

	p.credMu.Lock()
	defer p.credMu.Unlock()

	if p.opts.BaseURL != "" {
		tr.BaseURL = p.opts.BaseURL
	}

	opts := []github.ClientOptionsFunc{github.WithTimeout(p.opts.RequestTimeout)}
	if p.opts.BaseURL != "" {
		opts = append(opts, github.WithEnterpriseURLs(p.opts.BaseURL, p.opts.BaseURL))
	}
	client, err := github.NewClient(append(opts, github.WithTransport(tr))...)
	if err != nil {
		return err
	}

	p.appID = int64(appID)
	p.privateKey = []byte(privateKey)
	p.opts.AppID = strconv.Itoa(appID)
	p.opts.PrivateKey = privateKey
	if secret != "" {
		p.opts.Secret = secret
	}
	p.appTransport = tr
	p.appClient = client

	// Installation clients were built from the old credentials.
	p.installMu.Lock()
	p.installClients = make(map[int64]*github.Client)
	p.installMu.Unlock()

	return nil
}

// On subscribes to a webhook event. eventName may be a bare event name
// ("issues", matching any action) or an event with an action ("issues.opened").
func (p *Probot) On(eventName string, fn HandlerFunc) {
	p.handlers = append(p.handlers, registeredHandler{name: eventName, fn: fn})
}

// OnMany subscribes a single handler to several events.
func (p *Probot) OnMany(eventNames []string, fn HandlerFunc) {
	for _, name := range eventNames {
		p.On(name, fn)
	}
}

// OnAny subscribes to all webhook events.
func (p *Probot) OnAny(fn HandlerFunc) {
	p.anyHandlers = append(p.anyHandlers, fn)
}

// OnError registers a handler for errors thrown inside webhook handlers.
func (p *Probot) OnError(fn ErrorHandlerFunc) {
	p.errorHandler = fn
}

// Load invokes one or more ApplicationFuncs with this Probot instance,
// mirroring probot's app.load(). Functions run synchronously, so handlers they
// register are available immediately.
func (p *Probot) Load(fns ...ApplicationFunc) error {
	for _, fn := range fns {
		if err := fn(p, ApplicationFunctionOptions{Cwd: "."}); err != nil {
			return err
		}
	}
	return nil
}

// Auth returns an authenticated GitHub client. With no installationID it
// returns an app-level (JWT) client. With an installation ID it returns a
// client authenticated as that installation.
//
// In webhook handlers prefer ctx.GitHub() / ctx.Octokit(), which are already
// authenticated for the triggering installation.
func (p *Probot) Auth(installationID ...int64) (*github.Client, error) {
	if len(installationID) == 0 || installationID[0] == 0 {
		p.credMu.RLock()
		defer p.credMu.RUnlock()
		return p.appClient, nil
	}
	return p.newInstallationClient(installationID[0])
}
