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

	// Insert template records if provided
	if len(req.Records) > 0 {
		recCol := s.db.Collection(database.ColDNSRecords)
		for _, rec := range req.Records {
			rec.ZoneID = zone.ID
			rec.CreatedAt = now
			rec.UpdatedAt = now
			recCol.InsertOne(ctx, rec)
		}
	}

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
