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

	"github.com/rs/zerolog/log"

	"github.com/light-cms/probot-go"
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

	// Create a discussion whenever files are changed.
	app.On("push", func(ctx *probot.Context) error {
		// 1. Get repo details
		repoPay, ok := ctx.Payload()["repository"].(map[string]any)
		if !ok {
			return nil
		}
		repoName := repoPay["name"].(string)
		ownerPay := repoPay["owner"].(map[string]any)
		ownerName := ownerPay["name"].(string)
		if ownerName == "" {
			ownerName = ownerPay["login"].(string)
		}
		repoNodeID := repoPay["node_id"].(string)

		// 2. Extract changed files from commits
		commits, ok := ctx.Payload()["commits"].([]any)
		if !ok || len(commits) == 0 {
			return nil
		}

		changedFiles := []string{}
		seen := make(map[string]bool)
		for _, c := range commits {
			commit, ok := c.(map[string]any)
			if !ok {
				continue
			}
			for _, field := range []string{"added", "removed", "modified"} {
				if files, ok := commit[field].([]any); ok {
					for _, f := range files {
						name, ok := f.(string)
						if ok && !seen[name] {
							seen[name] = true
							changedFiles = append(changedFiles, name)
						}
					}
				}
			}
		}

		if len(changedFiles) == 0 {
			return nil
		}

		// 3. Fetch discussion categories to get a valid categoryId
		categoryID, err := getFirstDiscussionCategory(ctx, ownerName, repoName)
		if err != nil {
			return fmt.Errorf("failed to fetch discussion categories: %w", err)
		}
		logger := ctx.Log()
		if categoryID == "" {
			logger.Warn().Msg("no discussion categories found for repository")
			return nil
		}

		// 4. Create a new discussion with the changed files list
		title := fmt.Sprintf("File changes in push: %d files modified", len(changedFiles))
		body := "The following files were changed in the latest push:\n\n"
		for _, file := range changedFiles {
			body += fmt.Sprintf("- `%s`\n", file)
		}

		discussionURL, err := createDiscussion(ctx, repoNodeID, categoryID, title, body)
		if err != nil {
			return fmt.Errorf("failed to create discussion: %w", err)
		}

		logger.Info().Str("url", discussionURL).Msg("created discussion for file changes")
		return nil
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

	if os.Getenv("GITHUB_ACTIONS") == "true" {
		if err := app.RunAction(); err != nil {
			log.Fatal().Err(err).Msg("action failed")
		}
		return
	}

	fmt.Printf("probot %s listening on http://%s:%d%s\n",
		app.Version(), opts.Host, opts.Port, app.WebhookPath())

	server := probot.NewServer(probot.ServerOptions{Probot: app})
	if err := server.Start(); err != nil {
		log.Fatal().Err(err).Msg("server failed")
	}
}

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func getFirstDiscussionCategory(ctx *probot.Context, owner, name string) (string, error) {
	query := `query($owner: String!, $name: String!) {
		repository(owner: $owner, name: $name) {
			discussionCategories(first: 10) {
				nodes {
					id
					name
				}
			}
		}
	}`

	reqBody := graphQLRequest{
		Query: query,
		Variables: map[string]any{
			"owner": owner,
			"name":  name,
		},
	}

	req, err := ctx.GitHub().NewRequest(context.Background(), "POST", "graphql", reqBody)
	if err != nil {
		return "", err
	}

	var respData struct {
		Data struct {
			Repository struct {
				DiscussionCategories struct {
					Nodes []struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"discussionCategories"`
			} `json:"repository"`
		} `json:"data"`
	}

	_, err = ctx.GitHub().Do(req, &respData)
	if err != nil {
		return "", err
	}

	nodes := respData.Data.Repository.DiscussionCategories.Nodes
	if len(nodes) == 0 {
		return "", nil
	}

	for _, n := range nodes {
		if n.Name == "General" || n.Name == "Q&A" {
			return n.ID, nil
		}
	}
	return nodes[0].ID, nil
}

func createDiscussion(ctx *probot.Context, repoID, categoryID, title, body string) (string, error) {
	mutation := `mutation($repoId: ID!, $categoryId: ID!, $title: String!, $body: String!) {
		createDiscussion(input: {repositoryId: $repoId, categoryId: $categoryId, title: $title, body: $body}) {
			discussion {
				id
				url
			}
		}
	}`

	reqBody := graphQLRequest{
		Query: mutation,
		Variables: map[string]any{
			"repoId":     repoID,
			"categoryId": categoryID,
			"title":      title,
			"body":       body,
		},
	}

	req, err := ctx.GitHub().NewRequest(context.Background(), "POST", "graphql", reqBody)
	if err != nil {
		return "", err
	}

	var respData struct {
		Data struct {
			CreateDiscussion struct {
				Discussion struct {
					ID  string `json:"id"`
					URL string `json:"url"`
				} `json:"discussion"`
			} `json:"createDiscussion"`
		} `json:"data"`
	}

	_, err = ctx.GitHub().Do(req, &respData)
	if err != nil {
		return "", err
	}

	return respData.Data.CreateDiscussion.Discussion.URL, nil
}
