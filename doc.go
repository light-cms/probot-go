// Package probot is a Go port of Probot, a framework for building GitHub Apps.
//
// It eliminates the boilerplate of webhook validation, authentication and API
// setup so you can focus on what your app actually does:
//
//	app, _ := probot.New(probot.Options{
//		AppID:      os.Getenv("APP_ID"),
//		PrivateKey: os.Getenv("PRIVATE_KEY"),
//		Secret:     os.Getenv("WEBHOOK_SECRET"),
//		Webhook: 	os.Getenv("WEBHOOK_URL")
//	})
//
//	app.On("issues.opened", func(ctx *probot.Context) error {
//		params := ctx.Issue()
//		_, _, err := ctx.GitHub().Issues.CreateComment(ctx,
//			params.Owner, params.Repo, params.Number,
//			&github.IssueComment{Body: github.Ptr("Thanks for opening this issue!")},
//		)
//		return err
//	})
//
//	server := probot.NewServer(probot.ServerOptions{Probot: app})
//	_ = server.Start()
package probot
