package probot

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// VerifyGitHubSignature verifies the X-Hub-Signature-256 header against the
// payload using the given webhook secret. Returns an error when the signature
// is missing or invalid.
func VerifyGitHubSignature(secret, payload, signature string) error {
	if signature == "" {
		return errors.New("probot: missing X-Hub-Signature-256 header")
	}
	const prefix = "sha256="
	if len(signature) < len(prefix) || !strings.HasPrefix(signature, prefix) {
		return errors.New("probot: unsupported signature format")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	received := signature[len(prefix):]
	if !hmac.Equal([]byte(expected), []byte(received)) {
		return errors.New("probot: signature mismatch")
	}
	return nil
}

// WebhookHandler returns an http.HandlerFunc that validates and dispatches
// incoming GitHub webhooks. It is mounted at WebhookPath by Server, or can be
// mounted in any HTTP server via WebhookMiddleware / Handler.
func (p *Probot) WebhookHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		eventType := r.Header.Get("X-GitHub-Event")
		deliveryID := r.Header.Get("X-GitHub-Delivery")
		if eventType == "" {
			http.Error(w, "missing X-GitHub-Event header", http.StatusBadRequest)
			return
		}
		if deliveryID == "" {
			deliveryID = "unknown"
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			p.log.Warn().Err(err).Msg("failed to read webhook body")
			http.Error(w, "failed to read body", http.StatusInternalServerError)
			return
		}

		if err := VerifyGitHubSignature(p.secret(), string(body), r.Header.Get("X-Hub-Signature-256")); err != nil {
			p.log.Warn().Err(err).Str("event", eventType).Msg("invalid webhook signature")
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid JSON payload", http.StatusBadRequest)
			return
		}
		if payload == nil {
			http.Error(w, "expected JSON object payload", http.StatusBadRequest)
			return
		}

		p.log.Info().Str("event", eventType).Str("delivery_id", deliveryID).Msg("webhook received")
		if err := p.Receive(WebhookEvent{Name: eventType, ID: deliveryID, Payload: payload}); err != nil {
			p.log.Error().Err(err).Str("event", eventType).Msg("webhook handlers failed")
		}

		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "OK")
	}
}

// WebhookMiddleware returns a net/http middleware that handles webhook events.
// It is the Go equivalent of probot's getNodeMiddleware(): mount it in your own
// server. Requests to other paths are passed through.
func (p *Probot) WebhookMiddleware() func(http.Handler) http.Handler {
	handler := p.WebhookHandler()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == p.opts.WebhookPath {
				handler(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Handler returns an http.Handler that handles webhook events at WebhookPath.
// Requests to other paths are handled by next if provided, otherwise a 404.
func (p *Probot) Handler(next ...http.Handler) http.Handler {
	handler := p.WebhookHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == p.opts.WebhookPath {
			handler(w, r)
			return
		}
		if len(next) > 0 && next[0] != nil {
			next[0].ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}
