package handlers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/internal/services"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/response"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// TransferTokenHandler exposes the issue/list/revoke endpoints behind the
// authenticated WHM router and the public Redeem endpoint that the
// destination panel calls during a migration.
type TransferTokenHandler struct {
	service *services.TransferTokenService
	db      *mongo.Database

	// Per-IP rate limiter for the public redeem endpoint. The redeem path
	// performs bcrypt comparisons, which are intentionally slow — without a
	// limiter the endpoint would be a CPU-burner under enumeration. We
	// allow burst=20 hits with a 15-second sliding window per source IP.
	rl   map[string]*ipRateBucket
	rlMu sync.Mutex
}

type ipRateBucket struct {
	count int
	until time.Time
}

func NewTransferTokenHandler(s *services.TransferTokenService, db *mongo.Database) *TransferTokenHandler {
	return &TransferTokenHandler{
		service: s,
		db:      db,
		rl:      make(map[string]*ipRateBucket),
	}
}

// Issue mints a new transfer token. WHM-only — caller must hold
// server.manage (the route definition enforces this).
func (h *TransferTokenHandler) Issue(c *fiber.Ctx) error {
	var req models.IssueTransferTokenRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	user := h.lookupCaller(c)
	tok, err := h.service.Issue(c.UserContext(), &req, user)
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Created(c, tok)
}

// List returns all tokens (active + revoked + expired). Same auth gate as
// Issue.
func (h *TransferTokenHandler) List(c *fiber.Ctx) error {
	tokens, err := h.service.List(c.UserContext())
	if err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.Success(c, tokens)
}

// Revoke disables a token immediately and removes the corresponding
// authorized_keys line.
func (h *TransferTokenHandler) Revoke(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := h.service.Revoke(c.UserContext(), id); err != nil {
		return response.InternalError(c, err.Error())
	}
	return response.SuccessMessage(c, "Token revoked", nil)
}

// Redeem is the PUBLIC endpoint hit by the destination panel during a
// migration. It must NOT require auth — the token is the auth — so we
// register it on the public router (see main.go). Rate-limited per source
// IP to keep the bcrypt comparisons from being weaponised.
func (h *TransferTokenHandler) Redeem(c *fiber.Ctx) error {
	if !h.allowRedeem(c.IP()) {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"success": false,
			"message": "Too many redeem attempts; please wait and try again.",
		})
	}
	var req models.RedeemTransferTokenRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "Invalid request body", nil)
	}
	resp, err := h.service.Redeem(c.UserContext(), req.Token, c.IP())
	if err != nil {
		return response.BadRequest(c, err.Error(), nil)
	}
	return response.Success(c, resp)
}

// allowRedeem implements the per-IP sliding window. Returns false if the
// caller has burned through the budget.
func (h *TransferTokenHandler) allowRedeem(ip string) bool {
	if ip == "" {
		ip = "_unknown"
	}
	const windowSec = 15
	const burst = 20

	h.rlMu.Lock()
	defer h.rlMu.Unlock()
	now := time.Now()
	b, ok := h.rl[ip]
	if !ok || now.After(b.until) {
		h.rl[ip] = &ipRateBucket{count: 1, until: now.Add(windowSec * time.Second)}
		// Lazy GC to keep the map from growing forever.
		if len(h.rl) > 4096 {
			for k, v := range h.rl {
				if now.After(v.until) {
					delete(h.rl, k)
				}
			}
		}
		return true
	}
	if b.count >= burst {
		return false
	}
	b.count++
	return true
}

// lookupCaller resolves the requesting user from JWT claims so we can stamp
// CreatedBy{UserID,Email} on the issued token. Returns nil if anything
// fails — token issuance proceeds regardless because the audit trail is
// nice-to-have, not a security boundary.
func (h *TransferTokenHandler) lookupCaller(c *fiber.Ctx) *models.User {
	userHex, ok := c.Locals("user_id").(string)
	if !ok || userHex == "" {
		return nil
	}
	oid, err := primitive.ObjectIDFromHex(userHex)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 2*time.Second)
	defer cancel()
	var u models.User
	if err := h.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": oid}).Decode(&u); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil
		}
		return nil
	}
	return &u
}
