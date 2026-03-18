package services

import (
	"context"
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

type DomainService struct {
	db  *mongo.Database
	dns *DNSService
}

func NewDomainService(db *mongo.Database, dns *DNSService) *DomainService {
	return &DomainService{db: db, dns: dns}
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

	// Create domain directory under user's home
	if err := agent.CreateDomainDirectory(ctx, req.User, req.Domain); err != nil {
		return nil, fmt.Errorf("failed to create domain directory: %w", err)
	}

	// Create PHP-FPM pool (named after domain, runs as user)
	if err := agent.CreatePHPPool(ctx, req.Domain, req.User, req.PHPVersion); err != nil {
		return nil, fmt.Errorf("failed to create PHP pool: %w", err)
	}

	// Create Nginx vhost
	if err := agent.CreateVhost(ctx, &agent.VhostConfig{
		Domain:     req.Domain,
		User:       req.User,
		PHPVersion: req.PHPVersion,
	}); err != nil {
		return nil, fmt.Errorf("failed to create vhost: %w", err)
	}

	// Set disk quota if specified
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

	// Auto-create DNS zone with mail server setup (MX, SPF, DKIM, DMARC)
	if s.dns != nil {
		serverIP := req.ServerIP
		nameservers := req.Nameservers
		if serverIP == "" {
			serverIP = "187.127.132.4"
		}
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

	// Remove nginx vhost
	agent.DeleteVhost(ctx, domain.Domain)

	// Remove PHP-FPM pool (named after domain)
	agent.DeletePHPPool(ctx, domain.Domain)

	// Remove domain directory (NOT the user's home)
	domainDir := fmt.Sprintf("/home/%s/domains/%s", domain.User, domain.Domain)
	os.RemoveAll(domainDir)

	// Delete DNS zone if exists
	if s.dns != nil {
		s.dns.DeleteZone(ctx, domain.Domain)
	}

	// Delete associated mailboxes and forwarders
	s.db.Collection(database.ColMailboxes).DeleteMany(ctx, bson.M{"domain": domain.Domain})
	s.db.Collection(database.ColForwarders).DeleteMany(ctx, bson.M{"domain": domain.Domain})

	// Delete from database
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

	// Recreate vhost with new PHP version
	agent.CreateVhost(ctx, &agent.VhostConfig{
		Domain:     domain.Domain,
		User:       domain.User,
		PHPVersion: phpVersion,
	})

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
