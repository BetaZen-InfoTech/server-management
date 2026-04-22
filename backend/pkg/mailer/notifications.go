package mailer

import (
	"bytes"
	"fmt"
	"html"
	"strings"
	"text/template"
)

// This file groups the transactional-email templates that fan out from
// panel events (SMTP configured, domain added, SSL issued / failed,
// domain about to expire). The password-reset template lives in
// templates.go alongside its build-time constants; everything here is
// structured identically — one data struct, one Build* function per
// event — so callers can swap them in and out without learning a new
// shape.
//
// Every interpolated string that originates from user-controlled data
// (display name, email, domain, error reason) is HTML-escaped before
// it reaches the HTML body. Panel URLs are ours, so they're not
// escaped — escaping would break their `href` values.

// -----------------------------------------------------------------------
// SMTP setup confirmation
// -----------------------------------------------------------------------

// SMTPSetupData drives the "SMTP is live" confirmation email the panel
// sends to the owner-configured contact address right after they save
// a valid outgoing-mail config. It's the simplest possible proof the
// relay works end-to-end — if this email lands, password resets and
// the rest of the fan-out will too.
type SMTPSetupData struct {
	Name      string // recipient display name (falls back to email local-part)
	Email     string // recipient address — shown in the body
	Host      string // smtp host (smtp.gmail.com, …)
	Port      int
	TLSMode   string // "tls" / "starttls" / "none"
	FromAddr  string // the "From" address the relay now sends as
	PanelName string
	PanelURL  string // https://<panel-domain>/whm — header link
}

func BuildSMTPSetup(d SMTPSetupData) (subject, text, htmlBody string, err error) {
	applyDefaults(&d.Name, &d.Email, &d.PanelName)
	subject = fmt.Sprintf("%s — outgoing mail is now configured", d.PanelName)

	text = fmt.Sprintf(`Hi %s,

Your %s outgoing-mail relay is now live.

  Host      : %s
  Port      : %d
  Encryption: %s
  From      : %s

Password reset emails, domain-expiry warnings and SSL notifications will
now be delivered through this relay. If you didn't just save these
settings, open %s and review them immediately.

— %s
`, d.Name, d.PanelName, d.Host, d.Port, humanTLS(d.TLSMode), d.FromAddr, d.PanelURL, d.PanelName)

	esc := d
	esc.Name = html.EscapeString(esc.Name)
	esc.Email = html.EscapeString(esc.Email)
	esc.Host = html.EscapeString(esc.Host)
	esc.FromAddr = html.EscapeString(esc.FromAddr)
	esc.PanelName = html.EscapeString(esc.PanelName)
	esc.TLSMode = html.EscapeString(humanTLS(esc.TLSMode))

	var h bytes.Buffer
	if err := template.Must(template.New("h").Parse(smtpSetupHTML)).Execute(&h, esc); err != nil {
		return "", "", "", err
	}
	return subject, text, h.String(), nil
}

const smtpSetupHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>SMTP configured</title></head>
<body style="margin:0;padding:0;background:#0f172a;color:#e2e8f0;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#0f172a;padding:40px 0;">
    <tr><td align="center">
      <table role="presentation" width="560" cellpadding="0" cellspacing="0" style="background:#111827;border:1px solid #1f2937;border-radius:12px;padding:32px;">
        <tr><td>
          <h1 style="margin:0 0 20px;font-size:20px;font-weight:600;color:#f3f4f6;">Outgoing mail is live</h1>
          <p style="margin:0 0 16px;line-height:1.6;font-size:14px;color:#cbd5e1;">Hi {{.Name}},</p>
          <p style="margin:0 0 16px;line-height:1.6;font-size:14px;color:#cbd5e1;">
            Your <strong style="color:#f3f4f6;">{{.PanelName}}</strong> outgoing-mail relay is now configured. Password resets, domain-expiry warnings and SSL notifications will be delivered through this relay from now on.
          </p>
          <table cellpadding="6" cellspacing="0" style="width:100%;background:#0b1220;border:1px solid #1f2937;border-radius:8px;font-size:13px;color:#cbd5e1;margin:0 0 24px;">
            <tr><td style="color:#94a3b8;">Host</td><td style="color:#f3f4f6;font-family:ui-monospace,Menlo,monospace;">{{.Host}}</td></tr>
            <tr><td style="color:#94a3b8;">Port</td><td style="color:#f3f4f6;font-family:ui-monospace,Menlo,monospace;">{{.Port}}</td></tr>
            <tr><td style="color:#94a3b8;">Encryption</td><td style="color:#f3f4f6;">{{.TLSMode}}</td></tr>
            <tr><td style="color:#94a3b8;">From</td><td style="color:#f3f4f6;font-family:ui-monospace,Menlo,monospace;">{{.FromAddr}}</td></tr>
          </table>
          <p style="margin:0;line-height:1.5;font-size:12px;color:#64748b;">
            If you didn't just save these settings, open <a href="{{.PanelURL}}" style="color:#60a5fa;">{{.PanelName}}</a> and review them immediately.
          </p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body></html>`

// -----------------------------------------------------------------------
// New domain added
// -----------------------------------------------------------------------

// NewDomainData drives the "your new domain is live" email sent to the
// vendor who owns the account a domain was just added under. The email
// intentionally lands BEFORE the follow-up SSL email so the vendor sees
// the chronology correctly: domain created → SSL issued.
type NewDomainData struct {
	Name       string // vendor display name
	Email      string // vendor email
	Domain     string // e.g. "acme.example.com"
	PHPVersion string // as-configured, for confidence
	ServerIP   string // DNS target, so the vendor can sanity-check DNS
	PanelName  string
	PanelURL   string // https://<panel>/user-panel — vendor surface, not WHM
}

func BuildNewDomain(d NewDomainData) (subject, text, htmlBody string, err error) {
	applyDefaults(&d.Name, &d.Email, &d.PanelName)
	subject = fmt.Sprintf("%s — %s is live", d.PanelName, d.Domain)

	text = fmt.Sprintf(`Hi %s,

Your new domain %s is now live on %s.

  Domain      : %s
  PHP Version : %s
  Server IP   : %s

Point your DNS A record at %s if it isn't already. Your SSL certificate
is being issued automatically in the background — you'll get a separate
email when it's ready.

Sign in to manage the domain:
  %s

— %s
`, d.Name, d.Domain, d.PanelName, d.Domain, d.PHPVersion, d.ServerIP, d.ServerIP, d.PanelURL, d.PanelName)

	esc := d
	esc.Name = html.EscapeString(esc.Name)
	esc.Email = html.EscapeString(esc.Email)
	esc.Domain = html.EscapeString(esc.Domain)
	esc.PHPVersion = html.EscapeString(esc.PHPVersion)
	esc.ServerIP = html.EscapeString(esc.ServerIP)
	esc.PanelName = html.EscapeString(esc.PanelName)

	var h bytes.Buffer
	if err := template.Must(template.New("h").Parse(newDomainHTML)).Execute(&h, esc); err != nil {
		return "", "", "", err
	}
	return subject, text, h.String(), nil
}

const newDomainHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Domain live</title></head>
<body style="margin:0;padding:0;background:#0f172a;color:#e2e8f0;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#0f172a;padding:40px 0;">
    <tr><td align="center">
      <table role="presentation" width="560" cellpadding="0" cellspacing="0" style="background:#111827;border:1px solid #1f2937;border-radius:12px;padding:32px;">
        <tr><td>
          <h1 style="margin:0 0 20px;font-size:20px;font-weight:600;color:#f3f4f6;">{{.Domain}} is live</h1>
          <p style="margin:0 0 16px;line-height:1.6;font-size:14px;color:#cbd5e1;">Hi {{.Name}},</p>
          <p style="margin:0 0 16px;line-height:1.6;font-size:14px;color:#cbd5e1;">
            Your new domain is now live on <strong style="color:#f3f4f6;">{{.PanelName}}</strong>.
          </p>
          <table cellpadding="6" cellspacing="0" style="width:100%;background:#0b1220;border:1px solid #1f2937;border-radius:8px;font-size:13px;color:#cbd5e1;margin:0 0 16px;">
            <tr><td style="color:#94a3b8;">Domain</td><td style="color:#f3f4f6;font-family:ui-monospace,Menlo,monospace;">{{.Domain}}</td></tr>
            <tr><td style="color:#94a3b8;">PHP Version</td><td style="color:#f3f4f6;">{{.PHPVersion}}</td></tr>
            <tr><td style="color:#94a3b8;">Server IP</td><td style="color:#f3f4f6;font-family:ui-monospace,Menlo,monospace;">{{.ServerIP}}</td></tr>
          </table>
          <p style="margin:0 0 24px;line-height:1.5;font-size:13px;color:#cbd5e1;">
            Point your DNS A record at <code style="color:#f3f4f6;font-family:ui-monospace,Menlo,monospace;">{{.ServerIP}}</code> if it isn't already. The SSL certificate is being issued automatically in the background — you'll get a separate email once it's ready.
          </p>
          <p style="margin:0 0 8px;">
            <a href="{{.PanelURL}}" style="display:inline-block;padding:10px 18px;background:#2563eb;color:#ffffff;text-decoration:none;border-radius:8px;font-size:14px;font-weight:500;">Manage in {{.PanelName}}</a>
          </p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body></html>`

// -----------------------------------------------------------------------
// Domain expiry warning
// -----------------------------------------------------------------------

// DomainExpiryData drives the N-days-to-go registrar-expiry warning. The
// cron that calls this picks the stage (30 / 21 / 14 / 7 / 5 / 3 / 2 / 1
// days) and passes it through; the template just echoes the number and
// adapts the tone slightly (soft reminder vs. red-alert for <=3 days).
type DomainExpiryData struct {
	Name       string // vendor display name
	Email      string // vendor email
	Domain     string // e.g. "acme.example.com"
	Registrar  string // may be empty
	ExpiresOn  string // already-formatted "02 Jan 2006" string
	DaysLeft   int    // 30 | 21 | 14 | 7 | 5 | 3 | 2 | 1
	AutoRenew  bool
	PanelName  string
	PanelURL   string
}

func BuildDomainExpiry(d DomainExpiryData) (subject, text, htmlBody string, err error) {
	applyDefaults(&d.Name, &d.Email, &d.PanelName)
	urgent := d.DaysLeft <= 3

	if urgent {
		subject = fmt.Sprintf("URGENT: %s expires in %s", d.Domain, plural(d.DaysLeft, "day", "days"))
	} else {
		subject = fmt.Sprintf("%s expires in %s", d.Domain, plural(d.DaysLeft, "day", "days"))
	}

	renewHint := "Contact your registrar to renew the domain before it expires."
	if d.AutoRenew {
		renewHint = "Auto-renew is enabled; this is a heads-up. Confirm with your registrar if you're not sure the renewal will go through."
	}

	registrarLine := ""
	if d.Registrar != "" {
		registrarLine = fmt.Sprintf("  Registrar : %s\n", d.Registrar)
	}

	text = fmt.Sprintf(`Hi %s,

Your domain %s is registered until %s — that's %s from today.

  Domain    : %s
%s  Expires   : %s

%s

Manage the domain:
  %s

— %s
`, d.Name, d.Domain, d.ExpiresOn, plural(d.DaysLeft, "day", "days"), d.Domain, registrarLine, d.ExpiresOn, renewHint, d.PanelURL, d.PanelName)

	esc := d
	esc.Name = html.EscapeString(esc.Name)
	esc.Email = html.EscapeString(esc.Email)
	esc.Domain = html.EscapeString(esc.Domain)
	esc.Registrar = html.EscapeString(esc.Registrar)
	esc.ExpiresOn = html.EscapeString(esc.ExpiresOn)
	esc.PanelName = html.EscapeString(esc.PanelName)

	var h bytes.Buffer
	ht := template.Must(template.New("h").Funcs(template.FuncMap{
		"plural": func(n int, one, many string) string { return plural(n, one, many) },
		"urgent": func() bool { return urgent },
		"hint":   func() string { return renewHint },
	}).Parse(domainExpiryHTML))
	if err := ht.Execute(&h, esc); err != nil {
		return "", "", "", err
	}
	return subject, text, h.String(), nil
}

const domainExpiryHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Domain expiry</title></head>
<body style="margin:0;padding:0;background:#0f172a;color:#e2e8f0;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#0f172a;padding:40px 0;">
    <tr><td align="center">
      <table role="presentation" width="560" cellpadding="0" cellspacing="0" style="background:#111827;border:1px solid {{if urgent}}#b91c1c{{else}}#1f2937{{end}};border-radius:12px;padding:32px;">
        <tr><td>
          <h1 style="margin:0 0 20px;font-size:20px;font-weight:600;color:{{if urgent}}#fca5a5{{else}}#f3f4f6{{end}};">
            {{if urgent}}Urgent: {{end}}{{.Domain}} expires in {{plural .DaysLeft "day" "days"}}
          </h1>
          <p style="margin:0 0 16px;line-height:1.6;font-size:14px;color:#cbd5e1;">Hi {{.Name}},</p>
          <p style="margin:0 0 16px;line-height:1.6;font-size:14px;color:#cbd5e1;">
            Your domain <strong style="color:#f3f4f6;">{{.Domain}}</strong> is registered until
            <strong style="color:#f3f4f6;">{{.ExpiresOn}}</strong>.
          </p>
          <table cellpadding="6" cellspacing="0" style="width:100%;background:#0b1220;border:1px solid #1f2937;border-radius:8px;font-size:13px;color:#cbd5e1;margin:0 0 16px;">
            <tr><td style="color:#94a3b8;">Domain</td><td style="color:#f3f4f6;font-family:ui-monospace,Menlo,monospace;">{{.Domain}}</td></tr>
            {{if .Registrar}}<tr><td style="color:#94a3b8;">Registrar</td><td style="color:#f3f4f6;">{{.Registrar}}</td></tr>{{end}}
            <tr><td style="color:#94a3b8;">Expires</td><td style="color:#f3f4f6;">{{.ExpiresOn}}</td></tr>
            <tr><td style="color:#94a3b8;">Days left</td><td style="color:{{if urgent}}#fca5a5{{else}}#f3f4f6{{end}};font-weight:600;">{{.DaysLeft}}</td></tr>
          </table>
          <p style="margin:0 0 24px;line-height:1.5;font-size:13px;color:#cbd5e1;">
            {{hint}}
          </p>
          <p style="margin:0;">
            <a href="{{.PanelURL}}" style="display:inline-block;padding:10px 18px;background:#2563eb;color:#ffffff;text-decoration:none;border-radius:8px;font-size:14px;font-weight:500;">Open {{.PanelName}}</a>
          </p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body></html>`

// -----------------------------------------------------------------------
// SSL certificate issued / active
// -----------------------------------------------------------------------

// SSLActiveData drives both the first-issue and the auto-renewal
// confirmation emails — the panel treats them identically because
// from the vendor's perspective the outcome is the same (HTTPS now
// works on this domain until <expires_on>).
type SSLActiveData struct {
	Name      string
	Email     string
	Domain    string
	Issuer    string // "Let's Encrypt" / "Google Trust Services" / …
	ExpiresOn string // already-formatted "02 Jan 2006"
	AutoRenew bool
	Automated bool // true when triggered by a renewal job (vs. a manual Issue)
	PanelName string
	PanelURL  string
}

func BuildSSLActive(d SSLActiveData) (subject, text, htmlBody string, err error) {
	applyDefaults(&d.Name, &d.Email, &d.PanelName)
	if d.Issuer == "" {
		d.Issuer = "Let's Encrypt"
	}
	verb := "issued"
	if d.Automated {
		verb = "renewed"
	}
	subject = fmt.Sprintf("SSL %s for %s", verb, d.Domain)

	text = fmt.Sprintf(`Hi %s,

The SSL certificate for %s has been %s.

  Domain  : %s
  Issuer  : %s
  Expires : %s
  Auto-renew: %s

HTTPS is now active on https://%s. The panel will automatically renew
this certificate before it expires — you'll get another email then.

Manage the domain:
  %s

— %s
`, d.Name, d.Domain, verb, d.Domain, d.Issuer, d.ExpiresOn, yesNo(d.AutoRenew), d.Domain, d.PanelURL, d.PanelName)

	esc := d
	esc.Name = html.EscapeString(esc.Name)
	esc.Email = html.EscapeString(esc.Email)
	esc.Domain = html.EscapeString(esc.Domain)
	esc.Issuer = html.EscapeString(esc.Issuer)
	esc.ExpiresOn = html.EscapeString(esc.ExpiresOn)
	esc.PanelName = html.EscapeString(esc.PanelName)

	var h bytes.Buffer
	ht := template.Must(template.New("h").Funcs(template.FuncMap{
		"yesNo": yesNo,
		"verb":  func() string { return verb },
	}).Parse(sslActiveHTML))
	if err := ht.Execute(&h, esc); err != nil {
		return "", "", "", err
	}
	return subject, text, h.String(), nil
}

const sslActiveHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>SSL active</title></head>
<body style="margin:0;padding:0;background:#0f172a;color:#e2e8f0;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#0f172a;padding:40px 0;">
    <tr><td align="center">
      <table role="presentation" width="560" cellpadding="0" cellspacing="0" style="background:#111827;border:1px solid #166534;border-radius:12px;padding:32px;">
        <tr><td>
          <h1 style="margin:0 0 20px;font-size:20px;font-weight:600;color:#4ade80;">SSL {{verb}} for {{.Domain}}</h1>
          <p style="margin:0 0 16px;line-height:1.6;font-size:14px;color:#cbd5e1;">Hi {{.Name}},</p>
          <p style="margin:0 0 16px;line-height:1.6;font-size:14px;color:#cbd5e1;">
            HTTPS is now active on <strong style="color:#f3f4f6;">{{.Domain}}</strong>.
          </p>
          <table cellpadding="6" cellspacing="0" style="width:100%;background:#0b1220;border:1px solid #1f2937;border-radius:8px;font-size:13px;color:#cbd5e1;margin:0 0 16px;">
            <tr><td style="color:#94a3b8;">Domain</td><td style="color:#f3f4f6;font-family:ui-monospace,Menlo,monospace;">{{.Domain}}</td></tr>
            <tr><td style="color:#94a3b8;">Issuer</td><td style="color:#f3f4f6;">{{.Issuer}}</td></tr>
            <tr><td style="color:#94a3b8;">Expires</td><td style="color:#f3f4f6;">{{.ExpiresOn}}</td></tr>
            <tr><td style="color:#94a3b8;">Auto-renew</td><td style="color:#f3f4f6;">{{yesNo .AutoRenew}}</td></tr>
          </table>
          <p style="margin:0 0 24px;line-height:1.5;font-size:13px;color:#cbd5e1;">
            The panel will automatically renew this certificate before it expires — you'll get another email then.
          </p>
          <p style="margin:0;">
            <a href="{{.PanelURL}}" style="display:inline-block;padding:10px 18px;background:#16a34a;color:#ffffff;text-decoration:none;border-radius:8px;font-size:14px;font-weight:500;">Open {{.PanelName}}</a>
          </p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body></html>`

// -----------------------------------------------------------------------
// SSL certificate FAILED to issue / renew
// -----------------------------------------------------------------------

// SSLFailedData drives the "we tried, it didn't work" email the panel
// sends the vendor after the last retry attempt exhausts. The Reason
// is expected to come from ssl_service.friendlyCertbotError — so it's
// already operator-readable (rate limits, DNS NXDOMAIN, port 80
// unreachable, etc.).
type SSLFailedData struct {
	Name      string
	Email     string
	Domain    string
	Reason    string
	Attempts  int    // number of retry attempts that were made
	PanelName string
	PanelURL  string
}

func BuildSSLFailed(d SSLFailedData) (subject, text, htmlBody string, err error) {
	applyDefaults(&d.Name, &d.Email, &d.PanelName)
	if d.Reason == "" {
		d.Reason = "no specific reason reported by certbot"
	}
	subject = fmt.Sprintf("Action needed: SSL could not be issued for %s", d.Domain)

	attemptsLine := ""
	if d.Attempts > 1 {
		attemptsLine = fmt.Sprintf("We retried %d times before giving up.\n\n", d.Attempts)
	}

	text = fmt.Sprintf(`Hi %s,

We couldn't issue an SSL certificate for %s yet.

  Domain: %s
  Reason: %s

%sCommon fixes:
  - Make sure the domain's A record points at this server.
  - Make sure ports 80 and 443 are open on the firewall.
  - Wait a few minutes for DNS to propagate, then retry.

Retry from the panel once you've fixed the underlying issue:
  %s

— %s
`, d.Name, d.Domain, d.Domain, d.Reason, attemptsLine, d.PanelURL, d.PanelName)

	esc := d
	esc.Name = html.EscapeString(esc.Name)
	esc.Email = html.EscapeString(esc.Email)
	esc.Domain = html.EscapeString(esc.Domain)
	esc.Reason = html.EscapeString(esc.Reason)
	esc.PanelName = html.EscapeString(esc.PanelName)

	var h bytes.Buffer
	ht := template.Must(template.New("h").Parse(sslFailedHTML))
	if err := ht.Execute(&h, esc); err != nil {
		return "", "", "", err
	}
	return subject, text, h.String(), nil
}

const sslFailedHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>SSL failed</title></head>
<body style="margin:0;padding:0;background:#0f172a;color:#e2e8f0;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#0f172a;padding:40px 0;">
    <tr><td align="center">
      <table role="presentation" width="560" cellpadding="0" cellspacing="0" style="background:#111827;border:1px solid #b91c1c;border-radius:12px;padding:32px;">
        <tr><td>
          <h1 style="margin:0 0 20px;font-size:20px;font-weight:600;color:#fca5a5;">SSL could not be issued</h1>
          <p style="margin:0 0 16px;line-height:1.6;font-size:14px;color:#cbd5e1;">Hi {{.Name}},</p>
          <p style="margin:0 0 16px;line-height:1.6;font-size:14px;color:#cbd5e1;">
            The panel couldn't issue an SSL certificate for <strong style="color:#f3f4f6;">{{.Domain}}</strong>.
            {{if gt .Attempts 1}}We retried {{.Attempts}} times before giving up.{{end}}
          </p>
          <table cellpadding="6" cellspacing="0" style="width:100%;background:#0b1220;border:1px solid #1f2937;border-radius:8px;font-size:13px;color:#cbd5e1;margin:0 0 16px;">
            <tr><td style="color:#94a3b8;width:88px;">Domain</td><td style="color:#f3f4f6;font-family:ui-monospace,Menlo,monospace;">{{.Domain}}</td></tr>
            <tr><td style="color:#94a3b8;vertical-align:top;">Reason</td><td style="color:#fca5a5;">{{.Reason}}</td></tr>
          </table>
          <p style="margin:0 0 8px;line-height:1.5;font-size:13px;color:#cbd5e1;">Common fixes:</p>
          <ul style="margin:0 0 20px 18px;padding:0;line-height:1.6;font-size:13px;color:#cbd5e1;">
            <li>Make sure the domain's A record points at this server.</li>
            <li>Make sure ports 80 and 443 are open on the firewall.</li>
            <li>Wait a few minutes for DNS to propagate, then retry.</li>
          </ul>
          <p style="margin:0;">
            <a href="{{.PanelURL}}" style="display:inline-block;padding:10px 18px;background:#dc2626;color:#ffffff;text-decoration:none;border-radius:8px;font-size:14px;font-weight:500;">Retry in {{.PanelName}}</a>
          </p>
        </td></tr>
      </table>
    </td></tr>
  </table>
</body></html>`

// -----------------------------------------------------------------------
// Small helpers shared across the templates above.
// -----------------------------------------------------------------------

// applyDefaults fills in the recipient name + panel name used in every
// greeting. Kept small and mutation-based so each Build* doesn't
// repeat the same 6-line block.
func applyDefaults(name, email, panelName *string) {
	if *panelName == "" {
		*panelName = "Betazen Server Panel"
	}
	if *name == "" {
		if i := strings.Index(*email, "@"); i > 0 {
			*name = (*email)[:i]
		} else {
			*name = *email
		}
	}
}

func humanTLS(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "tls":
		return "TLS / SMTPS"
	case "starttls":
		return "STARTTLS"
	case "none":
		return "None"
	}
	return mode
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}
