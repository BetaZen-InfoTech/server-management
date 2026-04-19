package mailer

import (
	"bytes"
	"fmt"
	"html"
	"strings"
	"text/template"
)

// PasswordResetData is everything the password-reset template needs.
// Role is surfaced so the email subject can say "Your WHM admin
// password reset" vs "Your hosting account password reset" — tenants
// and vendor_owners see slightly different wording.
type PasswordResetData struct {
	Name        string // recipient's display name (falls back to email local-part)
	Email       string
	Role        string
	ResetURL    string // full URL with token embedded
	ExpiresMin  int    // minutes until the token expires
	PanelName   string // "ServerPanel" or operator's branded name
	SupportFrom string // the from address, reused as a "reply to" hint
}

// BuildPasswordReset produces the three pieces needed to send the
// password-reset email: subject, plain-text body, and HTML body.
// HTML escaping is done on every interpolated field to defuse any
// malicious display name / email / role values that somehow got in.
func BuildPasswordReset(d PasswordResetData) (subject, text, htmlBody string, err error) {
	if d.PanelName == "" {
		d.PanelName = "ServerPanel"
	}
	if d.Name == "" {
		// Fall back to the local-part of the email so the greeting
		// still feels personal ("Hi sayan," instead of "Hi ,").
		if i := strings.Index(d.Email, "@"); i > 0 {
			d.Name = d.Email[:i]
		} else {
			d.Name = d.Email
		}
	}
	if d.ExpiresMin <= 0 {
		d.ExpiresMin = 30
	}

	subject = fmt.Sprintf("Reset your %s password", d.PanelName)

	var t bytes.Buffer
	tt := template.Must(template.New("t").Parse(pwResetText))
	if err = tt.Execute(&t, d); err != nil {
		return "", "", "", err
	}
	text = t.String()

	var h bytes.Buffer
	ht := template.Must(template.New("h").Parse(pwResetHTML))
	if err = ht.Execute(&h, htmlEscapeData(d)); err != nil {
		return "", "", "", err
	}
	htmlBody = h.String()
	return
}

// htmlEscapeData escapes every user-controlled field before it
// reaches the HTML template. Does NOT touch ResetURL — that's
// generated server-side from a hex token and a config base URL,
// so it's already safe. Escaping it would break the href.
func htmlEscapeData(d PasswordResetData) PasswordResetData {
	d.Name = html.EscapeString(d.Name)
	d.Email = html.EscapeString(d.Email)
	d.Role = html.EscapeString(d.Role)
	d.PanelName = html.EscapeString(d.PanelName)
	d.SupportFrom = html.EscapeString(d.SupportFrom)
	return d
}

const pwResetText = `Hi {{.Name}},

Someone — hopefully you — requested a password reset for your {{.PanelName}} account ({{.Email}}).

To set a new password, open this link in your browser within the next {{.ExpiresMin}} minutes:

{{.ResetURL}}

If you didn't request this, you can ignore the email — your password stays as it is.

— {{.PanelName}}
`

const pwResetHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<title>Reset your password</title>
</head>
<body style="margin:0;padding:0;background:#0f172a;color:#e2e8f0;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#0f172a;padding:40px 0;">
    <tr><td align="center">
      <table role="presentation" width="560" cellpadding="0" cellspacing="0" style="background:#111827;border:1px solid #1f2937;border-radius:12px;padding:32px;">
        <tr><td>
          <h1 style="margin:0 0 20px;font-size:20px;font-weight:600;color:#f3f4f6;">Reset your {{.PanelName}} password</h1>
          <p style="margin:0 0 16px;line-height:1.6;font-size:14px;color:#cbd5e1;">
            Hi {{.Name}},
          </p>
          <p style="margin:0 0 16px;line-height:1.6;font-size:14px;color:#cbd5e1;">
            Someone — hopefully you — requested a password reset for the account
            <strong style="color:#f3f4f6;">{{.Email}}</strong>.
          </p>
          <p style="margin:0 0 24px;line-height:1.6;font-size:14px;color:#cbd5e1;">
            Click the button below within the next <strong>{{.ExpiresMin}} minutes</strong> to pick a new password.
            After that, the link stops working and you'll need to request a fresh one.
          </p>
          <p style="margin:0 0 24px;">
            <a href="{{.ResetURL}}" style="display:inline-block;padding:12px 22px;background:#2563eb;color:#ffffff;text-decoration:none;border-radius:8px;font-size:14px;font-weight:500;">
              Reset my password
            </a>
          </p>
          <p style="margin:0 0 8px;line-height:1.5;font-size:12px;color:#64748b;">
            Or copy &amp; paste this URL into your browser:
          </p>
          <p style="margin:0 0 24px;font-family:ui-monospace,Menlo,monospace;font-size:11px;color:#94a3b8;word-break:break-all;">
            {{.ResetURL}}
          </p>
          <p style="margin:0;line-height:1.5;font-size:12px;color:#64748b;">
            If you didn't request this, you can safely ignore this email — your password won't change.
          </p>
        </td></tr>
      </table>
      <p style="margin:16px 0 0;font-size:11px;color:#64748b;">
        Sent by {{.PanelName}}{{if .SupportFrom}} · reply to {{.SupportFrom}}{{end}}
      </p>
    </td></tr>
  </table>
</body></html>`
