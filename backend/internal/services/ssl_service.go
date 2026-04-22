package services

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
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

type SSLService struct {
	db       *mongo.Database
	notifier *NotifierService
}

func NewSSLService(db *mongo.Database) *SSLService {
	return &SSLService{db: db}
}

// SetNotifier wires the shared NotifierService so Issue + Renew can
// email the owning vendor on success / failure. Called from main.go
// after NotifierService is constructed. Leaving it nil is safe — the
// service simply doesn't send.
func (s *SSLService) SetNotifier(n *NotifierService) { s.notifier = n }

func (s *SSLService) List(ctx context.Context) ([]models.SSLCertificate, error) {
	col := s.db.Collection(database.ColSSLCerts)
	filter := bson.M{}
	if scope := GetCallerScope(ctx); scope != nil {
		filter = scope.ApplyDomainScope(ctx, s.db, "domain", filter)
	}
	cursor, err := col.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "domain", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var certs []models.SSLCertificate
	if err := cursor.All(ctx, &certs); err != nil {
		return nil, err
	}

	// Compute days remaining dynamically
	now := time.Now()
	for i := range certs {
		if certs[i].ExpiresAt != nil {
			days := int(math.Ceil(certs[i].ExpiresAt.Sub(now).Hours() / 24))
			if days < 0 {
				days = 0
			}
			certs[i].DaysRemaining = days
		}
	}

	if certs == nil {
		certs = []models.SSLCertificate{}
	}
	return certs, nil
}

func (s *SSLService) GetByDomain(ctx context.Context, domain string) (*models.SSLCertificate, error) {
	// Tenant gate: every per-cert op (Renew, Revoke, ForceSSL, Delete,
	// plus cpanel Get) funnels through here, so one AssertOwnsDomain
	// is enough to stop a vendor reading or mutating another tenant's
	// certs by guessing the domain name. Matches the pattern on
	// database_service and the new wordpress_service id lookups.
	if scope := GetCallerScope(ctx); scope != nil {
		if err := scope.AssertOwnsDomain(ctx, s.db, domain); err != nil {
			return nil, fmt.Errorf("SSL certificate not found")
		}
	}
	col := s.db.Collection(database.ColSSLCerts)
	var cert models.SSLCertificate
	if err := col.FindOne(ctx, bson.M{"domain": domain}).Decode(&cert); err != nil {
		return nil, err
	}
	if cert.ExpiresAt != nil {
		days := int(math.Ceil(cert.ExpiresAt.Sub(time.Now()).Hours() / 24))
		if days < 0 {
			days = 0
		}
		cert.DaysRemaining = days
	}
	return &cert, nil
}

// friendlyCertbotError unwraps certbot's multi-line stderr into the
// short, actionable message an operator actually needs. The stock
// wrapping looks like:
//
//	certbot failed: command failed: exit status 1: Saving debug log …
//	An unexpected error occurred:
//	too many failed authorizations (5) for "foo.tld" in the last 1h0m0s,
//	retry after 2026-04-20 20:06:33 UTC: see …
//
// We surface only the "too many failed authorizations … retry after …"
// line (plus a couple of other well-known LE failure modes) so the UI
// toast stays readable. On anything we don't recognise, we return the
// original error so we don't hide unknown failures.
func friendlyCertbotError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()

	// Rate limit — failed authorizations per hostname per account per hour.
	if m := reRateLimitFailedAuth.FindStringSubmatch(msg); len(m) >= 3 {
		return fmt.Errorf("Let's Encrypt rate limit: too many failed attempts for %q in the last hour — retry after %s UTC", m[1], m[2])
	}
	// Rate limit — duplicate certs per registered domain (5 / week).
	if strings.Contains(msg, "too many certificates") && strings.Contains(msg, "exact set of domains") {
		return fmt.Errorf("Let's Encrypt rate limit: too many duplicate certificates issued for this domain in the last 7 days — wait or revoke an existing cert before retrying")
	}
	// DNS / challenge reachability.
	if strings.Contains(msg, "DNS problem") || strings.Contains(msg, "NXDOMAIN") {
		return fmt.Errorf("DNS lookup failed for the domain — verify its A record points to this server and has propagated")
	}
	if strings.Contains(msg, "Connection refused") || strings.Contains(msg, "Timeout during connect") {
		return fmt.Errorf("Let's Encrypt could not reach this server on port 80 — check firewall / that the domain's A record resolves here")
	}
	if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "Invalid response") {
		return fmt.Errorf("ACME challenge failed — nginx served the wrong content at /.well-known/acme-challenge/. Re-issue after confirming /var/www/certbot is reachable through the vhost")
	}
	// Fall through: short-form the certbot exit-status preamble but keep
	// the core message so we never fully hide unknown failures.
	short := msg
	if i := strings.Index(short, "An unexpected error occurred:"); i >= 0 {
		short = strings.TrimSpace(short[i+len("An unexpected error occurred:"):])
	}
	// Strip the "see logfile / community" boilerplate.
	for _, cut := range []string{"\nAsk for help", "\nSee the logfile", "\nsee https://"} {
		if i := strings.Index(short, cut); i >= 0 {
			short = short[:i]
		}
	}
	short = strings.TrimSpace(short)
	if short == "" {
		return err
	}
	return fmt.Errorf("certbot failed: %s", short)
}

// reRateLimitFailedAuth captures the hostname + retry-after timestamp
// from Let's Encrypt's "too many failed authorizations" error so the
// friendly message can echo them back without the surrounding stack.
var reRateLimitFailedAuth = regexp.MustCompile(`too many failed authorizations \(\d+\) for "([^"]+)".*?retry after ([0-9\-: ]+) UTC`)

func (s *SSLService) IssueLetsEncrypt(ctx context.Context, req *models.IssueLetsEncryptRequest) (*models.SSLCertificate, error) {
	// Reuse the existing cert when one is already on disk (e.g. from a
	// previous deployment of the same domain). Saves a certbot round-trip
	// AND avoids burning a Let's Encrypt rate-limit slot (5 dup certs /
	// week per registered domain). The standard certbot renewal cron
	// keeps it fresh; if you genuinely want a brand-new cert, revoke
	// first via SSLService.Revoke.
	if !req.Wildcard && agent.LetsEncryptCertExists(req.Domain) {
		// fall through to DB upsert + vhost upgrade below
	} else if err := agent.IssueLetsEncrypt(ctx, req.Domain, req.Email, req.AdditionalDomains, req.Wildcard); err != nil {
		return nil, friendlyCertbotError(err)
	}

	// Parse certificate info
	issuedAt, expiresAt, issuer, serial := parseCertbotInfo(ctx, req.Domain)

	now := time.Now()
	domains := []string{req.Domain}
	domains = append(domains, req.AdditionalDomains...)

	cert := models.SSLCertificate{
		Domain:    req.Domain,
		Issuer:    issuer,
		Type:      "letsencrypt",
		Domains:   domains,
		IssuedAt:  issuedAt,
		ExpiresAt: expiresAt,
		AutoRenew: true,
		Wildcard:  req.Wildcard,
		KeyType:   "RSA",
		CertPath:  fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", req.Domain),
		KeyPath:   fmt.Sprintf("/etc/letsencrypt/live/%s/privkey.pem", req.Domain),
		SerialNumber: serial,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if expiresAt != nil {
		cert.DaysRemaining = int(math.Ceil(expiresAt.Sub(now).Hours() / 24))
	}

	col := s.db.Collection(database.ColSSLCerts)
	result, err := col.InsertOne(ctx, cert)
	if err != nil {
		return nil, err
	}
	cert.ID = result.InsertedID.(primitive.ObjectID)

	// Update domain ssl_active
	s.db.Collection(database.ColDomains).UpdateOne(ctx, bson.M{"domain": req.Domain}, bson.M{
		"$set": bson.M{"ssl_active": true, "ssl_expires": expiresAt, "updated_at": now},
	})

	// Upgrade nginx config to include HTTPS (443) block. Four shapes a
	// domain can take: (1) a deployed app — recreate the static or
	// reverse-proxy SSL vhost so its 443 block matches the app's served
	// dir / port; (2) a Deploy-Software project_service — reverse-proxy
	// SSL to the project's upstream port; (3) a regular file-based
	// domain — PHP-FPM SSL via CreateVhostWithSSL; (4) neither — leave
	// nginx alone and let the operator reload manually.
	//
	// Case (2) was missing originally: lookupAppByDomain returns nil for
	// project-backed domains, so the code fell through to (3) and wrote
	// a PHP-FPM SSL vhost rooted at /home/<user>/domains/<domain>/
	// public_html — serving the "Welcome" placeholder over HTTPS and
	// ignoring the project's upstream. After issuing SSL, the operator's
	// Deploy Software project "switched to normal website." Delete-SSL
	// then rebuilt the HTTP-only reverse-proxy and the project came
	// back — exactly the symptom reported.
	if app, err := s.lookupAppByDomain(ctx, req.Domain); err == nil && app != nil {
		isStatic := app.AppType == "static"
		var upgradeErr error
		if isStatic {
			servedDir := app.InstallPath
			if app.Framework != "" {
				if p, ok := lookupPreset(app.Framework); ok && p.StaticDir != "" {
					servedDir = filepath.Join(app.InstallPath, p.StaticDir)
				}
			}
			upgradeErr = agent.CreateStaticVhostWithSSL(ctx, req.Domain, servedDir, cert.CertPath, cert.KeyPath)
		} else if app.Port > 0 {
			upgradeErr = agent.CreateReverseProxyWithSSL(ctx, &agent.VhostConfig{
				Domain:   req.Domain,
				Port:     app.Port,
				CertPath: cert.CertPath,
				KeyPath:  cert.KeyPath,
			})
		}
		if upgradeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to upgrade app vhost to SSL for %s: %v\n", req.Domain, upgradeErr)
		}
	} else if svc, err := s.lookupProjectServiceByDomain(ctx, req.Domain); err == nil && svc != nil && svc.Port > 0 {
		if err := agent.CreateReverseProxyWithSSL(ctx, &agent.VhostConfig{
			Domain:   req.Domain,
			Port:     svc.Port,
			CertPath: cert.CertPath,
			KeyPath:  cert.KeyPath,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to upgrade project-service vhost to SSL for %s: %v\n", req.Domain, err)
		}
	} else if domain, err := s.lookupDomain(ctx, req.Domain); err == nil {
		vhostCfg := &agent.VhostConfig{
			Domain:     domain.Domain,
			User:       domain.User,
			PHPVersion: domain.PHPVersion,
			CertPath:   cert.CertPath,
			KeyPath:    cert.KeyPath,
		}
		if err := agent.CreateVhostWithSSL(ctx, vhostCfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to upgrade nginx to SSL for %s: %v\n", req.Domain, err)
		}
		// Update WordPress installations to use HTTPS
		s.updateWordPressURLs(ctx, domain.Domain, "https")
	}

	// SSL is live — tell the owning vendor. This covers both first
	// issue (manually from WHM SSL page + auto-issue during domain
	// create) AND renewals that flow through this same path. The
	// notifier looks up the owner from the Domain doc; background
	// context so a slow relay doesn't delay the HTTP response.
	if s.notifier != nil {
		issuerName := cert.Issuer
		if issuerName == "" {
			issuerName = "Let's Encrypt"
		}
		// "automated" = renewals. First issue is triggered from the UI
		// or auto-during-domain-create; neither counts as automated
		// from the vendor's perspective. We don't currently have a way
		// to distinguish a programmatic renew call from a manual one
		// at this layer, so automated stays false — the vendor will
		// still see "SSL issued for <domain>" which reads correctly
		// either way.
		go func(dom, iss string, exp *time.Time, auto bool) {
			_ = s.notifier.NotifySSLActive(
				context.Background(),
				dom,
				iss,
				exp,
				auto,
				false,
			)
		}(req.Domain, issuerName, cert.ExpiresAt, cert.AutoRenew)
	}

	return &cert, nil
}

func (s *SSLService) UploadCustom(ctx context.Context, req *models.UploadCustomCertRequest) (*models.SSLCertificate, error) {
	// Write certificate files
	certDir := fmt.Sprintf("/etc/ssl/custom/%s", req.Domain)
	os.MkdirAll(certDir, 0755)

	certPath := filepath.Join(certDir, "cert.pem")
	keyPath := filepath.Join(certDir, "key.pem")
	if err := os.WriteFile(certPath, []byte(req.Certificate), 0644); err != nil {
		return nil, fmt.Errorf("failed to write certificate: %w", err)
	}
	if err := os.WriteFile(keyPath, []byte(req.PrivateKey), 0600); err != nil {
		return nil, fmt.Errorf("failed to write private key: %w", err)
	}

	bundlePath := ""
	if req.CABundle != "" {
		bundlePath = filepath.Join(certDir, "ca-bundle.pem")
		os.WriteFile(bundlePath, []byte(req.CABundle), 0644)
	}

	// Parse certificate to get expiry and issuer
	issuer := "Custom"
	var expiresAt *time.Time
	result, err := agent.RunCommand(ctx, "openssl", "x509", "-noout", "-enddate", "-issuer", "-in", certPath)
	if err == nil {
		lines := strings.Split(result.Output, "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "notAfter=") {
				if t, err := time.Parse("Jan  2 15:04:05 2006 MST", strings.TrimPrefix(line, "notAfter=")); err == nil {
					expiresAt = &t
				}
			}
			if strings.HasPrefix(line, "issuer=") {
				issuer = strings.TrimPrefix(line, "issuer=")
			}
		}
	}

	now := time.Now()
	cert := models.SSLCertificate{
		Domain:       req.Domain,
		Issuer:       issuer,
		Type:         "custom",
		Domains:      []string{req.Domain},
		IssuedAt:     &now,
		ExpiresAt:    expiresAt,
		AutoRenew:    false,
		KeyType:      "RSA",
		CertPath:     certPath,
		KeyPath:      keyPath,
		CABundlePath: bundlePath,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if expiresAt != nil {
		cert.DaysRemaining = int(math.Ceil(expiresAt.Sub(now).Hours() / 24))
	}

	col := s.db.Collection(database.ColSSLCerts)
	res, err := col.InsertOne(ctx, cert)
	if err != nil {
		return nil, err
	}
	cert.ID = res.InsertedID.(primitive.ObjectID)

	// Update domain ssl_active
	s.db.Collection(database.ColDomains).UpdateOne(ctx, bson.M{"domain": req.Domain}, bson.M{
		"$set": bson.M{"ssl_active": true, "ssl_expires": expiresAt, "updated_at": now},
	})

	// Upgrade nginx config to include HTTPS with custom cert paths
	if domain, err := s.lookupDomain(ctx, req.Domain); err == nil {
		vhostCfg := &agent.VhostConfig{
			Domain:     domain.Domain,
			User:       domain.User,
			PHPVersion: domain.PHPVersion,
			CertPath:   certPath,
			KeyPath:    keyPath,
		}
		if err := agent.CreateVhostWithSSL(ctx, vhostCfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to upgrade nginx to SSL for %s: %v\n", req.Domain, err)
		}
		s.updateWordPressURLs(ctx, domain.Domain, "https")
	}

	return &cert, nil
}

func (s *SSLService) Renew(ctx context.Context, domain string) (*models.SSLCertificate, error) {
	if err := agent.RenewCertificate(ctx, domain); err != nil {
		return nil, fmt.Errorf("renewal failed: %w", err)
	}

	_, expiresAt, _, _ := parseCertbotInfo(ctx, domain)

	now := time.Now()
	col := s.db.Collection(database.ColSSLCerts)
	setFields := bson.M{"updated_at": now}
	if expiresAt != nil {
		setFields["expires_at"] = expiresAt
		setFields["days_remaining"] = int(math.Ceil(expiresAt.Sub(now).Hours() / 24))
	}

	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var cert models.SSLCertificate
	err := col.FindOneAndUpdate(ctx, bson.M{"domain": domain}, bson.M{"$set": setFields}, opts).Decode(&cert)
	if err != nil {
		return nil, err
	}

	// Update domain expiry
	s.db.Collection(database.ColDomains).UpdateOne(ctx, bson.M{"domain": domain}, bson.M{
		"$set": bson.M{"ssl_expires": expiresAt, "updated_at": now},
	})

	return &cert, nil
}

func (s *SSLService) Revoke(ctx context.Context, domainName string) error {
	if err := agent.RevokeCertificate(ctx, domainName); err != nil {
		return fmt.Errorf("revocation failed: %w", err)
	}

	s.db.Collection(database.ColSSLCerts).DeleteOne(ctx, bson.M{"domain": domainName})
	s.db.Collection(database.ColDomains).UpdateOne(ctx, bson.M{"domain": domainName}, bson.M{
		"$set": bson.M{"ssl_active": false, "ssl_expires": nil, "force_ssl": false, "updated_at": time.Now()},
	})

	// Downgrade nginx config back to HTTP-only, dispatching on the
	// domain's backing: app (static/reverse-proxy), project_service
	// (reverse-proxy), or plain PHP-FPM. Without the dispatch, a
	// project-backed domain would get a PHP-FPM HTTP vhost and the
	// project's upstream would go unrouted — same shape of bug as
	// Issue, just in reverse.
	s.downgradeNginxToHTTP(ctx, domainName)
	s.updateWordPressURLs(ctx, domainName, "http")

	return nil
}

func (s *SSLService) Delete(ctx context.Context, domainName string) error {
	// PRESERVE the on-disk cert material under /etc/ssl/custom and
	// /etc/letsencrypt. The panel's record of the cert goes away but the
	// files stay so (a) re-attaching the cert to the domain later needs
	// zero external calls and (b) an accidental Delete click doesn't
	// destroy a cert that's still paid for / still valid.

	s.db.Collection(database.ColSSLCerts).DeleteOne(ctx, bson.M{"domain": domainName})
	s.db.Collection(database.ColDomains).UpdateOne(ctx, bson.M{"domain": domainName}, bson.M{
		"$set": bson.M{"ssl_active": false, "ssl_expires": nil, "force_ssl": false, "updated_at": time.Now()},
	})

	// Downgrade nginx config back to HTTP-only with proper dispatch —
	// see downgradeNginxToHTTP for rationale.
	s.downgradeNginxToHTTP(ctx, domainName)
	s.updateWordPressURLs(ctx, domainName, "http")

	return nil
}

// downgradeNginxToHTTP rewrites the vhost for `domainName` as an
// HTTP-only block, picking the right template based on what's bound to
// the domain: deployed app (static dir / reverse proxy), Deploy-Software
// project_service (reverse proxy to upstream port), or plain
// PHP-FPM / file-based domain. Without this dispatch, a project-backed
// domain whose SSL is revoked/deleted ends up with a PHP-FPM HTTP-only
// vhost and the project's upstream becomes unroutable — a mirror of
// the issue-SSL bug, just in the opposite direction.
func (s *SSLService) downgradeNginxToHTTP(ctx context.Context, domainName string) {
	if app, err := s.lookupAppByDomain(ctx, domainName); err == nil && app != nil {
		if app.AppType == "static" {
			servedDir := app.InstallPath
			if app.Framework != "" {
				if p, ok := lookupPreset(app.Framework); ok && p.StaticDir != "" {
					servedDir = filepath.Join(app.InstallPath, p.StaticDir)
				}
			}
			if err := agent.CreateStaticVhost(ctx, domainName, servedDir); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to downgrade static-app vhost for %s: %v\n", domainName, err)
			}
			return
		}
		if app.Port > 0 {
			if err := agent.CreateReverseProxy(ctx, &agent.VhostConfig{Domain: domainName, Port: app.Port}); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to downgrade app vhost to HTTP for %s: %v\n", domainName, err)
			}
			return
		}
	}
	if svc, err := s.lookupProjectServiceByDomain(ctx, domainName); err == nil && svc != nil && svc.Port > 0 {
		if err := agent.CreateReverseProxy(ctx, &agent.VhostConfig{Domain: domainName, Port: svc.Port}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to downgrade project-service vhost to HTTP for %s: %v\n", domainName, err)
		}
		return
	}
	if domain, err := s.lookupDomain(ctx, domainName); err == nil {
		if err := agent.CreateVhost(ctx, &agent.VhostConfig{
			Domain:     domain.Domain,
			User:       domain.User,
			PHPVersion: domain.PHPVersion,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to downgrade nginx to HTTP for %s: %v\n", domainName, err)
		}
	}
}

func (s *SSLService) ForceSSL(ctx context.Context, domain string, enable bool) error {
	// Update nginx config
	if err := agent.ForceSSL(ctx, domain, enable); err != nil {
		return fmt.Errorf("failed to update nginx config: %w", err)
	}

	// Update SSL cert record
	col := s.db.Collection(database.ColSSLCerts)
	col.UpdateOne(ctx, bson.M{"domain": domain}, bson.M{
		"$set": bson.M{"force_ssl": enable, "updated_at": time.Now()},
	})

	// Update domain record
	_, err := s.db.Collection(database.ColDomains).UpdateOne(ctx, bson.M{"domain": domain}, bson.M{
		"$set": bson.M{"force_ssl": enable, "updated_at": time.Now()},
	})
	return err
}

// lookupDomain fetches a domain record by name.
func (s *SSLService) lookupDomain(ctx context.Context, domainName string) (*models.Domain, error) {
	var domain models.Domain
	err := s.db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": domainName}).Decode(&domain)
	if err != nil {
		return nil, err
	}
	return &domain, nil
}

// lookupAppByDomain returns the deployed app whose `domain` field matches the
// requested domain, if any. Used by the SSL upgrade path to pick the right
// vhost template (static, reverse proxy) instead of clobbering an app's
// custom config with the PHP-FPM template.
func (s *SSLService) lookupAppByDomain(ctx context.Context, domainName string) (*models.App, error) {
	var app models.App
	err := s.db.Collection(database.ColApps).FindOne(ctx, bson.M{"domain": domainName}).Decode(&app)
	if err != nil {
		return nil, err
	}
	return &app, nil
}

// lookupProjectServiceByDomain finds a Deploy Software project_service
// whose primary_domain OR alias_domains matches. Returned by SSL issue/
// delete flows to decide whether the domain's vhost needs a
// reverse-proxy SSL block (pointing at the project's upstream port)
// instead of the default PHP-FPM SSL template.
func (s *SSLService) lookupProjectServiceByDomain(ctx context.Context, domainName string) (*models.ProjectService, error) {
	var svc models.ProjectService
	err := s.db.Collection(database.ColProjectServices).FindOne(ctx, bson.M{
		"$or": []bson.M{
			{"primary_domain": domainName},
			{"alias_domains": domainName},
		},
	}).Decode(&svc)
	if err != nil {
		return nil, err
	}
	return &svc, nil
}

// updateWordPressURLs updates siteurl and home for all WordPress installations on a domain
// when SSL status changes (scheme should be "https" or "http").
func (s *SSLService) updateWordPressURLs(ctx context.Context, domainName, scheme string) {
	col := s.db.Collection(database.ColWordPress)
	cursor, err := col.Find(ctx, bson.M{"domain": domainName})
	if err != nil {
		return
	}
	defer cursor.Close(ctx)

	var installs []models.WordPress
	if err := cursor.All(ctx, &installs); err != nil {
		return
	}

	for _, wp := range installs {
		newSiteURL := fmt.Sprintf("%s://%s%s", scheme, wp.Domain, wp.Path)
		newAdminURL := fmt.Sprintf("%s://%s%s/wp-admin", scheme, wp.Domain, wp.Path)
		wpPath := fmt.Sprintf("/home/%s/domains/%s/public_html%s", wp.User, wp.Domain, wp.Path)

		// Update WordPress options via WP-CLI
		agent.WPCLICommand(ctx, wp.User, wpPath, fmt.Sprintf("option update siteurl '%s'", newSiteURL))
		agent.WPCLICommand(ctx, wp.User, wpPath, fmt.Sprintf("option update home '%s'", newSiteURL))

		// Update database record
		col.UpdateOne(ctx, bson.M{"_id": wp.ID}, bson.M{
			"$set": bson.M{
				"site_url":   newSiteURL,
				"admin_url":  newAdminURL,
				"updated_at": time.Now(),
			},
		})
	}
}

// parseCertbotInfo extracts certificate metadata from certbot output.
func parseCertbotInfo(ctx context.Context, domain string) (issuedAt *time.Time, expiresAt *time.Time, issuer string, serial string) {
	issuer = "Let's Encrypt"
	info, err := agent.GetCertInfo(ctx, domain)
	if err != nil {
		return
	}
	// Parse expiry date from certbot certificates output
	expiryRe := regexp.MustCompile(`Expiry Date: (\d{4}-\d{2}-\d{2})`)
	if m := expiryRe.FindStringSubmatch(info); len(m) > 1 {
		if t, err := time.Parse("2006-01-02", m[1]); err == nil {
			expiresAt = &t
			issued := t.AddDate(0, -3, 0) // LE certs are 90 days
			issuedAt = &issued
		}
	}
	serialRe := regexp.MustCompile(`Serial Number: ([0-9a-fA-F]+)`)
	if m := serialRe.FindStringSubmatch(info); len(m) > 1 {
		serial = m[1]
	}
	return
}
