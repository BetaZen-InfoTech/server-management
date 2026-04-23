package services

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
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
	// Get all zones from PowerDNS
	pdnsZones, err := agent.ListAllZones(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list PowerDNS zones: %w", err)
	}

	// Get MongoDB zone metadata for enrichment
	col := s.db.Collection(database.ColDNSZones)
	cursor, err := col.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var dbZones []models.DNSZone
	cursor.All(ctx, &dbZones)

	dbMap := make(map[string]models.DNSZone)
	for _, z := range dbZones {
		dbMap[z.Domain] = z
	}

	// Multi-tenant: vendors only see zones for domains they own.
	scope := GetCallerScope(ctx)
	var allowedDomains map[string]bool
	if scope != nil && constants.IsTenantScoped(scope.Role) {
		domains, _ := scope.TenantDomains(ctx, s.db)
		allowedDomains = make(map[string]bool, len(domains))
		for _, d := range domains {
			allowedDomains[d] = true
		}
	}

	// Build zone list: PowerDNS zones enriched with MongoDB metadata
	var zones []models.DNSZone
	for _, domain := range pdnsZones {
		if allowedDomains != nil && !allowedDomains[domain] {
			continue
		}
		if z, ok := dbMap[domain]; ok {
			zones = append(zones, z)
		} else {
			// Zone exists in PowerDNS but not in MongoDB — show it anyway
			zones = append(zones, models.DNSZone{
				Domain: domain,
				Status: "active",
			})
		}
	}
	if zones == nil {
		zones = []models.DNSZone{}
	}
	return zones, nil
}

// assertCallerOwnsDomain is the single gate every per-zone DNS method
// passes through before touching PowerDNS or Mongo. Platform operators
// (vendor_owner) are unrestricted; tenant-scoped callers are rejected
// when the target domain isn't in their tenant's domain list. Without
// this, a vendor_admin could GET /dns/zones/<other-tenant>.com/records
// and read or (worse) modify another customer's zone.
func (s *DNSService) assertCallerOwnsDomain(ctx context.Context, domain string) error {
	scope := GetCallerScope(ctx)
	if scope == nil {
		return nil
	}
	return scope.AssertOwnsDomain(ctx, s.db, domain)
}

func (s *DNSService) GetZone(ctx context.Context, domain string) (*models.DNSZone, error) {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return nil, err
	}
	col := s.db.Collection(database.ColDNSZones)
	var zone models.DNSZone
	if err := col.FindOne(ctx, bson.M{"domain": domain}).Decode(&zone); err != nil {
		return nil, err
	}
	zone.Status = "active"
	return &zone, nil
}

// GetOrCreateZone returns the zone from MongoDB, creating it if it only exists in PowerDNS.
func (s *DNSService) GetOrCreateZone(ctx context.Context, domain string) (*models.DNSZone, error) {
	zone, err := s.GetZone(ctx, domain)
	if err == nil {
		return zone, nil
	}
	// Not in MongoDB — create a minimal record so we can track records
	now := time.Now()
	z := models.DNSZone{
		Domain:    domain,
		Status:    "active",
		Serial:    1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	col := s.db.Collection(database.ColDNSZones)
	result, err := col.InsertOne(ctx, z)
	if err != nil {
		return nil, err
	}
	z.ID = result.InsertedID.(primitive.ObjectID)
	return &z, nil
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

	// Save default records (A, www CNAME, NS) to MongoDB. A records
	// use the 60s default so a fresh-install IP cutover propagates
	// quickly; CNAME/NS keep the historic 3600s since those rarely
	// change and short TTLs there hurt resolver caches.
	recCol := s.db.Collection(database.ColDNSRecords)
	defaultRecords := []interface{}{
		models.DNSRecord{ZoneID: zone.ID, Type: "A", Name: "@", Value: req.ServerIP, TTL: defaultTTLFor("A"), CreatedAt: now, UpdatedAt: now},
		models.DNSRecord{ZoneID: zone.ID, Type: "CNAME", Name: "www", Value: req.Domain + ".", TTL: defaultTTLFor("CNAME"), CreatedAt: now, UpdatedAt: now},
	}
	for _, ns := range req.Nameservers {
		defaultRecords = append(defaultRecords, models.DNSRecord{ZoneID: zone.ID, Type: "NS", Name: "@", Value: ns, TTL: defaultTTLFor("NS"), CreatedAt: now, UpdatedAt: now})
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
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return err
	}
	if err := agent.DeleteDNSZone(ctx, domain); err != nil {
		return fmt.Errorf("failed to delete DNS zone: %w", err)
	}

	// Clean up MongoDB records if zone exists there
	zone, err := s.GetZone(ctx, domain)
	if err == nil {
		s.db.Collection(database.ColDNSRecords).DeleteMany(ctx, bson.M{"zone_id": zone.ID})
		s.db.Collection(database.ColDNSZones).DeleteOne(ctx, bson.M{"_id": zone.ID})
	}
	return nil
}

func (s *DNSService) ListRecords(ctx context.Context, domain string) ([]models.DNSRecord, error) {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return nil, err
	}
	// Fetch records directly from PowerDNS
	parsed, err := agent.ListZoneRecords(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("failed to list zone records: %w", err)
	}

	// Get zone ID from MongoDB if available (for record IDs)
	zone, _ := s.GetZone(ctx, domain)

	// Try to match parsed records with MongoDB records for IDs
	var dbRecords []models.DNSRecord
	if zone != nil {
		col := s.db.Collection(database.ColDNSRecords)
		cursor, _ := col.Find(ctx, bson.M{"zone_id": zone.ID})
		if cursor != nil {
			cursor.All(ctx, &dbRecords)
			cursor.Close(ctx)
		}
	}

	// Build lookup map: type+name+value -> MongoDB record
	dbMap := make(map[string]models.DNSRecord)
	for _, r := range dbRecords {
		key := r.Type + "|" + r.Name + "|" + r.Value
		dbMap[key] = r
	}

	var records []models.DNSRecord
	for _, p := range parsed {
		ttl, _ := strconv.Atoi(p.TTL)
		rec := models.DNSRecord{
			Type:  p.Type,
			Name:  p.Name,
			Value: p.Value,
			TTL:   ttl,
		}
		// Use MongoDB ID if we have a match
		if dbRec, ok := dbMap[p.Type+"|"+p.Name+"|"+p.Value]; ok {
			rec.ID = dbRec.ID
			rec.ZoneID = dbRec.ZoneID
			rec.Priority = dbRec.Priority
			rec.CreatedAt = dbRec.CreatedAt
			rec.UpdatedAt = dbRec.UpdatedAt
		}
		records = append(records, rec)
	}
	if records == nil {
		records = []models.DNSRecord{}
	}
	return records, nil
}

// defaultTTLFor returns the cPanel-style default TTL for a record type
// when the caller didn't supply one. A records (and their AAAA siblings)
// default to 60 seconds because IPs are the most likely thing to change
// quickly during a migration and a long cache there hurts cutover.
// Everything else stays at 3600 (1 hour) — the historic default — so
// MX/NS/SOA/TXT records aren't churning resolver caches.
func defaultTTLFor(rtype string) int {
	switch rtype {
	case "A", "AAAA":
		return 60
	}
	return 3600
}

func (s *DNSService) AddRecord(ctx context.Context, domain string, req *models.CreateRecordRequest) (*models.DNSRecord, error) {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return nil, err
	}
	zone, err := s.GetOrCreateZone(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("zone not found: %w", err)
	}

	ttl := req.TTL
	if ttl == 0 {
		ttl = defaultTTLFor(req.Type)
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

// BulkAddRecords inserts N records into the zone serially. Per-item
// failure does NOT abort the batch — every entry produces an item in
// the response so the UI can render success / error inline against the
// pending row that produced it. Mirrors the SSL bulk-issue shape so
// frontends can use the same pattern.
//
// Why serial: PowerDNS's pdnsutil add-record uses an internal lock per
// zone; parallel adds would just block each other. Sequential keeps
// the per-record error surface clean and avoids race windows on the
// zone serial increment.
func (s *DNSService) BulkAddRecords(ctx context.Context, domain string, req *models.BulkRecordsRequest) (*models.BulkRecordsResponse, error) {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return nil, err
	}
	resp := &models.BulkRecordsResponse{
		Total: len(req.Records),
		Items: make([]models.BulkRecordResultItem, 0, len(req.Records)),
	}
	for i := range req.Records {
		r := req.Records[i]
		item := models.BulkRecordResultItem{Index: i}
		rec, err := s.AddRecord(ctx, domain, &r)
		if err != nil {
			item.Success = false
			item.Error = err.Error()
			resp.Items = append(resp.Items, item)
			resp.Failed++
			continue
		}
		item.Success = true
		item.Record = rec
		resp.Items = append(resp.Items, item)
		resp.Success++
	}
	return resp, nil
}

func (s *DNSService) UpdateRecord(ctx context.Context, domain string, id string, updates map[string]interface{}) (*models.DNSRecord, error) {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return nil, err
	}
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
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return err
	}
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

	col.DeleteOne(ctx, bson.M{"_id": oid})

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

// DeleteRecordByNameType deletes a DNS record by name and type (for records without MongoDB IDs).
func (s *DNSService) DeleteRecordByNameType(ctx context.Context, domain, name, rtype string) error {
	if err := agent.DeleteDNSRecord(ctx, domain, name, rtype); err != nil {
		return fmt.Errorf("failed to delete DNS record: %w", err)
	}
	return nil
}

func (s *DNSService) ExportZone(ctx context.Context, domain string) (string, error) {
	if err := s.assertCallerOwnsDomain(ctx, domain); err != nil {
		return "", err
	}
	output, err := agent.ExportDNSZone(ctx, domain)
	if err != nil {
		return "", fmt.Errorf("failed to export zone: %w", err)
	}
	return output, nil
}

// SetupSubdomainMail wires mail for a SUBDOMAIN that lives inside an
// existing parent DNS zone. Unlike setupMailServer (which runs on
// fresh zone creation) we don't own a separate zone here — records
// are added into the parent, and DKIM is registered under the
// subdomain's FQDN so OpenDKIM signs outbound mail properly.
//
// Without this, creating sub.example.com and then admin@sub.example.com
// produced three silent failures that together broke subdomain email:
//   1. No MX record at `sub` in example.com's zone → external senders
//      fell back to the A record for sub.example.com. Often worked,
//      but hostname mismatch tripped SPF.
//   2. sub.example.com never entered the OpenDKIM signing.table →
//      outbound mail from this subdomain went unsigned → Gmail's
//      DMARC alignment failed → spam folder or reject.
//   3. sub.example.com was only added to virtual_mailbox_domains
//      lazily, on the first CreateMailbox call. Any inbound delivery
//      that raced a CreateMailbox bounced "Recipient address rejected:
//      Domain not found".
//
// subPart is the label under the parent (e.g. "mail" or "shop.blog").
// parentDomain is the owning zone root (e.g. "example.com").
func (s *DNSService) SetupSubdomainMail(ctx context.Context, subPart, parentDomain, serverIP string) error {
	if subPart == "" || parentDomain == "" {
		return fmt.Errorf("subPart and parentDomain are required")
	}
	fqdn := subPart + "." + parentDomain
	escFqdn := regexp.QuoteMeta(fqdn)
	escParent := regexp.QuoteMeta(parentDomain)

	// 1. DKIM. We REUSE the parent zone's key rather than minting a new
	//    selector per subdomain. Two reasons:
	//      * the parent's DKIM TXT is already published and trusted; a
	//        fresh per-subdomain TXT adds nothing and means two DNS
	//        changes whenever we rotate.
	//      * Gmail's DMARC alignment prefers the key be in the parent
	//        zone for subdomain senders — a key-per-subdomain works but
	//        produces a more complex alignment story.
	//    If the parent has no key yet (edge case — someone called
	//    SetupSubdomainMail directly against a bare zone), fall back to
	//    generating one for the subdomain so we never leave mail
	//    unsigned.
	parentKeyDir := fmt.Sprintf("/etc/opendkim/keys/%s", parentDomain)
	parentHasKey := false
	if r, err := agent.RunCommand(ctx, "test", "-f", parentKeyDir+"/mail.private"); err == nil && r != nil {
		parentHasKey = true
	}

	if parentHasKey {
		// Reuse parent's selector for signing.
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
			`grep -qE '^\*@%s[[:space:]]' /etc/opendkim/signing.table 2>/dev/null || echo '*@%s mail._domainkey.%s' >> /etc/opendkim/signing.table`,
			escFqdn, fqdn, parentDomain))
	} else {
		// Fallback: mint a dedicated key for the subdomain.
		keyDir := fmt.Sprintf("/etc/opendkim/keys/%s", fqdn)
		agent.RunCommand(ctx, "mkdir", "-p", keyDir)
		agent.RunCommand(ctx, "opendkim-genkey", "-s", "mail", "-d", fqdn, "-D", keyDir)
		agent.RunCommand(ctx, "chown", "-R", "opendkim:opendkim", keyDir)
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
			`grep -qE '^\*@%s[[:space:]]' /etc/opendkim/signing.table 2>/dev/null || echo '*@%s mail._domainkey.%s' >> /etc/opendkim/signing.table`,
			escFqdn, fqdn, fqdn))
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
			`grep -qE '^mail\._domainkey\.%s[[:space:]]' /etc/opendkim/key.table 2>/dev/null || echo 'mail._domainkey.%s %s:mail:%s/mail.private' >> /etc/opendkim/key.table`,
			escFqdn, fqdn, fqdn, keyDir))
	}
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"grep -qxF '%s' /etc/opendkim/trusted.hosts 2>/dev/null || echo '%s' >> /etc/opendkim/trusted.hosts",
		fqdn, fqdn))
	agent.RunCommand(ctx, "systemctl", "restart", "opendkim")

	// 2. Postfix — accept mail for this subdomain NOW instead of lazily
	//    at CreateMailbox time. Closes the race where Postfix rejects
	//    inbound delivery in the window between domain create and
	//    mailbox create. Format is `<domain> OK` for hash: compatibility.
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		`grep -qE '^%s[[:space:]]' /etc/postfix/virtual_mailbox_domains 2>/dev/null || echo '%s OK' >> /etc/postfix/virtual_mailbox_domains`,
		escFqdn, fqdn))
	agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailbox_domains")
	agent.RunCommand(ctx, "systemctl", "reload", "postfix")

	// 3. DNS records in the parent zone. MX/SPF/DMARC always; DKIM TXT
	//    for the subdomain is only added when we minted a standalone
	//    key (parent-key reuse means DNS lookups for
	//    mail._domainkey.<sub>.<parent> follow the CNAME semantics —
	//    OpenDKIM itself is using the PARENT selector, so the subdomain
	//    TXT isn't required).
	//
	//    No separate `mail.sub` A record — subdomain mail traffic uses
	//    the parent's mail. hostname, which already has an A record.
	mailHost := fmt.Sprintf("mail.%s.", parentDomain)
	agent.RunCommand(ctx, "pdnsutil", "add-record", parentDomain, subPart, "MX", "3600",
		fmt.Sprintf("10 %s", mailHost))
	agent.RunCommand(ctx, "pdnsutil", "add-record", parentDomain, subPart, "TXT", "3600",
		fmt.Sprintf("\"v=spf1 ip4:%s ~all\"", serverIP))
	agent.RunCommand(ctx, "pdnsutil", "add-record", parentDomain, "_dmarc."+subPart, "TXT", "3600",
		fmt.Sprintf("\"v=DMARC1; p=none; rua=mailto:admin@%s\"", fqdn))

	var dkimValue string
	if !parentHasKey {
		keyDir := fmt.Sprintf("/etc/opendkim/keys/%s", fqdn)
		if r, _ := agent.RunCommand(ctx, "cat", fmt.Sprintf("%s/mail.txt", keyDir)); r != nil {
			if v := parseDKIMPublicKey(r.Output); v != "" {
				dkimValue = v
				agent.RunCommand(ctx, "pdnsutil", "add-record", parentDomain,
					"mail._domainkey."+subPart, "TXT", "3600",
					fmt.Sprintf("\"%s\"", v))
			}
		}
	}
	agent.RunCommand(ctx, "pdns_control", "reload")
	_ = escParent // kept for future IP-rewrite paths

	// 4. Persist to MongoDB under the parent zone's dns_records so the
	//    UI's DNS editor reflects them.
	var parentZone models.DNSZone
	if err := s.db.Collection(database.ColDNSZones).FindOne(ctx, bson.M{"domain": parentDomain}).Decode(&parentZone); err == nil {
		now := time.Now()
		recCol := s.db.Collection(database.ColDNSRecords)
		mxPri := 10
		toInsert := []interface{}{
			models.DNSRecord{ZoneID: parentZone.ID, Type: "MX", Name: subPart, Value: mailHost, TTL: 3600, Priority: &mxPri, CreatedAt: now, UpdatedAt: now},
			models.DNSRecord{ZoneID: parentZone.ID, Type: "TXT", Name: subPart, Value: fmt.Sprintf("v=spf1 ip4:%s ~all", serverIP), TTL: 3600, CreatedAt: now, UpdatedAt: now},
			models.DNSRecord{ZoneID: parentZone.ID, Type: "TXT", Name: "_dmarc." + subPart, Value: fmt.Sprintf("v=DMARC1; p=none; rua=mailto:admin@%s", fqdn), TTL: 3600, CreatedAt: now, UpdatedAt: now},
		}
		if dkimValue != "" {
			toInsert = append(toInsert, models.DNSRecord{ZoneID: parentZone.ID, Type: "TXT", Name: "mail._domainkey." + subPart, Value: dkimValue, TTL: 3600, CreatedAt: now, UpdatedAt: now})
		}
		recCol.InsertMany(ctx, toInsert)
		s.db.Collection(database.ColDNSZones).UpdateOne(ctx, bson.M{"_id": parentZone.ID}, bson.M{
			"$inc": bson.M{"serial": len(toInsert)},
			"$set": bson.M{"updated_at": now},
		})
	}
	return nil
}

// setupMailServer configures DKIM, Postfix virtual domain, and adds
// mail-related DNS records (MX, SPF, DKIM, DMARC) when a new zone is created.
//
// Every idempotent-append here anchors its grep on the full record
// format. The previous `grep -q 'example.com'` patterns treated the
// domain as a substring, so adding "foo.example.com" after "example.com"
// would see the older entry and silently skip the new one — leaving
// outbound mail unsigned and inbound bouncing "Domain not found".
func (s *DNSService) setupMailServer(ctx context.Context, domain, serverIP string, zone *models.DNSZone) {
	escDom := regexp.QuoteMeta(domain)

	// 1. Generate DKIM key
	keyDir := fmt.Sprintf("/etc/opendkim/keys/%s", domain)
	agent.RunCommand(ctx, "mkdir", "-p", keyDir)
	agent.RunCommand(ctx, "opendkim-genkey", "-s", "mail", "-d", domain, "-D", keyDir)
	agent.RunCommand(ctx, "chown", "-R", "opendkim:opendkim", keyDir)

	// OpenDKIM tables — anchored to avoid the substring-match bug.
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		`grep -qE '^\*@%s[[:space:]]' /etc/opendkim/signing.table 2>/dev/null || echo '*@%s mail._domainkey.%s' >> /etc/opendkim/signing.table`,
		escDom, domain, domain))
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		`grep -qE '^mail\._domainkey\.%s[[:space:]]' /etc/opendkim/key.table 2>/dev/null || echo 'mail._domainkey.%s %s:mail:%s/mail.private' >> /etc/opendkim/key.table`,
		escDom, domain, domain, keyDir))
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		"grep -qxF '%s' /etc/opendkim/trusted.hosts 2>/dev/null || echo '%s' >> /etc/opendkim/trusted.hosts",
		domain, domain))

	// Restart OpenDKIM
	agent.RunCommand(ctx, "systemctl", "restart", "opendkim")

	// 2. Add domain to Postfix virtual_mailbox_domains. main.cf references
	// this as `hash:`, so each line must be `<domain> OK` — a bare domain
	// name triggers the "expected format: key whitespace value" warning
	// on every postmap call and eventually produces an empty .db that
	// rejects inbound mail.
	agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
		`grep -qE '^%s[[:space:]]' /etc/postfix/virtual_mailbox_domains 2>/dev/null || echo '%s OK' >> /etc/postfix/virtual_mailbox_domains`,
		escDom, domain))
	agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailbox_domains")
	agent.RunCommand(ctx, "systemctl", "reload", "postfix")

	// 3. Read DKIM public key for DNS record
	dkimResult, _ := agent.RunCommand(ctx, "cat", fmt.Sprintf("%s/mail.txt", keyDir))
	dkimValue := ""
	if dkimResult != nil {
		dkimValue = parseDKIMPublicKey(dkimResult.Output)
	}

	// 4. Add mail DNS records to PowerDNS. mail A record uses the
	// 60s default so an IP migration of the mail host is fast; MX
	// stays 3600 since the hostname rarely changes.
	agent.RunCommand(ctx, "pdnsutil", "add-record", domain, "mail", "A", fmt.Sprint(defaultTTLFor("A")), serverIP)
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
		models.DNSRecord{ZoneID: zone.ID, Type: "A", Name: "mail", Value: serverIP, TTL: defaultTTLFor("A"), CreatedAt: now, UpdatedAt: now},
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
