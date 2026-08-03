# probot-go

A Go port of [Probot](https://probot.github.io) — a framework for building
GitHub Apps. It eliminates the boilerplate of webhook validation, GitHub App
authentication and API setup so you can focus on what your app actually does.

```go
app, err := probot.New(probot.Options{
    AppID:      os.Getenv("APP_ID"),
    PrivateKey: os.Getenv("PRIVATE_KEY"),
    Secret:     os.Getenv("WEBHOOK_SECRET"),
})

app.On("issues.opened", func(ctx *probot.Context) error {
    issue := ctx.Issue() // owner, repo, number
    _, _, err := ctx.GitHub().Issues.CreateComment(
        context.Background(), issue.Owner, issue.Repo, issue.Number,
        &github.IssueComment{Body: github.Ptr("Thanks for opening this issue!")},
    )
    return err
})

server := probot.NewServer(probot.ServerOptions{Probot: app})
server.Start()
```

## Feature parity with Probot

| Probot (Node)                          | probot-go                                  |
| -------------------------------------- | ------------------------------------------ |
| `new Probot(options)`                  | `probot.New(options)`                      |
| `Probot.defaults(options)`             | `probot.Defaults(options)` + `New`         |
| `probot.on("issues.opened", cb)`       | `(*Probot).On`                             |
| `probot.on(["a","b"], cb)`             | `(*Probot).OnMany`                         |
| `probot.onAny(cb)`                     | `(*Probot).OnAny`                          |
| `probot.onError(cb)`                   | `(*Probot).OnError`                        |
| `probot.load(fn)`                      | `(*Probot).Load`                           |
| `probot.auth(installationId?)`         | `(*Probot).Auth(id ...int64)`              |
| `probot.receive(event)`                | `(*Probot).Receive`                        |
| `probot.ready()`                       | `(*Probot).Ready()` / `Done()`             |
| `context.payload`                      | `(*Context).Payload()` / `ParseInto(&v)`   |
| `context.octokit`                      | `(*Context).GitHub()` (alias `Octokit()`)  |
| `context.log`                          | `(*Context).Log()`                         |
| `context.isBot`                        | `(*Context).IsBot()`                       |
| `context.repo()/issue()/pullRequest()` | `(*Context).Repo()/Issue()/PullRequest()`  |
| `context.config(file, defaults?)`      | `(*Context).Config(file, defaults...)`     |
| `Server` + setup wizard                | `probot.NewServer` / `(*Server).Start`     |
| webhook signature verification         | `probot.VerifyGitHubSignature` + `WebhookHandler` |
| GHES `baseUrl`                         | `Options.BaseURL`                          |
| pino logging                           | zerolog (pretty / json)                    |

## API overview

### Options

```go
type Options struct {
    AppID        string        // required unless GithubToken
    PrivateKey   string        // .pem contents, required unless GithubToken
    Secret       string        // webhook secret (default "development")
    GithubToken  string        // PAT / installation token alternative
    LogLevel     string        // trace|debug|info|warn|error|fatal|panic (default info)
    LogFormat    string        // "pretty" or "json"
    Port         int           // server port (default 3000)
    Host         string        // bind host (default "localhost")
    BaseURL      string        // GHES API URL, e.g. https://ghe.example/api/v3
    WebhookPath  string        // webhook mount (default "/api/github/webhooks")
    RequestTimeout time.Duration // per GitHub API request (default 30s)
}
```

`probot.OptionsFromEnv()` reads standard names (`APP_ID`, `PRIVATE_KEY`,
`PRIVATE_KEY_PATH`, `WEBHOOK_SECRET`, `GITHUB_TOKEN`, `LOG_LEVEL`, `LOG_FORMAT`,
`PORT`, `HOST`, `GHE_HOST`, `WEBHOOK_PATH`) and `probot.LoadEnvFile(".env")`
loads a `.env` file. A `PROBOT_` prefix is accepted for every variable.

### Handling events

Event names follow GitHub webhook naming: `"issues"` matches every issue action,
`"issues.opened"` matches only that action.

```go
app.On("pull_request.opened", func(ctx *probot.Context) error {
    pr := ctx.PullRequest()
    logger := ctx.Log()
    logger.Info().Str("owner", pr.Owner).Int("number", pr.Number).Msg("PR opened")
    return nil
})
```

The authenticated GitHub client is a [go-github](https://github.com/google/go-github)
`*github.Client`, pre-authenticated for the installation that triggered the
event. To keep handler signatures light, use `ctx.GitHub()`.

### Configuration files

`ctx.Config("config.yml", defaults...)` loads `.github/config.yml` from the
repository's default branch. YAML and JSON are supported, defaults are
deep-merged, and an `_extends: org/repo` key inherits a shared config from
another repository.

### GitHub App authentication

- **App credentials** (`AppID` + `PrivateKey`): a JWT signed app-level client
  (`Auth()`) and per-installation clients that obtain and cache installation
  access tokens (via `ghinstallation`). Clients are cached and tokens are
  refreshed automatically.
- **Token mode** (`GithubToken`): a static bearer token, ideal for tests and
  simple single-repo integrations.
- **GitHub Enterprise Server**: set `Options.BaseURL`.

### Server and setup wizard

`probot.NewServer` serves the webhook endpoint and a setup wizard at `/`. When
the app is not configured, the wizard walks through GitHub's manifest flow and
writes `APP_ID`, `WEBHOOK_SECRET` and `PRIVATE_KEY` to `.env`. Create an
unconfigured instance to start in wizard mode:

```go
app, _ := probot.New(probot.Options{})
probot.NewServer(probot.ServerOptions{Probot: app}).Start()
```

For embedding in an existing server use `app.WebhookMiddleware()` or
`app.Handler(next)`.

## Run the example

```sh
go run ./example/basic
```

Visit `http://localhost:3000` to register a GitHub App through the wizard, or
set `APP_ID` / `PRIVATE_KEY_PATH` / `WEBHOOK_SECRET` and point your app's
webhooks at `http://localhost:3000/api/github/webhooks`.

## Layout

```
options.go   Options, env parsing, .env loader
probot.go    Probot core (On/OnAny/OnError/Load/Auth/Receive/Ready)
context.go   Context helpers (payload, GitHub client, repo/issue/PR, config)
webhook.go   HMAC signature verification + webhook HTTP handler/middleware
emitter.go   event dispatch (Receive)
github.go    go-github client construction + ghinstallation auth
server.go    Server, setup wizard, manifest callback
logger.go    zerolog setup
config_test.go, probot_test.go, server_test.go   tests (no GitHub required)
example/basic   runnable example app
```

## License

ISC, matching Probot.
