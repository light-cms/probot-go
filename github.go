package probot

import (
	"net/http"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v88/github"
)

// tokenAuthTransport adds a bearer token to every request. Used for the
// GithubToken (PAT / installation token) authentication mode.
type tokenAuthTransport struct {
	token string
	base  http.RoundTripper
}

func (t tokenAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(r)
}

func newClientOpts(p *Probot) []github.ClientOptionsFunc {
	opts := []github.ClientOptionsFunc{github.WithTimeout(p.opts.RequestTimeout)}
	if p.opts.BaseURL != "" {
		opts = append(opts, github.WithEnterpriseURLs(p.opts.BaseURL, p.opts.BaseURL))
	}
	return opts
}

// newAppClient builds a GitHub API client authenticated at the app level
// (JWT), or with a static token when GithubToken is configured.
func (p *Probot) newAppClient() (*github.Client, error) {
	if p.opts.GithubToken != "" {
		return github.NewClient(append(newClientOpts(p),
			github.WithHTTPClient(&http.Client{
				Transport: tokenAuthTransport{token: p.opts.GithubToken, base: http.DefaultTransport},
			}),
			github.WithAuthToken(p.opts.GithubToken),
		)...)
	}

	tr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, p.appID, p.privateKey)
	if err != nil {
		return nil, err
	}
	if p.opts.BaseURL != "" {
		tr.BaseURL = p.opts.BaseURL
	}
	p.appTransport = tr
	return github.NewClient(append(newClientOpts(p),
		github.WithTransport(tr),
	)...)
}

// newInstallationClient builds (or reuses) a GitHub API client authenticated
// as the given installation. Clients are cached per installation; the
// underlying ghinstallation transport refreshes its access token before
// expiry, so this is safe to keep around.
func (p *Probot) newInstallationClient(installationID int64) (*github.Client, error) {
	if p.opts.GithubToken != "" {
		p.credMu.RLock()
		defer p.credMu.RUnlock()
		return p.appClient, nil
	}

	p.installMu.Lock()
	defer p.installMu.Unlock()

	if c, ok := p.installClients[installationID]; ok {
		return c, nil
	}

	p.credMu.Lock()
	if p.appTransport == nil {
		tr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, p.appID, p.privateKey)
		if err != nil {
			p.credMu.Unlock()
			return nil, err
		}
		if p.opts.BaseURL != "" {
			tr.BaseURL = p.opts.BaseURL
		}
		p.appTransport = tr
	}
	appTransport := p.appTransport
	p.credMu.Unlock()

	itr := ghinstallation.NewFromAppsTransport(appTransport, installationID)
	if p.opts.BaseURL != "" {
		itr.BaseURL = p.opts.BaseURL
	}

	client, err := github.NewClient(append(newClientOpts(p),
		github.WithTransport(itr),
	)...)
	if err != nil {
		return nil, err
	}

	p.installClients[installationID] = client
	return client, nil
}
