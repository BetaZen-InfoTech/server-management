package handlers

import (
	"strconv"
	"strings"

	"github.com/betazeninfotech/mail-suite/internal/services"
	"github.com/betazeninfotech/mail-suite/pkg/response"
	"github.com/gofiber/fiber/v2"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type TrackingHandler struct {
	svc *services.TrackingService
}

func NewTrackingHandler(svc *services.TrackingService) *TrackingHandler {
	return &TrackingHandler{svc: svc}
}

// transparent1x1GIF is the pixel body served by the open beacon.
var transparent1x1GIF = []byte{
	0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00,
	0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0x21, 0xf9, 0x04, 0x01, 0x00, 0x00, 0x00,
	0x00, 0x2c, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x02,
	0x44, 0x01, 0x00, 0x3b,
}

// trackID strips the .png/.gif extension the pixel URL carries so the raw id
// remains the lookup key.
func trackID(raw string) string {
	raw = strings.TrimSuffix(raw, ".png")
	raw = strings.TrimSuffix(raw, ".gif")
	return raw
}

// Open is the PUBLIC (no-auth) tracking pixel. It records the open and always
// returns a 1x1 transparent GIF with no-cache headers so re-opens re-fire.
func (h *TrackingHandler) Open(c *fiber.Ctx) error {
	h.svc.RecordOpen(c.UserContext(), trackID(c.Params("id")), c.IP(), c.Get("User-Agent"))
	c.Set("Content-Type", "image/gif")
	c.Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	c.Set("Pragma", "no-cache")
	c.Set("Expires", "0")
	return c.Send(transparent1x1GIF)
}

// Click is the PUBLIC (no-auth) click redirect. It records the click, then 302s
// to the original target. A missing/invalid target falls back to the home page.
func (h *TrackingHandler) Click(c *fiber.Ctx) error {
	target := h.svc.RecordClick(c.UserContext(), c.Params("id"), c.Query("u"), c.IP(), c.Get("User-Agent"))
	if target == "" {
		return c.Redirect("/", fiber.StatusFound)
	}
	return c.Redirect(target, fiber.StatusFound)
}

// ListSent (authed) backs the "Sent + tracking" dashboard.
func (h *TrackingHandler) ListSent(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	accID := primitive.NilObjectID
	if q := c.Query("account_id"); q != "" {
		if oid, err := primitive.ObjectIDFromHex(q); err == nil {
			accID = oid
		}
	}
	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	items, err := h.svc.ListSent(c.UserContext(), uid, accID, limit)
	if err != nil {
		return response.Internal(c, err.Error())
	}
	return response.OK(c, items)
}

// Detail (authed) returns one sent message plus its open/click events.
func (h *TrackingHandler) Detail(c *fiber.Ctx) error {
	uid, ok := userOID(c)
	if !ok {
		return response.Unauthorized(c, "invalid user")
	}
	sent, events, err := h.svc.Detail(c.UserContext(), uid, c.Params("track_id"))
	if err != nil {
		return response.NotFound(c, err.Error())
	}
	return response.OK(c, fiber.Map{"message": sent, "events": events})
}
