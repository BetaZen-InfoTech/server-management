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

func (s *EmailService) ListMailboxes(ctx context.Context, domain string, page, limit int) ([]models.Mailbox, int64, error) {
	col := s.db.Collection(database.ColMailboxes)
	filter := bson.M{}
	if domain != "" {
		filter["domain"] = domain
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
	return mailboxes, total, nil
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
	parts := strings.SplitN(mailbox.Email, "@", 2)
	if len(parts) == 2 {
		result, err := agent.RunCommand(ctx, "du", "-sm", fmt.Sprintf("/var/vmail/%s/%s", parts[1], parts[0]))
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

	// Check if this is the first mailbox for the domain - auto-setup mail DNS
	col := s.db.Collection(database.ColMailboxes)
	existingCount, _ := col.CountDocuments(ctx, bson.M{"domain": domain})
	if existingCount == 0 {
		s.setupMailDNS(ctx, domain)
	}

	// Create maildir
	maildir := fmt.Sprintf("/var/vmail/%s/%s", domain, localPart)
	agent.RunCommand(ctx, "mkdir", "-p", maildir+"/cur", maildir+"/new", maildir+"/tmp")
	agent.RunCommand(ctx, "chown", "-R", "vmail:vmail", fmt.Sprintf("/var/vmail/%s", domain))

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
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo '%s' >> /etc/dovecot/users", userLine))

	// Add virtual mailbox mapping for Postfix
	mapping := fmt.Sprintf("%s    %s/%s/", req.Email, domain, localPart)
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("echo '%s' >> /etc/postfix/virtual_mailboxes", mapping))
	agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailboxes")

	// Ensure domain is in virtual_mailbox_domains
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/postfix/virtual_domains || echo '%s OK' >> /etc/postfix/virtual_domains", domain, domain))
	agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_domains")

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

	setFields := bson.M{"updated_at": time.Now()}
	if v, ok := updates["quota_mb"]; ok {
		setFields["quota_mb"] = v
	}
	if v, ok := updates["send_limit_per_hour"]; ok {
		setFields["send_limit_per_hour"] = v
	}
	if v, ok := updates["password"]; ok {
		if pass, ok := v.(string); ok && pass != "" {
			if s.jwtSecret != "" {
				if enc, err := encryptPassword(pass, s.jwtSecret); err == nil {
					setFields["encrypted_pass"] = enc
				}
			}
		}
	}

	col := s.db.Collection(database.ColMailboxes)
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

	parts := strings.SplitN(mailbox.Email, "@", 2)
	if len(parts) == 2 {
		localPart := parts[0]
		domain := parts[1]

		// Remove from Dovecot users
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s:/d' /etc/dovecot/users", strings.ReplaceAll(mailbox.Email, ".", "\\.")))

		// Remove from Postfix virtual_mailboxes
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s /d' /etc/postfix/virtual_mailboxes", strings.ReplaceAll(mailbox.Email, ".", "\\.")))
		agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailboxes")

		// Remove maildir
		agent.RunCommand(ctx, "rm", "-rf", fmt.Sprintf("/var/vmail/%s/%s", domain, localPart))

		// Reload Postfix
		agent.RunCommand(ctx, "systemctl", "reload", "postfix")
	}

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

// setupMailDNS automatically sets up DKIM, MX, SPF, and DMARC DNS records
// when the first mailbox is created for a domain.
func (s *EmailService) setupMailDNS(ctx context.Context, domain string) {
	// Check if DNS zone exists in PowerDNS
	_, err := agent.RunCommand(ctx, "pdnsutil", "list-zone", domain)
	if err != nil {
		return // Zone doesn't exist, skip DNS setup
	}

	// Get server IP
	ipResult, _ := agent.RunCommand(ctx, "hostname", "-I")
	if ipResult == nil {
		return
	}
	fields := strings.Fields(strings.TrimSpace(ipResult.Output))
	if len(fields) == 0 {
		return
	}
	serverIP := fields[0]

	// Setup DKIM (generates key, adds to signing/key tables, reloads OpenDKIM)
	s.SetupDKIM(ctx, domain)

	// Fix key ownership
	agent.RunCommand(ctx, "chown", "-R", "opendkim:opendkim", fmt.Sprintf("/etc/opendkim/keys/%s", domain))

	// Read the generated DKIM public key
	dkimResult, _ := agent.RunCommand(ctx, "cat", fmt.Sprintf("/etc/opendkim/keys/%s/mail.txt", domain))
	dkimValue := ""
	if dkimResult != nil {
		dkimValue = parseDKIMPublicKey(dkimResult.Output)
	}

	// Add DNS records to PowerDNS
	agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "mail", "A", "3600", serverIP)
	agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "@", "MX", "3600", fmt.Sprintf("10 mail.%s.", domain))
	agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "@", "TXT", "3600", fmt.Sprintf("\"v=spf1 ip4:%s ~all\"", serverIP))
	if dkimValue != "" {
		agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "mail._domainkey", "TXT", "3600", fmt.Sprintf("\"%s\"", dkimValue))
	}
	agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "_dmarc", "TXT", "3600", fmt.Sprintf("\"v=DMARC1; p=none; rua=mailto:admin@%s\"", domain))

	// Reload PowerDNS
	agent.RunCommand(ctx, "pdns_control", "reload")

	// Also save records to MongoDB for display in DNS page
	zoneCol := s.db.Collection(database.ColDNSZones)
	var zone models.DNSZone
	if err := zoneCol.FindOne(ctx, bson.M{"domain": domain}).Decode(&zone); err == nil {
		now := time.Now()
		recCol := s.db.Collection(database.ColDNSRecords)
		mxPri := 10
		records := []interface{}{
			models.DNSRecord{ZoneID: zone.ID, Type: "A", Name: "mail", Value: serverIP, TTL: 3600, CreatedAt: now, UpdatedAt: now},
			models.DNSRecord{ZoneID: zone.ID, Type: "MX", Name: "@", Value: fmt.Sprintf("mail.%s.", domain), TTL: 3600, Priority: &mxPri, CreatedAt: now, UpdatedAt: now},
			models.DNSRecord{ZoneID: zone.ID, Type: "TXT", Name: "@", Value: fmt.Sprintf("v=spf1 ip4:%s ~all", serverIP), TTL: 3600, CreatedAt: now, UpdatedAt: now},
			models.DNSRecord{ZoneID: zone.ID, Type: "TXT", Name: "_dmarc", Value: fmt.Sprintf("v=DMARC1; p=none; rua=mailto:admin@%s", domain), TTL: 3600, CreatedAt: now, UpdatedAt: now},
		}
		if dkimValue != "" {
			records = append(records, models.DNSRecord{ZoneID: zone.ID, Type: "TXT", Name: "mail._domainkey", Value: dkimValue, TTL: 3600, CreatedAt: now, UpdatedAt: now})
		}
		recCol.InsertMany(ctx, records)
		zoneCol.UpdateOne(ctx, bson.M{"_id": zone.ID}, bson.M{
			"$inc": bson.M{"serial": len(records)},
			"$set": bson.M{"updated_at": now},
		})
	}
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
