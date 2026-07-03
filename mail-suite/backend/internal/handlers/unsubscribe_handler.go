package handlers

import (
	"html"

	"github.com/betazeninfotech/mail-suite/internal/services"
	"github.com/gofiber/fiber/v2"
)

type UnsubscribeHandler struct {
	contacts *services.ContactService
}

func NewUnsubscribeHandler(contacts *services.ContactService) *UnsubscribeHandler {
	return &UnsubscribeHandler{contacts: contacts}
}

// Unsubscribe is the PUBLIC (no-auth) one-click unsubscribe target embedded in
// campaign mail. Crucially, the mutation happens ONLY on POST — a GET returns a
// confirmation page with a button that POSTs. This prevents link-prefetching
// mail scanners (Microsoft Safe Links, Proofpoint, etc.), which fetch every URL
// in a message with GET, from silently unsubscribing the recipient. The RFC 8058
// List-Unsubscribe-Post one-click flow already POSTs, so it works directly.
func (h *UnsubscribeHandler) Unsubscribe(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	token := c.Params("token")

	if c.Method() == fiber.MethodPost {
		email := h.contacts.UnsubscribeByToken(c.UserContext(), token)
		if email == "" {
			return c.Status(fiber.StatusNotFound).SendString(page("This unsubscribe link is invalid or has expired.", "", ""))
		}
		return c.SendString(page("You have been unsubscribed.", html.EscapeString(email)+" will no longer receive campaign emails.", ""))
	}

	// GET: confirmation page only — no state change.
	form := `<form method="post" action="/u/` + html.EscapeString(token) + `" style="margin-top:18px">` +
		`<button type="submit" style="background:#0369a1;color:#fff;border:0;border-radius:9px;padding:11px 22px;font-size:15px;font-weight:600;cursor:pointer">Unsubscribe</button></form>`
	return c.SendString(page("Unsubscribe?", "Confirm you no longer want to receive campaign emails.", form))
}

func page(title, sub, extra string) string {
	subHTML := ""
	if sub != "" {
		subHTML = `<p style="color:#64748b">` + sub + `</p>`
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1"><title>Unsubscribe</title></head>` +
		`<body style="font-family:'Segoe UI',system-ui,sans-serif;background:#f4f9fd;margin:0;display:grid;place-items:center;min-height:100vh">` +
		`<div style="background:#fff;border:1px solid #d3e3f1;border-radius:14px;padding:32px 36px;max-width:440px;text-align:center;box-shadow:0 8px 24px -12px rgba(3,105,161,.2)">` +
		`<h1 style="color:#075985;font-size:20px;margin:0 0 8px">` + html.EscapeString(title) + `</h1>` + subHTML + extra +
		`<p style="color:#94a3b8;font-size:13px;margin-top:18px">Betazen Mail</p></div></body></html>`
}
