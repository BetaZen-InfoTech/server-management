package services

import (
	"context"
	"fmt"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type DNSService struct {
	db *mongo.Database
}

func NewDNSService(db *mongo.Database) *DNSService {
	return &DNSService{db: db}
}

func (s *DNSService) ListZones(ctx context.Context) ([]models.DNSZone, error) {
	col := s.db.Collection(database.ColDNSZones)
	cursor, err := col.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "domain", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var zones []models.DNSZone
	if err := cursor.All(ctx, &zones); err != nil {
		return nil, err
	}
	if zones == nil {
		zones = []models.DNSZone{}
	}
	return zones, nil
}

func (s *DNSService) GetZone(ctx context.Context, domain string) (*models.DNSZone, error) {
	col := s.db.Collection(database.ColDNSZones)
	var zone models.DNSZone
	if err := col.FindOne(ctx, bson.M{"domain": domain}).Decode(&zone); err != nil {
		return nil, err
	}
	return &zone, nil
}

func (s *DNSService) CreateZone(ctx context.Context, req *models.CreateZoneRequest) (*models.DNSZone, error) {
	if err := agent.CreateDNSZone(ctx, req.Domain, req.ServerIP, req.AdminEmail, req.Nameservers); err != nil {
		return nil, fmt.Errorf("failed to create DNS zone: %w", err)
	}

	now := time.Now()
	zone := models.DNSZone{
		Domain:      req.Domain,
		ServerIP:    req.ServerIP,
		AdminEmail:  req.AdminEmail,
		Nameservers: req.Nameservers,
		Serial:      1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	col := s.db.Collection(database.ColDNSZones)
	result, err := col.InsertOne(ctx, zone)
	if err != nil {
		return nil, fmt.Errorf("failed to save zone record: %w", err)
	}
	zone.ID = result.InsertedID.(primitive.ObjectID)

	// Save default records (A, www CNAME, NS) to MongoDB
	recCol := s.db.Collection(database.ColDNSRecords)
	defaultRecords := []interface{}{
		models.DNSRecord{ZoneID: zone.ID, Type: "A", Name: "@", Value: req.ServerIP, TTL: 3600, CreatedAt: now, UpdatedAt: now},
		models.DNSRecord{ZoneID: zone.ID, Type: "CNAME", Name: "www", Value: req.Domain + ".", TTL: 3600, CreatedAt: now, UpdatedAt: now},
	}
	for _, ns := range req.Nameservers {
		defaultRecords = append(defaultRecords, models.DNSRecord{ZoneID: zone.ID, Type: "NS", Name: "@", Value: ns, TTL: 3600, CreatedAt: now, UpdatedAt: now})
	}
	recCol.InsertMany(ctx, defaultRecords)

	// Insert template records if provided
	if len(req.Records) > 0 {
		for _, rec := range req.Records {
			rec.ZoneID = zone.ID
			rec.CreatedAt = now
			rec.UpdatedAt = now
			recCol.InsertOne(ctx, rec)
		}
	}

	// Auto-setup mail server and mail DNS records
	s.setupMailServer(ctx, req.Domain, req.ServerIP, &zone)

	return &zone, nil
}

func (s *DNSService) DeleteZone(ctx context.Context, domain string) error {
	zone, err := s.GetZone(ctx, domain)
	if err != nil {
		return fmt.Errorf("zone not found: %w", err)
	}

	if err := agent.DeleteDNSZone(ctx, domain); err != nil {
		return fmt.Errorf("failed to delete DNS zone: %w", err)
	}

	s.db.Collection(database.ColDNSRecords).DeleteMany(ctx, bson.M{"zone_id": zone.ID})
	_, err = s.db.Collection(database.ColDNSZones).DeleteOne(ctx, bson.M{"_id": zone.ID})
	return err
}

func (s *DNSService) ListRecords(ctx context.Context, domain string) ([]models.DNSRecord, error) {
	zone, err := s.GetZone(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("zone not found: %w", err)
	}

	col := s.db.Collection(database.ColDNSRecords)
	cursor, err := col.Find(ctx, bson.M{"zone_id": zone.ID}, options.Find().SetSort(bson.D{{Key: "type", Value: 1}, {Key: "name", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var records []models.DNSRecord
	if err := cursor.All(ctx, &records); err != nil {
		return nil, err
	}
	if records == nil {
		records = []models.DNSRecord{}
	}
	return records, nil
}

func (s *DNSService) AddRecord(ctx context.Context, domain string, req *models.CreateRecordRequest) (*models.DNSRecord, error) {
	zone, err := s.GetZone(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("zone not found: %w", err)
	}

	ttl := req.TTL
	if ttl == 0 {
		ttl = 3600
	}

	if err := agent.AddDNSRecord(ctx, domain, req.Name, req.Type, fmt.Sprint(ttl), req.Value); err != nil {
		return nil, fmt.Errorf("failed to add DNS record: %w", err)
	}

	now := time.Now()
	record := models.DNSRecord{
		ZoneID:    zone.ID,
		Type:      req.Type,
		Name:      req.Name,
		Value:     req.Value,
		TTL:       ttl,
		Priority:  req.Priority,
		Weight:    req.Weight,
		Port:      req.Port,
		CAAFlag:   req.CAAFlag,
		CAATag:    req.CAATag,
		CreatedAt: now,
		UpdatedAt: now,
	}

	col := s.db.Collection(database.ColDNSRecords)
	result, err := col.InsertOne(ctx, record)
	if err != nil {
		return nil, err
	}
	record.ID = result.InsertedID.(primitive.ObjectID)

	// Increment zone serial
	s.db.Collection(database.ColDNSZones).UpdateOne(ctx, bson.M{"_id": zone.ID}, bson.M{
		"$inc": bson.M{"serial": 1},
		"$set": bson.M{"updated_at": now},
	})

	return &record, nil
}

func (s *DNSService) UpdateRecord(ctx context.Context, domain string, id string, updates map[string]interface{}) (*models.DNSRecord, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid record ID")
	}

	col := s.db.Collection(database.ColDNSRecords)
	var existing models.DNSRecord
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&existing); err != nil {
		return nil, fmt.Errorf("record not found")
	}

	// Delete old record from PowerDNS
	agent.DeleteDNSRecord(ctx, domain, existing.Name, existing.Type)

	// Determine new values
	newName := existing.Name
	newType := existing.Type
	newValue := existing.Value
	newTTL := existing.TTL

	setFields := bson.M{"updated_at": time.Now()}
	if v, ok := updates["name"].(string); ok {
		newName = v
		setFields["name"] = v
	}
	if v, ok := updates["type"].(string); ok {
		newType = v
		setFields["type"] = v
	}
	if v, ok := updates["value"].(string); ok {
		newValue = v
		setFields["value"] = v
	}
	if v, ok := updates["ttl"].(float64); ok {
		newTTL = int(v)
		setFields["ttl"] = int(v)
	}

	// Add updated record to PowerDNS
	agent.AddDNSRecord(ctx, domain, newName, newType, fmt.Sprint(newTTL), newValue)

	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var record models.DNSRecord
	err = col.FindOneAndUpdate(ctx, bson.M{"_id": oid}, bson.M{"$set": setFields}, opts).Decode(&record)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *DNSService) DeleteRecord(ctx context.Context, domain string, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid record ID")
	}

	col := s.db.Collection(database.ColDNSRecords)
	var record models.DNSRecord
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&record); err != nil {
		return fmt.Errorf("record not found")
	}

	if err := agent.DeleteDNSRecord(ctx, domain, record.Name, record.Type); err != nil {
		return fmt.Errorf("failed to delete DNS record: %w", err)
	}

	_, err = col.DeleteOne(ctx, bson.M{"_id": oid})
	if err != nil {
		return err
	}

	// Increment zone serial
	zone, _ := s.GetZone(ctx, domain)
	if zone != nil {
		s.db.Collection(database.ColDNSZones).UpdateOne(ctx, bson.M{"_id": zone.ID}, bson.M{
			"$inc": bson.M{"serial": 1},
			"$set": bson.M{"updated_at": time.Now()},
		})
	}

	return nil
}

func (s *DNSService) ExportZone(ctx context.Context, domain string) (string, error) {
	output, err := agent.ExportDNSZone(ctx, domain)
	if err != nil {
		return "", fmt.Errorf("failed to export zone: %w", err)
	}
	return output, nil
}

// setupMailServer configures DKIM, Postfix virtual domain, and adds
// mail-related DNS records (MX, SPF, DKIM, DMARC) when a new zone is created.
func (s *DNSService) setupMailServer(ctx context.Context, domain, serverIP string, zone *models.DNSZone) {
	// 1. Generate DKIM key
	keyDir := fmt.Sprintf("/etc/opendkim/keys/%s", domain)
	agent.RunCommand(ctx, "mkdir", "-p", keyDir)
	agent.RunCommand(ctx, "opendkim-genkey", "-s", "mail", "-d", domain, "-D", keyDir)
	agent.RunCommand(ctx, "chown", "-R", "opendkim:opendkim", keyDir)

	// Add to OpenDKIM signing table
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/opendkim/signing.table || echo '*@%s mail._domainkey.%s' >> /etc/opendkim/signing.table", domain, domain, domain))

	// Add to OpenDKIM key table
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/opendkim/key.table || echo 'mail._domainkey.%s %s:mail:%s/mail.private' >> /etc/opendkim/key.table", domain, domain, domain, keyDir))

	// Add to OpenDKIM trusted hosts
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/opendkim/trusted.hosts || echo '%s' >> /etc/opendkim/trusted.hosts", domain, domain))

	// Restart OpenDKIM
	agent.RunCommand(ctx, "systemctl", "restart", "opendkim")

	// 2. Add domain to Postfix virtual domains
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/postfix/virtual_domains || echo '%s OK' >> /etc/postfix/virtual_domains", domain, domain))
	agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_domains")
	agent.RunCommand(ctx, "systemctl", "reload", "postfix")

	// 3. Read DKIM public key for DNS record
	dkimResult, _ := agent.RunCommand(ctx, "cat", fmt.Sprintf("%s/mail.txt", keyDir))
	dkimValue := ""
	if dkimResult != nil {
		dkimValue = parseDKIMPublicKey(dkimResult.Output)
	}

	// 4. Add mail DNS records to PowerDNS
	agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "mail", "A", "3600", serverIP)
	agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "@", "MX", "3600", fmt.Sprintf("10 mail.%s.", domain))
	agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "@", "TXT", "3600", fmt.Sprintf("\"v=spf1 ip4:%s ~all\"", serverIP))
	if dkimValue != "" {
		agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "mail._domainkey", "TXT", "3600", fmt.Sprintf("\"%s\"", dkimValue))
	}
	agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "_dmarc", "TXT", "3600", fmt.Sprintf("\"v=DMARC1; p=none; rua=mailto:admin@%s\"", domain))
	agent.RunCommand(ctx, "pdns_control", "reload")

	// 5. Save mail DNS records to MongoDB
	now := time.Now()
	recCol := s.db.Collection(database.ColDNSRecords)
	mxPri := 10
	mailRecords := []interface{}{
		models.DNSRecord{ZoneID: zone.ID, Type: "A", Name: "mail", Value: serverIP, TTL: 3600, CreatedAt: now, UpdatedAt: now},
		models.DNSRecord{ZoneID: zone.ID, Type: "MX", Name: "@", Value: fmt.Sprintf("mail.%s.", domain), TTL: 3600, Priority: &mxPri, CreatedAt: now, UpdatedAt: now},
		models.DNSRecord{ZoneID: zone.ID, Type: "TXT", Name: "@", Value: fmt.Sprintf("v=spf1 ip4:%s ~all", serverIP), TTL: 3600, CreatedAt: now, UpdatedAt: now},
		models.DNSRecord{ZoneID: zone.ID, Type: "TXT", Name: "_dmarc", Value: fmt.Sprintf("v=DMARC1; p=none; rua=mailto:admin@%s", domain), TTL: 3600, CreatedAt: now, UpdatedAt: now},
	}
	if dkimValue != "" {
		mailRecords = append(mailRecords, models.DNSRecord{ZoneID: zone.ID, Type: "TXT", Name: "mail._domainkey", Value: dkimValue, TTL: 3600, CreatedAt: now, UpdatedAt: now})
	}
	recCol.InsertMany(ctx, mailRecords)

	// Update zone serial
	s.db.Collection(database.ColDNSZones).UpdateOne(ctx, bson.M{"_id": zone.ID}, bson.M{
		"$inc": bson.M{"serial": len(mailRecords)},
		"$set": bson.M{"updated_at": now},
	})
}
