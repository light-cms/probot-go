package probot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/google/go-github/v88/github"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"
)

// Repo identifies a GitHub repository.
type Repo struct {
	Owner string
	Repo  string
}

// IssueParams identifies an issue or pull request for go-github API calls.
type IssueParams struct {
	Owner  string
	Repo   string
	Number int
}

// PullRequestParams identifies a pull request for go-github API calls.
type PullRequestParams = IssueParams

// Context is passed to every webhook event handler. It wraps the raw GitHub
// webhook event and provides an authenticated GitHub client, a scoped logger
// and convenience helpers for extracting repository, issue and pull request
// parameters.
type Context struct {
	probot  *Probot
	name    string
	id      string
	payload map[string]any
	octokit *github.Client
	log     zerolog.Logger
}

func newContext(p *Probot, name, id string, payload map[string]any) (*Context, error) {
	c := &Context{
		probot:  p,
		name:    name,
		id:      id,
		payload: payload,
		log: p.log.With().
			Str("event", name).
			Str("delivery_id", id).
			Logger(),
	}
	octokit, err := p.authenticatedClient(payload)
	if err != nil {
		p.log.Warn().Err(err).Str("event", name).Msg("falling back to app-level GitHub client")
		p.credMu.RLock()
		octokit = p.appClient
		p.credMu.RUnlock()
	}
	c.octokit = octokit
	return c, nil
}

// authenticatedClient picks the client for the triggering installation.
func (p *Probot) authenticatedClient(payload map[string]any) (*github.Client, error) {
	p.credMu.RLock()
	appClient := p.appClient
	p.credMu.RUnlock()

	if appClient == nil {
		return nil, nil
	}
	inst, _ := payload["installation"].(map[string]any)
	if id, ok := inst["id"].(float64); ok && id > 0 {
		return p.newInstallationClient(int64(id))
	}
	return appClient, nil
}

// Name returns the top-level webhook event name, e.g. "issues" or "push".
func (c *Context) Name() string { return c.name }

// ID returns the unique delivery ID GitHub assigned to this webhook delivery.
func (c *Context) ID() string { return c.id }

// Payload returns the parsed webhook JSON payload as a map.
func (c *Context) Payload() map[string]any { return c.payload }

// ParseInto unmarshals the webhook payload into a typed struct, e.g.
//
//	var ev probe.Github.PushEvent
//	_ = ctx.ParseInto(&ev)
func (c *Context) ParseInto(v any) error {
	b, err := json.Marshal(c.payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// GitHub returns the authenticated go-github client for the triggering
// installation. Octokit is an alias for the same value, mirroring probot's
// context.octokit.
func (c *Context) GitHub() *github.Client { return c.octokit }

// Octokit is an alias for GitHub(), mirroring probot's context.octokit.
func (c *Context) Octokit() *github.Client { return c.octokit }

// Log returns a logger scoped to this event delivery.
func (c *Context) Log() zerolog.Logger { return c.log }

// IsBot reports whether the event sender is a GitHub bot. Use it to avoid
// infinite loops when your app reacts to its own activity.
func (c *Context) IsBot() bool {
	sender, _ := c.payload["sender"].(map[string]any)
	return sender["type"] == "Bot"
}

// Repo returns the owner and repo extracted from payload["repository"].
func (c *Context) Repo() (Repo, error) {
	repo, _ := c.payload["repository"].(map[string]any)
	if repo == nil {
		return Repo{}, errors.New("probot: payload has no repository (org/app level event)")
	}
	owner, _ := repo["owner"].(map[string]any)
	login, _ := owner["login"].(string)
	name, _ := repo["name"].(string)
	return Repo{Owner: login, Repo: name}, nil
}

// Issue returns owner, repo and issue number resolved from the payload.
func (c *Context) Issue() (IssueParams, error) {
	base, err := c.Repo()
	if err != nil {
		return IssueParams{}, err
	}
	num := firstNumber(c.payload, "issue", "pull_request", "number")
	return IssueParams{Owner: base.Owner, Repo: base.Repo, Number: num}, nil
}

// PullRequest returns owner, repo and pull request number resolved from the
// payload.
func (c *Context) PullRequest() (PullRequestParams, error) {
	base, err := c.Repo()
	if err != nil {
		return PullRequestParams{}, err
	}
	num := firstNumber(c.payload, "pull_request", "issue", "number")
	return PullRequestParams{Owner: base.Owner, Repo: base.Repo, Number: num}, nil
}

// Config loads a YAML/JSON configuration file from the .github/ directory of
// the repository's default branch, merging any defaults and honouring the
// _extends cross-repository key. Returns nil if the file is absent and no
// defaults were provided.
func (c *Context) Config(fileName string, defaultConfig ...map[string]any) (map[string]any, error) {
	cfg, found, err := c.loadConfigFile(".github/" + fileName)
	if err != nil {
		return nil, fmt.Errorf("probot: loading config %q: %w", fileName, err)
	}
	def := map[string]any{}
	if len(defaultConfig) > 0 && defaultConfig[0] != nil {
		def = defaultConfig[0]
	}
	if !found && len(defaultConfig) == 0 {
		return nil, nil
	}
	return mergeMaps(def, cfg), nil
}

// loadConfigFile reads a config file, resolving the _extends key recursively
// and returning the merged config, or (nil,false,nil) when it does not exist.
func (c *Context) loadConfigFile(path string) (cfg map[string]any, found bool, err error) {
	repo, err := c.Repo()
	if err != nil {
		return nil, false, err
	}

	content, _, _, err := c.octokit.Repositories.GetContents(context.Background(), repo.Owner, repo.Repo, path, nil)
	if err != nil {
		var ghErr *github.ErrorResponse
		if errors.As(err, &ghErr) && ghErr.Response != nil && ghErr.Response.StatusCode == 404 {
			return nil, false, nil
		}
		return nil, false, err
	}

	raw, err := content.GetContent()
	if err != nil {
		return nil, false, err
	}

	data := map[string]any{}
	if err := decodeConfig(raw, path, &data); err != nil {
		return nil, false, err
	}

	if ext, ok := data["_extends"].(string); ok && ext != "" {
		delete(data, "_extends")
		base, f, e := c.loadExternalConfig(ext, path)
		if e != nil {
			return nil, false, e
		}
		if f {
			data = mergeMaps(base, data)
		}
	}
	return data, true, nil
}

// loadExternalConfig fetches a config file from the _extends owner/repo.
func (c *Context) loadExternalConfig(extends, path string) (map[string]any, bool, error) {
	owner, repo, ok := strings.Cut(extends, "/")
	if !ok || owner == "" || repo == "" {
		return nil, false, fmt.Errorf("invalid _extends %q", extends)
	}
	content, _, _, err := c.octokit.Repositories.GetContents(context.Background(), owner, repo, path, nil)
	if err != nil {
		var ghErr *github.ErrorResponse
		if errors.As(err, &ghErr) && ghErr.Response != nil && ghErr.Response.StatusCode == 404 {
			return nil, false, nil
		}
		return nil, false, err
	}
	raw, err := content.GetContent()
	if err != nil {
		return nil, false, err
	}
	data := map[string]any{}
	if err := decodeConfig(raw, path, &data); err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// decodeConfig parses a config document, choosing JSON or YAML by the file
// extension.
func decodeConfig(raw, _ string, out *map[string]any) error {
	if err := json.Unmarshal([]byte(raw), out); err == nil {
		return nil
	}
	return yaml.Unmarshal([]byte(raw), out)
}

func mergeMaps(base, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	maps.Copy(out, base)
	for k, v := range extra {
		if bm, ok := out[k].(map[string]any); ok {
			if em, ok2 := v.(map[string]any); ok2 {
				out[k] = mergeMaps(bm, em)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func firstNumber(payload map[string]any, keys ...string) int {
	for _, key := range keys {
		if obj, ok := payload[key].(map[string]any); ok {
			if n, ok := obj["number"].(float64); ok {
				return int(n)
			}
		}
	}
	return 0
}
