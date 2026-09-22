<p align="center">
  <img alt="header" src="https://shieldcn.dev/header/gradient.svg?title=probot-go&subtitle=GitHub+Apps+in+Go,+without+the+boilerplate&mode=dark" />
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/pratyay360/probot-go"><img alt="go reference" src="https://pkg.go.dev/badge/github.com/pratyay360/probot-go.svg" /></a>
  <a href="https://goreportcard.com/report/github.com/pratyay360/probot-go"><img alt="go report card" src="https://goreportcard.com/badge/github.com/pratyay360/probot-go" /></a>
  <img alt="license" src="https://img.shields.io/badge/license-ISC-555555.svg" />
</p>

- 🔐 **Webhook Validation**: HMAC signature verification built-in.
- 🤖 **Auth Handling**: Auto-refreshing installation tokens via `ghinstallation`.
- 🧩 **GitHub Client**: Pre-authenticated `go-github` client in every context.
- 📄 **Repo Config**: Load `.github/config.yml` with defaults and inheritance.
- 🧙 **Setup Wizard**: One-click GitHub App registration via manifest flow.

## Quick Start

```sh
go get github.com/pratyay360/probot-go
```

Create `main.go`:

```go
package main

import (
    "context"
    "os"

    "github.com/google/go-github/github"
    "github.com/pratyay360/probot-go"
)

func main() {
    app, err := probot.New(probot.Options{
        AppID:      os.Getenv("APP_ID"),
        PrivateKey: os.Getenv("PRIVATE_KEY"),
        Secret:     os.Getenv("WEBHOOK_SECRET"),
    })
    if err != nil {
        panic(err)
    }

    app.On("issues.opened", func(ctx *probot.Context) error {
        issue := ctx.Issue()
        _, _, err := ctx.GitHub().Issues.CreateComment(
            context.Background(), issue.Owner, issue.Repo, issue.Number,
            &github.IssueComment{Body: github.Ptr("Thanks for opening this issue!")},
        )
        return err
    })

    probot.NewServer(probot.ServerOptions{Probot: app}).Start()
}
```

Run it:
```sh
go run main.go
```
> Visit `http://localhost:3000` to register your app via the wizard if no env vars are set.

## API Overview

### Options
```go
type Options struct {
    AppID        string        // GitHub App ID
    PrivateKey   string        // PEM contents or path via PRIVATE_KEY_PATH
    Secret       string        // Webhook secret (default: "development")
    GithubToken  string        // Optional: Use PAT instead of App Auth
    LogLevel     string        // trace|debug|info|warn|error (default: info)
    Port         int           // Default: 3000
    BaseURL      string        // For GitHub Enterprise Server
}
```

### Event Handling
```go
// Single event
app.On("pull_request.opened", handler)

// Multiple events
app.OnMany([]string{"issues.closed", "pr.closed"}, handler)

// All events
app.OnAny(handler)

// Errors
app.OnError(func(ctx *probot.Context, err error) { ... })
```

### Context Helpers
- `ctx.GitHub()`: Authenticated `*github.Client`.
- `ctx.Issue()`, `ctx.Repo()`, `ctx.PullRequest()`: Parsed structs.
- `ctx.Config("config.yml", defaults)`: Loads repo config.
- `ctx.Log()`: Structured logger (zerolog).

### Embedding in Existing Server
```go
mux := http.NewServeMux()
mux.Handle("/webhooks", app.WebhookMiddleware(http.HandlerFunc(next)))
http.ListenAndServe(":8080", mux)
```

## Testing

Tests run offline. Use `Receive` to simulate events:

```go
app.Receive(&github.Event{
    Type:       github.String("issues"),
    Action:     github.String("opened"),
    // ... payload
})
```

## Contributing

1. Fork the repo.
2. Run `go test ./...` and `gofmt -l .`.
3. Submit a PR.

## License

[ISC](LICENSE)
