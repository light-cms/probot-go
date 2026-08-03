package probot

import "errors"

// WebhookEvent is a single webhook delivery, used with Receive.
type WebhookEvent struct {
	// Name is the webhook event name, e.g. "issues".
	Name string
	// ID is a unique delivery ID (e.g. the X-GitHub-Delivery header).
	ID string
	// Payload is the parsed webhook JSON payload.
	Payload map[string]any
}

// Receive dispatches a webhook event through the event pipeline. Errors raised
// by handlers are routed to the registered error handler (OnError) and also
// returned joined together. This mirrors probot's app.receive() and is useful
// for tests.
func (p *Probot) Receive(event WebhookEvent) error {
	ctx, err := newContext(p, event.Name, event.ID, event.Payload)
	if err != nil {
		return err
	}

	var errs []error

	action, _ := event.Payload["action"].(string)
	qualified := event.Name
	if action != "" {
		qualified = event.Name + "." + action
	}

	for _, h := range p.handlers {
		if h.name != event.Name && h.name != qualified {
			continue
		}
		if err := h.fn(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	for _, fn := range p.anyHandlers {
		if err := fn(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	var joined error
	for _, e := range errs {
		joined = errors.Join(joined, e)
	}
	if joined != nil && p.errorHandler != nil {
		p.errorHandler(ctx, joined)
	}
	return joined
}
