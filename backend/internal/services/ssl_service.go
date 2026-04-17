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
	db *mongo.Database
}

func NewSSLService(db *mongo.Database) *SSLService {
	return &SSLService{db: db}
}

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

func (s *SSLService) IssueLetsEncrypt(ctx context.Context, req *models.IssueLetsEncryptRequest) (*models.SSLCertificate, error) {
	if err := agent.IssueLetsEncrypt(ctx, req.Domain, req.Email, req.AdditionalDomains, req.Wildcard); err != nil {
		return nil, fmt.Errorf("certbot failed: %w", err)
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

	// Upgrade nginx config to include HTTPS (443) block. Three shapes a
	// domain can take: (1) a deployed app — recreate the static or
	// reverse-proxy SSL vhost so its 443 block matches the app's served
	// dir / port; (2) a regular file-based domain — use the PHP-FPM SSL
	// template via CreateVhostWithSSL; (3) neither — leave nginx alone
	// and let the operator reload manually.
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

	// Downgrade nginx config back to HTTP-only
	if domain, err := s.lookupDomain(ctx, domainName); err == nil {
		vhostCfg := &agent.VhostConfig{
			Domain:     domain.Domain,
			User:       domain.User,
			PHPVersion: domain.PHPVersion,
		}
		if err := agent.CreateVhost(ctx, vhostCfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to downgrade nginx to HTTP for %s: %v\n", domainName, err)
		}
		s.updateWordPressURLs(ctx, domainName, "http")
	}

	return nil
}

func (s *SSLService) Delete(ctx context.Context, domainName string) error {
	// Remove cert files
	agent.RunCommand(ctx, "rm", "-rf", fmt.Sprintf("/etc/ssl/custom/%s", domainName))

	s.db.Collection(database.ColSSLCerts).DeleteOne(ctx, bson.M{"domain": domainName})
	s.db.Collection(database.ColDomains).UpdateOne(ctx, bson.M{"domain": domainName}, bson.M{
		"$set": bson.M{"ssl_active": false, "ssl_expires": nil, "force_ssl": false, "updated_at": time.Now()},
	})

	// Downgrade nginx config back to HTTP-only
	if domain, err := s.lookupDomain(ctx, domainName); err == nil {
		vhostCfg := &agent.VhostConfig{
			Domain:     domain.Domain,
			User:       domain.User,
			PHPVersion: domain.PHPVersion,
		}
		if err := agent.CreateVhost(ctx, vhostCfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to downgrade nginx to HTTP for %s: %v\n", domainName, err)
		}
		s.updateWordPressURLs(ctx, domainName, "http")
	}

	return nil
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
