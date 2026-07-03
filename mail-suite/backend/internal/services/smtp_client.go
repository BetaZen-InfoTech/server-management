package services

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

func SendMail(a *models.MailAccount, req *models.SendRequest, signatureHTML string) error {
	if strings.TrimSpace(req.HTML) == "" && strings.TrimSpace(req.Text) == "" {
		return fmt.Errorf("empty body")
	}
	if signatureHTML != "" {
		if req.HTML == "" {
			req.HTML = "<p></p>"
		}
		req.HTML = req.HTML + "<br/><br/>" + signatureHTML
	}

	buf := &bytes.Buffer{}
	from := []*mail.Address{{Name: a.DisplayName, Address: a.Address}}
	to := toMailAddrs(req.To)

	var h mail.Header
	h.SetDate(time.Now())
	h.SetAddressList("From", from)
	h.SetAddressList("To", to)
	if len(req.Cc) > 0 {
		h.SetAddressList("Cc", toMailAddrs(req.Cc))
	}
	h.SetSubject(req.Subject)
	if req.InReplyTo != "" {
		h.Set("In-Reply-To", req.InReplyTo)
	}
	if len(req.References) > 0 {
		h.Set("References", strings.Join(req.References, " "))
	}

	w, err := mail.CreateWriter(buf, h)
	if err != nil {
		return err
	}
	if req.HTML != "" {
		var ih mail.InlineHeader
		ih.SetContentType("text/html", map[string]string{"charset": "utf-8"})
		pw, err := w.CreateSingleInline(ih)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(pw, req.HTML); err != nil {
			return err
		}
		pw.Close()
	} else {
		var ih mail.InlineHeader
		ih.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		pw, err := w.CreateSingleInline(ih)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(pw, req.Text); err != nil {
			return err
		}
		pw.Close()
	}
	w.Close()

	rcpts := flattenAddrs(req.To, req.Cc, req.Bcc)
	addr := fmt.Sprintf("%s:%d", a.SMTPHost, a.SMTPPort)
	auth := sasl.NewPlainClient("", a.Username, a.Secret)
	tlsCfg := loopbackAwareTLSConfig(a.SMTPHost)

	// Implicit TLS on 465, else STARTTLS submission on 587. Both use
	// loopbackAwareTLSConfig, which skips cert verification for 127.0.0.1 — the
	// local Postfix presents its public mail-host cert, not one valid for the
	// loopback IP, which otherwise fails with "cannot validate certificate for
	// 127.0.0.1 … doesn't contain any IP SANs" and blocks every send.
	var c *smtp.Client
	if a.SMTPSSL {
		c, err = smtp.DialTLS(addr, tlsCfg)
	} else {
		c, err = smtp.DialStartTLS(addr, tlsCfg)
	}
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	defer c.Close()

	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	return c.SendMail(a.Address, rcpts, buf)
}

func toMailAddrs(xs []models.Address) []*mail.Address {
	out := make([]*mail.Address, 0, len(xs))
	for _, a := range xs {
		out = append(out, &mail.Address{Name: a.Name, Address: a.Address})
	}
	return out
}

func flattenAddrs(parts ...[]models.Address) []string {
	out := []string{}
	for _, ps := range parts {
		for _, a := range ps {
			out = append(out, a.Address)
		}
	}
	return out
}
