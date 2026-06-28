// Package services — mail_log_service.go implements the source-agnostic
// mail-log ingestor and its tenant-scoped read API.
//
// Background: before v3.1.108 the panel only observed mail through the
// Dovecot Sieve webhook hook, which fires once per message DELIVERED to a
// local maildir. That hook cannot see outbound mail, mail submitted by
// third-party SMTP clients (Thunderbird, Outlook, mobile apps), or mail
// injected via local sendmail/API — so those never appeared in the panel's
// mail log. This service fixes that by tailing Postfix's own log (the one
// authoritative record of EVERY message, every source) and correlating the
// per-queue-id lines into structured MailLogEntry rows.
//
// Design notes:
//   - We follow /var/log/mail.log with `tail -n0 -F` (rotation-safe) rather
//     than re-implementing inode/offset tracking. On panel restart we start
//     at end-of-file; the sub-second gap is negligible and upserts are
//     idempotent anyway.
//   - Lines are correlated in-memory by queue id and flushed to Mongo on
//     `removed` (queue done) or after an idle TTL (so deferred mail still
//     shows up). A periodic flusher bounds memory.
//   - Subject + Content-Type are surfaced into the log via a Postfix
//     header_checks PCRE map installed idempotently at boot (WARN action —
//     it only logs, never rejects, so it cannot disrupt mail flow).
package services

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	mailLogPath        = "/var/log/mail.log"
	mailLogIdleFlush   = 3 * time.Minute  // flush a queue item this long after its last line
	mailLogFlushTick   = 30 * time.Second // how often the flusher runs
	mailLogMaxPartial  = 8000             // hard cap on in-flight queue items held in memory
	headerChecksFile   = "/etc/postfix/header_checks_betazen"
)

// MailLogService owns the ingestor goroutine + the read API.
type MailLogService struct {
	db       *mongo.Database
	serverIP string
	hostname string

	mu      sync.Mutex
	partial map[string]*partialEntry // queue_id -> accumulating entry

	hcOnce sync.Once
}

// partialEntry accumulates the fields seen across a queue item's log lines
// before it's finalized into a models.MailLogEntry.
type partialEntry struct {
	queueID    string
	firstSeen  time.Time
	lastEvent  time.Time
	client     string
	clientIP   string
	authUser   string
	authMethod string
	service    string // submission | smtps | smtp(25) | "" (local)
	hadSMTPD   bool
	pickup     bool
	sender     string
	size       int64
	nrcpt      int
	subject    string
	messageID  string
	contentType string
	hasAttach   bool
	recipients map[string]*models.MailLogRecipient
	removed    bool
	lastResp   string
}

func NewMailLogService(db *mongo.Database, serverIP, hostname string) *MailLogService {
	return &MailLogService{
		db:       db,
		serverIP: serverIP,
		hostname: hostname,
		partial:  make(map[string]*partialEntry),
	}
}

// ───────────────────────────── regexes ─────────────────────────────

var (
	// "<ts> <host> <program>[pid]: <msg>" — program may contain '/'
	mlLineRe = regexp.MustCompile(`^(\S+)\s+(\S+)\s+([^:\[]+?)(?:\[\d+\])?:\s+(.*)$`)
	// "<qid>: <detail>" — qid is 6+ alphanumerics (queue ids carry digits)
	// or the special "NOQUEUE" pseudo-id used for pre-queue rejects.
	mlQidRe = regexp.MustCompile(`^([0-9A-Za-z]{6,}|NOQUEUE):\s+(.*)$`)

	mlClientRe     = regexp.MustCompile(`client=([^\[,\s]+)\[([0-9a-fA-F:.]+)\]`)
	mlSaslUserRe   = regexp.MustCompile(`sasl_username=([^,\s]+)`)
	mlSaslMethodRe = regexp.MustCompile(`sasl_method=([^,\s]+)`)
	mlMsgIDRe      = regexp.MustCompile(`message-id=<([^>]+)>`)
	mlFromQmgrRe   = regexp.MustCompile(`from=<([^>]*)>,\s*size=(\d+),\s*nrcpt=(\d+)`)
	mlToRe         = regexp.MustCompile(`to=<([^>]*)>`)
	mlRelayRe      = regexp.MustCompile(`relay=([^,]+),`)
	mlDSNRe        = regexp.MustCompile(`dsn=([^,]+),`)
	mlDelayRe      = regexp.MustCompile(`delay=([^,]+),`)
	mlStatusRe     = regexp.MustCompile(`status=(\w+)\s*\((.*)\)\s*$`)
	// header_checks WARN: "warning: header Subject: <v> from <client>; from=<env> ..."
	// Greedy (.*) so a subject/content-type containing " from " or ";" still
	// captures fully — the engine backtracks to the LAST " from <client>; from=".
	mlHeaderWarnRe = regexp.MustCompile(`warning: header (Subject|Content-Type):\s?(.*) from [^;]*; from=`)
	mlGenFromRe    = regexp.MustCompile(`from=<([^>]*)>`)
	mlGenToRe      = regexp.MustCompile(`to=<([^>]*)>`)
	mlRejectRespRe = regexp.MustCompile(`:\s*(\d{3} \d\.\d\.\d[^;]*)`)
)

// ───────────────────────────── ingestor ─────────────────────────────

// StartIngestor wires up Subject logging then follows the mail log in a
// background goroutine. Safe to call once from main(); returns immediately.
func (s *MailLogService) StartIngestor(ctx context.Context) {
	// Best-effort: enrich the log with Subject + Content-Type so the
	// captured rows carry them. Never fatal — the ingestor works without it.
	go s.hcOnce.Do(func() {
		if err := s.EnsureHeaderChecks(ctx); err != nil {
			log.Warn().Err(err).Msg("mail-log: header_checks setup failed (subjects won't be captured); ingestor continues")
		} else {
			log.Info().Msg("mail-log: Postfix header_checks installed (Subject + Content-Type logging)")
		}
	})
	go s.flusher(ctx)
	go s.tailLoop(ctx)
	log.Info().Str("path", mailLogPath).Msg("mail-log: ingestor started (capturing ALL mail, every source)")
}

// tailLoop runs `tail -n0 -F` and feeds each line to parseLine, restarting
// the tail if it ever dies (rotation edge cases, OOM-killed child, etc.).
func (s *MailLogService) tailLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		cmd := exec.CommandContext(ctx, "tail", "-n", "0", "-F", mailLogPath)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Warn().Err(err).Msg("mail-log: tail stdout pipe failed; retrying in 5s")
			time.Sleep(5 * time.Second)
			continue
		}
		if err := cmd.Start(); err != nil {
			log.Warn().Err(err).Msg("mail-log: tail start failed; retrying in 5s")
			time.Sleep(5 * time.Second)
			continue
		}
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // up to 1 MiB lines
		for sc.Scan() {
			s.parseLine(sc.Text())
		}
		_ = cmd.Wait()
		select {
		case <-ctx.Done():
			return
		default:
			log.Warn().Msg("mail-log: tail exited; restarting in 3s")
			time.Sleep(3 * time.Second)
		}
	}
}

// parseLine routes one raw mail.log line to the right field extractor.
func (s *MailLogService) parseLine(line string) {
	m := mlLineRe.FindStringSubmatch(line)
	if m == nil {
		return
	}
	ts := parseMailTS(m[1])
	prog := m[3]
	msg := m[4]

	// Only Postfix lines carry queue ids we correlate. (Dovecot login
	// lines are interesting for auth auditing but belong to a different
	// feature; we skip them here to keep the mail-log message-centric.)
	if !strings.HasPrefix(prog, "postfix") {
		return
	}
	sub := prog[strings.LastIndex(prog, "/")+1:] // smtpd|cleanup|qmgr|smtp|lmtp|local|virtual|pipe|error|bounce|pickup
	// service instance between "postfix/" and the subprogram, e.g.
	// postfix/submission/smtpd -> "submission".
	service := ""
	if parts := strings.Split(prog, "/"); len(parts) == 3 {
		service = parts[1]
	}

	qm := mlQidRe.FindStringSubmatch(msg)
	if qm == nil {
		return // connection/banner/statistics noise without a queue id
	}
	qid := qm[1]
	detail := qm[2]
	if qid != "NOQUEUE" && !strings.ContainsAny(qid, "0123456789") {
		return // a lowercase word like "warning"/"connect" masquerading as a qid
	}

	if qid == "NOQUEUE" {
		s.handleReject(ts, detail)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.partial[qid]
	if e == nil {
		if len(s.partial) >= mailLogMaxPartial {
			// Backpressure: flush the oldest in-flight item to disk so a
			// flood of new queue ids can't grow the map without bound.
			s.evictOldestLocked()
		}
		e = &partialEntry{queueID: qid, firstSeen: ts, recipients: map[string]*models.MailLogRecipient{}}
		s.partial[qid] = e
	}
	e.lastEvent = ts
	if service != "" {
		e.service = service
	}

	switch sub {
	case "smtpd":
		e.hadSMTPD = true
		if cm := mlClientRe.FindStringSubmatch(detail); cm != nil {
			e.client = cm[1]
			e.clientIP = cm[2]
		}
		if um := mlSaslUserRe.FindStringSubmatch(detail); um != nil {
			e.authUser = um[1]
		}
		if am := mlSaslMethodRe.FindStringSubmatch(detail); am != nil {
			e.authMethod = am[1]
		}
	case "pickup":
		e.pickup = true
		if fm := mlGenFromRe.FindStringSubmatch(detail); fm != nil && e.sender == "" {
			e.sender = fm[1]
		}
	case "cleanup":
		if strings.HasPrefix(detail, "warning: header ") {
			if hm := mlHeaderWarnRe.FindStringSubmatch(detail); hm != nil {
				switch hm[1] {
				case "Subject":
					e.subject = strings.TrimSpace(hm[2])
				case "Content-Type":
					ct := strings.TrimSpace(hm[2])
					// A message can carry several Content-Type headers (the
					// top-level one plus each MIME part). Keep the FIRST
					// (top-level) for display, but OR-in attachment detection
					// across ALL of them so a multipart/mixed outer part isn't
					// masked by a later text/plain inner part.
					if e.contentType == "" {
						e.contentType = ct
					}
					if looksLikeAttachment(ct) {
						e.hasAttach = true
					}
				}
			}
		} else if im := mlMsgIDRe.FindStringSubmatch(detail); im != nil {
			e.messageID = im[1]
		}
	case "qmgr":
		if detail == "removed" {
			e.removed = true
			s.finalizeLocked(e)
			delete(s.partial, qid)
			return
		}
		if fm := mlFromQmgrRe.FindStringSubmatch(detail); fm != nil {
			e.sender = fm[1]
			e.size, _ = strconv.ParseInt(fm[2], 10, 64)
			e.nrcpt, _ = strconv.Atoi(fm[3])
		}
	case "smtp", "lmtp", "local", "virtual", "pipe", "error":
		s.applyDelivery(e, detail, ts)
	}
}

// applyDelivery records one recipient delivery attempt line.
func (s *MailLogService) applyDelivery(e *partialEntry, detail string, ts time.Time) {
	tm := mlToRe.FindStringSubmatch(detail)
	if tm == nil {
		return
	}
	addr := strings.ToLower(tm[1])
	r := e.recipients[addr]
	if r == nil {
		r = &models.MailLogRecipient{Address: addr}
		e.recipients[addr] = r
	}
	if rm := mlRelayRe.FindStringSubmatch(detail); rm != nil {
		r.Relay = strings.TrimSpace(rm[1])
	}
	if dm := mlDSNRe.FindStringSubmatch(detail); dm != nil {
		r.DSN = strings.TrimSpace(dm[1])
	}
	if dl := mlDelayRe.FindStringSubmatch(detail); dl != nil {
		r.Delay = strings.TrimSpace(dl[1])
	}
	if sm := mlStatusRe.FindStringSubmatch(detail); sm != nil {
		r.Status = sm[1]
		r.Response = strings.TrimSpace(sm[2])
		e.lastResp = r.Response
		if r.Status == "sent" {
			r.DeliveredAt = ts
		}
	}
}

// handleReject records a pre-queue (NOQUEUE) rejection as a standalone row.
// These never get a real queue id, so we can't correlate them; each reject
// line is a complete, self-contained event.
func (s *MailLogService) handleReject(ts time.Time, detail string) {
	from, to := "", ""
	if fm := mlGenFromRe.FindStringSubmatch(detail); fm != nil {
		from = fm[1]
	}
	if tm := mlGenToRe.FindStringSubmatch(detail); tm != nil {
		to = tm[1]
	}
	resp := ""
	if rm := mlRejectRespRe.FindStringSubmatch(detail); rm != nil {
		resp = strings.TrimSpace(rm[1])
	}
	clientIP := ""
	if cm := mlClientRe.FindStringSubmatch(detail); cm != nil {
		clientIP = cm[2]
	}
	entry := models.MailLogEntry{
		LogKey:    fmt.Sprintf("NOQUEUE:%d", ts.UnixNano()),
		QueueID:   "NOQUEUE",
		Direction: "inbound",
		Source:    "inbound-smtp",
		ClientIP:  clientIP,
		Sender:    from,
		Status:    "rejected",
		SMTPResponse: resp,
		FirstSeen: ts,
		LastEvent: ts,
	}
	if to != "" {
		entry.Recipients = []models.MailLogRecipient{{Address: strings.ToLower(to), Status: "rejected", Response: resp}}
		entry.NRcpt = 1
	}
	entry.Domains = domainsOf(from, to)
	s.upsert(entry)
}

// finalizeLocked converts an accumulated partialEntry into a MailLogEntry
// and upserts it. Caller must hold s.mu.
func (s *MailLogService) finalizeLocked(e *partialEntry) {
	entry := s.buildEntry(e)
	// Upsert outside the lock would be nicer, but Mongo calls are fast and
	// holding the lock keeps the partial map consistent; the flusher path
	// also calls this. Net contention is trivial at real mail volumes.
	s.upsert(entry)
}

// buildEntry derives the classified, rolled-up MailLogEntry from raw fields.
func (s *MailLogService) buildEntry(e *partialEntry) models.MailLogEntry {
	recips := make([]models.MailLogRecipient, 0, len(e.recipients))
	for _, r := range e.recipients {
		recips = append(recips, *r)
	}
	sort.Slice(recips, func(i, j int) bool { return recips[i].Address < recips[j].Address })

	source, direction := classify(e, recips)
	status := rollupStatus(e, recips)

	// Collect every domain that appears in the envelope for tenant scoping.
	domSet := map[string]bool{}
	for _, d := range domainsOf(e.sender) {
		domSet[d] = true
	}
	for _, r := range recips {
		for _, d := range domainsOf(r.Address) {
			domSet[d] = true
		}
	}
	domains := make([]string, 0, len(domSet))
	for d := range domSet {
		domains = append(domains, d)
	}
	sort.Strings(domains)

	now := time.Now()
	return models.MailLogEntry{
		LogKey:         fmt.Sprintf("%s:%d", e.queueID, e.firstSeen.Unix()),
		QueueID:        e.queueID,
		Direction:      direction,
		Source:         source,
		Client:         e.client,
		ClientIP:       e.clientIP,
		AuthUser:       e.authUser,
		AuthMethod:     e.authMethod,
		Sender:         e.sender,
		Recipients:     recips,
		Subject:        e.subject,
		MessageID:      e.messageID,
		ContentType:    e.contentType,
		HasAttachments: e.hasAttach || looksLikeAttachment(e.contentType),
		SizeBytes:      e.size,
		NRcpt:          e.nrcpt,
		Status:         status,
		SMTPResponse:   e.lastResp,
		Domains:        domains,
		ServerIP:       s.serverIP,
		Hostname:       s.hostname,
		Queued:         !e.removed,
		FirstSeen:      e.firstSeen,
		LastEvent:      e.lastEvent,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// upsert writes the entry keyed on log_key (idempotent — a deferred message
// flushed early then re-flushed after delivery updates the same row).
func (s *MailLogService) upsert(entry models.MailLogEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	set := bson.M{
		"queue_id":        entry.QueueID,
		"direction":       entry.Direction,
		"source":          entry.Source,
		"client":          entry.Client,
		"client_ip":       entry.ClientIP,
		"auth_user":       entry.AuthUser,
		"auth_method":     entry.AuthMethod,
		"sender":          entry.Sender,
		"recipients":      entry.Recipients,
		"subject":         entry.Subject,
		"message_id":      entry.MessageID,
		"content_type":    entry.ContentType,
		"has_attachments": entry.HasAttachments,
		"size_bytes":      entry.SizeBytes,
		"nrcpt":           entry.NRcpt,
		"status":          entry.Status,
		"smtp_response":   entry.SMTPResponse,
		"domains":         entry.Domains,
		"server_ip":       entry.ServerIP,
		"hostname":        entry.Hostname,
		"queued":          entry.Queued,
		"first_seen":      entry.FirstSeen,
		"last_event":      entry.LastEvent,
		"updated_at":      entry.UpdatedAt,
	}
	_, err := s.db.Collection(database.ColMailLogs).UpdateOne(ctx,
		bson.M{"log_key": entry.LogKey},
		bson.M{"$set": set, "$setOnInsert": bson.M{"log_key": entry.LogKey, "created_at": entry.CreatedAt}},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		log.Debug().Err(err).Str("log_key", entry.LogKey).Msg("mail-log: upsert failed")
	}
}

// flusher periodically writes out queue items that have gone quiet (so
// deferred / still-queued mail appears without waiting for "removed").
func (s *MailLogService) flusher(ctx context.Context) {
	t := time.NewTicker(mailLogFlushTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cutoff := time.Now().Add(-mailLogIdleFlush)
			s.mu.Lock()
			for qid, e := range s.partial {
				if e.lastEvent.Before(cutoff) {
					s.finalizeLocked(e)
					// Keep deferred (not removed) items? No — drop from memory
					// after flushing; if more lines arrive a fresh partial is
					// created and upsert merges by log_key (same first_seen).
					delete(s.partial, qid)
				}
			}
			s.mu.Unlock()
		}
	}
}

// evictOldestLocked flushes the single oldest in-flight item. Caller holds s.mu.
func (s *MailLogService) evictOldestLocked() {
	var oldestQID string
	var oldest time.Time
	for qid, e := range s.partial {
		if oldestQID == "" || e.firstSeen.Before(oldest) {
			oldestQID, oldest = qid, e.firstSeen
		}
	}
	if oldestQID != "" {
		s.finalizeLocked(s.partial[oldestQID])
		delete(s.partial, oldestQID)
	}
}

// ───────────────────────────── classification ─────────────────────────────

func classify(e *partialEntry, recips []models.MailLogRecipient) (source, direction string) {
	authed := e.authUser != ""
	localDeliv, externalDeliv := false, false
	for _, r := range recips {
		relay := strings.ToLower(r.Relay)
		switch {
		case strings.Contains(relay, "dovecot") || strings.Contains(relay, "virtual") || strings.HasPrefix(relay, "local"):
			// Delivered into a local mailbox (Dovecot LMTP / virtual / local).
			localDeliv = true
		case relay != "" && !strings.HasPrefix(relay, "none"):
			// Relayed to an external MX / smarthost.
			externalDeliv = true
		}
	}

	switch {
	case authed && isLoopback(e.clientIP):
		source = "webmail" // Roundcube (and any localhost-authenticated submitter)
	case authed:
		source = "smtp-client" // Thunderbird / Outlook / mobile / external app
	case e.pickup || (!e.hadSMTPD && e.clientIP == ""):
		source = "api-local" // local sendmail / PHP mail() / cron / panel
	case e.hadSMTPD && !authed:
		source = "inbound-smtp"
	default:
		source = "unknown"
	}

	switch {
	case source == "inbound-smtp":
		direction = "inbound"
	case localDeliv && !externalDeliv:
		if source == "api-local" || source == "webmail" || source == "smtp-client" {
			direction = "internal"
		} else {
			direction = "inbound"
		}
	case externalDeliv:
		direction = "outbound"
	default:
		direction = "unknown"
	}
	return
}

func rollupStatus(e *partialEntry, recips []models.MailLogRecipient) string {
	if len(recips) == 0 {
		if e.removed {
			return "received" // entered + left the queue with no delivery line we saw
		}
		return "queued"
	}
	var sent, deferred, bounced bool
	for _, r := range recips {
		switch r.Status {
		case "sent":
			sent = true
		case "deferred":
			deferred = true
		case "bounced", "expired":
			bounced = true
		}
	}
	switch {
	case bounced:
		return "bounced"
	case deferred && !sent:
		return "deferred"
	case sent:
		return "sent"
	default:
		return "queued"
	}
}

// ───────────────────────────── read API ─────────────────────────────

// MailLogFilter carries the query-string filters from the handler.
type MailLogFilter struct {
	Direction string
	Status    string
	Source    string
	Query     string // matches sender / recipient / subject / message-id
	Since     time.Time
	Until     time.Time
}

// List returns a tenant-scoped page of mail-log rows, newest first.
func (s *MailLogService) List(ctx context.Context, f MailLogFilter, page, limit int) ([]models.MailLogEntry, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	filter := bson.M{}
	if f.Direction != "" {
		filter["direction"] = f.Direction
	}
	if f.Status != "" {
		filter["status"] = f.Status
	}
	if f.Source != "" {
		filter["source"] = f.Source
	}
	if f.Query != "" {
		rx := primitive.Regex{Pattern: regexp.QuoteMeta(f.Query), Options: "i"}
		filter["$or"] = bson.A{
			bson.M{"sender": rx},
			bson.M{"recipients.address": rx},
			bson.M{"subject": rx},
			bson.M{"message_id": rx},
		}
	}
	if !f.Since.IsZero() || !f.Until.IsZero() {
		rng := bson.M{}
		if !f.Since.IsZero() {
			rng["$gte"] = f.Since
		}
		if !f.Until.IsZero() {
			rng["$lte"] = f.Until
		}
		filter["first_seen"] = rng
	}

	// Tenant scoping: the platform owner sees everything; tenant-scoped
	// callers only see rows touching one of their domains.
	if scope := GetCallerScope(ctx); scope != nil && constants.IsTenantScoped(scope.Role) {
		domains, _ := scope.TenantDomains(ctx, s.db)
		if len(domains) == 0 {
			return []models.MailLogEntry{}, 0, nil
		}
		filter["domains"] = bson.M{"$in": domains}
	}

	col := s.db.Collection(database.ColMailLogs)
	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	cur, err := col.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "first_seen", Value: -1}}).
		SetSkip(int64((page-1)*limit)).
		SetLimit(int64(limit)))
	if err != nil {
		return nil, 0, err
	}
	defer cur.Close(ctx)
	out := []models.MailLogEntry{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// Stats summarizes the last `windowDays` of mail for the cards above the table.
func (s *MailLogService) Stats(ctx context.Context, windowDays int) (*models.MailLogStats, error) {
	if windowDays < 1 || windowDays > 90 {
		windowDays = 7
	}
	match := bson.M{"first_seen": bson.M{"$gte": time.Now().AddDate(0, 0, -windowDays)}}
	if scope := GetCallerScope(ctx); scope != nil && constants.IsTenantScoped(scope.Role) {
		domains, _ := scope.TenantDomains(ctx, s.db)
		if len(domains) == 0 {
			return &models.MailLogStats{ByStatus: map[string]int64{}, ByDirection: map[string]int64{}, BySource: map[string]int64{}, WindowDays: windowDays}, nil
		}
		match["domains"] = bson.M{"$in": domains}
	}
	st := &models.MailLogStats{
		ByStatus:    map[string]int64{},
		ByDirection: map[string]int64{},
		BySource:    map[string]int64{},
		WindowDays:  windowDays,
	}
	col := s.db.Collection(database.ColMailLogs)
	total, _ := col.CountDocuments(ctx, match)
	st.Total = total
	for _, dim := range []struct {
		field string
		dst   map[string]int64
	}{{"status", st.ByStatus}, {"direction", st.ByDirection}, {"source", st.BySource}} {
		cur, err := col.Aggregate(ctx, mongo.Pipeline{
			{{Key: "$match", Value: match}},
			{{Key: "$group", Value: bson.M{"_id": "$" + dim.field, "n": bson.M{"$sum": 1}}}},
		})
		if err != nil {
			continue
		}
		var rows []struct {
			ID string `bson:"_id"`
			N  int64  `bson:"n"`
		}
		_ = cur.All(ctx, &rows)
		for _, r := range rows {
			if r.ID == "" {
				r.ID = "unknown"
			}
			dim.dst[r.ID] = r.N
		}
	}
	return st, nil
}

// ───────────────────────────── header_checks ─────────────────────────────

// EnsureHeaderChecks installs a tiny header_checks map that logs the Subject
// and Content-Type of every message (WARN action — logs only, never rejects),
// then wires it into main.cf's header_checks list (preserving any existing
// operator maps) and reloads Postfix. Idempotent.
//
// IMPORTANT: we use the "regexp:" map type, NOT "pcre:". Postfix only
// supports pcre when the postfix-pcre package is installed; on a stock
// install (e.g. Ubuntu 24.04 without that package) a "pcre:" map produces
// "unsupported dictionary type: pcre" at cleanup time and Postfix temp-fails
// EVERY message with "451 4.3.0 queue file write error" — i.e. it takes mail
// delivery DOWN. The "regexp:" type is built into Postfix and always
// available, and our `/^Header:/ WARN` patterns parse identically under it.
// We additionally validate the map actually loads via `postmap -q` BEFORE
// wiring it, and roll back on any reload failure, so this can never break
// mail flow.
func (s *MailLogService) EnsureHeaderChecks(ctx context.Context) error {
	body := "# Managed by Betazen Server Panel — surfaces Subject + Content-Type into\n" +
		"# /var/log/mail.log so the panel mail-log ingestor can record them.\n" +
		"# WARN only logs; it never rejects or alters mail. regexp: type is\n" +
		"# built into Postfix (no postfix-pcre package required).\n" +
		"/^Subject:/ WARN\n" +
		"/^Content-Type:/ WARN\n"
	writeCmd := fmt.Sprintf("cat > %s <<'BZEOF'\n%sBZEOF\nchmod 0644 %s", headerChecksFile, body, headerChecksFile)
	if _, err := agent.RunCommand(ctx, "bash", "-c", writeCmd); err != nil {
		return fmt.Errorf("write header_checks map: %w", err)
	}

	want := "regexp:" + headerChecksFile

	// Pre-flight: confirm the map type is supported AND the file parses by
	// querying it. If this errors, do NOT touch main.cf — wiring an
	// unloadable map would temp-fail all mail.
	if _, err := agent.RunCommand(ctx, "postmap", "-q", "Subject: probe", want); err != nil {
		return fmt.Errorf("header_checks regexp map failed validation (not wiring it to protect mail flow): %w", err)
	}

	cur, err := agent.RunCommand(ctx, "postconf", "-h", "header_checks")
	if err != nil {
		return fmt.Errorf("read current header_checks: %w", err)
	}
	existing := strings.TrimSpace(cur.Output)

	// Rebuild the directive: drop any prior reference to OUR map (whether a
	// stale "pcre:" entry from an older build or the current "regexp:" one),
	// preserve every other operator-configured map, then append ours.
	var kept []string
	for _, tok := range strings.Fields(existing) {
		if strings.Contains(tok, headerChecksFile) {
			continue
		}
		kept = append(kept, tok)
	}
	newVal := strings.Join(append(kept, want), " ")
	if newVal == existing {
		return nil // already wired exactly right
	}

	if _, err := agent.RunCommand(ctx, "postconf", "-e", "header_checks="+newVal); err != nil {
		return fmt.Errorf("set header_checks: %w", err)
	}
	// Reload (not restart) so in-flight connections aren't dropped. If the
	// reload fails, roll the directive back so we never leave Postfix
	// pointed at a map it rejected.
	if _, err := agent.RunCommand(ctx, "postfix", "reload"); err != nil {
		_, _ = agent.RunCommand(ctx, "postconf", "-e", "header_checks="+existing)
		_, _ = agent.RunCommand(ctx, "postfix", "reload")
		return fmt.Errorf("postfix reload after header_checks change failed (rolled back): %w", err)
	}
	return nil
}

// ───────────────────────────── helpers ─────────────────────────────

// parseMailTS parses rsyslog's RFC3339 high-precision stamp; falls back to
// the legacy "Mon  2 15:04:05" syslog format, then to now.
func parseMailTS(s string) time.Time {
	if t, err := time.Parse("2006-01-02T15:04:05.999999Z07:00", s); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	now := time.Now().UTC()
	if t, err := time.Parse(time.Stamp, s); err == nil {
		return time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
	}
	return now
}

func isLoopback(ip string) bool {
	return ip == "127.0.0.1" || ip == "::1" || strings.HasPrefix(ip, "127.") || ip == "::ffff:127.0.0.1"
}

func looksLikeAttachment(ct string) bool {
	c := strings.ToLower(ct)
	return strings.Contains(c, "multipart/mixed") ||
		strings.Contains(c, "multipart/related") ||
		strings.Contains(c, "application/")
}

// domainsOf returns the lowercased domain part of each non-empty address.
func domainsOf(addrs ...string) []string {
	var out []string
	for _, a := range addrs {
		a = strings.Trim(strings.TrimSpace(a), "<>")
		if at := strings.LastIndex(a, "@"); at >= 0 && at < len(a)-1 {
			out = append(out, strings.ToLower(a[at+1:]))
		}
	}
	return out
}
