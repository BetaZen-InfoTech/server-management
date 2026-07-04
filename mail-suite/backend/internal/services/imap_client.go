package services

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
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
// STARTTLS negotiation failure). Distinguishing the two lets callers return a
// precise 401 (bad password) vs 503 (server unreachable).
var errIMAPAuthRejected = errors.New("imap auth rejected")

// imapAttempt is one connection strategy: dial host:port using mode
// ("tls" = implicit TLS, "starttls" = plaintext dial then STARTTLS, "plain").
type imapAttempt struct {
	host string
	port int
	mode string
}

// buildIMAPAttempts returns the ordered strategies to try: the caller's
// configured combination first, then — ONLY for the local Dovecot (loopback) —
// robust 993/143 fallbacks. Those fallbacks make login + mailbox access work
// against Dovecot whatever its local listener config is (disable_plaintext_auth
// rejects LOGIN on a bare 143 connection, so plaintext is the last resort).
//
// For an EXTERNAL provider (Gmail, Outlook, …) we DON'T probe 143: those hosts
// expose exactly one working combination — typically 993 implicit TLS — and 143
// is usually filtered, so dialing it just hangs and adds latency + spurious 500s
// on the inbox. Real bug: an external Gmail account (imap.gmail.com:993) whose
// 993 attempt failed transiently then fell through to imap.gmail.com:143, which
// blocked until timeout and left the inbox stuck "loading…".
func buildIMAPAttempts(host string, port int, ssl bool) []imapAttempt {
	seen := map[imapAttempt]bool{}
	var out []imapAttempt
	add := func(a imapAttempt) {
		if a.port <= 0 || seen[a] {
			return
		}
		seen[a] = true
		out = append(out, a)
	}
	if ssl {
		add(imapAttempt{host, port, "tls"})
	} else if port != 0 {
		add(imapAttempt{host, port, "starttls"})
		add(imapAttempt{host, port, "plain"})
	}
	if isLoopbackHost(host) {
		add(imapAttempt{host, 993, "tls"})
		add(imapAttempt{host, 143, "starttls"})
		add(imapAttempt{host, 143, "plain"})
	}
	return out
}

// imapConnectAuthed opens an authenticated IMAP connection, trying each
// strategy in turn. On success it returns a LOGGED-IN client the caller must
// Logout(). A login rejected over a secure channel → ErrInvalidLogin (401);
// nothing reachable + authenticatable → ErrMailServerUnreachable (503).
//
// This is the single path used by BOTH the Gmail-style login check
// (VerifyIMAPLogin) and every mailbox operation (IMAPDial) — so a secure
// Dovecot that refuses plaintext auth, or one whose local cert only covers the
// public mail hostname (not 127.0.0.1), doesn't just break the login check but
// also mail reading.
func imapConnectAuthed(host string, port int, ssl bool, username, secret string) (*client.Client, error) {
	attempts := buildIMAPAttempts(host, port, ssl)
	var lastConnErr error
	// Retry the whole ladder once on a pure CONNECTION failure. External
	// providers (Gmail especially) briefly reject rapid IMAP connects with a
	// transient error; a short backoff usually clears it, so the inbox doesn't
	// flash a spurious 500. Auth rejections are NOT retried (they return below).
	for try := 0; try < 2; try++ {
		for _, a := range attempts {
			c, err := imapDialAndLogin(a.host, a.port, a.mode, username, secret)
			if err == nil {
				return c, nil
			}
			if errors.Is(err, errIMAPAuthRejected) && a.mode != "plain" {
				// LOGIN reached + rejected over TLS/STARTTLS → genuinely bad creds.
				// A plaintext rejection is NOT trusted (disable_plaintext_auth).
				return nil, ErrInvalidLogin
			}
			lastConnErr = err
		}
		if try == 0 {
			time.Sleep(600 * time.Millisecond)
		}
	}
	if lastConnErr == nil {
		lastConnErr = fmt.Errorf("no reachable IMAP listener on %s", host)
	}
	return nil, fmt.Errorf("%w: %v", ErrMailServerUnreachable, lastConnErr)
}

// imapDialAndLogin performs one connect→(optional STARTTLS)→LOGIN attempt. On
// success it returns a logged-in client (caller Logout()s). On failure it
// closes the connection and returns the error — a LOGIN rejection wrapped as
// errIMAPAuthRejected; every other failure (dial/TLS/STARTTLS) raw so the
// caller tries the next strategy.
func imapDialAndLogin(host string, port int, mode, username, secret string) (*client.Client, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	tlsCfg := loopbackAwareTLSConfig(host)
	// Bound the TCP connect. Without this a filtered/closed port (e.g. Gmail's
	// 143) blocks for the OS default timeout — minutes — which is what left the
	// inbox stuck "loading…". go-imap's plain Dial/DialTLS have no timeout, so
	// dial through a net.Dialer instead.
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var (
		c   *client.Client
		err error
	)
	if mode == "tls" {
		c, err = client.DialWithDialerTLS(dialer, addr, tlsCfg)
	} else {
		c, err = client.DialWithDialer(dialer, addr)
	}
	if err != nil {
		return nil, err
	}
	c.Timeout = 30 * time.Second
	if mode == "starttls" {
		if err := c.StartTLS(tlsCfg); err != nil {
			c.Logout()
			return nil, err
		}
	}
	if err := c.Login(username, secret); err != nil {
		c.Logout()
		return nil, fmt.Errorf("%w: %v", errIMAPAuthRejected, err)
	}
	return c, nil
}

// VerifyIMAPLogin verifies mailbox credentials against the mail server (the
// source of truth for the Gmail-style login flow) and logs out immediately.
func VerifyIMAPLogin(host string, port int, ssl bool, username, secret string) error {
	c, err := imapConnectAuthed(host, port, ssl, username, secret)
	if err != nil {
		return err
	}
	c.Logout()
	return nil
}

// loopbackAwareTLSConfig returns a TLS config for talking to the mail server
// (shared by IMAP and SMTP). For loopback the server presents its public
// hostname cert (e.g. mail.example.com), never one valid for 127.0.0.1, so
// verification would always fail ("cannot validate certificate for 127.0.0.1
// … no IP SANs") — we skip it there since the trust boundary is the local box
// itself. External hosts are verified normally against their name.
func loopbackAwareTLSConfig(host string) *tls.Config {
	if isLoopbackHost(host) {
		return &tls.Config{InsecureSkipVerify: true} //nolint:gosec // loopback cert never matches 127.0.0.1
	}
	return &tls.Config{ServerName: host}
}

// isLoopbackHost reports whether host is the local box — the only place the
// 993/143 fallback ladder and cert-verification skip apply.
func isLoopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "::1" || strings.EqualFold(host, "localhost")
}

// imapPoolEntry is one account's keep-alive IMAP connection. mu is held from
// IMAPDial() until the returned release() runs, so requests for the same
// mailbox serialize — an IMAP connection can only carry one command at a time.
type imapPoolEntry struct {
	mu       sync.Mutex
	client   *client.Client
	lastUsed time.Time
}

var (
	imapPool   = map[string]*imapPoolEntry{}
	imapPoolMu sync.Mutex
	imapReaper sync.Once
)

func imapPoolKey(a *models.MailAccount) string {
	if !a.ID.IsZero() {
		return a.ID.Hex()
	}
	return a.IMAPHost + "|" + a.Username
}

func imapEntry(key string) *imapPoolEntry {
	imapPoolMu.Lock()
	defer imapPoolMu.Unlock()
	e := imapPool[key]
	if e == nil {
		e = &imapPoolEntry{}
		imapPool[key] = e
	}
	return e
}

// IMAPDial returns a live authenticated connection for the account, reusing a
// pooled keep-alive connection when one is healthy. This skips the TLS
// handshake + LOGIN on every request — the reason external mailboxes (Gmail)
// felt like they were "always loading" (each fetch re-authenticated from
// scratch, ~2s). The per-account lock is held until the returned release() runs,
// so concurrent requests for the same mailbox serialize. Callers MUST
// `defer release()`.
func IMAPDial(a *models.MailAccount) (*client.Client, func(), error) {
	imapReaper.Do(func() { go imapPoolReaper() })
	e := imapEntry(imapPoolKey(a))
	e.mu.Lock()
	if e.client != nil {
		// Cheap liveness probe — reuse only a connection the server still holds.
		if err := e.client.Noop(); err == nil {
			e.lastUsed = time.Now()
			return e.client, e.mu.Unlock, nil
		}
		e.client.Logout()
		e.client = nil
	}
	c, err := imapConnectAuthed(a.IMAPHost, a.IMAPPort, a.IMAPSSL, a.Username, a.Secret)
	if err != nil {
		e.mu.Unlock()
		return nil, func() {}, err
	}
	e.client = c
	e.lastUsed = time.Now()
	return c, e.mu.Unlock, nil
}

// imapPoolReaper closes connections idle > 5 min so we don't hold open sockets
// (or a provider's limited IMAP connection slots) forever. Only reaps entries
// that aren't currently in use (TryLock).
func imapPoolReaper() {
	for {
		time.Sleep(2 * time.Minute)
		cutoff := time.Now().Add(-5 * time.Minute)
		imapPoolMu.Lock()
		for _, e := range imapPool {
			if e.mu.TryLock() {
				if e.client != nil && e.lastUsed.Before(cutoff) {
					e.client.Logout()
					e.client = nil
				}
				e.mu.Unlock()
			}
		}
		imapPoolMu.Unlock()
	}
}

func ListFolders(a *models.MailAccount) ([]models.Folder, error) {
	c, release, err := IMAPDial(a)
	if err != nil {
		return nil, err
	}
	defer release()

	mboxes := make(chan *imap.MailboxInfo, 32)
	done := make(chan error, 1)
	go func() {
		done <- c.List("", "*", mboxes)
	}()

	var out []models.Folder
	for m := range mboxes {
		f := models.Folder{Name: m.Name, Delimiter: m.Delimiter}
		// Prefer RFC 6154 SPECIAL-USE attributes — this is what makes Gmail's
		// "[Gmail]/Sent Mail", "[Gmail]/Trash" etc. classify correctly (their
		// names don't match "Sent"/"Trash"). Fall back to conventional names.
		for _, attr := range m.Attributes {
			switch strings.ToLower(attr) {
			case "\\sent":
				f.Special = "sent"
			case "\\drafts":
				f.Special = "drafts"
			case "\\junk":
				f.Special = "spam"
			case "\\trash":
				f.Special = "trash"
			case "\\flagged":
				f.Special = "starred"
			case "\\archive":
				f.Special = "archive"
			}
		}
		if f.Special == "" {
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
			case "starred":
				f.Special = "starred"
			}
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

// MailboxStatus returns a cheap STATUS snapshot of a folder (no message
// fetch): the next UID the server will assign and the unseen count. The
// new-mail poller compares uidNext across polls to detect arrivals without
// scanning the mailbox.
func MailboxStatus(a *models.MailAccount, folder string) (uidNext uint32, unseen uint32, err error) {
	c, release, err := IMAPDial(a)
	if err != nil {
		return 0, 0, err
	}
	defer release()
	st, err := c.Status(folder, []imap.StatusItem{imap.StatusUidNext, imap.StatusUnseen})
	if err != nil {
		return 0, 0, err
	}
	return st.UidNext, st.Unseen, nil
}

// ListHeaders returns the latest `limit` headers in `folder`, newest first.
func ListHeaders(a *models.MailAccount, folder string, limit, page int) ([]models.MessageHeader, int, error) {
	c, release, err := IMAPDial(a)
	if err != nil {
		return nil, 0, err
	}
	defer release()

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
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchUid, imap.FetchRFC822Size, imap.FetchBodyStructure, section.FetchItem()}
	ch := make(chan *imap.Message, 32)
	done := make(chan error, 1)
	go func() { done <- c.Fetch(seq, items, ch) }()

	out := []models.MessageHeader{}
	for m := range ch {
		out = append(out, buildHeader(m, section, folder))
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

// buildHeader maps a fetched IMAP message to a MessageHeader (nil-Envelope safe).
func buildHeader(m *imap.Message, section *imap.BodySectionName, folder string) models.MessageHeader {
	h := models.MessageHeader{UID: m.Uid, Folder: folder, Size: m.Size}
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
	h.HasAttach = hasAttachment(m.BodyStructure)
	if r := m.GetBody(section); r != nil {
		h.Snippet = snippet(r)
	}
	h.ThreadKey = threadKey(h.Subject, m.Envelope)
	return h
}

// ListStarred backs the webmail's virtual "Starred" folder: flagged (\Flagged)
// messages from INBOX, newest first. Most IMAP servers (Dovecot included) have
// no dedicated "Starred" mailbox, so selecting one by name always failed —
// leaving the Starred view permanently empty.
func ListStarred(a *models.MailAccount, limit, page int) ([]models.MessageHeader, int, error) {
	c, release, err := IMAPDial(a)
	if err != nil {
		return nil, 0, err
	}
	defer release()
	if _, err := c.Select("INBOX", true); err != nil {
		return nil, 0, err
	}
	criteria := imap.NewSearchCriteria()
	criteria.WithFlags = []string{imap.FlaggedFlag}
	uids, err := c.UidSearch(criteria)
	if err != nil {
		return nil, 0, err
	}
	total := len(uids)
	if total == 0 {
		return nil, 0, nil
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] > uids[j] }) // newest UID first
	if limit <= 0 {
		limit = 50
	}
	if page <= 0 {
		page = 1
	}
	start := (page - 1) * limit
	if start >= total {
		return []models.MessageHeader{}, total, nil
	}
	end := start + limit
	if end > total {
		end = total
	}
	pageUIDs := uids[start:end]
	seq := new(imap.SeqSet)
	for _, u := range pageUIDs {
		seq.AddNum(u)
	}
	section := &imap.BodySectionName{}
	items := []imap.FetchItem{imap.FetchEnvelope, imap.FetchFlags, imap.FetchUid, imap.FetchRFC822Size, imap.FetchBodyStructure, section.FetchItem()}
	ch := make(chan *imap.Message, 32)
	done := make(chan error, 1)
	go func() { done <- c.UidFetch(seq, items, ch) }()
	byUID := map[uint32]models.MessageHeader{}
	for m := range ch {
		byUID[m.Uid] = buildHeader(m, section, "Starred")
	}
	if err := <-done; err != nil {
		return nil, 0, err
	}
	// Emit in the sorted (newest-first) UID order.
	out := make([]models.MessageHeader, 0, len(pageUIDs))
	for _, u := range pageUIDs {
		if h, ok := byUID[u]; ok {
			out = append(out, h)
		}
	}
	return out, total, nil
}

// FetchMessage fetches the full message body by UID.
func FetchMessage(a *models.MailAccount, folder string, uid uint32) (*models.MessageBody, error) {
	c, release, err := IMAPDial(a)
	if err != nil {
		return nil, err
	}
	defer release()
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
	c, release, err := IMAPDial(a)
	if err != nil {
		return err
	}
	defer release()
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
	c, release, err := IMAPDial(a)
	if err != nil {
		return err
	}
	defer release()
	if _, err := c.Select(src, false); err != nil {
		return err
	}
	// Resolve a logical destination (Trash/Spam/Archive/…) to this account's REAL
	// mailbox. Gmail's trash is "[Gmail]/Trash", Outlook's is "Deleted Items" —
	// moving to the literal "Trash" fails on every non-Dovecot provider. No-op for
	// a destination that's already a concrete mailbox name.
	dst = resolveMailbox(c, dst)
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

// AppendToSent files a copy of an outbound message into the account's Sent
// mailbox with the \Seen flag. SMTP submission alone never does this, which is
// why sent mail was previously missing from the Sent folder. Best-effort: it
// resolves the real Sent folder name (server-reported \Sent special-use, else a
// common name), falling back to "Sent".
func AppendToSent(a *models.MailAccount, msg *bytes.Buffer) error {
	c, release, err := IMAPDial(a)
	if err != nil {
		return err
	}
	defer release()

	folder := findSentFolder(c)
	if folder == "" {
		folder = "Sent"
	}
	// *bytes.Buffer satisfies imap.Literal (Read + Len); Append consumes it once.
	return c.Append(folder, []string{imap.SeenFlag}, time.Now(), msg)
}

// findSentFolder discovers the mailbox that should hold sent mail: it prefers a
// folder carrying the \Sent special-use attribute, then a conventional name.
func findSentFolder(c *client.Client) string {
	mboxes := make(chan *imap.MailboxInfo, 32)
	done := make(chan error, 1)
	go func() { done <- c.List("", "*", mboxes) }()

	var byName, bySpecial string
	for m := range mboxes {
		switch strings.ToLower(m.Name) {
		case "sent", "sent items", "sent messages":
			byName = m.Name
		}
		for _, attr := range m.Attributes {
			if strings.EqualFold(attr, "\\Sent") {
				bySpecial = m.Name
			}
		}
	}
	<-done
	if bySpecial != "" {
		return bySpecial
	}
	return byName
}

// logicalFolders maps a logical destination name to its RFC 6154 special-use
// attribute + conventional fallback names (lowercased, matched by exact name or
// as the last path segment so "[Gmail]/Trash" and "INBOX.Trash" both match).
var logicalFolders = map[string]struct {
	attr  string
	names []string
}{
	"trash":   {"\\Trash", []string{"trash", "deleted", "deleted items", "deleted messages", "bin"}},
	"spam":    {"\\Junk", []string{"spam", "junk", "junk email", "bulk mail"}},
	"junk":    {"\\Junk", []string{"junk", "spam", "junk email"}},
	"archive": {"\\Archive", []string{"archive", "archives"}},
	"sent":    {"\\Sent", []string{"sent", "sent items", "sent messages"}},
	"drafts":  {"\\Drafts", []string{"drafts", "draft"}},
}

// resolveMailbox maps a logical destination (Trash/Spam/Archive/…) to the
// account's REAL mailbox name via the server's special-use attributes, then
// conventional names. A destination that isn't a known logical name is already a
// concrete mailbox and is returned unchanged.
func resolveMailbox(c *client.Client, dst string) string {
	spec, ok := logicalFolders[strings.ToLower(dst)]
	if !ok {
		return dst
	}
	mboxes := make(chan *imap.MailboxInfo, 32)
	done := make(chan error, 1)
	go func() { done <- c.List("", "*", mboxes) }()
	var byName, bySpecial string
	for m := range mboxes {
		for _, attr := range m.Attributes {
			if strings.EqualFold(attr, spec.attr) {
				bySpecial = m.Name
			}
		}
		ln := strings.ToLower(m.Name)
		for _, n := range spec.names {
			if ln == n || strings.HasSuffix(ln, "/"+n) || strings.HasSuffix(ln, "."+n) || strings.HasSuffix(ln, "]"+n) {
				if byName == "" {
					byName = m.Name
				}
			}
		}
	}
	<-done
	if bySpecial != "" {
		return bySpecial
	}
	if byName != "" {
		return byName
	}
	return dst // nothing matched — best-effort literal
}

// hasAttachment reports whether a message's BODYSTRUCTURE contains an
// "attachment"-disposition part (recursing into multipart bodies). Inline parts
// (embedded images) deliberately don't count.
func hasAttachment(bs *imap.BodyStructure) bool {
	if bs == nil {
		return false
	}
	if strings.EqualFold(bs.Disposition, "attachment") {
		return true
	}
	for _, p := range bs.Parts {
		if hasAttachment(p) {
			return true
		}
	}
	return false
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
