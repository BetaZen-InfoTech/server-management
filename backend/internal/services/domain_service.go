package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
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
	mailer   *mailer.Mailer // for OTP delivery on destructive bulk ops; SetMailer wires it post-construction
	cfg      DomainServiceConfig
}

// SetMailer wires the shared mailer handle so the WHM Bulk Delete
// flow can email a 6-digit OTP to the admin's address before any
// destructive action runs. Optional — when nil (mailer not yet
// configured on a fresh install) the OTP code is logged to stderr
// instead so the operator can still test the flow via journalctl.
// Mirrors AuthService.SetMailer's contract.
func (s *DomainService) SetMailer(m *mailer.Mailer) { s.mailer = m }

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

// parentZoneOf walks `domain`'s candidate parents and returns the
// SHORTEST registered parent — i.e., walks from the apex down toward
// most-specific. "" means the domain has no registered parent and
// should get its own DNS zone.
//
// Apex-wins (3.0.31+) replaced the prior most-specific-wins (3.0.24)
// rule because the latter was too easy to fool by stale dns_zones
// entries: pre-3.0.24 buggy GetOrCreateZone calls silently created
// Mongo rows for subdomain "zones" that PowerDNS never had. After
// the v3.0.24 lookup moved from `domains` to `dns_zones`, those
// orphan rows started winning the most-specific match and child
// creates routed through the wrong "parent" — e.g. a user with
//   dns_zones: { konsultkaro.com, api.users.konsultkaro.com }   ← second is stale
// creating `dev.api.users.konsultkaro.com` got subPart="dev"
// instead of "dev.api.users", and the A record either landed in
// the wrong zone or vanished entirely (if the stale "zone" had
// no pdns SOA).
//
// Apex-wins is robust to that stale state without needing the
// operator to clean up first: the shortest-suffix walk picks the
// real apex, and any stale subdomain dns_zones row is silently
// stepped over. Trade-off: when an operator legitimately delegates
// a subdomain (its own SOA + NS in pdns + a dns_zones row), the
// panel will still publish A/CNAME for deeper child names into
// the apex zone, not the delegated one. That's a niche case the
// panel's UI doesn't drive — operators with real delegations edit
// pdns directly via the WHM DNS Records page, where they can add
// records to whichever zone they choose.
//
// Pure function so iteration order is testable without a Mongo
// round-trip; findParentDomain wires this to the real dns_zones
// collection.
func parentZoneOf(domain string, isZone func(string) bool) string {
	d := strings.TrimSuffix(strings.TrimSpace(domain), ".")
	if d == "" {
		return ""
	}
	parts := strings.Split(d, ".")
	// Need at least 3 parts for a subdomain (e.g. app.example.com).
	if len(parts) < 3 {
		return ""
	}
	// Walk shortest-suffix-first (apex first). The two-label suffix
	// is the apex candidate; the longer suffixes are intermediate
	// labels that may or may not be stale dns_zones rows.
	for i := len(parts) - 2; i >= 1; i-- {
		candidate := strings.Join(parts[i:], ".")
		if isZone(candidate) {
			return candidate
		}
	}
	return ""
}

// findParentDomain returns the registered DNS parent of `domain` or ""
// if `domain` is itself a primary domain.
//
// CRITICAL: looks in dns_zones, NOT domains. A "subdomain" creation
// (DomainService.Create called with an FQDN that has a parent) writes
// a row into the domains collection so resource quotas can count it,
// but does NOT create a separate dns_zones row — the subdomain's A
// record lives inside its parent's zone. Querying domains here would
// greedy-match that subdomain entry as a parent for any
// even-deeper FQDN, slicing the new label down to the wrong relative
// form. Concrete reproduction:
//
//	dns_zones: {qwe.com}
//	domains:   {qwe.com, abc.xyz.qwe.com}   ← second is a panel subdomain
//
//	Create abc.abc.xyz.qwe.com.
//	OLD (looked in domains): findParentDomain returns abc.xyz.qwe.com,
//	subPart = TrimSuffix("abc.abc.xyz.qwe.com", ".abc.xyz.qwe.com") = "abc",
//	A record lands in zone abc.xyz.qwe.com which doesn't exist in
//	PowerDNS, the operator sees "label = abc only".
//	NEW (looks in dns_zones): returns qwe.com, subPart = "abc.abc.xyz",
//	A record lands correctly in zone qwe.com as abc.abc.xyz.
//
// Delegated subdomain zones still work: if the operator explicitly
// CreateZone'd corp.example.com as its own zone, it lives in
// dns_zones and most-specific-wins correctly puts
// app.corp.example.com under it.
func findParentDomain(ctx context.Context, db *mongo.Database, domain string) string {
	col := db.Collection(database.ColDNSZones)
	return parentZoneOf(domain, func(candidate string) bool {
		n, err := col.CountDocuments(ctx, bson.M{"domain": candidate})
		return err == nil && n > 0
	})
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
	// Hierarchical re-sort: apex first, then its subdomains immediately
	// underneath. Operators see apex example.com → app.example.com →
	// api.app.example.com grouped together rather than the creation-
	// time jumble where children show above their parent because the
	// operator added them later. Re-sort is stable so domains that
	// hash to the same reverse-label key (shouldn't happen — domain
	// is uniquely indexed — but defensive) keep their creation order.
	SortDomainsHierarchical(domains)
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

// DomainEnvironments is the canonical, ordered set of deployment tiers a
// domain can be tagged with. "prod" is index 0 so it stays the default.
// Kept here (service layer) so the create path, the Update whitelist, and
// any future bulk-import column all agree on the valid set.
var DomainEnvironments = []string{"prod", "dev", "test", "local"}

// NormalizeDomainEnvironment lower-cases + trims the caller-supplied
// environment and maps anything outside the known set (including empty)
// back to "prod". This is the single gate every write path runs through
// so a typo or a crafted API body can never persist an unknown tier.
func NormalizeDomainEnvironment(env string) string {
	env = strings.ToLower(strings.TrimSpace(env))
	for _, valid := range DomainEnvironments {
		if env == valid {
			return env
		}
	}
	return "prod"
}

func (s *DomainService) Create(ctx context.Context, req *models.CreateDomainRequest) (*models.Domain, error) {
	// setupWarnings accumulates non-fatal status messages from the
	// zone / mail / SSL / mailbox sub-flows. Pre-3.1.16 these went
	// to stderr only — invisible to a bulk-upload operator and
	// silently masked by the "domain created" success result. Now
	// every sub-step that can fail without aborting the create
	// appends a human-readable line here, and we stamp the slice
	// onto the returned Domain so the caller (single handler,
	// bulk-upload row, programmatic API) can render them.
	var setupWarnings []string
	warn := func(format string, args ...interface{}) {
		setupWarnings = append(setupWarnings, fmt.Sprintf(format, args...))
	}

	// Security (audit §8c): req.Domain is interpolated into shell commands
	// (e.g. `rm -f /run/php/*-fpm-<domain>.sock`, DNS/mail setup). Normalise +
	// reject any non-shell-safe domain at the service boundary so a crafted
	// name can't inject commands as root. Covers single create, bulk-upload
	// and the programmatic API.
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if !validator.IsSafeDNSName(req.Domain) {
		return nil, fmt.Errorf("invalid domain name %q: only letters, digits, '.', '-' and '_' are allowed", req.Domain)
	}

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

	// Ensure the Linux system user exists BEFORE we create + chown the domain
	// directory to it. A panel account migrated from another server keeps its
	// mongo `users` row, but the transfer doesn't always recreate the matching
	// system user on the destination — so the very first domain-create under
	// such an account died at `chown: invalid user: '<user>:<user>'`
	// (CreateDomainDirectory → EnsureWebPerms). ensureUser is idempotent
	// (`id` → `useradd -m -s /bin/bash`) and is the same guard the app /
	// project / backup provisioning paths already run; domain-create was the
	// one flow that skipped it.
	if err := ensureUser(ctx, req.User); err != nil {
		return nil, fmt.Errorf("failed to ensure system user %q: %w", req.User, err)
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

	// 3. Create Nginx vhost (HTTP only initially, will upgrade to SSL after cert is issued).
	// DocumentRoot is forwarded so a caller (single create, bulk row,
	// programmatic API) can pin a custom docroot at provision time;
	// empty falls back to /home/<user>/domains/<domain>/public_html
	// inside agent.CreateVhost.
	vhostCfg := &agent.VhostConfig{
		Domain:       req.Domain,
		User:         req.User,
		PHPVersion:   req.PHPVersion,
		DocumentRoot: strings.TrimSpace(req.DocumentRoot),
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

	// 3.1.88 — stamp creation source so the Domains list can show
	// "manual / bulk_upload / api" badges + filter. Bulk-upload +
	// programmatic API handlers populate req.Source before calling
	// Create; the manual-create handler leaves it blank, and we
	// default to "manual" here so legacy callers stay correct.
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "manual"
	}
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
		DocumentRoot:     strings.TrimSpace(req.DocumentRoot),
		Source:           source,
		Environment:      NormalizeDomainEnvironment(req.Environment),
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	col := s.db.Collection(database.ColDomains)
	result, err := col.InsertOne(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("failed to save domain record: %w", err)
	}
	domain.ID = result.InsertedID.(primitive.ObjectID)

	// Outbound webhook fan-out — fire-and-forget; webhook service handles
	// retries and delivery logging. Scoped by tenant so vendors see only
	// their own domain events; owner-issued endpoints see everything.
	EmitEvent(ctx, "domain.created", LookupTenantIDForUsername(ctx, s.db, domain.User), map[string]any{
		"id":          domain.ID.Hex(),
		"domain":      domain.Domain,
		"user":        domain.User,
		"php_version": domain.PHPVersion,
	})

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
			// Bootstrap TTLs (A=30s, CNAME=60s) — mirrors the
			// services.bootstrapTTLFor policy. Subdomain operators
			// iterate a lot in the first hours (different upstream,
			// nginx config still settling); short TTLs let those
			// changes propagate without a long wait.
			recReq := &models.CreateRecordRequest{
				Type:  "A",
				Name:  subPart,
				Value: serverIP,
				TTL:   bootstrapTTLFor("A"),
			}
			// AddRecord failures used to go to stderr only — invisible to
			// the operator, so the panel happily reported "subdomain
			// created successfully" even when DNS was broken (the orphan
			// rows from app.thewaapi.com / api.lamdainfotech.thewaapi.com
			// in the user's report). We now also write a structured zerolog
			// entry so journalctl tail surfaces the cause AND keep the
			// stderr line for backwards-compatible operator scripts that
			// grep for "warning:". Duplicate-record errors are downgraded
			// to debug because they're a harmless re-create.
			if _, err := s.dns.AddRecord(ctx, parentDomain, recReq); err != nil {
				if strings.Contains(err.Error(), "already exists") {
					log.Debug().Err(err).Str("domain", req.Domain).
						Str("parent_zone", parentDomain).Str("name", subPart).
						Msg("subdomain DNS A record already present")
				} else {
					log.Error().Err(err).Str("domain", req.Domain).
						Str("parent_zone", parentDomain).Str("name", subPart).
						Msg("failed to add subdomain DNS A record — site will not resolve until heal-dns runs")
					fmt.Fprintf(os.Stderr, "warning: failed to add subdomain DNS record for %s: %v\n", req.Domain, err)
					warn("subdomain A record add failed: %v (run bzpanel heal-dns to backfill)", err)
				}
			}
			// Also add www.subdomain CNAME (e.g. www.app -> app.example.com.)
			wwwRecReq := &models.CreateRecordRequest{
				Type:  "CNAME",
				Name:  "www." + subPart,
				Value: req.Domain + ".",
				TTL:   bootstrapTTLFor("CNAME"),
			}
			if _, err := s.dns.AddRecord(ctx, parentDomain, wwwRecReq); err != nil {
				if strings.Contains(err.Error(), "already exists") {
					log.Debug().Err(err).Str("domain", req.Domain).
						Msg("subdomain www CNAME already present")
				} else {
					log.Error().Err(err).Str("domain", req.Domain).
						Str("parent_zone", parentDomain).Str("name", "www."+subPart).
						Msg("failed to add subdomain DNS www CNAME — heal-dns can backfill")
					fmt.Fprintf(os.Stderr, "warning: failed to add www DNS record for %s: %v\n", req.Domain, err)
				}
			}
			// `cname.<subdomain>` flat alias — same pattern as the
			// apex's `cname.<apex>` published by DNSService.CreateZone.
			// Multi-label subdomains are handled naturally by the
			// existing subPart machinery: for `api.abc.users.X` the
			// subPart is `api.abc.users`, so this lands as
			// `cname.api.abc.users` in the apex zone, pointing at
			// `api.abc.users.X.`. Same sub-CNAME pattern third-party
			// services ask for at every level of the hierarchy.
			cnameRecReq := &models.CreateRecordRequest{
				Type:  "CNAME",
				Name:  "cname." + subPart,
				Value: req.Domain + ".",
				TTL:   bootstrapTTLFor("CNAME"),
			}
			if _, err := s.dns.AddRecord(ctx, parentDomain, cnameRecReq); err != nil {
				if strings.Contains(err.Error(), "already exists") {
					log.Debug().Err(err).Str("domain", req.Domain).
						Msg("subdomain cname CNAME already present")
				} else {
					log.Error().Err(err).Str("domain", req.Domain).
						Str("parent_zone", parentDomain).Str("name", "cname."+subPart).
						Msg("failed to add subdomain DNS cname CNAME — heal-dns can backfill")
					fmt.Fprintf(os.Stderr, "warning: failed to add cname DNS record for %s: %v\n", req.Domain, err)
				}
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
				log.Error().Err(err).Str("domain", req.Domain).
					Msg("subdomain mail setup failed — outbound mail will be unsigned and inbound may bounce")
				fmt.Fprintf(os.Stderr, "warning: mail setup for subdomain %s failed: %v\n", req.Domain, err)
				warn("subdomain mail setup failed: %v (outbound mail will be unsigned, run bzpanel heal-mail to retry)", err)
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
			// Pre-3.1.16 this error was discarded — a `pdnsutil
			// create-zone` failure left the domain row stamped
			// "active" with NO DNS authority on the box. Mail setup
			// inside CreateZone never ran (it's chained off zone
			// creation), Let's Encrypt would later fail HTTP-01
			// because the panel-side A record was nowhere on disk,
			// and the operator had no way to find the failure short
			// of tailing journalctl. Surface it now.
			if _, zErr := s.dns.CreateZone(ctx, dnsReq); zErr != nil {
				log.Error().Err(zErr).Str("domain", req.Domain).
					Msg("apex zone create failed — DNS won't resolve, mail setup didn't run")
				warn("apex zone create failed: %v (DNS won't resolve until heal-dns runs or the operator retries)", zErr)
			}
		}
	}

	// 6. Auto-issue SSL certificate and upgrade nginx to HTTPS
	// DNS may not have propagated yet, so retry with delays
	if s.ssl != nil {
		sslEmail := s.cfg.SSLEmail
		if sslEmail == "" {
			sslEmail = "admin@betazeninfotech.com"
		}
		// Cover both the www alias AND the new `cname.<domain>` flat
		// alias on the same cert. Without `cname.<domain>` here, a
		// browser visiting `https://cname.<domain>` would resolve
		// (the CNAME is in pdns) but get an SSL handshake mismatch
		// because the cert's SAN list wouldn't include the alias.
		// Let's Encrypt verifies each SAN via HTTP-01 against the
		// already-published nginx vhost — same retry-with-backoff
		// the www case already relies on covers DNS propagation.
		sslReq := &models.IssueLetsEncryptRequest{
			Domain:            req.Domain,
			Email:             sslEmail,
			AdditionalDomains: []string{"www." + req.Domain, "cname." + req.Domain},
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
			warn("SSL issuance failed after 3 attempts: %v (DNS may not have propagated; click Issue Certificate on the SSL page once `dig @1.1.1.1 %s` returns this server's IP)", sslErr, req.Domain)
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

	// 7. DKIM safety net. setupMailServer / SetupSubdomainMail should
	// already have wired the Postfix vhost entry, Dovecot config, DNS
	// MX/SPF/DMARC records and DKIM keys for most creates, but a
	// transfer import or a race with dns-service unavailability could
	// leave /etc/opendkim/signing.table missing the new domain — in
	// which case outbound mail would go unsigned and Gmail would
	// reject it. EnsureDKIMForDomain is idempotent and cheap; calling
	// it here closes the gap without duplicating work when the DNS
	// path already populated the tables.
	//
	// 3.1.89 — STOPPED auto-creating admin@<domain> mailbox on every
	// domain create. The full mail server is still provisioned (DKIM
	// keys, Postfix vhost, Dovecot, MX/SPF/DMARC DNS records — all
	// unaffected by this change), but the operator now decides who
	// gets the first mailbox via the Email page or the
	// `email:write` external API endpoint. Rationale: many operators
	// create domains for static sites / Deploy Software apps that
	// will never send or receive mail — auto-provisioning a 1024 MB
	// admin@ mailbox + leaking a one-shot password to the create
	// response was wasted disk + a credential the operator had to
	// either save or revoke. Mail-only customers still get the same
	// flow, they just create their admin@ mailbox in one explicit
	// click after Add Domain.
	if s.email != nil {
		s.email.EnsureDKIMForDomain(ctx, req.Domain)
	}
	// AdminMailboxPassword stays as a transient field on the Domain
	// model for backwards compatibility but is now always empty —
	// frontend consumers that watched for it (the bulk-upload result
	// table, the Add Domain modal's "Save these credentials" prompt)
	// already gate on `if adminMailboxPassword` and gracefully omit
	// the section, so no UI changes are required.
	var adminPass string

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
	// domain-type AND whois (registrar / registered / expires /
	// nameservers) onto the just-inserted record so the Domains page
	// + the registrar-expiry widget are populated without a manual
	// "Refresh whois" click.
	//
	// Pre-3.1.16 the whois fields the preflight gathered were
	// THROWN AWAY here — only domain_type / ip_matches_server / etc
	// were stamped. Operators who left the registrar fields blank in
	// the Add Domain form (the common case) ended up with rows that
	// had no expiry tracking and never showed up in the dashboard's
	// "expiring soon" widget until the next manual refresh. Now the
	// whois data lands automatically on every create entry point —
	// single Add Domain modal, programmatic API, AND bulk upload —
	// because all three route through this Create flow.
	//
	// Operator-provided values still WIN over whois (the form's
	// registrar/dates aren't overwritten when populated). Whois only
	// fills the blanks so a manual override can't be silently
	// clobbered by a registrar-side typo.
	//
	// Failures are swallowed — a brand-new domain often hasn't
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
		// Whois fill-in: only when the operator's form left the field
		// blank. Pre-flight's whois fields are already YYYY-MM-DD-
		// normalised by parseFlexibleDate, so the date strings round-
		// trip cleanly through the Domain document.
		whoisStamped := false
		if domain.Registrar == "" && pf.Registrar != "" {
			set["registrar"] = pf.Registrar
			domain.Registrar = pf.Registrar
			whoisStamped = true
		}
		if domain.RegisteredOn == nil && pf.RegisteredOn != "" {
			if t := parseFlexibleDate(pf.RegisteredOn); t != nil {
				set["registered_on"] = *t
				domain.RegisteredOn = t
				whoisStamped = true
			}
		}
		if domain.ExpiresOn == nil && pf.ExpiresOn != "" {
			if t := parseFlexibleDate(pf.ExpiresOn); t != nil {
				set["expires_on"] = *t
				domain.ExpiresOn = t
				whoisStamped = true
			}
		}
		if whoisStamped {
			now := time.Now()
			set["whois_synced_at"] = now
			domain.WhoisSyncedAt = &now
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

	// Stamp the in-memory-only fields so the caller (single handler,
	// bulk-upload row, programmatic API) sees them in the create
	// response. Both fields are bson:"-" so they never persist to
	// Mongo — fetching the domain by id later returns it without
	// these (which is correct: SetupWarnings is a one-shot status
	// from THIS create, AdminMailboxPassword is the auto-generated
	// secret that the operator should save to a vault now).
	domain.SetupWarnings = setupWarnings
	domain.AdminMailboxPassword = adminPass

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
		"environment": true,
	}
	setFields := bson.M{"updated_at": time.Now()}
	for k, v := range updates {
		if !allowed[k] {
			continue
		}
		// Environment is the one free-text field here — clamp it to the
		// known tier set so an inline-edit PUT can't persist a typo.
		if k == "environment" {
			env, _ := v.(string)
			setFields[k] = NormalizeDomainEnvironment(env)
			continue
		}
		setFields[k] = v
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

	// 1. Fully remove the nginx vhost. We previously wrote a "site not
	// deployed" placeholder vhost here so nginx wouldn't fall through to
	// a different domain's server block on alphabetical order and emit
	// CERT_COMMON_NAME_INVALID in the browser — but the placeholder also
	// kept `server_name <domain>;` on disk, which leaked the domain back
	// into transfer Discover and re-imported it on the destination
	// (the user's "deleted domain reappears after transfer" report).
	//
	// Removing the vhost wins on net: cleaner state, no ghost in the
	// transfer wizard, and visitors hitting a deleted domain just land
	// on nginx's catch-all default — which is the correct outcome for
	// "this domain was deleted." Operators who want a custom 410 page
	// for deleted domains can configure the default vhost once, server-
	// wide, instead of leaving per-domain placeholder shells behind.
	agent.DeleteVhost(ctx, domain.Domain)

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
			// Subdomain: clean up EVERY record SetupSubdomainMail +
			// the create flow wrote into the parent zone for this
			// label.  Pre-3.0.41 only the A + www CNAME were
			// removed; MX, SPF (TXT), DMARC (TXT _dmarc.<sub>), and
			// the optional standalone DKIM TXT
			// (mail._domainkey.<sub>) all stayed orphaned. After
			// re-creating the same subdomain, you'd see duplicate
			// entries in the WHM DNS Records page — and stale SPF
			// pointed at an old IP after a server move.
			//
			// We match by EXACT name only (subPart, www.subPart,
			// _dmarc.subPart, mail._domainkey.subPart) — a regex
			// over "starts with subPart" would over-delete and
			// nuke records belonging to deeper subdomains
			// (e.g. deleting `users.example.com` should NOT
			// remove the `api.users.example.com` A record).
			subPart := strings.TrimSuffix(domain.Domain, "."+parentDomain)
			subPartTargets := map[string]bool{
				subPart:                       true,
				"www." + subPart:              true,
				"cname." + subPart:            true,
				"_dmarc." + subPart:           true,
				"mail._domainkey." + subPart:  true,
			}
			records, _ := s.dns.ListRecords(ctx, parentDomain)
			for _, r := range records {
				if subPartTargets[r.Name] {
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

	// 5. Remove SSL certificate — DB row AND on-disk artifacts. We
	// previously preserved /etc/letsencrypt/{live,archive,renewal}/<d>
	// to avoid Let's Encrypt's "5 duplicate certs / week" rate limit
	// on re-create, but that left the destination's transfer
	// rediscovery flow able to detect the domain via cert files and
	// re-import it. Aligning Delete with the user's stated intent —
	// "fully remove from server, keep only files and folders" — means
	// the cert files go too. The rate-limit risk is real but narrow:
	// it only kicks in if you delete and re-create the same domain
	// 5+ times within a week, which doesn't happen in normal
	// operation. Operators who hit that case can use a manual
	// `certbot certificates` import or wait out the window.
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"rm -rf /etc/letsencrypt/live/%s /etc/letsencrypt/archive/%s /etc/letsencrypt/renewal/%s.conf /etc/ssl/custom/%s",
		domain.Domain, domain.Domain, domain.Domain, domain.Domain))
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

	// 7. Unregister the domain from OpenDKIM's config tables AND remove
	// the keypair under /etc/opendkim/keys/<domain>. Previously the
	// keypair was preserved so re-creating the domain could reuse the
	// existing DNS TXT selector without republishing — but that left
	// panel-managed crypto material on disk after a delete the
	// operator explicitly intended as a full wipe, and contributed to
	// the "deleted domain reappears" leak surface area. Aligning with
	// the user's "fully remove, keep only files and folders" rule:
	// the DKIM keys go. Re-creating the domain mints a fresh selector,
	// which the DNS-zone create flow publishes automatically.
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/%s/d' /etc/opendkim/signing.table 2>/dev/null", escapedDomain))
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/%s/d' /etc/opendkim/key.table 2>/dev/null", escapedDomain))
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s$/d' /etc/opendkim/trusted.hosts 2>/dev/null", escapedDomain))
	agent.RunCommand(ctx, "rm", "-rf", fmt.Sprintf("/etc/opendkim/keys/%s", domain.Domain))
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

	// 10b. Detach this domain from any Deploy Software project_service that
	// references it — WITHOUT deleting the project (the operator may still
	// run it on another domain). This closes the "deleted domain reappears
	// after migration" leak: a project_service row whose primary_domain /
	// alias_domains still names a now-deleted domain is the exact reference
	// the transfer's materializeReferencedDomains step re-inflates into a
	// fresh domain row on the destination. It also stops
	// lookupProjectServiceByDomain (SSL issue + vhost dispatch) from later
	// matching the stale binding and rebuilding a reverse-proxy vhost to a
	// dead upstream if the domain is re-added. The project + service rows
	// survive; they just stop claiming this hostname.
	psNow := time.Now()
	psCol := s.db.Collection(database.ColProjectServices)
	psCol.UpdateMany(ctx,
		bson.M{"alias_domains": domain.Domain},
		bson.M{"$pull": bson.M{"alias_domains": domain.Domain}, "$set": bson.M{"updated_at": psNow}})
	psCol.UpdateMany(ctx,
		bson.M{"primary_domain": domain.Domain},
		bson.M{"$set": bson.M{"primary_domain": "", "updated_at": psNow}})

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

	// Outbound webhook fan-out — emit AFTER the row is gone so a vendor
	// integration can reconcile against the panel state and find the
	// record already deleted. Fire-and-forget; errors are logged in the
	// dispatcher.
	EmitEvent(ctx, "domain.deleted", LookupTenantIDForUsername(ctx, s.db, domain.User), map[string]any{
		"id":     domain.ID.Hex(),
		"domain": domain.Domain,
		"user":   domain.User,
	})
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

	// Recreate vhost with new PHP version — use SSL template if SSL is
	// active. 3.1.83 — preserve domain.DocumentRoot through the rewrite
	// so switching PHP no longer silently resets a custom docroot
	// (Laravel /public, Next.js /out etc.) back to the default
	// /public_html path. Pre-fix, an operator who pinned a docroot then
	// changed PHP would lose the override without a warning.
	vhostCfg := &agent.VhostConfig{
		Domain:       domain.Domain,
		User:         domain.User,
		PHPVersion:   phpVersion,
		DocumentRoot: domain.DocumentRoot,
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

// SetDocumentRoot changes the nginx `root` directive for a domain in
// place. Empty string resets the docroot to the cPanel-default
// /home/<user>/domains/<domain>/public_html. Non-empty must be an
// absolute path — the agent enforces that nginx itself accepts it on
// reload, so a typo (wrong path, no directory) fails the change
// without leaving the domain in a broken state.
//
// Rewrites HTTP or SSL vhost depending on Domain.SSLActive, reloads
// nginx, then persists the new value to mongo. On nginx-reload
// failure the agent restores the previous config so the public site
// stays up; we surface that error and skip the mongo write so the
// stored value continues to match what's actually on disk.
//
// Survives server-to-server transfer via the transfer pipeline's
// healMissingVhosts (also patched in 3.1.83 to forward d.DocumentRoot).
func (s *DomainService) SetDocumentRoot(ctx context.Context, id string, docRoot string) error {
	domain, err := s.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("domain not found: %w", err)
	}
	docRoot = strings.TrimSpace(docRoot)
	if docRoot != "" && !strings.HasPrefix(docRoot, "/") {
		return fmt.Errorf("document_root must be an absolute path (e.g. /home/user/laravel-app/public)")
	}
	vhostCfg := &agent.VhostConfig{
		Domain:       domain.Domain,
		User:         domain.User,
		PHPVersion:   domain.PHPVersion,
		DocumentRoot: docRoot, // empty → CreateVhost fills the default
	}
	if domain.SSLActive {
		if err := agent.CreateVhostWithSSL(ctx, vhostCfg); err != nil {
			return fmt.Errorf("failed to rewrite SSL vhost: %w", err)
		}
	} else {
		if err := agent.CreateVhost(ctx, vhostCfg); err != nil {
			return fmt.Errorf("failed to rewrite vhost: %w", err)
		}
	}
	col := s.db.Collection(database.ColDomains)
	_, err = col.UpdateOne(ctx, bson.M{"_id": domain.ID}, bson.M{
		"$set": bson.M{"document_root": docRoot, "updated_at": time.Now()},
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
	// Hierarchical re-sort: apex first, then its subdomains immediately
	// underneath. Operators see apex example.com → app.example.com →
	// api.app.example.com grouped together rather than the creation-
	// time jumble where children show above their parent because the
	// operator added them later. Re-sort is stable so domains that
	// hash to the same reverse-label key (shouldn't happen — domain
	// is uniquely indexed — but defensive) keep their creation order.
	SortDomainsHierarchical(domains)
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
	// Tenant guard: a vendor calling /cpanel/domains/:id/registration
	// can only edit registration on domains they own. Without this the
	// service blindly UpdateById'd, which would let a vendor pass any
	// other tenant's domain id and rewrite their registrar/expiry.
	// GetByID applies the standard scope check (vendor_owner bypasses
	// it via constants.IsTenantScoped).
	if _, gerr := s.GetByID(ctx, id); gerr != nil {
		return nil, fmt.Errorf("domain not found")
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

// ExpiringBucketCounts returns the number of visible domains whose
// expiry falls within each canonical bucket (60, 45, 30, 15, 7, 5,
// 4, 3, 2, 1 — see services.ExpiryBuckets). Drives the filter-pill
// counts on the dashboard "Domains Expiring Soon" widget.
//
// Buckets are CUMULATIVE — the 60-day bucket counts every domain
// expiring within 60 days INCLUDING the ones inside the 30 / 15 / 7
// buckets. Operators reading the widget see "60: 12 · 30: 5 · 7: 1"
// and immediately understand "12 domains need attention in the next
// 60 days, 5 of them are inside the next 30 days, 1 is inside a
// week" without doing the subtraction in their head.
//
// Tenant scope is applied so vendors only see their own counts;
// platform owner sees everything.
func (s *DomainService) ExpiringBucketCounts(ctx context.Context, buckets []int) (map[int]int, error) {
	col := s.db.Collection(database.ColDomains)
	baseFilter := bson.M{"expires_on": bson.M{"$ne": nil}}
	if scope := GetCallerScope(ctx); scope != nil {
		baseFilter = scope.ApplyTo(ctx, s.db, "user", baseFilter)
	}

	now := time.Now()
	out := make(map[int]int, len(buckets))
	for _, b := range buckets {
		if b <= 0 {
			continue
		}
		cutoff := now.Add(time.Duration(b) * 24 * time.Hour)
		f := bson.M{}
		for k, v := range baseFilter {
			f[k] = v
		}
		// Merge the expires_on $ne with the per-bucket $gte/$lte. We
		// also bound the lower side to NOW so an already-expired
		// domain doesn't get counted in every bucket and inflate the
		// numbers.
		f["expires_on"] = bson.M{"$ne": nil, "$gte": now, "$lte": cutoff}
		n, err := col.CountDocuments(ctx, f)
		if err != nil {
			return nil, err
		}
		out[b] = int(n)
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

// WhoisLookup resolves registrar / created / expiry / nameservers for
// a domain. RDAP first (HTTPS + structured JSON, RFC 9224 — works for
// every gTLD and the modern CC-TLDs including .in), system `whois`
// fallback for the few TLDs that haven't published RDAP yet (and so
// our regex parser can keep doing what it always did for them).
//
// The fallback ordering matters because the user-reported regression
// — `.in` whois returning empty on this box — is exactly the case
// RDAP fixes. RDAP also removes the package-installed-but-blocked
// failure mode where a firewall blocks port 43 (whois) but allows
// 443 outbound; RDAP rides 443 like every other API call.
func (s *DomainService) WhoisLookup(ctx context.Context, domain string) (*WhoisResult, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}

	// 1. RDAP. Returns nil error on a successful structured fetch even
	//    if some fields came back empty — TLD coverage is what we
	//    care about, not field completeness.
	if w, err := rdapLookup(ctx, domain); err == nil && w != nil {
		return w, nil
	}

	// 2. Fall back to system whois.
	if _, err := agent.RunCommand(ctx, "which", "whois"); err != nil {
		return nil, fmt.Errorf("whois command not available on this server — install the `whois` package, or note that the registry's RDAP service was unreachable")
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

// ---------------------------------------------------------------------------
// RDAP — modern HTTPS+JSON replacement for whois (RFC 9224)
// ---------------------------------------------------------------------------

// rdapBootstrap is IANA's mapping of TLDs → authoritative RDAP server
// URLs. Cached for 24 hours since it changes only when a registry
// flips RDAP servers (rare). Refresh on first use after expiry.
type rdapBootstrap struct {
	// Services is the raw `[ [tlds...], [urls...] ] ` array IANA
	// publishes. We rebuild a flat tld→url map after fetch.
	Services [][][]string `json:"services"`

	tldMap   map[string]string
	loadedAt time.Time
}

var (
	rdapBootstrapMu  sync.Mutex
	rdapBootstrapVal *rdapBootstrap
)

// rdapResponse is the subset of an RDAP `domain` response we read.
// Spec: https://datatracker.ietf.org/doc/html/rfc9083
type rdapResponse struct {
	Events []struct {
		EventAction string `json:"eventAction"` // "registration", "expiration", ...
		EventDate   string `json:"eventDate"`   // RFC 3339
	} `json:"events"`
	Entities []struct {
		Roles      []string        `json:"roles"`     // includes "registrar"
		VCardArray []json.RawMessage `json:"vcardArray"`
		PublicIDs  []struct {
			Type       string `json:"type"`
			Identifier string `json:"identifier"`
		} `json:"publicIds"`
	} `json:"entities"`
	Nameservers []struct {
		LDHName string `json:"ldhName"`
	} `json:"nameservers"`
}

// rdapLookup hits the authoritative RDAP server for the domain's TLD
// and converts the JSON into a WhoisResult. Returns (nil, error) on
// any of: bootstrap unreachable, TLD not on RDAP, server 4xx/5xx,
// JSON parse failure. Caller falls back to whois on error.
func rdapLookup(ctx context.Context, domain string) (*WhoisResult, error) {
	tld := domainTLD(domain)
	if tld == "" {
		return nil, fmt.Errorf("no TLD")
	}
	server, err := rdapServerForTLD(ctx, tld)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(server, "/") + "/domain/" + domain

	reqCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/rdap+json")
	req.Header.Set("User-Agent", "BetazenServerPanel/RDAP")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("rdap %s: HTTP %d", server, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	if err != nil {
		return nil, err
	}

	var doc rdapResponse
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}

	out := &WhoisResult{
		Raw:       string(body),
		FetchedAt: time.Now(),
	}
	for _, e := range doc.Events {
		switch strings.ToLower(e.EventAction) {
		case "registration":
			out.RegisteredOn = e.EventDate
		case "expiration":
			out.ExpiresOn = e.EventDate
		}
	}
	for _, ent := range doc.Entities {
		isRegistrar := false
		for _, r := range ent.Roles {
			if strings.EqualFold(r, "registrar") {
				isRegistrar = true
				break
			}
		}
		if !isRegistrar {
			continue
		}
		// Prefer the vCard FN ("formatted name") field, fall back to
		// the publicIDs identifier (the IANA registrar ID — useful in
		// the UI when the FN happens to be missing).
		if name := vcardFN(ent.VCardArray); name != "" {
			out.Registrar = name
		} else if len(ent.PublicIDs) > 0 {
			out.Registrar = ent.PublicIDs[0].Identifier
		}
	}
	seen := map[string]bool{}
	for _, ns := range doc.Nameservers {
		host := strings.ToLower(strings.TrimSpace(ns.LDHName))
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		out.Nameservers = append(out.Nameservers, host)
	}
	// If RDAP responded but nothing useful was parsed (very unusual TLD
	// shape), let the caller fall back to whois.
	if out.Registrar == "" && out.RegisteredOn == "" && out.ExpiresOn == "" && len(out.Nameservers) == 0 {
		return nil, fmt.Errorf("rdap returned no parseable fields")
	}
	return out, nil
}

// domainTLD returns the last label of the domain, lower-cased — `.in`
// for `iafoundation.in`, `.com` for `betazeninfotech.com`. Multi-part
// TLDs like `.co.uk` resolve to just `uk` here, which is fine because
// IANA's bootstrap maps `uk` directly to Nominet's RDAP server.
func domainTLD(domain string) string {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if domain == "" {
		return ""
	}
	if i := strings.LastIndex(domain, "."); i >= 0 {
		return domain[i+1:]
	}
	return ""
}

// rdapServerForTLD pulls the authoritative RDAP server URL for `tld`
// from the cached IANA bootstrap, refreshing it when older than 24h.
// Falls back to a public bootstrap mirror at `rdap.org` when IANA's
// own data file can't be fetched (very rare; usually means egress
// firewall blocks).
func rdapServerForTLD(ctx context.Context, tld string) (string, error) {
	rdapBootstrapMu.Lock()
	defer rdapBootstrapMu.Unlock()

	if rdapBootstrapVal == nil || time.Since(rdapBootstrapVal.loadedAt) > 24*time.Hour {
		bs, err := fetchRDAPBootstrap(ctx)
		if err != nil {
			// Last-resort universal redirector. rdap.org runs a public
			// RFC 9224 implementation that 302's to the right server.
			return "https://rdap.org", nil
		}
		rdapBootstrapVal = bs
	}
	if url, ok := rdapBootstrapVal.tldMap[tld]; ok && url != "" {
		return url, nil
	}
	// TLD not in IANA's bootstrap (some CC-TLDs lag). Let rdap.org try.
	return "https://rdap.org", nil
}

// fetchRDAPBootstrap downloads + parses IANA's dns.json registry.
func fetchRDAPBootstrap(ctx context.Context) (*rdapBootstrap, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, "GET",
		"https://data.iana.org/rdap/dns.json", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("iana rdap bootstrap: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	bs := &rdapBootstrap{}
	if err := json.Unmarshal(body, bs); err != nil {
		return nil, err
	}
	bs.tldMap = make(map[string]string, 1500)
	for _, svc := range bs.Services {
		if len(svc) < 2 {
			continue
		}
		tlds := svc[0]
		urls := svc[1]
		if len(urls) == 0 {
			continue
		}
		// Pick the first HTTPS URL when one exists; otherwise the
		// first URL of any scheme.
		pick := ""
		for _, u := range urls {
			if strings.HasPrefix(u, "https://") {
				pick = u
				break
			}
		}
		if pick == "" {
			pick = urls[0]
		}
		for _, tld := range tlds {
			bs.tldMap[strings.ToLower(strings.TrimSpace(tld))] = pick
		}
	}
	bs.loadedAt = time.Now()
	return bs, nil
}

// vcardFN extracts the "FN" (formatted name) field from an RDAP
// vCardArray. The shape is jCard (RFC 7095): the first element is
// the literal string "vcard", followed by an array of property
// triples ["fn", {}, "text", "Registrar Name"]. Returns "" when the
// FN can't be located — caller falls back to publicIDs.
func vcardFN(arr []json.RawMessage) string {
	if len(arr) < 2 {
		return ""
	}
	// The second element is the property array. Decode it as
	// [][]json.RawMessage so we can scan property names without
	// type-asserting deeply nested mixed slices.
	var props [][]json.RawMessage
	if err := json.Unmarshal(arr[1], &props); err != nil {
		return ""
	}
	for _, p := range props {
		if len(p) < 4 {
			continue
		}
		var name string
		if err := json.Unmarshal(p[0], &name); err != nil {
			continue
		}
		if !strings.EqualFold(name, "fn") {
			continue
		}
		var val string
		if err := json.Unmarshal(p[3], &val); err == nil {
			return val
		}
	}
	return ""
}
