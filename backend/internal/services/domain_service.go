package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
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
	db    *mongo.Database
	dns   *DNSService
	ssl   *SSLService
	email *EmailService
	cfg   DomainServiceConfig
}

type DomainServiceConfig struct {
	SSLEmail  string // email for Let's Encrypt registration
	JWTSecret string // for encrypting FTP/mail passwords
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
	return &domain, nil
}

func (s *DomainService) Create(ctx context.Context, req *models.CreateDomainRequest) (*models.Domain, error) {
	// Validate that the user account exists
	userCol := s.db.Collection(database.ColUsers)
	count, _ := userCol.CountDocuments(ctx, bson.M{"username": req.User})
	if count == 0 {
		return nil, fmt.Errorf("user account '%s' not found", req.User)
	}

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

	// 5. DNS setup: detect if subdomain of an existing domain
	if s.dns != nil {
		serverIP := req.ServerIP
		if serverIP == "" {
			serverIP = "187.127.132.4"
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
		if _, sslErr := s.ssl.IssueLetsEncrypt(ctx, sslReq); sslErr != nil {
			fmt.Fprintf(os.Stderr, "warning: auto-SSL failed for %s: %v\n", req.Domain, sslErr)
		} else {
			// SSL issued successfully, upgrade nginx config to include 443 block
			if err := agent.CreateVhostWithSSL(ctx, vhostCfg); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to upgrade nginx to SSL for %s: %v\n", req.Domain, err)
			}
		}
	}

	// 7. Auto-create admin@domain.com mailbox
	if s.email != nil {
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

	// 1. Remove nginx vhost
	agent.DeleteVhost(ctx, domain.Domain)

	// 2. Remove PHP-FPM pool (named after domain)
	agent.DeletePHPPool(ctx, domain.Domain)

	// 3. Remove domain directory (NOT the user's home)
	domainDir := fmt.Sprintf("/home/%s/domains/%s", domain.User, domain.Domain)
	os.RemoveAll(domainDir)

	// 4. Delete DNS: remove subdomain record from parent zone, or delete full zone
	if s.dns != nil {
		parentDomain := findParentDomain(ctx, s.db, domain.Domain)
		if parentDomain != "" {
			// Subdomain: remove A record from parent zone using relative name
			subPart := strings.TrimSuffix(domain.Domain, "."+parentDomain)
			records, _ := s.dns.ListRecords(ctx, parentDomain)
			for _, r := range records {
				if r.Type == "A" && r.Name == subPart {
					s.dns.DeleteRecord(ctx, parentDomain, r.ID.Hex())
					break
				}
			}
		} else {
			s.dns.DeleteZone(ctx, domain.Domain)
		}
	}

	// 5. Delete SSL certificate (DB record + custom cert files)
	if s.ssl != nil {
		s.ssl.Delete(ctx, domain.Domain)
	}
	// Also remove Let's Encrypt live/archive/renewal files
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"rm -rf /etc/letsencrypt/live/%s /etc/letsencrypt/archive/%s /etc/letsencrypt/renewal/%s.conf",
		domain.Domain, domain.Domain, domain.Domain))

	// 6. Delete mailboxes from system (postfix/dovecot config files) and DB
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
	// Remove mail directory for the domain
	agent.RunCommand(ctx, "rm", "-rf", fmt.Sprintf("/home/%s/mail/%s", domain.User, domain.Domain))
	// Rebuild postfix maps and reload
	agent.RunCommand(ctx, "bash", "-c", "postmap /etc/postfix/virtual_mailboxes 2>/dev/null")
	// Remove domain from virtual_domains if present
	escapedDomain := strings.ReplaceAll(domain.Domain, ".", "\\.")
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("sed -i '/^%s$/d' /etc/postfix/virtual_domains 2>/dev/null", escapedDomain))
	agent.RunCommand(ctx, "bash", "-c", "postmap /etc/postfix/virtual_domains 2>/dev/null; systemctl reload postfix 2>/dev/null")

	s.db.Collection(database.ColMailboxes).DeleteMany(ctx, bson.M{"domain": domain.Domain})
	s.db.Collection(database.ColForwarders).DeleteMany(ctx, bson.M{"domain": domain.Domain})
	s.db.Collection(database.ColAutoresponders).DeleteMany(ctx, bson.M{"domain": domain.Domain})

	// 7. Delete FTP accounts (from system + DB)
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

	// 8. Delete subdomains, aliases, redirects
	s.db.Collection(database.ColSubdomains).DeleteMany(ctx, bson.M{"domain_id": domain.ID})
	s.db.Collection(database.ColAliases).DeleteMany(ctx, bson.M{"domain_id": domain.ID})
	s.db.Collection(database.ColRedirects).DeleteMany(ctx, bson.M{"domain_id": domain.ID})

	// 9. Delete apps and deployments
	s.db.Collection(database.ColApps).DeleteMany(ctx, bson.M{"domain": domain.Domain})
	s.db.Collection(database.ColDeployments).DeleteMany(ctx, bson.M{"domain": domain.Domain})

	// 10. Delete databases and database users
	s.db.Collection(database.ColDatabases).DeleteMany(ctx, bson.M{"domain": domain.Domain})
	s.db.Collection(database.ColDBUsers).DeleteMany(ctx, bson.M{"domain": domain.Domain})

	// 11. Delete WordPress installs
	s.db.Collection(database.ColWordPress).DeleteMany(ctx, bson.M{"domain": domain.Domain})

	// 12. Delete cron jobs
	s.db.Collection(database.ColCronJobs).DeleteMany(ctx, bson.M{"domain": domain.Domain})

	// 13. Delete backups and backup schedules
	s.db.Collection(database.ColBackups).DeleteMany(ctx, bson.M{"domain": domain.Domain})
	s.db.Collection(database.ColBackupSchedules).DeleteMany(ctx, bson.M{"domain": domain.Domain})

	// 14. Remove nginx log files
	agent.RunCommand(ctx, "rm", "-f",
		fmt.Sprintf("/var/log/nginx/%s-access.log", domain.Domain),
		fmt.Sprintf("/var/log/nginx/%s-error.log", domain.Domain))

	// 15. Delete the domain record itself
	col := s.db.Collection(database.ColDomains)
	_, err = col.DeleteOne(ctx, bson.M{"_id": domain.ID})
	return err
}

func (s *DomainService) Suspend(ctx context.Context, id string) error {
	domain, err := s.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("domain not found: %w", err)
	}

	// Remove nginx sites-enabled symlink to disable the domain
	os.Remove(fmt.Sprintf("/etc/nginx/sites-enabled/%s", domain.Domain))
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
	os.Symlink(src, dst)
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

func (s *DomainService) ListByUser(ctx context.Context, userID string, page, limit int) ([]models.Domain, int64, error) {
	col := s.db.Collection(database.ColDomains)
	filter := bson.M{"user": userID}

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
