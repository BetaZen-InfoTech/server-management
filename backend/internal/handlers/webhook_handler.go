package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/mongo"
)

// WebhookHandler serves the public webhook endpoints that GitHub (and
// compatible providers — GitLab, Gitea — speak the same shape) call to
// trigger an auto-deploy of an app.
//
// The route is intentionally NOT behind the JWT auth middleware: GitHub
// can't present a Bearer token. Instead the request is authenticated by
// HMAC-SHA256 of the raw body using the per-app WebhookSecret, plus the
// secret-bearing path parameter (WebhookID, an unguessable 16-byte hex).
type WebhookHandler struct {
	service *services.AppService
}

func NewWebhookHandler(s *services.AppService) *WebhookHandler {
	return &WebhookHandler{service: s}
}

// githubPushPayload is the subset of GitHub's push event we care about
// — just the ref so we can decide whether the push targeted the branch
// the app is configured to track.
type githubPushPayload struct {
	Ref string `json:"ref"` // e.g. "refs/heads/main"
}

// GitHubPush handles POST /api/v1/webhooks/github/:id.
//
// Flow:
//  1. Look up the app by webhook ID. 404 on miss (don't leak existence).
//  2. Reject if AutoDeploy was disabled since the secret was issued.
//  3. Verify X-Hub-Signature-256 against WebhookSecret.
//  4. Parse the payload, ignore pushes to branches we don't track, ignore
//     anything that isn't a "push" event.
//  5. Kick off Redeploy in a background goroutine and 202 immediately so
//     GitHub doesn't time out the webhook (their cap is ~10s).
func (h *WebhookHandler) GitHubPush(c *fiber.Ctx) error {
	id := strings.TrimSpace(c.Params("id"))
	if id == "" {
		return response.NotFound(c, "webhook not found")
	}

	app, err := h.service.GetByWebhookID(c.UserContext(), id)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return response.NotFound(c, "webhook not found")
		}
		return response.InternalError(c, err.Error())
	}
	if !app.AutoDeploy || app.WebhookSecret == "" {
		// Auto-deploy was turned off but the secret/URL still existed at
		// some point. Treat as not-found to avoid revealing that the URL
		// was ever valid.
		return response.NotFound(c, "webhook not found")
	}

	body := c.Body()

	// HMAC verification. GitHub sends "sha256=<hex>"; GitLab sends a raw
	// X-Gitlab-Token header instead. We accept both shapes so the same
	// endpoint works for either provider.
	if !verifyGitHubSignature(body, app.WebhookSecret, c.Get("X-Hub-Signature-256")) &&
		!verifyGitLabToken(app.WebhookSecret, c.Get("X-Gitlab-Token")) {
		return response.Unauthorized(c, "invalid webhook signature")
	}

	// Filter on event type. GitHub sets X-GitHub-Event ("push", "ping", …);
	// "ping" is GitHub's setup probe and we acknowledge it without
	// triggering a redeploy. Anything else (issues, releases) is ignored.
	event := c.Get("X-GitHub-Event")
	if event == "ping" {
		return response.Success(c, fiber.Map{"pong": true, "app": app.Name})
	}
	if event != "" && event != "push" {
		return response.Success(c, fiber.Map{"ignored": event})
	}

	// Branch filter. If the app is pinned to a specific branch, ignore
	// pushes to other branches so a feature-branch CI push doesn't
	// stomp on prod.
	var p githubPushPayload
	_ = json.Unmarshal(body, &p) // best-effort; an empty Ref means "match any"
	if p.Ref != "" && app.GitBranch != "" {
		expected := "refs/heads/" + app.GitBranch
		if p.Ref != expected {
			return response.Success(c, fiber.Map{
				"ignored": "branch mismatch",
				"got":     p.Ref,
				"want":    expected,
			})
		}
	}

	// Fire-and-forget the redeploy. GitHub gets a fast 202 — anything
	// slower than ~10s and they retry, which would queue duplicate
	// deploys. The redeploy itself runs against a fresh background
	// context so the request cancellation doesn't kill it mid-build.
	appName := app.Name
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if _, err := h.service.Redeploy(ctx, appName); err != nil {
			fmt.Printf("[webhook] auto-redeploy of %s failed: %v\n", appName, err)
		}
	}()

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"success": true,
		"data":    fiber.Map{"queued": true, "app": appName},
	})
}

// verifyGitHubSignature returns true when the HMAC-SHA256 of `body`
// using `secret` matches the value in the `sha256=<hex>` header that
// GitHub sends. Constant-time compare so timing oracles don't leak the
// expected signature.
func verifyGitHubSignature(body []byte, secret, header string) bool {
	if header == "" || secret == "" {
		return false
	}
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got, err := hex.DecodeString(header[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// verifyGitLabToken accepts GitLab's plain-shared-secret model where the
// header value is the secret itself (no HMAC). Constant-time compare to
// keep the auth check uniform.
func verifyGitLabToken(secret, header string) bool {
	if header == "" || secret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(secret)) == 1
}

// EnableWebhook (POST /apps/:name/webhook/enable) generates a fresh ID +
// secret for an existing app. The secret is returned ONCE in the
// response — operators who lose it must re-enable to rotate.
func (h *WebhookHandler) EnableWebhook(c *fiber.Ctx) error {
	name := c.Params("name")
	id, secret, err := h.service.EnableWebhook(c.UserContext(), name)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, fiber.Map{
		"webhook_id":          id,
		"webhook_secret_once": secret,
	})
}

// DisableWebhook (POST /apps/:name/webhook/disable) clears the
// credentials so the URL stops working.
func (h *WebhookHandler) DisableWebhook(c *fiber.Ctx) error {
	name := c.Params("name")
	if err := h.service.DisableWebhook(c.UserContext(), name); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "webhook disabled", nil)
}
