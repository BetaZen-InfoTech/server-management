package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
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

func generateRandomPassword(length int) string {
	bytes := make([]byte, length/2+1)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)[:length]
}

type DomainService struct {
	db       *mongo.Database
	dns      *DNSService
	ssl      *SSLService
	email    *EmailService
	notifier *NotifierService
	cfg      DomainServiceConfig
}

// SetNotifier wires the shared NotifierService so Create can email the
// owning vendor that their domain is live. Called from main.go after
// NotifierService is constructed. Leaving it nil just means no email
// fires — safe for unit tests.
func (s *DomainService) SetNotifier(n *NotifierService) { s.notifier = n }

type DomainServiceConfig struct {
	SSLEmail  string // email for Let's Encrypt registration
	JWTSecret string // for encrypting FTP/mail passwords
	ServerIP  string // server IP for DNS A records
}

func NewDomainService(db *mongo.Database, dns *DNSService, ssl *SSLService, email *EmailService, cfg DomainServiceConfig) *DomainService {
	return &DomainService{db: db, dns: dns, ssl: ssl, email: email, cfg: cfg}
}

// findParentDomain checks if the given domain is a subdomain of any existing domain in the DB.
// Returns the parent domain string if found, or "" if this is a primary domain.
func findParentDomain(ctx context.Context, db *mongo.Database, domain string) string {
	parts := strings.Split(domain, ".")
	// Need at least 3 parts for a subdomain (e.g. app.example.com)
	if len(parts) < 3 {
		return ""
	}
	col := db.Collection(database.ColDomains)
	// Try progressively shorter parent domains: app.example.com -> example.com
	for i := 1; i < len(parts)-1; i++ {
		candidate := strings.Join(parts[i:], ".")
		count, _ := col.CountDocuments(ctx, bson.M{"domain": candidate})
		if count > 0 {
			return candidate
		}
	}
	return ""
}

func (s *DomainService) List(ctx context.Context, page, limit int, search string) ([]models.Domain, int64, error) {
	col := s.db.Collection(database.ColDomains)
	filter := bson.M{}
	if search != "" {
		filter["$or"] = bson.A{
			bson.M{"domain": bson.M{"$regex": search, "$options": "i"}},
			bson.M{"user": bson.M{"$regex": search, "$options": "i"}},
		}
	}
	// Multi-tenant: vendor_admin / vendor_staff only see domains owned by
	// users in their tenant. middleware.InjectScope attaches the scope to
	// ctx; vendor_owner gets nil scope and sees everything.
	if scope := GetCallerScope(ctx); scope != nil {
		filter = scope.ApplyTo(ctx, s.db, "user", filter)
	}

	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().SetSkip(skip).SetLimit(int64(limit)).SetSort(bson.D{{Key: "created_at", Value: -1}})
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var domains []models.Domain
	if err := cursor.All(ctx, &domains); err != nil {
		return nil, 0, err
	}
	if domains == nil {
		domains = []models.Domain{}
	}
	return domains, total, nil
}

// EnrichOwnerEmails populates the (transient) OwnerEmail field on each
// domain by walking from the domain's linux owner up to its tenant
// root. Used by the SSL page to autofill the Issue Certificate
// "Email" field — the operator picks N domains and the modal already
// knows whose vendor reg email to default to.
//
// The lookup is cached per username inside the loop so a vendor with
// 50 domains still costs one lookup, not 50. Failures are silent: a
// domain whose owner can't be resolved just keeps OwnerEmail = "" and
// the frontend falls back to its own auth-me email or asks the
// operator to type one. Never errors — enrichment is best-effort.
func (s *DomainService) EnrichOwnerEmails(ctx context.Context, domains []models.Domain) {
	if len(domains) == 0 {
		return
	}
	cache := make(map[string]string, len(domains))
	for i := range domains {
		user := strings.TrimSpace(domains[i].User)
		if user == "" {
			continue
		}
		if email, ok := cache[user]; ok {
			domains[i].OwnerEmail = email
			continue
		}
		email, _ := LookupVendorEmailForUsername(ctx, s.db, user)
		cache[user] = email
		domains[i].OwnerEmail = email
	}
}

func (s *DomainService) GetByID(ctx context.Context, id string) (*models.Domain, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid domain ID")
	}
	col := s.db.Collection(database.ColDomains)
	var domain models.Domain
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&domain); err != nil {
		return nil, err
	}
	// Multi-tenant: don't leak a domain from another vendor's tenant via ID guessing.
	if scope := GetCallerScope(ctx); scope != nil {
		if err := scope.AssertOwns(ctx, s.db, domain.User); err != nil {
			return nil, fmt.Errorf("domain not found")
		}
	}
	return &domain, nil
}

func (s *DomainService) Create(ctx context.Context, req *models.CreateDomainRequest) (*models.Domain, error) {
	// Validate that the user account exists
	userCol := s.db.Collection(database.ColUsers)
	count, _ := userCol.CountDocuments(ctx, bson.M{"username": req.User})
	if count == 0 {
		return nil, fmt.Errorf("user account '%s' not found", req.User)
	}

	// Tenant-scope guard: a non-owner caller can only create domains
	// under their own tenant. vendor_owner (the platform admin) is
	// unrestricted. Without this the frontend's read-only Account
	// field is defence-in-depth only — a crafted curl could still POST
	// req.user = "<another-tenant>" and create a domain under their
	// account. AssertOwns returns nil for vendor_owner and for
	// anyone whose tenant matches the target user.
	if scope := GetCallerScope(ctx); scope != nil {
		if err := scope.AssertOwns(ctx, s.db, req.User); err != nil {
			return nil, fmt.Errorf("you don't have permission to create domains under %q", req.User)
		}
	}

	// Pre-cleanup: remove any leftover files from a previously deleted domain with the same name.
	// This prevents "nginx config test failed" errors when re-adding a domain.
	agent.DeleteVhost(ctx, req.Domain)
	agent.DeletePHPPool(ctx, req.Domain)
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("rm -f /run/php/*-fpm-%s.sock", req.Domain))

	// 1. Create domain directory under user's home (also sets /home/{user} to 711)
	if err := agent.CreateDomainDirectory(ctx, req.User, req.Domain); err != nil {
		return nil, fmt.Errorf("failed to create domain directory: %w", err)
	}

	// 2. Create PHP-FPM pool (named after domain, runs as user)
	if err := agent.CreatePHPPool(ctx, req.Domain, req.User, req.PHPVersion); err != nil {
		return nil, fmt.Errorf("failed to create PHP pool: %w", err)
	}

	// 3. Create Nginx vhost (HTTP only initially, will upgrade to SSL after cert is issued)
	vhostCfg := &agent.VhostConfig{
		Domain:     req.Domain,
		User:       req.User,
		PHPVersion: req.PHPVersion,
	}
	if err := agent.CreateVhost(ctx, vhostCfg); err != nil {
		return nil, fmt.Errorf("failed to create vhost: %w", err)
	}

	// 4. Set disk quota if specified
	if req.DiskQuotaMB > 0 {
		if err := agent.SetDiskQuota(ctx, req.User, req.DiskQuotaMB); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to set disk quota: %v\n", err)
		}
	}

	now := time.Now()
	// Parse optional registration dates. parseFlexibleDate accepts both
	// YYYY-MM-DD (the HTML date input's default) and full RFC3339 so the
	// API stays friendly whether the caller is our UI or a script.
	registeredOn := parseFlexibleDate(req.RegisteredOn)
	expiresOn := parseFlexibleDate(req.ExpiresOn)

	domain := models.Domain{
		Domain:           req.Domain,
		User:             req.User,
		PHPVersion:       req.PHPVersion,
		DiskQuotaMB:      req.DiskQuotaMB,
		BandwidthLimitGB: req.BandwidthLimitGB,
		MaxDatabases:     req.MaxDatabases,
		MaxEmailAccounts: req.MaxEmailAccounts,
		MaxSubdomains:    req.MaxSubdomains,
		MaxApps:          req.MaxApps,
		Registrar:        strings.TrimSpace(req.Registrar),
		RegisteredOn:     registeredOn,
		ExpiresOn:        expiresOn,
		AutoRenew:        req.AutoRenew,
		Nameservers:      req.Nameservers,
		Status:           "active",
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	col := s.db.Collection(database.ColDomains)
	result, err := col.InsertOne(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("failed to save domain record: %w", err)
	}
	domain.ID = result.InsertedID.(primitive.ObjectID)

	// Tell the owning vendor their domain is live — fires BEFORE
	// auto-SSL so the chronology of emails matches the reality
	// ("new domain added" → "SSL issued"). Background context so a
	// slow SMTP relay can't stall the rest of the create flow; errors
	// are logged inside the notifier (zerolog) rather than bubbled up.
	if s.notifier != nil {
		go func(d models.Domain, ip string) {
			_ = s.notifier.NotifyNewDomain(context.Background(), &d, ip)
		}(domain, s.cfg.ServerIP)
	}

	// 5. DNS setup: detect if subdomain of an existing domain
	if s.dns != nil {
		serverIP := req.ServerIP
		if serverIP == "" {
			serverIP = s.cfg.ServerIP
		}

		parentDomain := findParentDomain(ctx, s.db, req.Domain)
		if parentDomain != "" {
			// Subdomain: add A record to parent zone instead of creating a new zone
			// pdnsutil expects relative name (e.g. "app"), not FQDN
			subPart := strings.TrimSuffix(req.Domain, "."+parentDomain)
			recReq := &models.CreateRecordRequest{
				Type:  "A",
				Name:  subPart,
				Value: serverIP,
				TTL:   3600,
			}
			if _, err := s.dns.AddRecord(ctx, parentDomain, recReq); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to add subdomain DNS record for %s: %v\n", req.Domain, err)
			}
			// Also add www.subdomain CNAME (e.g. www.app -> app.example.com.)
			wwwRecReq := &models.CreateRecordRequest{
				Type:  "CNAME",
				Name:  "www." + subPart,
				Value: req.Domain + ".",
				TTL:   3600,
			}
			if _, err := s.dns.AddRecord(ctx, parentDomain, wwwRecReq); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to add www DNS record for %s: %v\n", req.Domain, err)
			}

			// Wire mail for the subdomain. Previously we stopped at the A
			// + www CNAME above, which meant that creating a mailbox like
			// admin@sub.example.com later produced broken mail flow:
			//   * no MX record at `sub` → external senders fell back to
			//     the A record; SPF then failed on hostname mismatch.
			//   * no OpenDKIM signing table entry for the subdomain →
			//     outbound mail went unsigned.
			//   * sub.example.com wasn't in virtual_mailbox_domains until
			//     the first CreateMailbox ran, so any inbound delivery
			//     that raced mailbox creation bounced.
			// SetupSubdomainMail plugs all three holes by registering the
			// subdomain in OpenDKIM + Postfix and publishing MX / SPF /
			// DMARC / DKIM records into the parent zone.
			if err := s.dns.SetupSubdomainMail(ctx, subPart, parentDomain, serverIP); err != nil {
				fmt.Fprintf(os.Stderr, "warning: mail setup for subdomain %s failed: %v\n", req.Domain, err)
			}
		} else {
			// Primary domain: create full DNS zone with mail server setup
			nameservers := req.Nameservers
			if len(nameservers) == 0 {
				nameservers = []string{"dns1.betazeninfotech.com.", "dns2.betazeninfotech.com.", "dns3.betazeninfotech.com.", "dns4.betazeninfotech.com."}
			}
			dnsReq := &models.CreateZoneRequest{
				Domain:      req.Domain,
				ServerIP:    serverIP,
				AdminEmail:  "hostmaster." + req.Domain,
				Nameservers: nameservers,
			}
			s.dns.CreateZone(ctx, dnsReq)
		}
	}

	// 6. Auto-issue SSL certificate and upgrade nginx to HTTPS
	// DNS may not have propagated yet, so retry with delays
	if s.ssl != nil {
		sslEmail := s.cfg.SSLEmail
		if sslEmail == "" {
			sslEmail = "admin@betazeninfotech.com"
		}
		sslReq := &models.IssueLetsEncryptRequest{
			Domain:            req.Domain,
			Email:             sslEmail,
			AdditionalDomains: []string{"www." + req.Domain},
		}
		// Try SSL issuance with retries (DNS propagation can take a few seconds)
		var sslErr error
		for attempt := 1; attempt <= 3; attempt++ {
			if _, sslErr = s.ssl.IssueLetsEncrypt(ctx, sslReq); sslErr == nil {
				break
			}
			fmt.Fprintf(os.Stderr, "warning: auto-SSL attempt %d failed for %s: %v\n", attempt, req.Domain, sslErr)
			if attempt < 3 {
				time.Sleep(time.Duration(attempt*10) * time.Second) // 10s, 20s delay
			}
		}
		if sslErr != nil {
			fmt.Fprintf(os.Stderr, "warning: auto-SSL failed after 3 attempts for %s: %v\n", req.Domain, sslErr)
			// Tell the vendor the SSL attempt gave up so they can fix
			// DNS / firewall and retry from the panel. The reason
			// string here is already the friendly form produced by
			// SSLService.friendlyCertbotError.
			if s.notifier != nil {
				go func(dom, reason string) {
					_ = s.notifier.NotifySSLFailed(context.Background(), dom, reason, 3)
				}(req.Domain, sslErr.Error())
			}
		}
		// Note: nginx upgrade to SSL is now handled inside SSLService.IssueLetsEncrypt()
	}

	// 7. DKIM + Postfix safety net. setupMailServer / SetupSubdomainMail
	// should already have wired these for most creates, but a transfer
	// import or a race with dns-service unavailability could leave
	// /etc/opendkim/signing.table missing the new domain — in which
	// case outbound mail would go unsigned and Gmail would reject it.
	// ensureDKIMForDomain is idempotent and cheap; calling it here
	// closes the gap without duplicating work when the DNS path
	// already populated the tables.
	if s.email != nil {
		s.email.EnsureDKIMForDomain(ctx, req.Domain)

		// Auto-create admin@domain.com mailbox
		adminPass := generateRandomPassword(16)
		adminMailReq := &models.CreateMailboxRequest{
			Email:    "admin@" + req.Domain,
			Password: adminPass,
			QuotaMB:  1024,
		}
		if _, mailErr := s.email.CreateMailbox(ctx, adminMailReq); mailErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to create admin mailbox for %s: %v\n", req.Domain, mailErr)
		}
	}

	// 8. Auto-create root FTP account (non-deletable)
	ftpUser := req.User + "_" + strings.ReplaceAll(req.Domain, ".", "_")
	ftpPass := generateRandomPassword(16)
	ftpHome := fmt.Sprintf("/home/%s/domains/%s/public_html", req.User, req.Domain)
	if ftpErr := agent.CreateFTPAccount(ctx, ftpUser, ftpPass, ftpHome); ftpErr != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to create FTP account for %s: %v\n", req.Domain, ftpErr)
	} else {
		ftpCol := s.db.Collection(database.ColFTPAccounts)
		ftpCol.InsertOne(ctx, models.FTPAccount{
			Username:  ftpUser,
			Domain:    req.Domain,
			HomeDir:   ftpHome,
			IsRoot:    true,
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	// 9. Best-effort preflight enrichment. Stamps resolved DNS / IP /
	// domain-type onto the just-inserted record so the dashboard and
	// recheck-button starting state are populated without a manual
	// click. Failures are swallowed — a brand-new domain often hasn't
	// propagated DNS yet and we don't want to fail the create.
	if pf := s.RunPreflight(ctx, req.Domain); pf != nil {
		set := bson.M{
			"domain_type":       pf.DomainType,
			"ip_matches_server": pf.IPMatchesServer,
			"last_checked_at":   pf.CheckedAt,
			"updated_at":        time.Now(),
		}
		if len(pf.ResolvedIPs) > 0 {
			set["resolved_ip"] = pf.ResolvedIPs[0]
		}
		if len(pf.Nameservers) > 0 && len(domain.Nameservers) == 0 {
			set["nameservers"] = pf.Nameservers
		}
		col.UpdateByID(ctx, domain.ID, bson.M{"$set": set})
		// Reflect on the in-memory copy so the response carries the new
		// fields without an extra round-trip.
		domain.DomainType = pf.DomainType
		domain.IPMatchesServer = pf.IPMatchesServer
		domain.LastCheckedAt = &pf.CheckedAt
		if len(pf.ResolvedIPs) > 0 {
			domain.ResolvedIP = pf.ResolvedIPs[0]
		}
		if len(pf.Nameservers) > 0 && len(domain.Nameservers) == 0 {
			domain.Nameservers = pf.Nameservers
		}
	}

	return &domain, nil
}

func (s *DomainService) Update(ctx context.Context, id string, updates map[string]interface{}) (*models.Domain, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid domain ID")
	}

	// Only allow safe fields to be updated
	allowed := map[string]bool{
		"disk_quota_mb": true, "bandwidth_limit_gb": true,
		"max_databases": true, "max_email_accounts": true,
		"max_subdomains": true, "max_apps": true,
	}
	setFields := bson.M{"updated_at": time.Now()}
	for k, v := range updates {
		if allowed[k] {
			setFields[k] = v
		}
	}

	col := s.db.Collection(database.ColDomains)
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var domain models.Domain
	err = col.FindOneAndUpdate(ctx, bson.M{"_id": oid}, bson.M{"$set": setFields}, opts).Decode(&domain)
	if err != nil {
		return nil, err
	}
	return &domain, nil
}

func (s *DomainService) Delete(ctx context.Context, id string) error {
	domain, err := s.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("domain not found: %w", err)
	}

	// 1. Replace nginx vhost with a "site not deployed" placeholder. Same
	// reasoning as in AppService.Delete — fully removing the vhost makes
	// nginx pick a different domain's server block by alphabetical order
	// and serve the wrong cert, producing CERT_COMMON_NAME_INVALID in
	// the browser. The placeholder keeps the domain identity + cert
	// binding intact and shows a clear "site deactivated" page.
	agent.WritePlaceholderVhost(ctx, domain.Domain)

	// 2. Remove PHP-FPM pool config and socket for all PHP versions
	agent.DeletePHPPool(ctx, domain.Domain)
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("rm -f /run/php/*-fpm-%s.sock", domain.Domain))

	// 3. PRESERVE the domain's public_html (and everything below it). We
	// used to `rm -rf /home/<user>/domains/<domain>`, which silently
	// destroyed every byte the operator had uploaded — a real data-loss
	// risk on an accidental Delete click. The directory now stays so the
	// operator can recover the site or re-create the domain at the same
	// hostname without losing files. File Manager is the explicit way
	// to remove them when they're sure.

	// 4. Delete DNS: remove subdomain records from parent zone, or delete full zone
	if s.dns != nil {
		parentDomain := findParentDomain(ctx, s.db, domain.Domain)
		if parentDomain != "" {
			// Subdomain: remove A record and www CNAME from parent zone
			subPart := strings.TrimSuffix(domain.Domain, "."+parentDomain)
			records, _ := s.dns.ListRecords(ctx, parentDomain)
			for _, r := range records {
				// Remove the A record for the subdomain
				if r.Type == "A" && r.Name == subPart {
					s.dns.DeleteRecord(ctx, parentDomain, r.ID.Hex())
				}
				// Remove the www.subdomain CNAME record
				if r.Type == "CNAME" && r.Name == "www."+subPart {
					s.dns.DeleteRecord(ctx, parentDomain, r.ID.Hex())
				}
			}
		} else {
			// Primary domain: delete full zone (removes all DNS records)
			s.dns.DeleteZone(ctx, domain.Domain)
		}
		// Also delete zone records from DB in case zone deletion missed them
		s.db.Collection(database.ColDNSRecords).DeleteMany(ctx, bson.M{"zone_id": bson.M{
			"$in": s.getZoneIDs(ctx, domain.Domain),
		}})
		s.db.Collection(database.ColDNSZones).DeleteMany(ctx, bson.M{"domain": domain.Domain})
	}

	// 5. Remove SSL certificate DB record ONLY. The actual cert files
	// under /etc/letsencrypt/{live,archive,renewal} and /etc/ssl/custom
	// are PRESERVED — deleting them triggered two real problems:
	//   a) re-creating a domain at the same hostname had to re-issue,
	//      which silently fails when Let's Encrypt rate-limits (5
	//      duplicate certs / week) so the site went back to HTTP.
	//   b) it threw away a valid cert with weeks of life left, for no
	//      reason other than "the panel record went away".
	// The DB flip below marks SSL inactive so the UI reflects reality;
	// if the operator re-creates the domain, the deploy path sees the
	// on-disk cert via agent.LetsEncryptCertExists and re-attaches it
	// without a new certbot call.
	s.db.Collection(database.ColSSLCerts).DeleteMany(ctx, bson.M{"domain": domain.Domain})

	// 6. Delete ALL email data: mailboxes, forwarders, autoresponders (system + DB)
	var mailboxes []models.Mailbox
	mailCursor, _ := s.db.Collection(database.ColMailboxes).Find(ctx, bson.M{"domain": domain.Domain})
	if mailCursor != nil {
		mailCursor.All(ctx, &mailboxes)
		mailCursor.Close(ctx)
	}
	for _, mb := range mailboxes {
		escapedEmail := strings.ReplaceAll(mb.Email, ".", "\\.")
		// Remove from Dovecot users file
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s:/d' /etc/dovecot/users", escapedEmail))
		// Remove from Postfix virtual_mailboxes
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s /d' /etc/postfix/virtual_mailboxes", escapedEmail))
	}

	// Remove email forwarders from Postfix virtual_alias_maps
	var forwarders []models.EmailForwarder
	fwdCursor, _ := s.db.Collection(database.ColForwarders).Find(ctx, bson.M{"domain": domain.Domain})
	if fwdCursor != nil {
		fwdCursor.All(ctx, &forwarders)
		fwdCursor.Close(ctx)
	}
	for _, fwd := range forwarders {
		escapedSource := strings.ReplaceAll(fwd.Source, ".", "\\.")
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s /d' /etc/postfix/virtual_alias_maps", escapedSource))
	}
	agent.RunCommand(ctx, "bash", "-c", "postmap /etc/postfix/virtual_alias_maps 2>/dev/null")

	// PRESERVE the on-disk mail store at /home/<user>/mail/<domain>.
	// Auto-deleting every message when a domain is dropped destroys
	// compliance-relevant data and is irreversible. The postfix /
	// dovecot maps above are trimmed so new mail for the domain is
	// rejected, but existing Maildirs stay. If the operator later
	// recreates the domain, existing mailboxes reappear.
	// Rebuild postfix maps
	agent.RunCommand(ctx, "bash", "-c", "postmap /etc/postfix/virtual_mailboxes 2>/dev/null")
	// Remove domain from virtual_domains
	escapedDomain := strings.ReplaceAll(domain.Domain, ".", "\\.")
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s$/d' /etc/postfix/virtual_domains 2>/dev/null", escapedDomain))
	agent.RunCommand(ctx, "bash", "-c", "postmap /etc/postfix/virtual_domains 2>/dev/null")

	// 7. Unregister the domain from OpenDKIM's config tables so the daemon
	// stops signing outbound mail for it, but PRESERVE the keypair under
	// /etc/opendkim/keys/<domain>. Re-creating the domain reuses the
	// same key → the DNS TXT record already published for that domain
	// keeps validating and the operator doesn't have to republish a
	// fresh selector.
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/%s/d' /etc/opendkim/signing.table 2>/dev/null", escapedDomain))
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/%s/d' /etc/opendkim/key.table 2>/dev/null", escapedDomain))
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s$/d' /etc/opendkim/trusted.hosts 2>/dev/null", escapedDomain))
	agent.RunCommand(ctx, "systemctl", "reload", "opendkim")

	// Reload postfix after all email/DKIM cleanup
	agent.RunCommand(ctx, "systemctl", "reload", "postfix")

	// Delete all email DB records
	s.db.Collection(database.ColMailboxes).DeleteMany(ctx, bson.M{"domain": domain.Domain})
	s.db.Collection(database.ColForwarders).DeleteMany(ctx, bson.M{"domain": domain.Domain})
	s.db.Collection(database.ColAutoresponders).DeleteMany(ctx, bson.M{"domain": domain.Domain})

	// 8. Delete FTP accounts (from system + DB)
	var ftpAccounts []models.FTPAccount
	ftpCursor, _ := s.db.Collection(database.ColFTPAccounts).Find(ctx, bson.M{"domain": domain.Domain})
	if ftpCursor != nil {
		ftpCursor.All(ctx, &ftpAccounts)
		ftpCursor.Close(ctx)
	}
	for _, ftp := range ftpAccounts {
		agent.DeleteFTPAccount(ctx, ftp.Username)
	}
	s.db.Collection(database.ColFTPAccounts).DeleteMany(ctx, bson.M{"domain": domain.Domain})

	// 9. Delete subdomains, aliases, redirects
	s.db.Collection(database.ColSubdomains).DeleteMany(ctx, bson.M{"domain_id": domain.ID})
	s.db.Collection(database.ColAliases).DeleteMany(ctx, bson.M{"domain_id": domain.ID})
	s.db.Collection(database.ColRedirects).DeleteMany(ctx, bson.M{"domain_id": domain.ID})

	// 10. Delete apps and deployments
	s.db.Collection(database.ColApps).DeleteMany(ctx, bson.M{"domain": domain.Domain})
	s.db.Collection(database.ColDeployments).DeleteMany(ctx, bson.M{"domain": domain.Domain})

	// 11. Delete databases and database users
	s.db.Collection(database.ColDatabases).DeleteMany(ctx, bson.M{"domain": domain.Domain})
	s.db.Collection(database.ColDBUsers).DeleteMany(ctx, bson.M{"domain": domain.Domain})

	// 12. Delete WordPress installs
	s.db.Collection(database.ColWordPress).DeleteMany(ctx, bson.M{"domain": domain.Domain})

	// 13. Delete cron jobs
	s.db.Collection(database.ColCronJobs).DeleteMany(ctx, bson.M{"domain": domain.Domain})

	// 14. Delete backups and backup schedules
	s.db.Collection(database.ColBackups).DeleteMany(ctx, bson.M{"domain": domain.Domain})
	s.db.Collection(database.ColBackupSchedules).DeleteMany(ctx, bson.M{"domain": domain.Domain})

	// 15. PRESERVE nginx access/error logs under /var/log/nginx/. They
	// are useful for post-incident forensics even after the domain is
	// gone, and they'll rotate out of existence on their own via the
	// standard logrotate config. Deleting them on every Delete kills
	// any audit trail the sysadmin might need.

	// 16. Final nginx reload to ensure clean state
	agent.RunCommand(ctx, "bash", "-c", "nginx -t 2>/dev/null && systemctl reload nginx 2>/dev/null")

	// 17. Delete the domain record itself
	col := s.db.Collection(database.ColDomains)
	_, err = col.DeleteOne(ctx, bson.M{"_id": domain.ID})
	return err
}

// getZoneIDs returns all DNS zone IDs for a domain (used for cleanup)
func (s *DomainService) getZoneIDs(ctx context.Context, domain string) []primitive.ObjectID {
	var zones []struct {
		ID primitive.ObjectID `bson:"_id"`
	}
	cursor, err := s.db.Collection(database.ColDNSZones).Find(ctx, bson.M{"domain": domain})
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)
	cursor.All(ctx, &zones)
	ids := make([]primitive.ObjectID, len(zones))
	for i, z := range zones {
		ids[i] = z.ID
	}
	return ids
}

// Suspend disables a domain by removing its nginx symlink and marking it
// `suspended` in mongo. Linked apps / project_services are detected and
// surfaced in the error so the operator can't accidentally break a running
// Deploy Software project that depends on the domain — suspending a
// project's primary domain drops its :80/:443 routing, the project's
// upstream port keeps answering but nginx returns 404 / catch-all, and
// from the user's side "Deploy Software and service not to link with
// domain" — which is exactly the confusing shape of the original bug
// report. Force via force=true to suspend anyway (still disables
// everything; caller acknowledged the consequence).
func (s *DomainService) Suspend(ctx context.Context, id string, force bool) error {
	domain, err := s.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("domain not found: %w", err)
	}

	if !force {
		// Apps whose .domain matches, OR project_services whose
		// primary_domain OR alias_domains contain this domain.
		var blockers []string
		appCur, _ := s.db.Collection(database.ColApps).Find(ctx, bson.M{"domain": domain.Domain})
		if appCur != nil {
			var apps []models.App
			appCur.All(ctx, &apps)
			appCur.Close(ctx)
			for _, a := range apps {
				blockers = append(blockers, fmt.Sprintf("app %q", a.Name))
			}
		}
		svcCur, _ := s.db.Collection(database.ColProjectServices).Find(ctx, bson.M{
			"$or": []bson.M{
				{"primary_domain": domain.Domain},
				{"alias_domains": domain.Domain},
			},
		})
		if svcCur != nil {
			var svcs []models.ProjectService
			svcCur.All(ctx, &svcs)
			svcCur.Close(ctx)
			for _, sv := range svcs {
				blockers = append(blockers, fmt.Sprintf("project service %q", sv.Name))
			}
		}
		if len(blockers) > 0 {
			return fmt.Errorf("cannot suspend %s: still used by %s (stop or remove those first, or pass force=true)",
				domain.Domain, strings.Join(blockers, ", "))
		}
	}

	// Remove nginx sites-enabled symlink to disable the domain
	agent.RunCommand(ctx, "rm", "-f", fmt.Sprintf("/etc/nginx/sites-enabled/%s", domain.Domain))
	agent.ReloadNginx(ctx)

	col := s.db.Collection(database.ColDomains)
	_, err = col.UpdateOne(ctx, bson.M{"_id": domain.ID}, bson.M{
		"$set": bson.M{"status": "suspended", "updated_at": time.Now()},
	})
	return err
}

func (s *DomainService) Unsuspend(ctx context.Context, id string) error {
	domain, err := s.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("domain not found: %w", err)
	}

	// Re-enable nginx vhost
	src := fmt.Sprintf("/etc/nginx/sites-available/%s", domain.Domain)
	dst := fmt.Sprintf("/etc/nginx/sites-enabled/%s", domain.Domain)
	agent.RunCommand(ctx, "ln", "-sf", src, dst)
	agent.ReloadNginx(ctx)

	col := s.db.Collection(database.ColDomains)
	_, err = col.UpdateOne(ctx, bson.M{"_id": domain.ID}, bson.M{
		"$set": bson.M{"status": "active", "updated_at": time.Now()},
	})
	return err
}

func (s *DomainService) SwitchPHP(ctx context.Context, id string, phpVersion string) error {
	domain, err := s.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("domain not found: %w", err)
	}

	// Switch PHP pool (pool named after domain, runs as user)
	if err := agent.SwitchPHPVersion(ctx, domain.Domain, domain.User, domain.PHPVersion, phpVersion); err != nil {
		return fmt.Errorf("failed to switch PHP version: %w", err)
	}

	// Recreate vhost with new PHP version — use SSL template if SSL is active
	vhostCfg := &agent.VhostConfig{
		Domain:     domain.Domain,
		User:       domain.User,
		PHPVersion: phpVersion,
	}
	if domain.SSLActive {
		agent.CreateVhostWithSSL(ctx, vhostCfg)
	} else {
		agent.CreateVhost(ctx, vhostCfg)
	}

	col := s.db.Collection(database.ColDomains)
	_, err = col.UpdateOne(ctx, bson.M{"_id": domain.ID}, bson.M{
		"$set": bson.M{"php_version": phpVersion, "updated_at": time.Now()},
	})
	return err
}

func (s *DomainService) GetStats(ctx context.Context, id string) (map[string]interface{}, error) {
	domain, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("domain not found: %w", err)
	}

	stats := map[string]interface{}{
		"domain": domain.Domain,
		"user":   domain.User,
		"status": domain.Status,
	}

	// Count associated resources
	appCount, _ := s.db.Collection(database.ColApps).CountDocuments(ctx, bson.M{"domain": domain.Domain})
	dbCount, _ := s.db.Collection(database.ColDatabases).CountDocuments(ctx, bson.M{"domain": domain.Domain})
	mailCount, _ := s.db.Collection(database.ColMailboxes).CountDocuments(ctx, bson.M{"domain": domain.Domain})
	stats["apps"] = appCount
	stats["databases"] = dbCount
	stats["email_accounts"] = mailCount

	// Get disk usage for the domain directory
	result, err := agent.RunCommand(ctx, "du", "-sm", fmt.Sprintf("/home/%s/domains/%s", domain.User, domain.Domain))
	if err == nil {
		parts := strings.Fields(result.Output)
		if len(parts) > 0 {
			stats["disk_usage_mb"] = parts[0]
		}
	}

	return stats, nil
}

// ListByUser returns domains visible to the caller from the cpanel /
// user-panel side. Previously this filtered by `user == <hex ObjectID>`
// which never matched anything — the domains.user field stores the Linux
// username (e.g. "jagoanaandadhaii"), not an ObjectID, so vendors saw an
// empty list even when WHM had assigned domains to them.
//
// Now we reuse the same tenant scoping as the WHM List method: the
// InjectScope middleware attaches a CallerScope to ctx and ApplyTo
// rewrites the filter to match every domains.user that belongs to the
// caller's tenant (their own username, plus any tenant child they own).
// userID is kept in the signature for API stability but is unused — the
// auth/tenant context on ctx is the source of truth.
func (s *DomainService) ListByUser(ctx context.Context, userID string, page, limit int) ([]models.Domain, int64, error) {
	col := s.db.Collection(database.ColDomains)
	filter := bson.M{}

	if scope := GetCallerScope(ctx); scope != nil {
		filter = scope.ApplyTo(ctx, s.db, "user", filter)
	} else {
		// Fallback for calls outside the normal middleware chain (tests,
		// migrations). Look up the caller's username from their user_id
		// and filter by that. Keeps the function usable standalone.
		if userID != "" {
			if oid, err := primitive.ObjectIDFromHex(userID); err == nil {
				var u models.User
				if err := s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": oid}).Decode(&u); err == nil && u.Username != "" {
					filter["user"] = u.Username
				}
			}
		}
	}

	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().SetSkip(skip).SetLimit(int64(limit)).SetSort(bson.D{{Key: "created_at", Value: -1}})
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var domains []models.Domain
	if err := cursor.All(ctx, &domains); err != nil {
		return nil, 0, err
	}
	if domains == nil {
		domains = []models.Domain{}
	}
	return domains, total, nil
}

// ----------------------------------------------------------------------
// Registration / whois — domain purchase + expiry tracking
// ----------------------------------------------------------------------

// parseFlexibleDate turns an operator-entered date string into a Time
// parseFlexibleDate accepts pretty much every date shape WHOIS servers
// (and HTML5 <input type=date>) actually emit and returns a *time.Time
// pointer. Real-world WHOIS responses are inconsistent enough that a
// narrow list of layouts quietly dropped perfectly good dates on the
// floor — the fix reported on this session ("data from whois, not to
// save") was exactly that symptom for TLDs emitting 17-Apr-2026 or
// 2026.04.17. Empty or unparseable strings still return nil so the
// caller can leave the existing DB value untouched.
func parseFlexibleDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// Strip trailing noise some registries append ("(GMT)", "UTC",
	// timezone annotations, etc.) — the leading date shape is the only
	// part we actually care about.
	if i := strings.Index(s, " ("); i > 0 {
		s = s[:i]
	}
	layouts := []string{
		// ISO / HTML5 input formats
		"2006-01-02",
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05-0700",
		"2006-01-02 15:04:05",
		// Slash-separated variants
		"2006/01/02",
		"2006/01/02 15:04:05",
		"01/02/2006",
		"02/01/2006",
		// Dot-separated (Eastern European / some Asian registries)
		"2006.01.02",
		"02.01.2006",
		// Dashed with month names (.uk, Nominet)
		"2-Jan-2006",
		"02-Jan-2006",
		"2-January-2006",
		"02-January-2006",
		// Spaced with month names (.de, .fr occasionally)
		"2 Jan 2006",
		"02 Jan 2006",
		"2 January 2006",
		"02 January 2006",
		// Reverse human-readable (some IANA outputs)
		"Jan 2, 2006",
		"January 2, 2006",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

// UpdateRegistration patches just the registrar / purchase / expiry /
// auto-renew / nameservers fields on an existing domain. Ignores every
// other field on the request so this endpoint stays narrow and can't
// accidentally reset PHP version or resource limits.
func (s *DomainService) UpdateRegistration(ctx context.Context, id string, req *models.UpdateRegistrationRequest) (*models.Domain, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid domain id")
	}
	col := s.db.Collection(database.ColDomains)
	set := bson.M{"updated_at": time.Now()}
	set["registrar"] = strings.TrimSpace(req.Registrar)
	set["registered_on"] = parseFlexibleDate(req.RegisteredOn)
	set["expires_on"] = parseFlexibleDate(req.ExpiresOn)
	if req.AutoRenew != nil {
		set["auto_renew"] = *req.AutoRenew
	}
	if req.Nameservers != nil {
		set["nameservers"] = req.Nameservers
	}

	res, err := col.UpdateByID(ctx, oid, bson.M{"$set": set})
	if err != nil {
		return nil, err
	}
	if res.MatchedCount == 0 {
		return nil, fmt.Errorf("domain not found")
	}
	var out models.Domain
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ExpiringSoon returns domains whose ExpiresOn falls within `days` from
// now, sorted by nearest expiry. Tenant scope is applied so vendors
// only see their own domains in the dashboard widget; platform owner
// sees everything.
func (s *DomainService) ExpiringSoon(ctx context.Context, days int) ([]models.Domain, error) {
	if days <= 0 {
		days = 30
	}
	cutoff := time.Now().Add(time.Duration(days) * 24 * time.Hour)
	col := s.db.Collection(database.ColDomains)
	filter := bson.M{
		"expires_on": bson.M{"$ne": nil, "$lte": cutoff},
	}
	if scope := GetCallerScope(ctx); scope != nil {
		filter = scope.ApplyTo(ctx, s.db, "user", filter)
	}
	opts := options.Find().SetSort(bson.M{"expires_on": 1}).SetLimit(100)
	cur, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []models.Domain
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []models.Domain{}
	}
	return out, nil
}

// WhoisResult is the parsed subset we expose to the UI; Raw is the
// unparsed response so admins can inspect when the parser misses a
// field on a less-common TLD.
type WhoisResult struct {
	Registrar    string    `json:"registrar"`
	RegisteredOn string    `json:"registered_on"`
	ExpiresOn    string    `json:"expires_on"`
	Nameservers  []string  `json:"nameservers"`
	Raw          string    `json:"raw"`
	FetchedAt    time.Time `json:"fetched_at"`
}

var (
	whoisRegistrarRe = regexp.MustCompile(`(?i)(?:Registrar|Sponsoring Registrar):\s*(.+)`)
	whoisCreatedRe   = regexp.MustCompile(`(?i)(?:Creation Date|Registered On|Created On|Created|Domain Registration Date):\s*(.+)`)
	whoisExpiryRe    = regexp.MustCompile(`(?i)(?:Registry Expiry Date|Registrar Registration Expiration Date|Expiration Date|Expires On|Expiry Date|Renewal Date):\s*(.+)`)
	whoisNSRe        = regexp.MustCompile(`(?i)(?:Name Server|Nserver|Nameserver):\s*(\S+)`)
)

// WhoisLookup shells out to /usr/bin/whois. If the binary isn't
// installed we return an error the handler can surface as a clear
// "install the whois package" message instead of a generic 500.
// Every TLD has its own whois response shape — missing fields are
// left empty rather than errored.
func (s *DomainService) WhoisLookup(ctx context.Context, domain string) (*WhoisResult, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if _, err := agent.RunCommand(ctx, "which", "whois"); err != nil {
		return nil, fmt.Errorf("whois command not available on this server — install the `whois` package")
	}
	res, err := agent.RunCommand(ctx, "whois", domain)
	if err != nil || res == nil {
		return nil, fmt.Errorf("whois lookup failed")
	}
	raw := res.Output

	firstMatch := func(re *regexp.Regexp) string {
		m := re.FindStringSubmatch(raw)
		if len(m) < 2 {
			return ""
		}
		return strings.TrimSpace(m[1])
	}
	nsMatches := whoisNSRe.FindAllStringSubmatch(raw, -1)
	seen := map[string]bool{}
	var ns []string
	for _, m := range nsMatches {
		host := strings.ToLower(strings.TrimSpace(m[1]))
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		ns = append(ns, host)
	}

	return &WhoisResult{
		Registrar:    firstMatch(whoisRegistrarRe),
		RegisteredOn: firstMatch(whoisCreatedRe),
		ExpiresOn:    firstMatch(whoisExpiryRe),
		Nameservers:  ns,
		Raw:          raw,
		FetchedAt:    time.Now(),
	}, nil
}
