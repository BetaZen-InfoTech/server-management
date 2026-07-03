package services

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/emersion/go-message/mail"
)

// newToken returns a random hex token used as an opaque tracking id and as the
// local part of a generated Message-ID.
func newToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// buildMessageID returns an RFC5322 Message-ID anchored to the sender's domain,
// set explicitly so a SentMessage record can be correlated to the delivered mail.
func buildMessageID(fromAddr string) string {
	domain := "localhost"
	if i := strings.LastIndex(fromAddr, "@"); i >= 0 && i+1 < len(fromAddr) {
		domain = fromAddr[i+1:]
	}
	return fmt.Sprintf("<%s@%s>", newToken(16), domain)
}

// applySignature appends the signature HTML to the body (two <br/> separators,
// empty-body guard) — preserving the previous inline-send behaviour.
func applySignature(htmlBody, signatureHTML string) string {
	if signatureHTML == "" {
		return htmlBody
	}
	if strings.TrimSpace(htmlBody) == "" {
		htmlBody = "<p></p>"
	}
	return htmlBody + "<br/><br/>" + signatureHTML
}

var (
	hrefRe        = regexp.MustCompile(`(?i)(href\s*=\s*)("|')(https?://[^"']+)("|')`)
	tagRe         = regexp.MustCompile(`(?s)<[^>]+>`)
	styleScriptRe = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	brRe          = regexp.MustCompile(`(?i)<br\s*/?>`)
	pCloseRe      = regexp.MustCompile(`(?i)</p>`)
	trailWSRe     = regexp.MustCompile(`[ \t]+\n`)
	blankLinesRe  = regexp.MustCompile(`\n{3,}`)
)

// injectOpenPixel appends a 1x1 invisible tracking pixel pointing at
// /t/open/{trackID}.png, right before </body> when present.
func injectOpenPixel(htmlBody, baseURL, trackID string) string {
	pixel := fmt.Sprintf(
		`<img src="%s/t/open/%s.png" width="1" height="1" alt="" style="display:none;max-height:0;overflow:hidden"/>`,
		strings.TrimRight(baseURL, "/"), trackID,
	)
	if i := strings.LastIndex(strings.ToLower(htmlBody), "</body>"); i >= 0 {
		return htmlBody[:i] + pixel + htmlBody[i:]
	}
	return htmlBody + pixel
}

// rewriteLinks routes every absolute http(s) link through /t/click/{trackID}?u=
// (base64url of the original target), so clicks can be recorded before redirect.
func rewriteLinks(htmlBody, baseURL, trackID string) string {
	base := strings.TrimRight(baseURL, "/")
	return hrefRe.ReplaceAllStringFunc(htmlBody, func(m string) string {
		g := hrefRe.FindStringSubmatch(m)
		if len(g) != 5 {
			return m
		}
		enc := base64.RawURLEncoding.EncodeToString([]byte(g[3]))
		return fmt.Sprintf(`%s%s%s/t/click/%s?u=%s%s`, g[1], g[2], base, trackID, enc, g[4])
	})
}

// htmlToText produces a rough plain-text alternative: drop script/style, turn
// <br>/<p> into newlines, strip remaining tags, decode entities, tidy whitespace.
func htmlToText(htmlBody string) string {
	s := styleScriptRe.ReplaceAllString(htmlBody, "")
	s = brRe.ReplaceAllString(s, "\n")
	s = pCloseRe.ReplaceAllString(s, "\n\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = trailWSRe.ReplaceAllString(s, "\n")
	s = blankLinesRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// buildMIME assembles the RFC5322 message. With a non-empty htmlBody it emits a
// multipart/alternative carrying a text/plain fallback (a real deliverability
// win over HTML-only); otherwise a single text/plain part.
func buildMIME(a *models.MailAccount, req *models.SendRequest, htmlBody, textBody, messageID string) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	from := []*mail.Address{{Name: a.DisplayName, Address: a.Address}}

	var h mail.Header
	h.SetDate(time.Now())
	h.SetAddressList("From", from)
	h.SetAddressList("To", toMailAddrs(req.To))
	if len(req.Cc) > 0 {
		h.SetAddressList("Cc", toMailAddrs(req.Cc))
	}
	h.SetSubject(req.Subject)
	if messageID != "" {
		h.Set("Message-Id", messageID)
	}
	if req.InReplyTo != "" {
		h.Set("In-Reply-To", req.InReplyTo)
	}
	if len(req.References) > 0 {
		h.Set("References", strings.Join(req.References, " "))
	}

	w, err := mail.CreateWriter(buf, h)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(htmlBody) != "" {
		tw, err := w.CreateInline()
		if err != nil {
			return nil, err
		}
		// text/plain listed first, text/html second: in multipart/alternative
		// the last understood part wins, so HTML is preferred where supported.
		var th mail.InlineHeader
		th.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		if pw, err := tw.CreatePart(th); err == nil {
			_, _ = io.WriteString(pw, textBody)
			pw.Close()
		}
		var hh mail.InlineHeader
		hh.SetContentType("text/html", map[string]string{"charset": "utf-8"})
		if hw, err := tw.CreatePart(hh); err == nil {
			_, _ = io.WriteString(hw, htmlBody)
			hw.Close()
		}
		tw.Close()
	} else {
		var ih mail.InlineHeader
		ih.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		pw, err := w.CreateSingleInline(ih)
		if err != nil {
			return nil, err
		}
		if _, err := io.WriteString(pw, textBody); err != nil {
			return nil, err
		}
		pw.Close()
	}
	w.Close()
	return buf, nil
}
