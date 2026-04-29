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
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
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
}

func NewEmailService(db *mongo.Database, jwtSecret ...string) *EmailService {
	secret := ""
	if len(jwtSecret) > 0 {
		secret = jwtSecret[0]
	}
	return &EmailService{db: db, jwtSecret: secret}
}

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
				}
			}
		}
	}

	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var mailbox models.Mailbox
	err = col.FindOneAndUpdate(ctx, bson.M{"_id": oid}, bson.M{"$set": setFields}, opts).Decode(&mailbox)
	if err != nil {
		return nil, err
	}
	return &mailbox, nil
}

func (s *EmailService) DeleteMailbox(ctx context.Context, id string) error {
	mailbox, err := s.GetMailbox(ctx, id)
	if err != nil {
		return fmt.Errorf("mailbox not found: %w", err)
	}

	// Remove from Dovecot users
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s:/d' /etc/dovecot/users", strings.ReplaceAll(mailbox.Email, ".", "\\.")))

	// Remove from the virtual_mailbox_maps file that main.cf actually
	// references (see CreateMailbox for the matching write path).
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s /d' /etc/postfix/virtual_mailbox_maps", strings.ReplaceAll(mailbox.Email, ".", "\\.")))
	agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailbox_maps")

	// Remove maildir
	maildir := s.getMaildirPath(ctx, mailbox.Email)
	if maildir != "" {
		agent.RunCommand(ctx, "rm", "-rf", maildir)
	}

	// Reload Postfix
	agent.RunCommand(ctx, "systemctl", "reload", "postfix")

	col := s.db.Collection(database.ColMailboxes)
	_, err = col.DeleteOne(ctx, bson.M{"_id": mailbox.ID})
	return err
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
	// Add to Postfix virtual alias maps
	destinations := strings.Join(fwd.Destinations, ", ")
	mapping := fmt.Sprintf("%s    %s\n", fwd.Source, destinations)
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo '%s' >> /etc/postfix/virtual_alias_maps", mapping))
	agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_alias_maps")
	agent.RunCommand(ctx, "systemctl", "reload", "postfix")

	fwd.CreatedAt = time.Now()
	if fwd.Domain == "" {
		parts := strings.SplitN(fwd.Source, "@", 2)
		if len(parts) == 2 {
			fwd.Domain = parts[1]
		}
	}

	col := s.db.Collection(database.ColForwarders)
	result, err := col.InsertOne(ctx, fwd)
	if err != nil {
		return nil, err
	}
	fwd.ID = result.InsertedID.(primitive.ObjectID)
	return fwd, nil
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
	agent.RunCommand(ctx, "systemctl", "reload", "spamassassin")

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
func (s *EmailService) GenerateWebmailToken(ctx context.Context, email string) (string, error) {
	// Get the mailbox with encrypted password
	col := s.db.Collection(database.ColMailboxes)
	var mailbox models.Mailbox
	if err := col.FindOne(ctx, bson.M{"email": email}).Decode(&mailbox); err != nil {
		return "", fmt.Errorf("mailbox not found")
	}

	// Decrypt the password
	if mailbox.EncryptedPass == "" || s.jwtSecret == "" {
		return "", fmt.Errorf("webmail SSO not available for this mailbox")
	}
	plainPass, err := decryptPassword(mailbox.EncryptedPass, s.jwtSecret)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt credentials")
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

	return log.String(), nil
}
