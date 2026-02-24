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
	cursor, err := col.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "domain", Value: 1}}))
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

	// Reload nginx
	agent.ReloadNginx(ctx)

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

func (s *SSLService) Revoke(ctx context.Context, domain string) error {
	if err := agent.RevokeCertificate(ctx, domain); err != nil {
		return fmt.Errorf("revocation failed: %w", err)
	}

	s.db.Collection(database.ColSSLCerts).DeleteOne(ctx, bson.M{"domain": domain})
	s.db.Collection(database.ColDomains).UpdateOne(ctx, bson.M{"domain": domain}, bson.M{
		"$set": bson.M{"ssl_active": false, "ssl_expires": nil, "updated_at": time.Now()},
	})
	return nil
}

func (s *SSLService) Delete(ctx context.Context, domain string) error {
	// Remove cert files
	os.RemoveAll(fmt.Sprintf("/etc/ssl/custom/%s", domain))

	s.db.Collection(database.ColSSLCerts).DeleteOne(ctx, bson.M{"domain": domain})
	s.db.Collection(database.ColDomains).UpdateOne(ctx, bson.M{"domain": domain}, bson.M{
		"$set": bson.M{"ssl_active": false, "ssl_expires": nil, "updated_at": time.Now()},
	})
	return nil
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
