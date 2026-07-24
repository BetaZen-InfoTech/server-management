package services

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/mailer"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/validator"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func encryptPassword(plaintext, key string) (string, error) {
	keyHash := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decryptPassword(encrypted, key string) (string, error) {
	keyHash := sha256.Sum256([]byte(key))
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

type EmailService struct {
	db        *mongo.Database
	jwtSecret string
	// mailer is the shared SMTP relay handle wired post-construction
	// via SetMailer. Used by the bulk OTP flow (Bulk Delete, password
	// reveal in Bulk Export) to email a 6-digit code to the admin's
	// registered address. Nil-safe: when SMTP isn't configured the
	// OTP code falls through to stderr so a fresh-install admin can
	// still complete the flow via `journalctl -u serverpanel`.
	mailer *mailer.Mailer
	// domainCreator is the bulk-upload path's escape hatch for "the
	// CSV references a domain that doesn't exist yet". Wired
	// post-construction via SetDomainCreator (DomainService satisfies
	// the interface). Defined here as an interface — not as a direct
	// *DomainService field — to avoid an import cycle: DomainService
	// already holds a *EmailService reference for its admin@domain
	// auto-mailbox creation. Nil-safe: when unset, a missing domain
	// in the CSV simply fails the row with an actionable error
	// instead of attempting auto-create.
	domainCreator EmailDomainCreator
}

// EmailDomainCreator is the minimal slice of DomainService the email
// bulk flow needs. Defined as a method-set so we don't have to import
// the concrete type and create a service-to-service circular import.
// DomainService satisfies it implicitly via its existing Create.
type EmailDomainCreator interface {
	Create(ctx context.Context, req *models.CreateDomainRequest) (*models.Domain, error)
}

func NewEmailService(db *mongo.Database, jwtSecret ...string) *EmailService {
	secret := ""
	if len(jwtSecret) > 0 {
		secret = jwtSecret[0]
	}
	return &EmailService{db: db, jwtSecret: secret}
}

// SetMailer wires the shared mailer handle for OTP delivery on bulk
// destructive / sensitive operations. Optional; when nil the OTP is
// logged to stderr. Mirrors DomainService.SetMailer's contract.
func (s *EmailService) SetMailer(m *mailer.Mailer) { s.mailer = m }

// SetDomainCreator wires the auto-create-missing-domain path used by
// Bulk Mailbox Upload on the WHM surface. Wired post-construction in
// main.go after DomainService is built. Optional — when nil, a
// missing domain in the upload CSV/XLSX fails the row with an
// actionable error message instead of attempting to auto-provision.
func (s *EmailService) SetDomainCreator(d EmailDomainCreator) { s.domainCreator = d }

// ReencryptForTransfer translates a webmail-SSO ciphertext from the
// source panel's encryption (its JWT_SECRET) into this panel's. Each
// install picks a random JWT_SECRET, so the source's encrypted_pass
// blob is undecryptable on the destination — webmail SSO ("Open"
// arrow on the Email page) does nothing because GenerateWebmailToken
// can't recover the plaintext to feed Roundcube. The transfer step
// calls this once per mailbox after sync to re-encrypt under the
// destination's secret so SSO keeps working.
//
// Returns ("", nil) when SSO isn't possible — operator must reset the
// password from the panel UI to enable it (typically because the
// source's JWT_SECRET wasn't readable).
func (s *EmailService) ReencryptForTransfer(srcCipher, srcKey string) (string, error) {
	if srcCipher == "" || srcKey == "" || s.jwtSecret == "" {
		return "", nil
	}
	plain, err := decryptPassword(srcCipher, srcKey)
	if err != nil {
		return "", err
	}
	return encryptPassword(plain, s.jwtSecret)
}

// SSOHealReport summarises one HealStaleSSOEncryption sweep. Returned to
// the CLI / API caller so the operator sees how many SSO blobs survived
// the migration, how many were silently broken, and how many had no
// SSO data to begin with (the panel never set it, usually because the
// mailbox was added pre-encrypted_pass or because JWT_SECRET was empty
// at create time).
type SSOHealReport struct {
	Total      int      // mailboxes scanned
	Healthy    int      // encrypted_pass decrypted cleanly with current JWT_SECRET
	NoSSO      int      // encrypted_pass was empty / missing — nothing to do
	Cleared    int      // encrypted_pass was present but undecryptable (stale) — $unset
	ClearedTo  []string // first N emails that were cleared, surfaced to the operator log
}

// HealStaleSSOEncryption walks every mailbox row and clears
// encrypted_pass blobs that this panel's JWT_SECRET can't decrypt —
// the diagnostic shape of a server-to-server migration that
// happened BEFORE the v3.1.61 reencryptSyncedMailboxes pass was
// available, or a JWT_SECRET rotation that wasn't accompanied by
// password-reset emails to every mailbox owner.
//
// Why clear instead of try-to-fix: without the source's JWT_SECRET
// we CAN'T recover the plaintext. The panel's "Open in Webmail"
// arrow currently feeds the garbage bytes from decrypt() straight
// to Roundcube, which then tries IMAP auth with that garbage and
// shows the user the "Internal error" toast. Clearing makes the
// panel render a clean "Reset password to enable SSO" CTA instead,
// matching the UX for mailboxes that never had SSO set up in the
// first place. The mailbox itself still works for IMAP/SMTP login
// (the SHA512-CRYPT hash on the password field is portable).
//
// Idempotent. Operator can re-run safely; the second pass finds
// nothing to clear and reports it.
func (s *EmailService) HealStaleSSOEncryption(ctx context.Context) (SSOHealReport, error) {
	var rep SSOHealReport
	col := s.db.Collection(database.ColMailboxes)
	cur, err := col.Find(ctx, bson.M{})
	if err != nil {
		return rep, fmt.Errorf("list mailboxes: %w", err)
	}
	defer cur.Close(ctx)

	for cur.Next(ctx) {
		var mb models.Mailbox
		if cur.Decode(&mb) != nil {
			continue
		}
		rep.Total++
		ciph := strings.TrimSpace(mb.EncryptedPass)
		if ciph == "" {
			rep.NoSSO++
			continue
		}
		if s.jwtSecret == "" {
			// Panel has no JWT_SECRET to decrypt with — we'd
			// clear EVERY row, which is destructive in the
			// rare case where the secret just isn't loaded yet
			// (e.g. early boot). Abort with a clear error
			// rather than silently nuking the column.
			return rep, fmt.Errorf("JWT_SECRET is empty on this panel — refusing to clear encrypted_pass for %d mailboxes; check /opt/serverpanel/.env", rep.Total)
		}
		if _, derr := decryptPassword(ciph, s.jwtSecret); derr == nil {
			rep.Healthy++
			continue
		}
		// Stale under THIS panel's key. PARK the blob in
		// legacy_encrypted_pass instead of destroying it (v3.1.176) — it's
		// the only copy of that password on the box, and RekeyLegacyMailboxes
		// can still recover SSO from it once the operator supplies the key it
		// was sealed under. The active field is $unset (not $set:"") so the
		// column shape matches a mailbox that never had SSO and the panel
		// renders the clean "Reset password to enable SSO" CTA.
		if _, uerr := col.UpdateOne(ctx,
			bson.M{"_id": mb.ID},
			bson.M{
				"$set":   bson.M{"legacy_encrypted_pass": ciph, "updated_at": time.Now()},
				"$unset": bson.M{"encrypted_pass": ""},
			},
		); uerr != nil {
			log.Warn().Err(uerr).Str("email", mb.Email).Msg("heal-mail-sso: unset encrypted_pass failed")
			continue
		}
		rep.Cleared++
		if len(rep.ClearedTo) < 10 {
			rep.ClearedTo = append(rep.ClearedTo, mb.Email)
		}
	}
	return rep, nil
}

// SSORekeyReport summarises one RekeyLegacyMailboxes pass.
type SSORekeyReport struct {
	Candidates int      `json:"candidates"` // rows carrying a parked legacy blob
	Recovered  int      `json:"recovered"`  // re-sealed under this panel's key — SSO works again
	Failed     int      `json:"failed"`     // blob would not decrypt under the supplied key
	FailedTo   []string `json:"failed_to"`  // first N emails that failed, for the operator log
}

// RekeyLegacyMailboxes restores webmail SSO for mailboxes whose
// encrypted_pass a transfer could not re-key and therefore PARKED in
// legacy_encrypted_pass (see reencryptSyncedMailboxes / HealStaleSSOEncryption).
//
// The operator supplies srcKey — the JWT_SECRET of the server the blobs were
// originally sealed under (from that box's /opt/serverpanel/.env, or a backup
// of it). Each parked blob is decrypted with srcKey and re-sealed under THIS
// panel's key; on success the row gets a working encrypted_pass and the parked
// copy is dropped.
//
// This is the difference between "recover SSO for every migrated mailbox with
// one action" and "reset 35 users' passwords and make them all reconfigure
// their mail clients". Rows whose blob doesn't decrypt are left parked and
// untouched, so a second attempt with a different key is free.
//
// Idempotent and non-destructive: it never clears a working encrypted_pass and
// never deletes a parked blob it couldn't decrypt.
func (s *EmailService) RekeyLegacyMailboxes(ctx context.Context, srcKey string) (SSORekeyReport, error) {
	var rep SSORekeyReport
	srcKey = strings.TrimSpace(srcKey)
	if srcKey == "" {
		return rep, fmt.Errorf("source JWT_SECRET is required")
	}
	if s.jwtSecret == "" {
		return rep, fmt.Errorf("JWT_SECRET is empty on this panel — cannot re-seal; check /opt/serverpanel/.env")
	}

	col := s.db.Collection(database.ColMailboxes)
	cur, err := col.Find(ctx, bson.M{"legacy_encrypted_pass": bson.M{"$exists": true, "$ne": ""}})
	if err != nil {
		return rep, fmt.Errorf("list mailboxes: %w", err)
	}
	defer cur.Close(ctx)

	for cur.Next(ctx) {
		var mb models.Mailbox
		if cur.Decode(&mb) != nil {
			continue
		}
		legacy := strings.TrimSpace(mb.LegacyEncryptedPass)
		if legacy == "" {
			continue
		}
		rep.Candidates++
		newCipher, rErr := s.ReencryptForTransfer(legacy, srcKey)
		if rErr != nil || newCipher == "" {
			rep.Failed++
			if len(rep.FailedTo) < 10 {
				rep.FailedTo = append(rep.FailedTo, mb.Email)
			}
			continue
		}
		if _, uerr := col.UpdateOne(ctx,
			bson.M{"_id": mb.ID},
			bson.M{
				"$set":   bson.M{"encrypted_pass": newCipher, "updated_at": time.Now()},
				"$unset": bson.M{"legacy_encrypted_pass": ""},
			},
		); uerr != nil {
			log.Warn().Err(uerr).Str("email", mb.Email).Msg("rekey-legacy-sso: update failed")
			rep.Failed++
			continue
		}
		rep.Recovered++
	}
	return rep, nil
}

func (s *EmailService) ListMailboxes(ctx context.Context, domain string, page, limit int) ([]models.Mailbox, int64, error) {
	col := s.db.Collection(database.ColMailboxes)
	filter := bson.M{}
	if domain != "" {
		filter["domain"] = domain
	}
	if scope := GetCallerScope(ctx); scope != nil {
		filter = scope.ApplyDomainScope(ctx, s.db, "domain", filter)
	}

	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().SetSkip(skip).SetLimit(int64(limit)).SetSort(bson.D{{Key: "email", Value: 1}})
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var mailboxes []models.Mailbox
	if err := cursor.All(ctx, &mailboxes); err != nil {
		return nil, 0, err
	}
	if mailboxes == nil {
		mailboxes = []models.Mailbox{}
	}
	// Derive sso_ready so the list surfaces which mailboxes still need a
	// password reset to re-enable one-click webmail SSO (encrypted_pass gone,
	// typically after a migration that couldn't re-key it).
	for i := range mailboxes {
		mailboxes[i].SSOReady = strings.TrimSpace(mailboxes[i].EncryptedPass) != ""
	}

	// Live disk usage. ListMailboxes used to return used_mb=0 for every
	// row because the DB never stores the growing maildir size — only
	// GetMailbox ran `du`. The list UI therefore always rendered "0 MB /
	// N MB 0%" regardless of actual inbox state.
	//
	// Batch one `du -sm` across every maildir on the page so we pay a
	// single fork + filesystem walk per request, not N. Missing
	// directories (freshly-created mailbox, no mail yet) just drop out
	// of the output and leave UsedMB at 0.
	if len(mailboxes) > 0 {
		paths := make([]string, 0, len(mailboxes))
		pathByEmail := make(map[string]string, len(mailboxes))
		for _, mb := range mailboxes {
			p := s.getMaildirPath(ctx, mb.Email)
			if p == "" {
				continue
			}
			paths = append(paths, p)
			pathByEmail[mb.Email] = p
		}
		if len(paths) > 0 {
			// Quote each path for the shell; `du -sm` prints size<TAB>path
			// lines, one per arg. We suppress errors so a missing dir for
			// a never-received mailbox doesn't abort the whole batch.
			quoted := make([]string, 0, len(paths))
			for _, p := range paths {
				quoted = append(quoted, "'"+strings.ReplaceAll(p, "'", `'\''`)+"'")
			}
			cmd := "du -sm " + strings.Join(quoted, " ") + " 2>/dev/null || true"
			if r, err := agent.RunCommand(ctx, "bash", "-c", cmd); err == nil && r != nil {
				sizeByPath := make(map[string]float64, len(paths))
				for _, line := range strings.Split(r.Output, "\n") {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					fields := strings.Fields(line)
					if len(fields) < 2 {
						continue
					}
					var sz float64
					if _, perr := fmt.Sscanf(fields[0], "%f", &sz); perr != nil {
						continue
					}
					// Path is whatever follows the size; may contain spaces.
					p := strings.Join(fields[1:], " ")
					sizeByPath[p] = sz
				}
				for i := range mailboxes {
					if p, ok := pathByEmail[mailboxes[i].Email]; ok {
						if sz, ok := sizeByPath[p]; ok {
							mailboxes[i].UsedMB = sz
						}
					}
				}
			}
		}
	}

	return mailboxes, total, nil
}

// getMaildirPath returns the maildir path for a given email, looking up the domain owner.
func (s *EmailService) getMaildirPath(ctx context.Context, email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return ""
	}
	localPart := parts[0]
	domain := parts[1]

	// Look up domain to get the owning user
	domCol := s.db.Collection(database.ColDomains)
	var dom models.Domain
	if err := domCol.FindOne(ctx, bson.M{"domain": domain}).Decode(&dom); err == nil && dom.User != "" {
		return fmt.Sprintf("/home/%s/mail/%s/%s", dom.User, domain, localPart)
	}
	// Fallback if domain not found in our DB
	return fmt.Sprintf("/var/vmail/%s/%s", domain, localPart)
}

// GetMailboxByAddress returns a mailbox by its full email address. Used by
// the programmatic API so an integration can address mailboxes by name
// instead of having to call List first to discover the ObjectID. Returns
// nil + ErrNoDocuments if the address isn't on file.
//
// Lookup is case-insensitive on the local-part: an integrator who calls
// `…/mailboxes/Admin@example.com/…` lands on the same row created from
// the panel UI as `admin@example.com`. Pre-3.1.29 the Mongo `email`
// field was stored verbatim from `req.Email` (no normalisation), so a
// mailbox typed `Admin@…` and queried `admin@…` would 404 — the cause
// of the user-reported "mailbox not found" toast on the External
// webmail-link API.
func (s *EmailService) GetMailboxByAddress(ctx context.Context, address string) (*models.Mailbox, error) {
	return s.findMailboxByEmail(ctx, address)
}

// findMailboxByEmail is the shared case-insensitive lookup used by every
// "address → mailbox" path (programmatic API, webmail SSO, send-test).
// Tries an exact match first (cheapest, hits the unique index directly)
// and only falls back to a case-insensitive regex when nothing comes
// back — so the happy path's index-only lookup stays index-only.
func (s *EmailService) findMailboxByEmail(ctx context.Context, address string) (*models.Mailbox, error) {
	col := s.db.Collection(database.ColMailboxes)
	addr := strings.TrimSpace(address)
	if addr == "" {
		return nil, mongo.ErrNoDocuments
	}
	var mailbox models.Mailbox
	if err := col.FindOne(ctx, bson.M{"email": addr}).Decode(&mailbox); err == nil {
		return &mailbox, nil
	} else if err != mongo.ErrNoDocuments {
		return nil, err
	}
	// Case-insensitive fallback for pre-3.1.29 mailboxes whose stored
	// email kept its original casing. Anchor the regex so the match is
	// exact-string + case-folded, never substring.
	pattern := "^" + regexQuoteEmail(addr) + "$"
	if err := col.FindOne(ctx, bson.M{"email": bson.M{"$regex": pattern, "$options": "i"}}).Decode(&mailbox); err != nil {
		return nil, err
	}
	return &mailbox, nil
}

// regexQuoteEmail escapes the regex metacharacters that can legitimately
// appear in an email address. Avoids pulling in regexp.QuoteMeta to keep
// the regex safe for the small set of chars we actually need.
func regexQuoteEmail(s string) string {
	const meta = `\.+*?()|[]{}^$`
	var b strings.Builder
	b.Grow(len(s) + 4)
	for _, r := range s {
		if strings.ContainsRune(meta, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (s *EmailService) GetMailbox(ctx context.Context, id string) (*models.Mailbox, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid mailbox ID")
	}
	col := s.db.Collection(database.ColMailboxes)
	var mailbox models.Mailbox
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&mailbox); err != nil {
		return nil, err
	}
	mailbox.SSOReady = strings.TrimSpace(mailbox.EncryptedPass) != ""

	// Get live disk usage
	maildir := s.getMaildirPath(ctx, mailbox.Email)
	if maildir != "" {
		result, err := agent.RunCommand(ctx, "du", "-sm", maildir)
		if err == nil {
			fields := strings.Fields(result.Output)
			if len(fields) > 0 {
				fmt.Sscanf(fields[0], "%f", &mailbox.UsedMB)
			}
		}
	}

	return &mailbox, nil
}

func (s *EmailService) CreateMailbox(ctx context.Context, req *models.CreateMailboxRequest) (*models.Mailbox, error) {
	// Normalise email to lowercase + trimmed at the create boundary so
	// every downstream lookup (programmatic API, webmail SSO, send-test,
	// /etc/dovecot/users sed, postfix virtual_mailbox_maps) keys off a
	// canonical form. Pre-3.1.29 the row was stored verbatim from
	// req.Email and case-mismatched lookups hit "mailbox not found".
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Domain != "" {
		req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	}
	// Security (audit §8a): this value is interpolated into shell commands
	// (sed/echo into /etc/dovecot/users + postfix maps). Reject anything that
	// isn't a strictly shell-safe email/domain so a crafted address can't break
	// out of the echo '…' quoting and run commands as root. Gates ALL callers
	// (single create, bulk-upload, programmatic API) since it's at the service.
	if !validator.IsSafeEmail(req.Email) {
		return nil, fmt.Errorf("invalid email address %q", req.Email)
	}
	if req.Domain != "" && !validator.IsSafeDNSName(req.Domain) {
		return nil, fmt.Errorf("invalid domain %q", req.Domain)
	}
	parts := strings.SplitN(req.Email, "@", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid email format")
	}
	localPart := parts[0]
	domain := parts[1]

	// Look up domain to get the owning user for mail path
	var maildir string
	domCol := s.db.Collection(database.ColDomains)
	var dom models.Domain
	if err := domCol.FindOne(ctx, bson.M{"domain": domain}).Decode(&dom); err == nil && dom.User != "" {
		maildir = fmt.Sprintf("/home/%s/mail/%s/%s", dom.User, domain, localPart)
	} else {
		maildir = fmt.Sprintf("/var/vmail/%s/%s", domain, localPart)
	}

	// Create maildir
	agent.RunCommand(ctx, "mkdir", "-p", maildir+"/cur", maildir+"/new", maildir+"/tmp")
	agent.RunCommand(ctx, "chown", "-R", "vmail:vmail", maildir)

	// Make sure vmail (gid 5000) can traverse into /home/<owner>/ to
	// reach the maildir tree. Default /home/<owner> is mode 0750 owned
	// by <owner>:<owner>, which excludes vmail entirely — Dovecot then
	// can't open the maildir and every delivery bounces with EACCES.
	// chgrp to vmail + g+x just on the parent home (NOT recursive — we
	// only need traversal).
	if dom.User != "" {
		homeDir := "/home/" + dom.User
		agent.RunCommand(ctx, "chgrp", "vmail", homeDir)
		agent.RunCommand(ctx, "chmod", "g+x", homeDir)
	}

	// Generate password hash for Dovecot
	passResult, err := agent.RunCommand(ctx, "doveadm", "pw", "-s", "SHA512-CRYPT", "-p", req.Password)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}
	passHash := strings.TrimSpace(passResult.Output)

	// Add to Dovecot users file (format: user:pass:uid:gid::home::userdb_mail=maildir:path)
	quota := req.QuotaMB
	if quota == 0 {
		quota = 1024
	}
	userLine := fmt.Sprintf("%s:%s:5000:5000::%s::userdb_mail=maildir:%s", req.Email, passHash, maildir, maildir)
	// Idempotent write: remove ANY existing line for this email first,
	// then append. Without this, repeated create-after-delete (typical
	// when an operator re-creates a subdomain whose admin@<sub>
	// auto-mailbox previously existed) accumulates duplicate rows in
	// /etc/dovecot/users. Dovecot's auth handler logs "User <email>
	// exists more than once" and picks the FIRST match — which still
	// holds the OLD password hash, so manual IMAP/SMTP login fails
	// against the visible "current" panel password. Roundcube
	// auto-login works because it bypasses passdb (HMAC-signed SSO),
	// which is exactly the symptom the user reported.
	escEmail := strings.ReplaceAll(req.Email, ".", "\\.")
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"sed -i '/^%s:/d' /etc/dovecot/users 2>/dev/null; echo '%s' >> /etc/dovecot/users",
		escEmail, userLine,
	))

	// Postfix virtual-mailbox wiring.
	//
	// CRITICAL: these file names must match the `virtual_mailbox_domains`
	// and `virtual_mailbox_maps` directives in main.cf — which are
	// generated by agent/email_install.go as
	//     virtual_mailbox_domains = hash:/etc/postfix/virtual_mailbox_domains
	//     virtual_mailbox_maps    = hash:/etc/postfix/virtual_mailbox_maps
	//
	// The earlier implementation here wrote to `/etc/postfix/virtual_domains`
	// and `/etc/postfix/virtual_mailboxes`, which Postfix never reads.
	// Result: panel-created mailboxes existed in our MongoDB + on disk,
	// but Postfix rejected every inbound mail for the domain and had no
	// LMTP recipient to deliver locally either. That's the "emails not
	// sending / receiving — even subdomains" bug.
	mapping := fmt.Sprintf("%s    %s/%s/", req.Email, domain, localPart)
	// Idempotent: same dedupe pattern as the dovecot users line above —
	// without this, Postfix's virtual_mailbox_maps grows duplicate rows
	// on every re-create. Postfix tolerates duplicates better than
	// Dovecot does (postmap silently picks the last entry), but
	// they're still confusing to debug and add fsck noise.
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"sed -i '/^%s\\s/d' /etc/postfix/virtual_mailbox_maps 2>/dev/null; echo '%s' >> /etc/postfix/virtual_mailbox_maps",
		strings.ReplaceAll(req.Email, ".", "\\."), mapping,
	))
	agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailbox_maps")

	// virtual_mailbox_domains is referenced as `hash:...` in main.cf, so
	// each line needs a key + value. We emit `<domain> OK` so postmap
	// can build a .db without the "expected format: key whitespace
	// value" warnings that used to flood /var/log/mail.log on every
	// mailbox create.
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"grep -qE '^%s( |\t)' /etc/postfix/virtual_mailbox_domains 2>/dev/null || echo '%s OK' >> /etc/postfix/virtual_mailbox_domains",
		strings.ReplaceAll(domain, ".", "\\."), domain))
	agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailbox_domains")

	// Ensure OpenDKIM can sign outbound mail for this domain. Three
	// cases: (1) the domain already has its own key — nothing to do;
	// (2) the domain is a subdomain and the parent already has a key
	// — reuse the parent's selector so a single DNS TXT covers the
	// whole tree; (3) neither — generate a fresh key. Missing this
	// step is what produced Gmail's "550 5.7.26 DKIM = did not pass"
	// bounces for subdomain senders whose domain was added via
	// transfer rather than the normal CreateDomain flow.
	s.EnsureDKIMForDomain(ctx, domain)

	// Reload Postfix
	agent.RunCommand(ctx, "systemctl", "reload", "postfix")

	// First-mailbox-on-this-box hook: ensure the Sieve pipeline that
	// fires `email.message.received` webhooks is installed. Safe to
	// call on every create — agent.EnsureMailHookInstalled is idempotent
	// (apt + writeFileSecure + systemctl restart), so the second call
	// is a no-op except for a quick dovecot restart. We swallow errors
	// here on purpose: the mailbox itself was created successfully and
	// the missing Sieve hook degrades only the inbound webhook feature.
	go func() {
		// Best-effort, off the request path. Use the package context so a
		// cancelled request doesn't tear down the apt-get install.
		_, _ = agent.EnsureMailHookInstalled(context.Background(), "")
	}()

	now := time.Now()

	// Encrypt plaintext password for webmail SSO
	var encPass string
	if s.jwtSecret != "" {
		encPass, _ = encryptPassword(req.Password, s.jwtSecret)
	}

	mailbox := models.Mailbox{
		Email:            req.Email,
		Password:         passHash,
		EncryptedPass:    encPass,
		Domain:           domain,
		QuotaMB:          quota,
		SendLimitPerHour: req.SendLimitPerHour,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	mbCol := s.db.Collection(database.ColMailboxes)
	result, err := mbCol.InsertOne(ctx, mailbox)
	if err != nil {
		return nil, err
	}
	mailbox.ID = result.InsertedID.(primitive.ObjectID)

	// Outbound webhook fan-out — vendor integrations can react by syncing
	// the new mailbox into their CRM, mailchimp lists, etc.
	EmitEvent(ctx, "email.mailbox.created", LookupTenantIDForDomain(ctx, s.db, domain), map[string]any{
		"id":       mailbox.ID.Hex(),
		"email":    mailbox.Email,
		"domain":   mailbox.Domain,
		"quota_mb": mailbox.QuotaMB,
	})
	return &mailbox, nil
}

func (s *EmailService) UpdateMailbox(ctx context.Context, id string, updates map[string]interface{}) (*models.Mailbox, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid mailbox ID")
	}

	col := s.db.Collection(database.ColMailboxes)

	// Load the existing mailbox first — we need the email and current
	// dovecot user-line shape so a password change can rewrite the
	// /etc/dovecot/users entry in place (preserving the maildir path,
	// uid/gid, and any extra fields the line carries).
	var existing models.Mailbox
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&existing); err != nil {
		return nil, fmt.Errorf("mailbox not found: %w", err)
	}

	setFields := bson.M{"updated_at": time.Now()}
	if v, ok := updates["quota_mb"]; ok {
		setFields["quota_mb"] = v
	}
	if v, ok := updates["send_limit_per_hour"]; ok {
		setFields["send_limit_per_hour"] = v
	}
	if v, ok := updates["password"]; ok {
		if pass, ok := v.(string); ok && pass != "" {
			// Hash the new plaintext via doveadm pw — same shape used in
			// CreateMailbox so dovecot reads it back identically.
			passResult, err := agent.RunCommand(ctx, "doveadm", "pw", "-s", "SHA512-CRYPT", "-p", pass)
			if err != nil {
				return nil, fmt.Errorf("hash password: %w", err)
			}
			newHash := strings.TrimSpace(passResult.Output)
			if newHash == "" {
				return nil, fmt.Errorf("doveadm pw returned empty hash")
			}

			// Rewrite the user's line in /etc/dovecot/users in place. Use
			// awk so we only touch the password field of the matching row
			// (line may be `email:hash:5000:5000::maildir::userdb_mail=...`)
			// — without preserving the trailing fields, dovecot loses the
			// maildir path and every login lands in /var/empty/Maildir/.
			//
			// awk rewrites field 2 (password) when field 1 matches; emits
			// every line otherwise unchanged. FS/OFS = ":" so the
			// surrounding colons stay where they are.
			emailEsc := strings.ReplaceAll(existing.Email, "'", "'\\''")
			hashEsc := strings.ReplaceAll(newHash, "'", "'\\''")
			awkProg := fmt.Sprintf(
				`awk -F: -v OFS=: -v E='%s' -v H='%s' '$1==E{$2=H} {print}' /etc/dovecot/users > /etc/dovecot/users.new && mv /etc/dovecot/users.new /etc/dovecot/users && chown dovecot:dovecot /etc/dovecot/users && chmod 0640 /etc/dovecot/users`,
				emailEsc, hashEsc)
			if _, err := agent.RunCommand(ctx, "bash", "-c", awkProg); err != nil {
				return nil, fmt.Errorf("update /etc/dovecot/users: %w", err)
			}

			// If the email isn't in the file at all (mailbox rows that
			// pre-date the per-user line write), append a fresh line so
			// auth starts working after this update.
			if _, err := agent.RunCommand(ctx, "bash", "-c",
				fmt.Sprintf(`grep -qE '^%s:' /etc/dovecot/users || echo '%s:%s:5000:5000::%s::userdb_mail=maildir:%s' >> /etc/dovecot/users`,
					strings.ReplaceAll(existing.Email, ".", `\.`),
					existing.Email, newHash,
					s.getMaildirPath(ctx, existing.Email),
					s.getMaildirPath(ctx, existing.Email))); err != nil {
				// Non-fatal — awk update above succeeded for existing rows.
			}

			setFields["password"] = newHash
			if s.jwtSecret != "" {
				if enc, err := encryptPassword(pass, s.jwtSecret); err == nil {
					setFields["encrypted_pass"] = enc
					// A fresh encrypted_pass re-arms webmail SSO, so any parked
					// legacy blob is now obsolete — drop it so it can't shadow
					// the live credential during a later rekey pass.
					setFields["legacy_encrypted_pass"] = ""
				}
			}
			// Password-reset provenance (v3.1.180). Stamp WHEN + through which
			// surface + by whom, so `password_set_via:"api"` rows are an
			// auditable record of every reset done through the public API. The
			// via/by values come from the handler, NOT the request body, so a
			// caller can't forge them.
			now := time.Now()
			setFields["password_set_at"] = now
			if via, _ := updates["password_set_via"].(string); via != "" {
				setFields["password_set_via"] = via
			}
			if by, _ := updates["password_set_by"].(string); by != "" {
				setFields["password_set_by"] = by
			}
		}
	}

	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var mailbox models.Mailbox
	err = col.FindOneAndUpdate(ctx, bson.M{"_id": oid}, bson.M{"$set": setFields}, opts).Decode(&mailbox)
	if err != nil {
		return nil, err
	}
	mailbox.SSOReady = strings.TrimSpace(mailbox.EncryptedPass) != ""
	return &mailbox, nil
}

// DeleteMailbox is the public single-row delete. Always runs the
// postmap + Postfix reload at the end. For bulk callers that delete
// dozens of mailboxes in a row, use DeleteMailboxBatched(skipReload=
// true) inside the loop and call PostfixApplyMailboxDeleteFlush once
// at the end so the operator pays the postmap + reload cost ONCE
// instead of N times.
func (s *EmailService) DeleteMailbox(ctx context.Context, id string) error {
	return s.deleteMailbox(ctx, id, false)
}

// DeleteMailboxBatched is the bulk-loop variant — same delete steps,
// but skips the per-row postmap + systemctl reload when skipReload=
// true. Caller must call PostfixApplyMailboxDeleteFlush after the
// loop. Pre-3.1.42 the bulk-delete loop ran postmap + reload PER ROW
// and a 50-mailbox delete fired 50 systemctl-reload-postfix calls
// back-to-back; on a busy box Postfix occasionally failed to reload
// (kernel rate-limit) and the operator would see "deleted ok" in the
// panel + still-routed mail in production for the half-second window
// before the next reload won.
func (s *EmailService) DeleteMailboxBatched(ctx context.Context, id string, skipReload bool) error {
	return s.deleteMailbox(ctx, id, skipReload)
}

// PostfixApplyMailboxDeleteFlush runs postmap + Postfix reload once
// after a batched delete loop. Pair with DeleteMailboxBatched(_,
// true) calls.
func (s *EmailService) PostfixApplyMailboxDeleteFlush(ctx context.Context) error {
	if _, err := agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailbox_maps"); err != nil {
		return fmt.Errorf("postmap: %w", err)
	}
	if _, err := agent.RunCommand(ctx, "systemctl", "reload", "postfix"); err != nil {
		log.Warn().Err(err).Msg("postfix reload failed after batched mailbox delete")
	}
	return nil
}

// deleteMailbox is the shared implementation. New v3.1.42 hardening:
//   - PATH-SAFETY GUARD on the maildir before `rm -rf`. Pre-3.1.42 a
//     malformed mailbox.Email (corrupted Mongo row, missing @-part)
//     made getMaildirPath return paths like `/var/vmail/` or
//     `/home/<user>/mail//` and `rm -rf` would happily nuke entire
//     tenant mail trees. The guard rejects any path with a blank
//     segment, `..`, or fewer than 4 path components after the
//     /var/vmail or /home prefix.
//   - REGEX-META ESCAPE on the email before sed, not just `.`. A
//     mailbox like `sales+leads@example.com` previously made sed
//     interpret `+` as a quantifier and either match nothing or
//     match the wrong line.
//   - SURFACE RunCommand errors as a structured WARN log so a botched
//     delete (file locked, sed failed, dovecot/users missing) shows
//     up in journalctl. Pre-3.1.42 every command was fire-and-forget
//     and a partial-failure left the operator with a "deleted in
//     panel but still receiving mail" mystery.
//   - SKIP postmap + reload when skipReload=true so a 50-mailbox
//     bulk delete only reloads Postfix once.
func (s *EmailService) deleteMailbox(ctx context.Context, id string, skipReload bool) error {
	mailbox, err := s.GetMailbox(ctx, id)
	if err != nil {
		return fmt.Errorf("mailbox not found: %w", err)
	}
	if mailbox.Email == "" || !strings.Contains(mailbox.Email, "@") {
		// Refuse to act on a Mongo row whose email field is blank or
		// missing an @-part. Otherwise the sed patterns below would
		// run with empty match strings (sed would interpret `^:` as
		// "lines starting with :" and delete every postmap entry
		// that begins with the path-separator after a virtual_alias
		// flatten).
		return fmt.Errorf("mailbox %s has malformed email %q — refusing to delete to avoid wiping the wrong rows", id, mailbox.Email)
	}

	// Remove from /etc/dovecot/users — escape every regex meta-char,
	// not just `.`, so a `+` / `*` / `?` in the local-part doesn't
	// turn into a quantifier in the sed pattern.
	emailEsc := postfixSedEscape(mailbox.Email)
	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"sed -i '/^%s:/d' /etc/dovecot/users", emailEsc,
	)); err != nil {
		log.Warn().Err(err).Str("email", mailbox.Email).Msg("delete mailbox: sed dovecot/users failed")
	}

	// Remove from the virtual_mailbox_maps file that main.cf actually
	// references (see CreateMailbox for the matching write path).
	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"sed -i '/^%s\\s/d' /etc/postfix/virtual_mailbox_maps", emailEsc,
	)); err != nil {
		log.Warn().Err(err).Str("email", mailbox.Email).Msg("delete mailbox: sed postfix maps failed")
	}

	// Maildir removal — guarded against the path-safety failure modes
	// described in the function header. If the path doesn't pass the
	// guard, we log + skip the rm so a corrupted row can be cleaned
	// out of Mongo + Postfix without nuking arbitrary directories.
	maildir := s.getMaildirPath(ctx, mailbox.Email)
	if !isSafeMaildirPath(maildir) {
		log.Warn().
			Str("email", mailbox.Email).
			Str("maildir", maildir).
			Msg("delete mailbox: maildir path failed safety guard — skipping rm -rf")
	} else {
		if _, err := agent.RunCommand(ctx, "rm", "-rf", maildir); err != nil {
			log.Warn().Err(err).Str("maildir", maildir).Msg("delete mailbox: rm -rf maildir failed")
		}
	}

	// postmap + reload — skipped in the batched bulk path. The caller
	// must invoke PostfixApplyMailboxDeleteFlush after the loop.
	if !skipReload {
		if _, err := agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailbox_maps"); err != nil {
			log.Warn().Err(err).Msg("delete mailbox: postmap failed")
		}
		if _, err := agent.RunCommand(ctx, "systemctl", "reload", "postfix"); err != nil {
			log.Warn().Err(err).Msg("delete mailbox: postfix reload failed (map is in place; will pick up on next mail)")
		}
	}

	col := s.db.Collection(database.ColMailboxes)
	if _, err := col.DeleteOne(ctx, bson.M{"_id": mailbox.ID}); err != nil {
		return fmt.Errorf("delete mongo row: %w", err)
	}

	// Outbound webhook fan-out so vendor integrations can clean up
	// downstream state (mailing-list memberships, IMAP forward rules, ...).
	EmitEvent(ctx, "email.mailbox.deleted", LookupTenantIDForDomain(ctx, s.db, mailbox.Domain), map[string]any{
		"id":     mailbox.ID.Hex(),
		"email":  mailbox.Email,
		"domain": mailbox.Domain,
	})
	return nil
}

// isSafeMaildirPath rejects any path that looks unsafe to feed to
// `rm -rf`. Specifically:
//   - empty
//   - contains "/.." or starts with ".."
//   - has a blank segment (e.g. `/var/vmail//foo` or `/home/x/mail//bar`)
//   - doesn't start with one of the two known maildir prefixes
//   - has fewer than 4 path components after the prefix (so we never
//     `rm -rf /var/vmail/` or `/home/<user>/mail/<domain>/`)
//
// The two legitimate shapes are exactly:
//
//	/var/vmail/<domain>/<localpart>      → 5 components: ["", "var", "vmail", "<domain>", "<localpart>"]
//	/home/<user>/mail/<domain>/<localpart> → 6 components: ["", "home", "<user>", "mail", "<domain>", "<localpart>"]
//
// Any other shape fails the guard.
func isSafeMaildirPath(p string) bool {
	if p == "" || strings.Contains(p, "..") {
		return false
	}
	parts := strings.Split(p, "/")
	for _, seg := range parts[1:] { // skip leading "" from the absolute-path slash
		if seg == "" {
			return false
		}
	}
	if strings.HasPrefix(p, "/var/vmail/") {
		// Must be exactly /var/vmail/<domain>/<localpart>
		return len(parts) == 5 && parts[1] == "var" && parts[2] == "vmail"
	}
	if strings.HasPrefix(p, "/home/") {
		// Must be exactly /home/<user>/mail/<domain>/<localpart>
		return len(parts) == 6 && parts[1] == "home" && parts[3] == "mail"
	}
	return false
}

func (s *EmailService) ListForwarders(ctx context.Context, domain string) ([]models.EmailForwarder, error) {
	col := s.db.Collection(database.ColForwarders)
	filter := bson.M{}
	if domain != "" {
		filter["domain"] = domain
	}
	if scope := GetCallerScope(ctx); scope != nil {
		filter = scope.ApplyDomainScope(ctx, s.db, "domain", filter)
	}

	cursor, err := col.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "source", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var forwarders []models.EmailForwarder
	if err := cursor.All(ctx, &forwarders); err != nil {
		return nil, err
	}
	if forwarders == nil {
		forwarders = []models.EmailForwarder{}
	}
	return forwarders, nil
}

func (s *EmailService) CreateForwarder(ctx context.Context, fwd *models.EmailForwarder) (*models.EmailForwarder, error) {
	// Normalise the source + destinations + domain BEFORE writing
	// anywhere so the Mongo row, the Postfix line, and downstream
	// lookups all key on the same canonical form. Pre-3.1.37 the
	// row was stored with the operator's exact casing — a typed
	// `Alice@example.com` source then refused to match a query for
	// `alice@example.com` (RFC 5321 says local-part is case-
	// preserving but case-insensitive in practice; every other auth
	// path in the panel lowercases).
	fwd.Source = strings.ToLower(strings.TrimSpace(fwd.Source))
	for i, d := range fwd.Destinations {
		fwd.Destinations[i] = strings.ToLower(strings.TrimSpace(d))
	}
	if fwd.Domain == "" {
		parts := strings.SplitN(fwd.Source, "@", 2)
		if len(parts) == 2 {
			fwd.Domain = parts[1]
		}
	}
	fwd.Domain = strings.ToLower(strings.TrimSpace(fwd.Domain))

	// Security + correctness (audit §8a): reject a non-shell-safe source or
	// destination at the entry point so we neither shell-inject in
	// applyForwarderToPostfix NOR persist a junk Mongo row when the postfix
	// sink rejects it (postfix-apply failure is non-fatal below).
	if !validator.IsSafeEmail(fwd.Source) {
		return nil, fmt.Errorf("invalid forwarder source %q", fwd.Source)
	}
	for _, d := range fwd.Destinations {
		if d != "" && !validator.IsSafeEmail(d) {
			return nil, fmt.Errorf("invalid forwarder destination %q", d)
		}
	}

	// Apply to Postfix idempotently — sed-removes any prior line with
	// the same source key, then appends the fresh one. Without the
	// dedupe, repeated CreateForwarder calls on the same source (the
	// usual case during a transfer rehydrate) accumulated duplicate
	// rows in /etc/postfix/virtual_alias_maps; postmap silently picked
	// the LAST one but the file grew unbounded.
	if err := s.applyForwarderToPostfix(ctx, fwd.Source, fwd.Destinations, fwd.KeepCopy, true); err != nil {
		// Postfix wiring failure is non-fatal for the Mongo write —
		// the row is still authoritative and a future bzpanel
		// rebuild-forwarders will reconcile. We log so the operator
		// sees it in journalctl, then continue.
		log.Warn().Err(err).Str("source", fwd.Source).Msg("forwarder: postfix apply failed, continuing with Mongo write")
	}

	fwd.CreatedAt = time.Now()
	col := s.db.Collection(database.ColForwarders)
	result, err := col.InsertOne(ctx, fwd)
	if err != nil {
		return nil, err
	}
	fwd.ID = result.InsertedID.(primitive.ObjectID)

	// Outbound webhook fan-out — useful for vendors who replicate forwarder
	// rules into Google Workspace / external SMTP routers.
	EmitEvent(ctx, "email.forwarder.created", LookupTenantIDForDomain(ctx, s.db, fwd.Domain), map[string]any{
		"id":           fwd.ID.Hex(),
		"source":       fwd.Source,
		"destinations": fwd.Destinations,
		"domain":       fwd.Domain,
	})
	return fwd, nil
}

// composeForwarderDestinations returns the final destination list to
// write into /etc/postfix/virtual_alias_maps for one forwarder.
//
// When keepCopy is true the source address is appended to the
// destinations so Postfix delivers a copy back to the source mailbox
// in addition to forwarding (cPanel "Keep a copy in this account"
// semantics). When false, just the forward destinations.
//
// De-duped + lower-cased + trimmed so:
//   * `keep_copy=true` with source already in destinations doesn't
//     produce `bob@x.com, sales@x.com, sales@x.com`.
//   * mixed casing in operator input collapses to a canonical line.
//   * blank entries from a trailing comma are dropped.
//
// Pre-3.1.44 the bug: applyForwarderToPostfix ignored keep_copy
// entirely. The Mongo row + UI toggle + transfer-recovery hydration
// all carried the flag but the line written to virtual_alias_maps
// was always `source → joined destinations` — toggling "Keep a copy"
// in the UI had ZERO effect on actual mail routing. Operators who
// turned it on still saw forwarded mail vanish from the source
// mailbox.
func composeForwarderDestinations(source string, destinations []string, keepCopy bool) []string {
	src := strings.ToLower(strings.TrimSpace(source))
	seen := make(map[string]struct{}, len(destinations)+1)
	out := make([]string, 0, len(destinations)+1)
	for _, d := range destinations {
		v := strings.ToLower(strings.TrimSpace(d))
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	if keepCopy && src != "" {
		if _, dup := seen[src]; !dup {
			out = append(out, src)
		}
	}
	return out
}

// applyForwarderToPostfix writes one forwarder line to
// /etc/postfix/virtual_alias_maps idempotently (delete any existing
// line for `source`, then append). When `reload` is true the map is
// re-built (postmap) and Postfix is reloaded — pass false when bulk-
// processing many forwarders so the caller can amortise both costs
// to the end of the loop via PostfixApplyForwardersFlush.
//
// When `keepCopy` is true, the source address is appended to the
// destinations list so Postfix also delivers a copy to the source
// mailbox (cPanel "Keep a copy in this account" semantics). The
// canonical source-of-truth lives on the Mongo forwarder row's
// keep_copy field; every caller (CreateForwarder, bulk-row
// executor, RebuildVirtualAliasMaps) must pass it through.
func (s *EmailService) applyForwarderToPostfix(ctx context.Context, source string, destinations []string, keepCopy, reload bool) error {
	if source == "" || len(destinations) == 0 {
		return fmt.Errorf("source + at least one destination required")
	}
	// source + destinations are interpolated into a root `echo '...' >>`
	// virtual_alias_maps write — reject anything that isn't a strictly
	// shell-safe email so a crafted address can't break out of the
	// single-quoted echo and run commands as root (security audit §8a).
	// Mirrors the CreateMailbox guard above.
	if !validator.IsSafeEmail(source) {
		return fmt.Errorf("invalid forwarder source %q", source)
	}
	for _, d := range destinations {
		if d = strings.TrimSpace(d); d != "" && !validator.IsSafeEmail(d) {
			return fmt.Errorf("invalid forwarder destination %q", d)
		}
	}
	finalDests := composeForwarderDestinations(source, destinations, keepCopy)
	if len(finalDests) == 0 {
		return fmt.Errorf("no usable destinations after dedupe")
	}
	dst := strings.Join(finalDests, ", ")
	// Escape regex metacharacters in the source for the sed pattern.
	// Single-quote-bash-quote the whole sed expression so the operator's
	// source can't smuggle in shell metacharacters.
	srcEsc := postfixSedEscape(source)
	rewriteCmd := fmt.Sprintf(
		"sed -i '/^%s\\s/d' /etc/postfix/virtual_alias_maps 2>/dev/null; "+
			"echo '%s    %s' >> /etc/postfix/virtual_alias_maps",
		srcEsc, source, dst,
	)
	if _, err := agent.RunCommand(ctx, "bash", "-c", rewriteCmd); err != nil {
		return fmt.Errorf("rewrite virtual_alias_maps: %w", err)
	}
	if reload {
		if _, err := agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_alias_maps"); err != nil {
			return fmt.Errorf("postmap: %w", err)
		}
		if _, err := agent.RunCommand(ctx, "systemctl", "reload", "postfix"); err != nil {
			// Log only — postmap success means the routing is
			// already in place; the reload just makes Postfix
			// re-read sooner.
			log.Warn().Err(err).Msg("postfix reload failed after forwarder write")
		}
	}
	return nil
}

// PostfixApplyForwardersFlush runs postmap + systemctl reload once.
// Used at the end of a bulk-upload loop after many
// applyForwarderToPostfix(reload=false) calls so the operator pays
// the postmap + reload cost ONCE rather than per row.
func (s *EmailService) PostfixApplyForwardersFlush(ctx context.Context) error {
	if _, err := agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_alias_maps"); err != nil {
		return fmt.Errorf("postmap: %w", err)
	}
	if _, err := agent.RunCommand(ctx, "systemctl", "reload", "postfix"); err != nil {
		log.Warn().Err(err).Msg("postfix reload failed after bulk forwarder flush")
	}
	return nil
}

// postfixSedEscape escapes the regex metacharacters that legitimately
// appear in an email address (the dot, plus the `+` aliases use), so
// a sed pattern matching `^source\s` matches exactly that source and
// can't accidentally match `source-other@example.com`.
func postfixSedEscape(s string) string {
	const meta = `\.+*?()|[]{}^$/`
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		if strings.ContainsRune(meta, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// RebuildMailboxMaps rewrites /etc/dovecot/users + /etc/postfix/
// virtual_mailbox_maps + /etc/postfix/virtual_mailbox_domains from
// scratch using every mailbox row currently in Mongo, runs postmap +
// reloads Dovecot + Postfix ONCE.
//
// Used by:
//   - Server-transfer recovery (panel-records sync). Mailbox rows
//     land in destination Mongo via syncByDomain, but pre-3.1.47 the
//     destination's dovecot/users + virtual_mailbox_maps + domains
//     were NEVER rebuilt from those rows. The mailboxes appeared in
//     the panel UI on the new server but inbound mail bounced
//     "user unknown in virtual_mailbox_maps" + IMAP login failed
//     because dovecot had no passdb entry. Mirror of v3.1.37's
//     forwarder fix.
//   - The new POST /api/v1/whm/email/mailboxes/rehydrate endpoint
//     (operator UI button + curl recovery path).
//   - The new `bzpanel heal-mailboxes` CLI subcommand for SSH-only
//     recovery when the panel itself isn't reachable.
//
// Tenant-scope-blind on purpose — system-level reconcile, the
// OPERATOR who runs it is implicitly the platform owner. Returns
// the mailbox count that landed in the new files so the caller can
// log it.
//
// Idempotent: running it twice produces the same files. Atomic
// file replace (write to .tmp, mv into place) so a crash mid-run
// doesn't leave Dovecot or Postfix reading a half-written file.
func (s *EmailService) RebuildMailboxMaps(ctx context.Context) (int, error) {
	col := s.db.Collection(database.ColMailboxes)
	cur, err := col.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "email", Value: 1}}))
	if err != nil {
		return 0, fmt.Errorf("list mailboxes: %w", err)
	}
	defer cur.Close(ctx)

	var dovecotLines []string
	var postfixLines []string
	domains := make(map[string]struct{})
	count := 0
	for cur.Next(ctx) {
		var mb models.Mailbox
		if err := cur.Decode(&mb); err != nil {
			continue
		}
		email := strings.ToLower(strings.TrimSpace(mb.Email))
		if email == "" || !strings.Contains(email, "@") {
			continue
		}
		parts := strings.SplitN(email, "@", 2)
		localPart := parts[0]
		domain := strings.ToLower(strings.TrimSpace(parts[1]))
		if domain == "" {
			continue
		}

		// Resolve maildir the same way CreateMailbox does — domain row's
		// User wins, /var/vmail fallback for orphaned domains. This
		// must match CreateMailbox's path layout exactly OR Dovecot
		// authenticates but can't open the maildir (EACCES) and
		// every IMAP fetch returns empty.
		domCol := s.db.Collection(database.ColDomains)
		var dom models.Domain
		var maildir string
		if err := domCol.FindOne(ctx, bson.M{"domain": domain}).Decode(&dom); err == nil && dom.User != "" {
			maildir = fmt.Sprintf("/home/%s/mail/%s/%s", dom.User, domain, localPart)
		} else {
			maildir = fmt.Sprintf("/var/vmail/%s/%s", domain, localPart)
		}

		// Hash field — the source's transferred password hash IS what
		// /etc/dovecot/users needs (passdb passwd-file accepts any
		// scheme prefix doveadm pw produces). If it's blank (very
		// old row, never rotated through CreateMailbox), we skip the
		// dovecot line so the operator can re-set the password from
		// the panel; alternative would be a no-auth account.
		passHash := strings.TrimSpace(mb.Password)
		if passHash != "" {
			dovecotLines = append(dovecotLines,
				fmt.Sprintf("%s:%s:5000:5000::%s::userdb_mail=maildir:%s",
					email, passHash, maildir, maildir))
		}

		// virtual_mailbox_maps: `email    domain/localpart/`. Trailing
		// slash mandatory — Postfix reads it as the maildir path
		// segment.
		postfixLines = append(postfixLines,
			fmt.Sprintf("%s    %s/%s/", email, domain, localPart))

		domains[domain] = struct{}{}
		count++
	}

	// Atomic file replace for /etc/dovecot/users
	dovecotPath := "/etc/dovecot/users"
	if err := s.atomicReplaceFile(ctx, dovecotPath, strings.Join(dovecotLines, "\n")+"\n"); err != nil {
		return 0, fmt.Errorf("write dovecot/users: %w", err)
	}
	if _, err := agent.RunCommand(ctx, "chown", "dovecot:dovecot", dovecotPath); err != nil {
		log.Warn().Err(err).Msg("rebuild mailbox maps: chown dovecot/users failed (non-fatal)")
	}
	if _, err := agent.RunCommand(ctx, "chmod", "0640", dovecotPath); err != nil {
		log.Warn().Err(err).Msg("rebuild mailbox maps: chmod dovecot/users failed (non-fatal)")
	}

	// Atomic file replace for /etc/postfix/virtual_mailbox_maps
	postfixPath := "/etc/postfix/virtual_mailbox_maps"
	if err := s.atomicReplaceFile(ctx, postfixPath, strings.Join(postfixLines, "\n")+"\n"); err != nil {
		return 0, fmt.Errorf("write virtual_mailbox_maps: %w", err)
	}

	// Atomic file replace for /etc/postfix/virtual_mailbox_domains —
	// one `<domain> OK` line per unique domain (sorted for diff-
	// friendliness so the file doesn't churn between runs).
	domainsPath := "/etc/postfix/virtual_mailbox_domains"
	domainList := make([]string, 0, len(domains))
	for d := range domains {
		domainList = append(domainList, d)
	}
	// Stable sort so two consecutive runs produce byte-identical files.
	for i := 0; i < len(domainList); i++ {
		for j := i + 1; j < len(domainList); j++ {
			if domainList[j] < domainList[i] {
				domainList[i], domainList[j] = domainList[j], domainList[i]
			}
		}
	}
	var domainsBody strings.Builder
	for _, d := range domainList {
		domainsBody.WriteString(d)
		domainsBody.WriteString(" OK\n")
	}
	if err := s.atomicReplaceFile(ctx, domainsPath, domainsBody.String()); err != nil {
		return 0, fmt.Errorf("write virtual_mailbox_domains: %w", err)
	}

	// Single postmap + reload at the end (cheap; Postfix re-reads on
	// next mail anyway, but reloading immediately avoids the operator
	// having to wait for the next inbound message to verify).
	if _, err := agent.RunCommand(ctx, "postmap", postfixPath); err != nil {
		return 0, fmt.Errorf("postmap virtual_mailbox_maps: %w", err)
	}
	if _, err := agent.RunCommand(ctx, "postmap", domainsPath); err != nil {
		return 0, fmt.Errorf("postmap virtual_mailbox_domains: %w", err)
	}
	if _, err := agent.RunCommand(ctx, "systemctl", "reload", "postfix"); err != nil {
		log.Warn().Err(err).Msg("rebuild mailbox maps: postfix reload failed — maps in place, will pick up on next mail")
	}
	if _, err := agent.RunCommand(ctx, "systemctl", "reload", "dovecot"); err != nil {
		// Dovecot's passwd-file passdb is read on every auth so a
		// reload is best-effort — failing here doesn't block IMAP
		// logins from picking up the new entries.
		log.Warn().Err(err).Msg("rebuild mailbox maps: dovecot reload failed — passdb is per-auth, entries still active")
	}

	// v3.1.62 — chown migrated maildirs to vmail:vmail. Server-to-
	// server file transfer carries maildirs across as a tarball
	// pinned to the SOURCE linux user's uid:gid (cpuser:cpuser or
	// vmail:vmail-with-different-uid). On the destination box uids
	// rarely line up so the files land owned by either the destination
	// system user or a numeric-uid orphan. Dovecot LMTP runs as
	// vmail, can't write to <maildir>/tmp/, and every inbound
	// delivery aborts: "open(...tmp/...) failed: Permission denied"
	// → Postfix defers with 451 → operator reports "email not
	// receiving". Fresh mailboxes were fine because CreateMailbox
	// chmod/chowns at create time; only migrated rows were broken.
	//
	// Idempotent. One `chown -R vmail:vmail` per unique linux user
	// rather than per mailbox so a 49-mailbox install runs ~15 chowns
	// instead of 49. Errors are logged but don't abort the rebuild —
	// the file maps are already written and IMAP login works even if
	// a chown blip leaves one tree wrong.
	if rep := s.repairMaildirOwnership(ctx); rep.failures > 0 {
		log.Warn().Int("trees_fixed", rep.healed).Int("failures", rep.failures).
			Msg("rebuild mailbox maps: maildir ownership repair completed with some failures")
	} else if rep.healed > 0 {
		log.Info().Int("trees_fixed", rep.healed).
			Msg("rebuild mailbox maps: chowned migrated maildir trees to vmail:vmail")
	}

	log.Info().
		Int("mailbox_count", count).
		Int("domain_count", len(domains)).
		Msg("rebuilt /etc/dovecot/users + virtual_mailbox_maps + virtual_mailbox_domains from Mongo")
	return count, nil
}

// maildirOwnershipReport summarises one repair pass. Surfaced via log
// fields rather than the function return because the caller (transfer
// rehydrate, CLI heal-mail-perms, ad-hoc reconcile) doesn't need
// per-tree detail to act — the count is enough for the operator log.
type maildirOwnershipReport struct {
	healed   int // trees that completed `chown -R vmail:vmail` cleanly
	failures int // trees where chown returned non-zero (logged at warn)
}

// repairMaildirOwnership walks every Mongo domain row and, for each
// distinct linux user that owns at least one domain, runs
// `chown -R vmail:vmail /home/<user>/mail/` plus `chgrp vmail
// /home/<user>` + `chmod g+x /home/<user>` so Dovecot/Postfix can
// traverse into the maildir tree. Skips users with no /home/<u>/mail
// directory (no mailboxes provisioned) and silently noops when the
// tree is already owned by vmail (chown is a fast metadata-only walk
// when nothing changes).
//
// Pulls the user list from the domains collection (one row per
// hosting account) rather than the mailboxes collection, so even a
// domain whose mailboxes haven't been provisioned yet but whose
// maildir parent was created by a previous mailbox add gets fixed.
// De-duplicates with a set to avoid running chown twice for the same
// /home/<u>/mail/ tree even if the user owns many domains.
func (s *EmailService) repairMaildirOwnership(ctx context.Context) maildirOwnershipReport {
	domCol := s.db.Collection(database.ColDomains)
	cur, err := domCol.Find(ctx, bson.M{}, options.Find().SetProjection(bson.M{"user": 1}))
	if err != nil {
		log.Warn().Err(err).Msg("repair maildir ownership: list domains failed")
		return maildirOwnershipReport{}
	}
	defer cur.Close(ctx)

	seen := make(map[string]struct{})
	for cur.Next(ctx) {
		var d struct {
			User string `bson:"user"`
		}
		if cur.Decode(&d) != nil {
			continue
		}
		u := strings.TrimSpace(d.User)
		if u == "" {
			continue
		}
		seen[u] = struct{}{}
	}
	if len(seen) == 0 {
		return maildirOwnershipReport{}
	}

	var rep maildirOwnershipReport
	for u := range seen {
		// Existence + bash single-arg quoting handled inline; the
		// chgrp+chmod on the parent home is a no-op when already set
		// (Linux flags-only, not metadata-replacing).
		homeDir := "/home/" + u
		mailDir := homeDir + "/mail"
		// `test -d` short-circuits before chown so the warn log only
		// fires on a real chown failure, not on the (common) "this
		// user never set up mail" case.
		cmd := fmt.Sprintf(
			"if [ -d %q ]; then chgrp vmail %q && chmod g+x %q && chown -R vmail:vmail %q; else exit 99; fi",
			mailDir, homeDir, homeDir, mailDir)
		r, err := agent.RunCommand(ctx, "bash", "-c", cmd)
		if err != nil {
			log.Warn().Err(err).Str("user", u).Msg("repair maildir ownership: chown failed")
			rep.failures++
			continue
		}
		if r != nil && r.ExitCode == 99 {
			// no /home/<u>/mail — nothing to fix, not a failure
			continue
		}
		rep.healed++
	}
	return rep
}

// atomicReplaceFile writes `body` to `path` via a tmp file + mv so a
// reader (Dovecot/Postfix) never sees a half-written file. The tmp
// file is on the SAME filesystem as the target so `mv` is atomic on
// POSIX (rename(2) within one filesystem is atomic; cross-fs would
// fall back to copy + delete and break the atomicity guarantee).
func (s *EmailService) atomicReplaceFile(ctx context.Context, path, body string) error {
	tmp := path + ".bzpanel.tmp"
	// Write via a heredoc so any single quotes / dollar signs in the
	// body are emitted literally (no bash expansion). The closing
	// 'BZPANEL_EOF' MUST be at column 0 — bash's heredoc rule.
	cmd := fmt.Sprintf("cat > %s <<'BZPANEL_EOF'\n%sBZPANEL_EOF", tmp, body)
	if _, err := agent.RunCommand(ctx, "bash", "-c", cmd); err != nil {
		return fmt.Errorf("write tmp file %s: %w", tmp, err)
	}
	if _, err := agent.RunCommand(ctx, "mv", tmp, path); err != nil {
		return fmt.Errorf("install file %s: %w", path, err)
	}
	return nil
}

// RebuildVirtualAliasMaps rewrites /etc/postfix/virtual_alias_maps from
// scratch using every forwarder row in Mongo. Used by:
//
//   - Server transfer recovery (forwarders land in destination Mongo
//     via the panel-records sync, but Postfix's own map file isn't
//     touched — this rebuilds it so mail actually routes).
//   - The new `bzpanel heal-forwarders` (alias `repair-forwarders`)
//     CLI subcommand for operators who notice forward routing is
//     broken on a long-lived install.
//   - The bulk-upload + bulk-delete flows (called once at the end
//     instead of per-row reloads).
//
// Tenant-scope-blind on purpose — this is a system-level reconcile;
// the OPERATOR who runs it is implicitly the platform owner (transfer
// recovery + bzpanel are platform-owner-only). Returns the count of
// forwarder rows that landed in the new map so the caller can log it.
func (s *EmailService) RebuildVirtualAliasMaps(ctx context.Context) (int, error) {
	col := s.db.Collection(database.ColForwarders)
	cur, err := col.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "source", Value: 1}}))
	if err != nil {
		return 0, fmt.Errorf("list forwarders: %w", err)
	}
	defer cur.Close(ctx)

	var lines []string
	count := 0
	for cur.Next(ctx) {
		var fwd models.EmailForwarder
		if err := cur.Decode(&fwd); err != nil {
			continue
		}
		src := strings.TrimSpace(fwd.Source)
		if src == "" || len(fwd.Destinations) == 0 {
			continue
		}
		// Honour keep_copy when rebuilding from Mongo. Pre-3.1.44
		// the rebuild ignored the flag and every transferred /
		// healed forwarder lost its "keep a copy" behaviour even
		// though the Mongo field said true. Use the same
		// composeForwarderDestinations helper as the live write
		// path so the heal output matches what a fresh
		// CreateForwarder would emit.
		finalDests := composeForwarderDestinations(src, fwd.Destinations, fwd.KeepCopy)
		if len(finalDests) == 0 {
			continue
		}
		dst := strings.Join(finalDests, ", ")
		lines = append(lines, fmt.Sprintf("%s    %s", src, dst))
		count++
	}

	// Atomic file replace: write to a temp file then mv into place.
	// Avoids leaving Postfix mid-rewrite if the agent dies mid-loop.
	tmpPath := "/etc/postfix/virtual_alias_maps.bzpanel.tmp"
	body := strings.Join(lines, "\n") + "\n"
	if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"cat > %s <<'BZPANEL_EOF_FORWARDERS'\n%sBZPANEL_EOF_FORWARDERS",
		tmpPath, body,
	)); err != nil {
		return 0, fmt.Errorf("write tmp map: %w", err)
	}
	if _, err := agent.RunCommand(ctx, "mv", tmpPath, "/etc/postfix/virtual_alias_maps"); err != nil {
		return 0, fmt.Errorf("install map: %w", err)
	}
	if _, err := agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_alias_maps"); err != nil {
		return 0, fmt.Errorf("postmap: %w", err)
	}
	if _, err := agent.RunCommand(ctx, "systemctl", "reload", "postfix"); err != nil {
		log.Warn().Err(err).Msg("postfix reload failed after rebuild — map is in place, will pick up on next mail")
	}
	log.Info().Int("forwarder_count", count).Msg("rebuilt /etc/postfix/virtual_alias_maps from Mongo")
	return count, nil
}

// DeleteForwarderInDomain is DeleteForwarder with a domain guard: it refuses to
// delete a forwarder whose source domain isn't `domain`. The programmatic API
// (token-authenticated /external/email/:domain/forwarders/:id) uses it because
// the forwarder :id is otherwise a GLOBAL handle — without this check a token
// authorized for domain A could delete a forwarder that belongs to another
// tenant's domain B just by knowing its id. Returns the same "not found" for a
// missing id and a wrong-domain id so the caller can't distinguish the two.
func (s *EmailService) DeleteForwarderInDomain(ctx context.Context, id, domain string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid forwarder ID")
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	var fwd models.EmailForwarder
	if err := s.db.Collection(database.ColForwarders).FindOne(ctx, bson.M{"_id": oid}).Decode(&fwd); err != nil {
		return fmt.Errorf("forwarder not found")
	}
	if strings.ToLower(strings.TrimSpace(fwd.Domain)) != domain {
		return fmt.Errorf("forwarder not found")
	}
	return s.DeleteForwarder(ctx, id)
}

func (s *EmailService) DeleteForwarder(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid forwarder ID")
	}

	col := s.db.Collection(database.ColForwarders)
	var fwd models.EmailForwarder
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&fwd); err != nil {
		return fmt.Errorf("forwarder not found")
	}

	// Remove from Postfix virtual alias maps
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s /d' /etc/postfix/virtual_alias_maps", strings.ReplaceAll(fwd.Source, ".", "\\.")))
	agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_alias_maps")
	agent.RunCommand(ctx, "systemctl", "reload", "postfix")

	_, err = col.DeleteOne(ctx, bson.M{"_id": oid})
	return err
}

func (s *EmailService) UpdateSpamSettings(ctx context.Context, settings *models.SpamSettings) error {
	// Domain is interpolated into a root `echo '...' > /etc/spamassassin/<domain>.cf`
	// and the file path — reject anything that isn't a strictly shell-safe DNS name
	// so a crafted :domain can't break out and run commands as root (security audit §8).
	if !validator.IsSafeDNSName(settings.Domain) {
		return fmt.Errorf("invalid domain %q", settings.Domain)
	}
	// whitelist_from / blacklist_from accept glob patterns (e.g. *@example.com), so
	// allow * and ? but reject every shell metacharacter (quotes, ; | & $ ` () ws).
	safeGlob := func(p string) bool {
		if p == "" || len(p) > 320 {
			return false
		}
		for _, r := range p {
			switch {
			case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			case r == '@' || r == '.' || r == '_' || r == '-' || r == '+' || r == '%' || r == '*' || r == '?':
			default:
				return false
			}
		}
		return true
	}
	for _, w := range settings.Whitelist {
		if !safeGlob(w) {
			return fmt.Errorf("invalid whitelist entry %q", w)
		}
	}
	for _, b := range settings.Blacklist {
		if !safeGlob(b) {
			return fmt.Errorf("invalid blacklist entry %q", b)
		}
	}
	// Write SpamAssassin local config for the domain
	configPath := fmt.Sprintf("/etc/spamassassin/%s.cf", settings.Domain)
	var lines []string
	lines = append(lines, fmt.Sprintf("required_score %.1f", settings.SpamThreshold))
	if settings.SpamAction == "delete" {
		lines = append(lines, "report_safe 2")
	} else {
		lines = append(lines, "report_safe 1")
	}
	for _, w := range settings.Whitelist {
		lines = append(lines, fmt.Sprintf("whitelist_from %s", w))
	}
	for _, b := range settings.Blacklist {
		lines = append(lines, fmt.Sprintf("blacklist_from %s", b))
	}

	content := strings.Join(lines, "\n") + "\n"
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo '%s' > %s", content, configPath))
	// The SpamAssassin daemon unit on Ubuntu 24.04 is spamd.service —
	// there is no spamassassin.service, so the previous reload silently
	// failed and saved spam settings never took effect.
	agent.RunCommand(ctx, "systemctl", "reload", "spamd")

	return nil
}

// syncPostfixChrootCmd populates /var/spool/postfix/etc/ from the host's
// /etc/ so smtp(8)'s chrooted resolver works. Without this, postfix
// defers every outbound delivery with "Name service error for
// name=<host> type=MX: Host not found, try again" whenever the host's
// resolv.conf is a systemd-resolved stub (127.0.0.53) — the stub socket
// isn't reachable from inside the chroot.
//
// Also rewrites a stub-resolver file to real upstream nameservers so
// DNS works even before systemd-resolved is set up. Idempotent.
func syncPostfixChrootCmd(ctx context.Context) (*agent.CommandResult, error) {
	return agent.RunCommand(ctx, "bash", "-c", `
install -d -m 0755 /var/spool/postfix/etc
cp -fL /etc/resolv.conf    /var/spool/postfix/etc/resolv.conf 2>/dev/null
cp -fL /etc/hosts          /var/spool/postfix/etc/hosts       2>/dev/null
cp -fL /etc/nsswitch.conf  /var/spool/postfix/etc/nsswitch.conf 2>/dev/null
cp -fL /etc/host.conf      /var/spool/postfix/etc/host.conf    2>/dev/null || true
cp -fL /etc/services       /var/spool/postfix/etc/services     2>/dev/null
if grep -q '127.0.0.53' /var/spool/postfix/etc/resolv.conf 2>/dev/null; then
    cat > /var/spool/postfix/etc/resolv.conf <<'RESOLVEOF'
nameserver 8.8.8.8
nameserver 1.1.1.1
nameserver 8.8.4.4
options timeout:3 attempts:2
RESOLVEOF
fi
chmod 0644 /var/spool/postfix/etc/*.conf /var/spool/postfix/etc/services /var/spool/postfix/etc/hosts 2>/dev/null || true
`)
}

// SyncPostfixChroot is the exported hook other services (transfer,
// reconcile, health checks) call to repair the chroot's /etc/ without
// going through the full EnsureEmailStack path. Safe to call any time;
// issues a postfix reload after the sync so resolver changes take
// effect without dropping live connections.
func (s *EmailService) SyncPostfixChroot(ctx context.Context) error {
	if _, err := syncPostfixChrootCmd(ctx); err != nil {
		return err
	}
	agent.RunCommand(ctx, "systemctl", "reload", "postfix")
	return nil
}

// EnsureMailHook installs the Dovecot Sieve pipeline that fires
// `email.message.received` webhooks for every delivered message.
// Idempotent — safe to call from CreateMailbox (best-effort, never
// blocks the create) and from ReconcileConfig.
//
// callbackURL is the panel's mail-incoming endpoint. When empty, the
// installer defaults to http://127.0.0.1:8080/api/v1/agent/mail/incoming
// which works on a stock single-host install.
func (s *EmailService) EnsureMailHook(ctx context.Context, callbackURL string) (string, error) {
	return agent.EnsureMailHookInstalled(ctx, callbackURL)
}

// EnsureDKIMForDomain makes sure OpenDKIM can sign mail for domain.
// Idempotent — safe to call on every CreateMailbox. Lookup order:
//
//  1. If domain already appears in /etc/opendkim/signing.table (left
//     column), do nothing — it's wired up.
//  2. Else if any proper suffix of domain has a signing.table entry
//     (e.g. domain="d1.example.com", parent="example.com"), add a
//     subdomain row that points at the parent's selector. This reuses
//     the parent's key + DNS TXT instead of minting new ones.
//  3. Else generate a fresh selector=mail key under
//     /etc/opendkim/keys/<domain>/ and register it.
//
// Also keeps trusted.hosts in sync so OpenDKIM treats the domain as
// one of its own (required for signing to happen at all).
func (s *EmailService) EnsureDKIMForDomain(ctx context.Context, domain string) {
	domain = strings.TrimSpace(strings.ToLower(domain))
	if domain == "" {
		return
	}
	if r, err := agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("grep -qE '^\\*@%s( |\t)' /etc/opendkim/signing.table 2>/dev/null && echo yes || echo no",
			strings.ReplaceAll(domain, ".", "\\."))); err == nil && strings.TrimSpace(r.Output) == "yes" {
		return
	}

	// Walk domain labels left-to-right looking for a parent that's
	// already registered. "sub.foo.example.com" → try foo.example.com,
	// then example.com.
	parts := strings.Split(domain, ".")
	for i := 1; i < len(parts)-1; i++ {
		parent := strings.Join(parts[i:], ".")
		probe := fmt.Sprintf("grep -qE '^\\*@%s( |\t)' /etc/opendkim/signing.table 2>/dev/null && echo yes || echo no",
			strings.ReplaceAll(parent, ".", "\\."))
		if r, err := agent.RunCommand(ctx, "bash", "-c", probe); err == nil && strings.TrimSpace(r.Output) == "yes" {
			agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
				"echo '*@%s mail._domainkey.%s' >> /etc/opendkim/signing.table", domain, parent))
			agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
				"grep -qxF '%s' /etc/opendkim/trusted.hosts || echo '%s' >> /etc/opendkim/trusted.hosts",
				domain, domain))
			agent.RunCommand(ctx, "systemctl", "restart", "opendkim")
			return
		}
	}

	// No parent — mint a fresh key.
	keyDir := fmt.Sprintf("/etc/opendkim/keys/%s", domain)
	agent.RunCommand(ctx, "mkdir", "-p", keyDir)
	agent.RunCommand(ctx, "opendkim-genkey", "-s", "mail", "-d", domain, "-D", keyDir)
	agent.RunCommand(ctx, "chown", "-R", "opendkim:opendkim", keyDir)
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"echo '*@%s mail._domainkey.%s' >> /etc/opendkim/signing.table", domain, domain))
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"echo 'mail._domainkey.%s %s:mail:%s/mail.private' >> /etc/opendkim/key.table",
		domain, domain, keyDir))
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"grep -qxF '%s' /etc/opendkim/trusted.hosts || echo '%s' >> /etc/opendkim/trusted.hosts",
		domain, domain))
	agent.RunCommand(ctx, "systemctl", "restart", "opendkim")
}

func (s *EmailService) SetupDKIM(ctx context.Context, domain string) (map[string]interface{}, error) {
	keyDir := fmt.Sprintf("/etc/opendkim/keys/%s", domain)
	agent.RunCommand(ctx, "mkdir", "-p", keyDir)

	// Generate DKIM key
	_, err := agent.RunCommand(ctx, "opendkim-genkey", "-s", "mail", "-d", domain, "-D", keyDir)
	if err != nil {
		return nil, fmt.Errorf("failed to generate DKIM key: %w", err)
	}

	// Add to signing table
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo '*@%s mail._domainkey.%s' >> /etc/opendkim/signing.table", domain, domain))

	// Add to key table
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo 'mail._domainkey.%s %s:mail:%s/mail.private' >> /etc/opendkim/key.table", domain, domain, keyDir))

	// Add to trusted hosts
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/opendkim/trusted.hosts || echo '%s' >> /etc/opendkim/trusted.hosts", domain, domain))

	// Reload OpenDKIM
	agent.RunCommand(ctx, "systemctl", "reload", "opendkim")

	// Read the public key to return as DNS record
	pubResult, _ := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("cat %s/mail.txt", keyDir))
	dnsRecord := ""
	if pubResult != nil {
		dnsRecord = strings.TrimSpace(pubResult.Output)
	}

	return map[string]interface{}{
		"domain":     domain,
		"selector":   "mail",
		"dns_record": dnsRecord,
		"record_type": "TXT",
		"record_name": fmt.Sprintf("mail._domainkey.%s", domain),
	}, nil
}

// parseDKIMPublicKey extracts the DKIM value from opendkim-genkey output.
func parseDKIMPublicKey(txt string) string {
	var parts []string
	inQuote := false
	current := ""
	for _, c := range txt {
		if c == '"' {
			if inQuote {
				parts = append(parts, current)
				current = ""
			}
			inQuote = !inQuote
			continue
		}
		if inQuote {
			current += string(c)
		}
	}
	return strings.Join(parts, "")
}

// GenerateWebmailToken creates a signed SSO token for Roundcube auto-login.
//
// Auto-heal path: before returning the token, we rewrite this user's
// line in /etc/dovecot/users with a fresh SHA512-CRYPT hash of the
// decrypted plaintext. Reasoning — the SSO token carries the
// PLAINTEXT password (so Roundcube can do a real IMAP login against
// Dovecot), and Dovecot has its OWN copy of the password hash. If
// the two ever drift (mailbox row imported from a transfer with a
// stale Dovecot file, manual edit of /etc/dovecot/users, partial
// failure inside UpdateMailbox), every webmail-SSO click hits the
// "Login failed for X" wall on sso.php with no clear cause. The
// re-sync is cheap (one doveadm pw + one bash sed/echo per click)
// and treats the encrypted_pass blob as the source of truth — which
// it is, since the panel UI's "Edit password" path goes through
// UpdateMailbox which writes BOTH fields atomically.
//
// Failure of the heal path is logged but NOT fatal: if /etc/dovecot/
// users isn't writable, we still return the token and let the SSO
// proceed — the user gets the same "Login failed" they'd have got
// without the heal, but at least the click flow is unchanged.
func (s *EmailService) GenerateWebmailToken(ctx context.Context, email string) (string, error) {
	// Case-insensitive lookup. Pre-3.1.29 mailboxes were inserted with
	// req.Email's original casing, so a UI / API consumer that queries
	// in a different case would hit "mailbox not found" even though the
	// row existed. findMailboxByEmail tries exact-match first (index
	// hit) and only regex-folds on miss.
	email = strings.TrimSpace(email)
	if email == "" {
		return "", fmt.Errorf("email is required")
	}
	mailboxPtr, err := s.findMailboxByEmail(ctx, email)
	if err != nil {
		return "", fmt.Errorf("mailbox not found: %s", email)
	}
	mailbox := *mailboxPtr
	// Use the canonical email from the row from here on, so /etc/dovecot/
	// users heal + roundcube SSO key off the EXACT case the mailbox was
	// stored under.
	email = mailbox.Email

	// Decrypt the password
	if mailbox.EncryptedPass == "" || s.jwtSecret == "" {
		return "", fmt.Errorf("webmail SSO not available for this mailbox — re-set its password from the Edit modal to enable")
	}
	plainPass, err := decryptPassword(mailbox.EncryptedPass, s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt credentials — JWT_SECRET may have rotated since this mailbox was created. Re-set the password from the Edit modal")
	}

	// Pre-flight: confirm MariaDB is reachable. Roundcube needs the
	// `roundcube.users` table to insert the post-auth session row;
	// when the DB is down (manual stop, OOM kill, package upgrade
	// hold-up), Roundcube's login() returns false EVEN THOUGH IMAP
	// auth succeeded — the operator lands on /webmail/sso.php with
	// "Login failed for X" and zero clue what's broken. Catching it
	// here turns 30 minutes of journalctl spelunking into a single
	// actionable panel toast.
	//
	// Cheap check: a Unix-socket stat on /run/mysqld/mysqld.sock.
	// We don't open a real MySQL handshake (would require credentials
	// + drag in a MariaDB driver dep) — just probe the listening
	// socket. False-negative scenarios (socket present, DB hung) are
	// rare enough that the next mailbox click will surface the
	// actual error if they happen.
	if _, statErr := agent.RunCommand(ctx, "test", "-S", "/run/mysqld/mysqld.sock"); statErr != nil {
		return "", fmt.Errorf("webmail database (MariaDB) is unavailable — Roundcube cannot create the user session even though IMAP auth would succeed. Run `systemctl start mariadb` on the server, then retry")
	}

	// AUTO-HEAL: re-sync /etc/dovecot/users with a fresh hash of the
	// decrypted plaintext, then run `doveadm auth test` to verify
	// Dovecot now accepts the credentials. If verification fails,
	// return a clear actionable error INSTEAD of issuing a token
	// that's guaranteed to land on sso.php's "Login failed for X"
	// page — the operator gets a useful message in the panel toast
	// rather than chasing a generic webmail error.
	if syncErr := s.syncDovecotPasswordLine(ctx, email, plainPass); syncErr != nil {
		fmt.Fprintf(os.Stderr, "[webmail-sso] dovecot sync failed for %s: %v\n", email, syncErr)
		return "", fmt.Errorf("could not establish webmail credentials: %v", syncErr)
	}

	// Read the HMAC secret from the server
	result, err := agent.RunCommand(ctx, "cat", "/etc/roundcube/sso_hmac_secret")
	if err != nil || result == nil {
		return "", fmt.Errorf("SSO not configured on server")
	}
	hmacSecret := strings.TrimSpace(result.Output)
	if hmacSecret == "" {
		return "", fmt.Errorf("SSO secret is empty")
	}

	// Generate signed token with password
	ts := fmt.Sprintf("%d", time.Now().Unix())
	message := email + "|" + ts
	mac := hmac.New(sha256.New, []byte(hmacSecret))
	mac.Write([]byte(message))
	sig := hex.EncodeToString(mac.Sum(nil))

	payload := map[string]string{
		"email": email,
		"ts":    ts,
		"sig":   sig,
		"pass":  plainPass,
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to create token: %w", err)
	}

	// Base64url encode
	token := base64.RawURLEncoding.EncodeToString(jsonBytes)
	return token, nil
}

// syncDovecotPasswordLine rewrites /etc/dovecot/users for one email
// so the line's password hash is a fresh SHA512-CRYPT of plainPass.
// Idempotent — running it twice with the same plaintext produces the
// same effective auth state (the salt differs each time, but
// SHA512-CRYPT verifies any salt+hash pair against the right
// plaintext, so Dovecot accepts both). Safe to call from the hot
// path of every webmail click.
//
// Strategy: unconditional DELETE-then-APPEND. We tried in-place awk
// rewriting in v3.1.19 but that has a silent-fail mode — when the
// existing line has weird whitespace or pre-dates the per-user
// write entirely, awk's $1==E never matches and the file passes
// through unchanged. Now we always delete any matching line and
// append a fresh one with the new hash + a known-good shape. This
// is the same pattern CreateMailbox uses, and it can't drift.
//
// After the rewrite we verify via `doveadm auth test`. If the test
// still fails, we return that error so the caller (GenerateWebmail-
// Token) can surface an actionable message instead of issuing a
// token we know will fail at the IMAP layer.
//
// Preserves the maildir path by resolving it via getMaildirPath
// (which reads the domain row's User to pick /var/vmail/<domain>/<u>
// or /home/<u>/mail/<domain>/<u> depending on the install layout).
// If getMaildirPath returns empty (no domain row found), the rewrite
// is skipped and the caller logs+continues.
func (s *EmailService) syncDovecotPasswordLine(ctx context.Context, email, plainPass string) error {
	if email == "" || plainPass == "" {
		return fmt.Errorf("email + password required")
	}
	passResult, err := agent.RunCommand(ctx, "doveadm", "pw", "-s", "SHA512-CRYPT", "-p", plainPass)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	newHash := strings.TrimSpace(passResult.Output)
	if newHash == "" {
		return fmt.Errorf("doveadm pw returned empty hash")
	}

	maildir := s.getMaildirPath(ctx, email)
	if maildir == "" {
		// Without a resolvable maildir we can't write a complete
		// line. Tell the caller so it surfaces an actionable error.
		return fmt.Errorf("could not resolve maildir path for %s — domain row may be missing", email)
	}

	// Delete any existing line for this email, then append a fresh
	// one. The sed pattern uses escaped dots so a typo'd "." can't
	// match a different mailbox by accident. Single-quoted echo
	// preserves the SHA512-CRYPT hash's $-signs literally — bash's
	// inner quote rules don't expand variables inside single quotes.
	emailRegEsc := strings.ReplaceAll(email, ".", `\.`)
	emailRegEsc = strings.ReplaceAll(emailRegEsc, "/", `\/`)
	rewriteCmd := fmt.Sprintf(
		`sed -i '/^%s:/d' /etc/dovecot/users 2>/dev/null; echo '%s:%s:5000:5000::%s::userdb_mail=maildir:%s' >> /etc/dovecot/users && chown dovecot:dovecot /etc/dovecot/users 2>/dev/null; chmod 0640 /etc/dovecot/users 2>/dev/null`,
		emailRegEsc, email, newHash, maildir, maildir)
	if _, err := agent.RunCommand(ctx, "bash", "-c", rewriteCmd); err != nil {
		return fmt.Errorf("rewrite /etc/dovecot/users: %w", err)
	}

	// Verify Dovecot now accepts the credentials. `doveadm auth test`
	// runs the full passdb lookup + hash verification + userdb fetch
	// against the live config — same path Roundcube's IMAP login
	// will exercise next. Exit 0 = success; any non-zero is treated
	// as failure even though the stderr typically describes the
	// reason ("Password mismatch", "user not found", etc.).
	if _, verr := agent.RunCommand(ctx, "doveadm", "auth", "test", email, plainPass); verr != nil {
		return fmt.Errorf("doveadm auth test failed after sync: %w (the encrypted_pass blob may decrypt to a different plaintext than what was last set; reset the password from the Edit modal)", verr)
	}
	return nil
}

// SendTest submits a test message to localhost:587 authenticating as
// `mailboxID` itself. Surfaces the real SMTP error — auth failures,
// missing sender domain, relay rejections — directly to the operator
// so "email not sending" becomes a diagnosable event instead of a
// silent defer/bounce in /var/log/mail.log.
//
// The plaintext password is only available when the mailbox was
// created with webmail SSO enabled (jwtSecret set + EncryptedPass
// populated). For legacy rows without EncryptedPass we return a clear
// error explaining how to fix it (re-create the mailbox or update
// its password) — we do NOT guess the password or skip the AUTH.
func (s *EmailService) SendTest(ctx context.Context, mailboxID, to string) (string, error) {
	to = strings.TrimSpace(strings.ToLower(to))
	if to == "" || !strings.Contains(to, "@") {
		return "", fmt.Errorf("valid 'to' email is required")
	}
	mailbox, err := s.GetMailbox(ctx, mailboxID)
	if err != nil {
		return "", fmt.Errorf("mailbox not found")
	}
	if mailbox.EncryptedPass == "" || s.jwtSecret == "" {
		return "", fmt.Errorf("test send unavailable — this mailbox was created without stored credentials; reset its password from the UI and try again")
	}
	plainPass, err := decryptPassword(mailbox.EncryptedPass, s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("could not decrypt stored credentials (server secret may have rotated)")
	}

	// Use swaks if available — it emits a tcpdump-like trace that is
	// far easier to read than a bare SMTP error. Falls back to Go's
	// net/smtp otherwise so we don't depend on swaks being installed.
	//
	// We shell out to localhost:587 with PLAIN auth. The server
	// (Postfix smtpd) is behind STARTTLS; swaks handles TLS
	// auto-negotiation. If auth fails, swaks prints the full exchange
	// including the 535 5.7.8 response line, which is what the
	// operator actually needs to debug.
	subject := "Betazen Server Panel test email"
	body := fmt.Sprintf("This is a test message from %s sent via Betazen Server Panel's email diagnostic.\n\nIf you are reading this, SMTP submission + AUTH are working correctly for %s.\n", mailbox.Email, mailbox.Email)

	if _, err := agent.RunCommand(ctx, "bash", "-c", "command -v swaks >/dev/null 2>&1"); err == nil {
		cmdStr := fmt.Sprintf(
			"swaks --server localhost:587 --tls --auth LOGIN --auth-user %s --auth-password %s --from %s --to %s --header 'Subject: %s' --body %s",
			shellQuoteLocal(mailbox.Email),
			shellQuoteLocal(plainPass),
			shellQuoteLocal(mailbox.Email),
			shellQuoteLocal(to),
			shellQuoteLocal(subject),
			shellQuoteLocal(body),
		)
		res, runErr := agent.RunCommand(ctx, "bash", "-c", cmdStr)
		trace := ""
		if res != nil {
			trace = res.Output + res.Error
		}
		if runErr != nil {
			return trace, fmt.Errorf("swaks reported SMTP failure: %w", runErr)
		}
		return trace, nil
	}

	// Fallback: sendmail via the submission port using a heredoc. If
	// auth or delivery fails, sendmail exits non-zero and the error
	// text is surfaced. Less informative than swaks but available on
	// every Postfix install.
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s", mailbox.Email, to, subject, body)
	pipeCmd := fmt.Sprintf("printf %s | sendmail -f %s %s",
		shellQuoteLocal(msg), shellQuoteLocal(mailbox.Email), shellQuoteLocal(to))
	res, runErr := agent.RunCommand(ctx, "bash", "-c", pipeCmd)
	trace := ""
	if res != nil {
		trace = res.Output + res.Error
	}
	if runErr != nil {
		return trace, fmt.Errorf("sendmail failed: %w", runErr)
	}
	// sendmail is fire-and-forget at MTA level; a 0 exit just means it
	// was queued. Point the operator at the mail log for the actual
	// delivery outcome.
	return "Message queued via sendmail. Check /var/log/mail.log for delivery confirmation.\n" + trace, nil
}

// shellQuoteLocal wraps s in POSIX single-quotes, escaping embedded
// quotes with '\''. Local to email_service so we don't need to export
// the same helper from the agent package.
func shellQuoteLocal(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ReconcileConfig is a one-shot fix for VPSes provisioned with the
// earlier (sed-based, fragile) Dovecot/Postfix setup. It writes the
// canonical /etc/dovecot/conf.d/99-panel.conf override file, applies
// the Postfix SASL directives, repoints Postfix's virtual map
// directives at the file names EmailService actually writes to, and
// restarts the affected services. Safe to run on an already-working
// server — the operations are all idempotent.
func (s *EmailService) ReconcileConfig(ctx context.Context) (string, error) {
	var log strings.Builder
	step := func(label string, out *agent.CommandResult, err error) {
		fmt.Fprintf(&log, "• %s\n", label)
		if out != nil && out.Output != "" {
			fmt.Fprintf(&log, "  %s\n", strings.TrimSpace(out.Output))
		}
		if out != nil && out.Error != "" {
			fmt.Fprintf(&log, "  stderr: %s\n", strings.TrimSpace(out.Error))
		}
		if err != nil {
			fmt.Fprintf(&log, "  ERROR: %v\n", err)
		}
	}

	// Write the override file (same content the installer ships).
	const doveOverride = `# Managed by Betazen Server Panel — regenerated by /email/reconcile
protocols = imap pop3 lmtp

passdb {
  driver = passwd-file
  args = scheme=SHA512-CRYPT username_format=%u /etc/dovecot/users
}
userdb {
  driver = passwd-file
  args = username_format=%u /etc/dovecot/users
  default_fields = uid=5000 gid=5000 home=/var/mail/vhosts/%d/%n
}

mail_location = maildir:~/Maildir
mail_privileged_group = vmail

disable_plaintext_auth = no
auth_mechanisms = plain login

service auth {
  unix_listener /var/spool/postfix/private/auth {
    mode = 0660
    user = postfix
    group = postfix
  }
}

service lmtp {
  unix_listener /var/spool/postfix/private/dovecot-lmtp {
    mode = 0600
    user = postfix
    group = postfix
  }
}
`
	out, err := agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("cat > /etc/dovecot/conf.d/99-panel.conf <<'DOVE99'\n%sDOVE99", doveOverride))
	step("write /etc/dovecot/conf.d/99-panel.conf", out, err)

	// Disable the PAM include so system /etc/passwd can't authenticate.
	out, err = agent.RunCommand(ctx, "bash", "-c",
		"sed -i 's|^!include auth-system.conf.ext|#!include auth-system.conf.ext|' /etc/dovecot/conf.d/10-auth.conf 2>/dev/null || true")
	step("disable auth-system.conf.ext include", out, err)

	// Ensure the panel's users file exists with the right perms.
	out, err = agent.RunCommand(ctx, "bash", "-c",
		"touch /etc/dovecot/users && chgrp dovecot /etc/dovecot/users && chmod 0640 /etc/dovecot/users")
	step("ensure /etc/dovecot/users perms", out, err)

	// Postfix SASL directives — idempotent.
	//
	// inet_protocols=ipv4 forces outbound SMTP over IPv4 only. Our hosts
	// generally have an IPv6 address without a matching PTR record, and
	// Gmail enforces 5.7.25 "no PTR" on IPv6 — so half the outbound
	// deliveries would bounce non-deterministically depending on which
	// family Postfix tried first. Easier to disable IPv6 entirely than
	// to chase a PTR we don't control.
	for _, d := range []string{
		"smtpd_sasl_type=dovecot",
		"smtpd_sasl_path=private/auth",
		"smtpd_sasl_auth_enable=yes",
		"smtpd_sasl_security_options=noanonymous",
		"broken_sasl_auth_clients=yes",
		"inet_protocols=ipv4",
		"virtual_mailbox_domains=hash:/etc/postfix/virtual_mailbox_domains",
		"virtual_mailbox_maps=hash:/etc/postfix/virtual_mailbox_maps",
		"virtual_transport=lmtp:unix:private/dovecot-lmtp",
	} {
		out, err = agent.RunCommand(ctx, "postconf", "-e", d)
		step("postconf -e "+d, out, err)
	}

	// Ensure the referenced map files exist so postmap doesn't fail.
	out, err = agent.RunCommand(ctx, "bash", "-c",
		"touch /etc/postfix/virtual_mailbox_domains /etc/postfix/virtual_mailbox_maps && postmap /etc/postfix/virtual_mailbox_domains && postmap /etc/postfix/virtual_mailbox_maps")
	step("postmap virtual_mailbox_{domains,maps}", out, err)

	// Sync the postfix chroot's /etc/ — see SyncPostfixChroot for why.
	out, err = syncPostfixChrootCmd(ctx)
	step("sync postfix chroot resolver files", out, err)

	// Restart dovecot first (Postfix depends on its socket).
	out, err = agent.RunCommand(ctx, "systemctl", "restart", "dovecot")
	step("systemctl restart dovecot", out, err)
	out, err = agent.RunCommand(ctx, "systemctl", "restart", "postfix")
	step("systemctl restart postfix", out, err)

	// Verify SASL socket is present — this is the one file whose
	// absence caused the "SMTP Error (): Authentication failed" bug.
	out, err = agent.RunCommand(ctx, "bash", "-c",
		"ls -l /var/spool/postfix/private/auth 2>&1")
	step("verify /var/spool/postfix/private/auth", out, err)

	// Roundcube SMTP config must use tls:// so Roundcube negotiates
	// STARTTLS before AUTH. Postfix's smtpd_tls_auth_only=yes rejects
	// plaintext AUTH, so the older plain-`localhost:587` config
	// produced the exact "SMTP Error (): Authentication failed" toast
	// users saw from the Compose view.
	//
	// Handled via a small Python script to dodge the sed/bash/Go
	// quoting rabbit-hole that PHP $config[...] assignments create.
	// Idempotent: regex rewrites any existing smtp_host assignment and
	// only appends smtp_conn_options when it's missing.
	const rcPy = `
import re, sys
p = "/etc/roundcube/config.inc.php"
try:
    with open(p) as f: src = f.read()
except FileNotFoundError:
    print("config.inc.php missing — nothing to do")
    sys.exit(0)
orig = src
src = re.sub(
    r"\$config\[\s*['\"]smtp_host['\"]\s*\]\s*=\s*['\"][^'\"]*['\"]\s*;",
    "$config['smtp_host'] = 'tls://localhost:587';",
    src,
)
if "smtp_conn_options" not in src:
    src = src.rstrip() + """

// Managed by Betazen Server Panel reconcile — snake-oil cert on localhost is OK.
$config['smtp_conn_options'] = [
    'ssl' => [
        'verify_peer'       => false,
        'verify_peer_name'  => false,
        'allow_self_signed' => true,
    ],
];
"""
if src != orig:
    with open(p, "w") as f: f.write(src)
    print("roundcube config updated (tls:// + smtp_conn_options)")
else:
    print("roundcube config already correct")
`
	out, err = agent.RunCommand(ctx, "python3", "-c", rcPy)
	step("Roundcube config: force tls:// + trust snake-oil", out, err)

	// Install / refresh the Sieve mail-hook pipeline so existing servers
	// pick up the email.message.received webhook surface without an
	// operator having to re-run email-install. No-op on a server that
	// already has it (apt prints "newest version", file writes diff to
	// nothing, dovecot restarts cleanly).
	hookLog, hookErr := agent.EnsureMailHookInstalled(ctx, "")
	if strings.TrimSpace(hookLog) != "" {
		log.WriteString(hookLog)
	}
	step("install Sieve mail-hook pipeline", nil, hookErr)

	return log.String(), nil
}
