package services

import (
	"bytes"
	"fmt"

	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// smtpSend delivers a pre-built MIME message to the account's SMTP submission
// server: implicit TLS on 465, else STARTTLS on 587 — both via
// loopbackAwareTLSConfig, which skips cert verification for 127.0.0.1 (the local
// Postfix presents its public mail-host cert, not one valid for the loopback IP,
// which would otherwise fail with "cannot validate certificate for 127.0.0.1 …
// no IP SANs" and block every send).
func smtpSend(a *models.MailAccount, rcpts []string, buf *bytes.Buffer) error {
	addr := fmt.Sprintf("%s:%d", a.SMTPHost, a.SMTPPort)
	auth := sasl.NewPlainClient("", a.Username, a.Secret)
	tlsCfg := loopbackAwareTLSConfig(a.SMTPHost)

	var (
		c   *smtp.Client
		err error
	)
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

// VerifySMTPLogin dials the SMTP submission server and authenticates WITHOUT
// sending anything — so the account-add flow can validate credentials before
// saving. Mirrors smtpSend's connection strategy (implicit TLS on 465, else
// STARTTLS on 587) with the same loopback-aware cert handling.
func VerifySMTPLogin(host string, port int, ssl bool, username, secret string) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	tlsCfg := loopbackAwareTLSConfig(host)
	var (
		c   *smtp.Client
		err error
	)
	if ssl {
		c, err = smtp.DialTLS(addr, tlsCfg)
	} else {
		c, err = smtp.DialStartTLS(addr, tlsCfg)
	}
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Auth(sasl.NewPlainClient("", username, secret))
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
