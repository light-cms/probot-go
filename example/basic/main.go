// Command basic is a complete, minimal Probot app in Go. It replies to newly
// opened issues with a thanks comment and logs every webhook event.
//
// Run it after setting credentials:
//
//	export APP_ID=12345
//	export PRIVATE_KEY_PATH=~/github-app.pem
//	export WEBHOOK_SECRET=development
//	go run ./example/basic
//
// Point your GitHub App webhooks at http://localhost:3000/api/github/webhooks.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/go-github/v88/github"
	"github.com/rs/zerolog/log"

	"github.com/Pratyay360/probot-go"
)

func main() {
	if err := probot.LoadEnvFile(".env"); err != nil && !os.IsNotExist(err) {
		log.Warn().Err(err).Msg("failed to load .env")
	}
	if cwd, err := os.Getwd(); err == nil {
		log.Info().Str("cwd", cwd).Msg(".env is read from and written to this directory")
	}

	opts, err := probot.OptionsFromEnv()
	if err != nil {
		log.Fatal().Err(err).Msg("invalid options from environment")
	}
	opts.Host = "localhost"

	app, err := probot.New(opts)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create probot")
	}

	// Respond to newly opened issues — but never to bots, to avoid loops.
	app.On("issues.opened", func(ctx *probot.Context) error {
		if ctx.IsBot() {
			return nil
		}
		issue, err := ctx.Issue()
		if err != nil {
			return err
		}
		logger := ctx.Log()
		logger.Info().
			Str("repo", issue.Repo).
			Int("issue_number", issue.Number).
			Msg("thanking for new issue")

		_, _, err = ctx.GitHub().Issues.CreateComment(context.Background(),
			issue.Owner, issue.Repo, issue.Number,
			&github.IssueComment{Body: github.Ptr("Thanks for opening this issue!")},
		)
		return err
	})

	// Log every event.
	app.OnAny(func(ctx *probot.Context) error {
		logger := ctx.Log()
		logger.Info().
			Str("name", ctx.Name()).
			Str("delivery_id", ctx.ID()).
			Msg("received webhook event")
		return nil
	})

	// Surface handler errors.
	app.OnError(func(ctx *probot.Context, err error) {
		log.Error().Err(err).Str("event", ctx.Name()).Msg("webhook handler failed")
	})

	fmt.Printf("probot %s listening on http://%s:%d%s\n",
		app.Version(), opts.Host, opts.Port, app.WebhookPath())

	server := probot.NewServer(probot.ServerOptions{Probot: app})
	if err := server.Start(); err != nil {
		log.Fatal().Err(err).Msg("server failed")
	}
}
