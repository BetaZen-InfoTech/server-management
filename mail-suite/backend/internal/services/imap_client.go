package services

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/betazeninfotech/mail-suite/internal/models"
	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	gomsg "github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
)

// errIMAPAuthRejected is an internal sentinel: the server spoke IMAP and
// rejected the credentials at LOGIN (as opposed to a connection / TLS /
// STARTTLS negotiation failure). Distinguishing the two lets VerifyIMAPLogin
// return a precise 401 (bad password) vs 503 (server unreachable).
var errIMAPAuthRejected = errors.New("imap auth rejected")

// VerifyIMAPLogin verifies mailbox credentials against the mail server. It
// backs the Gmail-style login flow, where the mailbox on the mail server —
// not a separate app password — is the source of truth.
//
// It tries several connection strategies in turn — implicit TLS (993),
// STARTTLS (143) and finally plaintext — so verification works against a real
// Dovecot whatever its local listener config is. This matters because
// Dovecot's secure default, disable_plaintext_auth=yes, REJECTS LOGIN on a
// bare 143 connection: a naive plaintext dial+login therefore fails even with
// the correct password (the exact "invalid email or password" symptom on a
// freshly-installed box). So we prefer TLS/STARTTLS and only fall back to
// plaintext for dev boxes that allow it.
//
// A rejected login over a secure channel → ErrInvalidLogin (401). No listener
// we could reach and authenticate against → ErrMailServerUnreachable (503).
func VerifyIMAPLogin(host string, port int, ssl bool, username, secret string) error {
	type attempt struct {
		host string
		port int
		mode string // "tls" | "starttls" | "plain"
	}
	seen := map[attempt]bool{}
	var attempts []attempt
	add := func(a attempt) {
		if a.port <= 0 || seen[a] {
			return
		}
		seen[a] = true
		attempts = append(attempts, a)
	}
	// Caller's configured combination first…
	if ssl {
		add(attempt{host, port, "tls"})
	} else if port != 0 {
		add(attempt{host, port, "starttls"})
		add(attempt{host, port, "plain"})
	}
	// …then robust fallbacks: implicit TLS on 993, STARTTLS then plaintext on 143.
	add(attempt{host, 993, "tls"})
	add(attempt{host, 143, "starttls"})
	add(attempt{host, 143, "plain"})

	var lastConnErr error
	for _, a := range attempts {
		err := imapTryLogin(a.host, a.port, a.mode, username, secret)
		if err == nil {
			return nil
		}
		if errors.Is(err, errIMAPAuthRejected) {
			// A TLS/STARTTLS session that reached LOGIN and got NO/BAD means
			// the password is genuinely wrong. A plaintext rejection is NOT
			// trusted (could be disable_plaintext_auth) — keep trying.
			if a.mode != "plain" {
				return ErrInvalidLogin
			}
		}
		lastConnErr = err
	}
	if lastConnErr == nil {
		lastConnErr = fmt.Errorf("no reachable IMAP listener on %s", host)
	}
	return fmt.Errorf("%w: %v", ErrMailServerUnreachable, lastConnErr)
}

// imapTryLogin performs one connect→(optional STARTTLS)→LOGIN→logout attempt.
// A nil return means the credentials are valid. A LOGIN rejection is wrapped
// as errIMAPAuthRejected; every other failure (dial, TLS, STARTTLS) is
// returned raw so the caller treats it as "try the next strategy".
func imapTryLogin(host string, port int, mode, username, secret string) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	tlsCfg := imapTLSConfig(host)
	var (
		c   *client.Client
		err error
	)
	if mode == "tls" {
		c, err = client.DialTLS(addr, tlsCfg)
	} else {
		c, err = client.Dial(addr)
	}
	if err != nil {
		return err
	}
	c.Timeout = 20 * time.Second
	defer c.Logout()
	if mode == "starttls" {
		if err := c.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if err := c.Login(username, secret); err != nil {
		return fmt.Errorf("%w: %v", errIMAPAuthRejected, err)
	}
	return nil
}

// imapTLSConfig returns a TLS config for verifying against the mail server.
// For loopback connections the mail server presents its public hostname cert
// (e.g. mail.example.com), never one valid for 127.0.0.1, so verification
// would always fail — we skip it there since the trust boundary is the local
// box itself. External IMAP hosts are verified normally against their name.
func imapTLSConfig(host string) *tls.Config {
	if host == "127.0.0.1" || host == "::1" || strings.EqualFold(host, "localhost") {
		return &tls.Config{InsecureSkipVerify: true} //nolint:gosec // loopback cert never matches 127.0.0.1
	}
	return &tls.Config{ServerName: host}
}

// IMAPDial opens an authenticated IMAP connection for the given account.
// Callers must Logout() when done.
func IMAPDial(a *models.MailAccount) (*client.Client, error) {
	addr := fmt.Sprintf("%s:%d", a.IMAPHost, a.IMAPPort)
	var (
		c   *client.Client
		err error
	)
	if a.IMAPSSL {
		c, err = client.DialTLS(addr, &tls.Config{ServerName: a.IMAPHost})
	} else {
		c, err = client.Dial(addr)
	}
	if err != nil {
		return nil, err
	}
	c.Timeout = 30 * time.Second
	if err := c.Login(a.Username, a.Secret); err != nil {
		c.Logout()
		return nil, fmt.Errorf("imap login: %w", err)
	}
	return c, nil
}

func ListFolders(a *models.MailAccount) ([]models.Folder, error) {
	c, err := IMAPDial(a)
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	mboxes := make(chan *imap.MailboxInfo, 32)
	done := make(chan error, 1)
	go func() {
		done <- c.List("", "*", mboxes)
	}()

	var out []models.Folder
	for m := range mboxes {
		f := models.Folder{Name: m.Name, Delimiter: m.Delimiter}
		switch strings.ToLower(m.Name) {
		case "inbox":
			f.Special = "inbox"
		case "sent", "sent items":
			f.Special = "sent"
		case "drafts":
			f.Special = "drafts"
		case "spam", "junk":
			f.Special = "spam"
		case "trash", "deleted":
			f.Special = "trash"
		}
		// status: total + unread
		st, err := c.Status(m.Name, []imap.StatusItem{imap.StatusMessages, imap.StatusUnseen})
		if err == nil {
			f.Total = int(st.Messages)
			f.Unread = int(st.Unseen)
		}
		out = append(out, f)
	}
	if err := <-done; err != nil {
		return nil, err
	}
	return out, nil
}

// ListHeaders returns the latest `limit` headers in `folder`, newest first.
func ListHeaders(a *models.MailAccount, folder string, limit, page int) ([]models.MessageHeader, int, error) {
	c, err := IMAPDial(a)
	if err != nil {
		return nil, 0, err
	}
	defer c.Logout()

	mbox, err := c.Select(folder, true)
	if err != nil {
		return nil, 0, err
	}
	total := int(mbox.Messages)
	if total == 0 {
		return nil, 0, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if page <= 0 {
		page = 1
	}
	from := total - (page-1)*limit - limit + 1
	to := total - (page-1)*limit
	if from < 1 {
		from = 1
	}
	seq := new(imap.SeqSet)
	seq.AddRange(uint32(from), uint32(to))

	section := &imap.BodySectionName{}
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchUid, imap.FetchRFC822Size, section.FetchItem()}
	ch := make(chan *imap.Message, 32)
	done := make(chan error, 1)
	go func() { done <- c.Fetch(seq, items, ch) }()

	out := []models.MessageHeader{}
	for m := range ch {
		h := models.MessageHeader{
			UID: m.Uid,
			Folder: folder,
			Size: m.Size,
			Date: m.Envelope.Date,
		}
		if m.Envelope != nil {
			h.Subject = m.Envelope.Subject
			h.MessageID = m.Envelope.MessageId
			h.From = addressesFromIMAP(m.Envelope.From)
			h.To = addressesFromIMAP(m.Envelope.To)
			h.Cc = addressesFromIMAP(m.Envelope.Cc)
			if !m.Envelope.Date.IsZero() {
				h.Date = m.Envelope.Date
			}
		}
		h.Unread = !hasFlag(m.Flags, imap.SeenFlag)
		h.Starred = hasFlag(m.Flags, imap.FlaggedFlag)
		// snippet from first ~200 chars of text part
		if r := m.GetBody(section); r != nil {
			h.Snippet = snippet(r)
		}
		h.ThreadKey = threadKey(h.Subject, m.Envelope)
		out = append(out, h)
	}
	if err := <-done; err != nil {
		return nil, 0, err
	}
	// reverse to newest-first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, total, nil
}

// FetchMessage fetches the full message body by UID.
func FetchMessage(a *models.MailAccount, folder string, uid uint32) (*models.MessageBody, error) {
	c, err := IMAPDial(a)
	if err != nil {
		return nil, err
	}
	defer c.Logout()
	if _, err := c.Select(folder, false); err != nil {
		return nil, err
	}
	seq := new(imap.SeqSet)
	seq.AddNum(uid)
	section := &imap.BodySectionName{}
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchUid, section.FetchItem()}
	ch := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() { done <- c.UidFetch(seq, items, ch) }()

	var body *models.MessageBody
	for m := range ch {
		body = &models.MessageBody{UID: m.Uid}
		if m.Envelope != nil {
			body.MessageID = m.Envelope.MessageId
			body.Subject = m.Envelope.Subject
			body.From = addressesFromIMAP(m.Envelope.From)
			body.To = addressesFromIMAP(m.Envelope.To)
			body.Cc = addressesFromIMAP(m.Envelope.Cc)
			body.Bcc = addressesFromIMAP(m.Envelope.Bcc)
			body.ReplyTo = addressesFromIMAP(m.Envelope.ReplyTo)
			body.Date = m.Envelope.Date
		}
		if r := m.GetBody(section); r != nil {
			parseBody(r, body)
		}
	}
	if err := <-done; err != nil {
		return nil, err
	}
	if body == nil {
		return nil, fmt.Errorf("message not found")
	}
	return body, nil
}

func SetFlags(a *models.MailAccount, folder string, uid uint32, addSeen, removeSeen, addStar, removeStar bool) error {
	c, err := IMAPDial(a)
	if err != nil {
		return err
	}
	defer c.Logout()
	if _, err := c.Select(folder, false); err != nil {
		return err
	}
	seq := new(imap.SeqSet)
	seq.AddNum(uid)
	apply := func(op imap.FlagsOp, flag string) error {
		item := imap.FormatFlagsOp(op, true)
		return c.UidStore(seq, item, []interface{}{flag}, nil)
	}
	if addSeen {
		if err := apply(imap.AddFlags, imap.SeenFlag); err != nil { return err }
	}
	if removeSeen {
		if err := apply(imap.RemoveFlags, imap.SeenFlag); err != nil { return err }
	}
	if addStar {
		if err := apply(imap.AddFlags, imap.FlaggedFlag); err != nil { return err }
	}
	if removeStar {
		if err := apply(imap.RemoveFlags, imap.FlaggedFlag); err != nil { return err }
	}
	return nil
}

func MoveMessage(a *models.MailAccount, src, dst string, uid uint32) error {
	c, err := IMAPDial(a)
	if err != nil {
		return err
	}
	defer c.Logout()
	if _, err := c.Select(src, false); err != nil {
		return err
	}
	seq := new(imap.SeqSet)
	seq.AddNum(uid)
	// try MOVE extension first
	if err := c.UidMove(seq, dst); err == nil {
		return nil
	}
	// fallback: COPY + delete
	if err := c.UidCopy(seq, dst); err != nil {
		return err
	}
	if err := c.UidStore(seq, imap.FormatFlagsOp(imap.AddFlags, true), []interface{}{imap.DeletedFlag}, nil); err != nil {
		return err
	}
	return c.Expunge(nil)
}

func addressesFromIMAP(in []*imap.Address) []models.Address {
	out := make([]models.Address, 0, len(in))
	for _, a := range in {
		if a == nil {
			continue
		}
		addr := models.Address{Name: a.PersonalName}
		if a.MailboxName != "" && a.HostName != "" {
			addr.Address = a.MailboxName + "@" + a.HostName
		}
		out = append(out, addr)
	}
	return out
}

func hasFlag(flags []string, f string) bool {
	for _, x := range flags {
		if strings.EqualFold(x, f) {
			return true
		}
	}
	return false
}

func snippet(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 4096))
	mr, err := gomsg.Read(bytes.NewReader(b))
	if err != nil {
		return strings.TrimSpace(string(b))
	}
	if mr.MultipartReader() != nil {
		mp := mr.MultipartReader()
		for {
			p, err := mp.NextPart()
			if err != nil {
				break
			}
			ct, _, _ := p.Header.ContentType()
			if strings.HasPrefix(ct, "text/plain") {
				body, _ := io.ReadAll(p.Body)
				return clip(string(body), 180)
			}
		}
	}
	body, _ := io.ReadAll(mr.Body)
	return clip(string(body), 180)
}

func parseBody(r io.Reader, out *models.MessageBody) {
	mr, err := mail.CreateReader(r)
	if err != nil {
		body, _ := io.ReadAll(r)
		out.Text = string(body)
		return
	}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := h.ContentType()
			b, _ := io.ReadAll(p.Body)
			if strings.HasPrefix(ct, "text/html") {
				out.HTML = string(b)
			} else {
				out.Text += string(b)
			}
		case *mail.AttachmentHeader:
			fn, _ := h.Filename()
			ct, _, _ := h.ContentType()
			b, _ := io.ReadAll(p.Body)
			out.Attachments = append(out.Attachments, models.Attachment{
				ID: fn, Filename: fn, ContentType: ct, Size: len(b),
			})
		}
	}
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func threadKey(subj string, env *imap.Envelope) string {
	if env != nil && env.InReplyTo != "" {
		return env.InReplyTo
	}
	s := strings.TrimSpace(subj)
	low := strings.ToLower(s)
	for strings.HasPrefix(low, "re:") || strings.HasPrefix(low, "fwd:") || strings.HasPrefix(low, "fw:") {
		s = strings.TrimSpace(s[strings.Index(s, ":")+1:])
		low = strings.ToLower(s)
	}
	return s
}
