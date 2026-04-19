package services

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type DashboardService struct {
	db *mongo.Database
}

func NewDashboardService(db *mongo.Database) *DashboardService {
	return &DashboardService{db: db}
}

// Response DTOs

type WHMDashboardStats struct {
	TotalDomains    int64 `json:"totalDomains"`
	ActiveApps      int64 `json:"activeApps"`
	Databases       int64 `json:"databases"`
	SSLCertificates int64 `json:"sslCertificates"`
}

type CPanelDashboardStats struct {
	Domains       int64  `json:"domains"`
	Apps          int64  `json:"apps"`
	Databases     int64  `json:"databases"`
	StorageUsed   string `json:"storageUsed"`
	StorageTotal  string `json:"storageTotal"`
	EmailAccounts int64  `json:"emailAccounts"`
	SSLCerts      int64  `json:"sslCerts"`
}

type DashboardActivity struct {
	ID        string `json:"id"`
	Action    string `json:"action"`
	Resource  string `json:"resource"`
	Timestamp string `json:"timestamp"`
	Status    string `json:"status"`
}

type ServerStatus struct {
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryPercent float64 `json:"memoryPercent"`
	DiskPercent   float64 `json:"diskPercent"`
	UptimeString  string  `json:"uptimeString"`
}

// GetWHMStats returns dashboard counts. vendor_owner sees global stats;
// other roles see only resources linked to their assigned domains.
func (s *DashboardService) GetWHMStats(ctx context.Context, userID, role string) (*WHMDashboardStats, error) {
	stats := &WHMDashboardStats{}

	// vendor_owner sees everything; others see only their own resources
	domainFilter := bson.M{}
	resourceFilter := bson.M{}
	if role != "vendor_owner" {
		userDomains, err := s.getUserDomains(ctx, userID)
		if err != nil || len(userDomains) == 0 {
			return stats, nil
		}
		domainFilter = bson.M{"domain": bson.M{"$in": userDomains}}
		resourceFilter = bson.M{"domain": bson.M{"$in": userDomains}}
	}

	totalDomains, err := s.db.Collection(database.ColDomains).CountDocuments(ctx, domainFilter)
	if err != nil {
		return nil, err
	}
	stats.TotalDomains = totalDomains

	appFilter := bson.M{"status": "running"}
	for k, v := range resourceFilter {
		appFilter[k] = v
	}
	activeApps, err := s.db.Collection(database.ColApps).CountDocuments(ctx, appFilter)
	if err != nil {
		fallbackFilter := bson.M{}
		for k, v := range resourceFilter {
			fallbackFilter[k] = v
		}
		activeApps, _ = s.db.Collection(database.ColApps).CountDocuments(ctx, fallbackFilter)
	}
	stats.ActiveApps = activeApps

	databases, err := s.db.Collection(database.ColDatabases).CountDocuments(ctx, resourceFilter)
	if err != nil {
		return nil, err
	}
	stats.Databases = databases

	sslCerts, err := s.db.Collection(database.ColSSLCerts).CountDocuments(ctx, resourceFilter)
	if err != nil {
		return nil, err
	}
	stats.SSLCertificates = sslCerts

	return stats, nil
}

// GetCPanelStats returns user-scoped counts filtered by the user's domains.
func (s *DashboardService) GetCPanelStats(ctx context.Context, userID string) (*CPanelDashboardStats, error) {
	stats := &CPanelDashboardStats{
		StorageUsed:  "0 GB",
		StorageTotal: "50 GB",
	}

	userDomains, username, err := s.getUserDomainsAndName(ctx, userID)
	if err != nil {
		return stats, nil
	}

	stats.Domains = int64(len(userDomains))
	if len(userDomains) > 0 {
		domainFilter := bson.M{"domain": bson.M{"$in": userDomains}}
		stats.Apps, _ = s.db.Collection(database.ColApps).CountDocuments(ctx, domainFilter)
		stats.Databases, _ = s.db.Collection(database.ColDatabases).CountDocuments(ctx, domainFilter)
		stats.EmailAccounts, _ = s.db.Collection(database.ColMailboxes).CountDocuments(ctx, domainFilter)
		stats.SSLCerts, _ = s.db.Collection(database.ColSSLCerts).CountDocuments(ctx, domainFilter)
	}

	// Even with zero domains, the user may have databases that aren't tied
	// to a domain (vendor-only dbs created via the new Vendor dropdown). Add
	// any database whose name starts with "<username>_" to the count so the
	// dashboard reflects what the user actually owns. Same trick for apps
	// (which are keyed on app.user) and mailboxes / SSL certs (domain-keyed,
	// already covered above when len(userDomains) > 0).
	if username != "" {
		// Apps for legacy single-app deploys keyed on user.
		appByUser, _ := s.db.Collection(database.ColApps).CountDocuments(ctx, bson.M{"user": username})
		if appByUser > stats.Apps {
			stats.Apps = appByUser
		}
		// Databases prefixed with "<username>_" — picks up the new
		// vendor-only databases (no domain attached).
		prefix := username + "_"
		dbByPrefix, _ := s.db.Collection(database.ColDatabases).CountDocuments(ctx, bson.M{"db_name": bson.M{"$regex": "^" + regexp.QuoteMeta(prefix)}})
		if dbByPrefix > stats.Databases {
			stats.Databases = dbByPrefix
		}
		// Real disk usage of /home/<username>/ via du -sb (apparent size,
		// bytes). Cheap on a small home dir, capped at 5s so a huge tree
		// doesn't stall the dashboard render. On error we fall through
		// to the default "0 GB".
		duCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		out, err := exec.CommandContext(duCtx, "du", "-sb", "--apparent-size", "/home/"+username).Output()
		cancel()
		if err == nil {
			if fields := strings.Fields(string(out)); len(fields) > 0 {
				if bytes, perr := strconv.ParseInt(fields[0], 10, 64); perr == nil {
					stats.StorageUsed = humanBytes(bytes)
				}
			}
		}
		// Per-user disk quota (if set on the Domain doc as DiskQuotaMB)
		// is the ceiling shown alongside StorageUsed. We sum quotas
		// across the user's domains since one user can own many.
		if cur, qerr := s.db.Collection(database.ColDomains).Find(ctx, bson.M{"user": username}); qerr == nil {
			defer cur.Close(ctx)
			var totalMB int64
			for cur.Next(ctx) {
				var d struct {
					DiskQuotaMB int64 `bson:"disk_quota_mb"`
				}
				if cur.Decode(&d) == nil {
					totalMB += d.DiskQuotaMB
				}
			}
			if totalMB > 0 {
				stats.StorageTotal = humanBytes(totalMB * 1024 * 1024)
			}
		}
	}

	return stats, nil
}

// humanBytes formats a byte count into the most-readable IEC unit
// (B/KB/MB/GB/TB). Always emits one decimal for values >= 1 KB so the
// dashboard tile shows "12.4 GB" rather than rounded "12 GB" — useful
// when the operator is hovering near a quota threshold.
func humanBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	const unit = 1024.0
	val := float64(b)
	units := []string{"KB", "MB", "GB", "TB"}
	for _, u := range units {
		val /= unit
		if val < unit {
			return fmt.Sprintf("%.1f %s", val, u)
		}
	}
	return fmt.Sprintf("%.1f PB", val/unit)
}

// getUserDomainsAndName returns every domain owned by the user (queried
// from the domains collection where Domain.User matches the user's
// linux username) AND the username itself. Used by GetCPanelStats to
// scope the dashboard's counters to the logged-in vendor.
//
// The previous implementation read user.Domains (a slice on the user
// document) which was never written by the domain creation flow — it
// always came back empty, so the dashboard showed zero for everyone.
// Domain.User has been the source of truth since the multi-tenant
// rewrite; this function reads it directly.
func (s *DashboardService) getUserDomainsAndName(ctx context.Context, userID string) ([]string, string, error) {
	objectID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return nil, "", err
	}
	var user struct {
		Username string `bson:"username"`
	}
	if err := s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": objectID}).Decode(&user); err != nil {
		return nil, "", err
	}
	if user.Username == "" {
		return nil, "", nil
	}
	cur, err := s.db.Collection(database.ColDomains).Find(ctx, bson.M{"user": user.Username})
	if err != nil {
		return nil, user.Username, err
	}
	defer cur.Close(ctx)
	var docs []struct {
		Domain string `bson:"domain"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, user.Username, err
	}
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.Domain)
	}
	return out, user.Username, nil
}

// getUserDomains is kept as a back-compat wrapper for any caller still
// using the older signature. New code should call getUserDomainsAndName.
func (s *DashboardService) getUserDomains(ctx context.Context, userID string) ([]string, error) {
	domains, _, err := s.getUserDomainsAndName(ctx, userID)
	return domains, err
}

// GetWHMActivity returns recent audit log entries.
// vendor_owner sees all activity; other roles see only their own.
func (s *DashboardService) GetWHMActivity(ctx context.Context, userID, role string) ([]DashboardActivity, error) {
	filter := bson.M{}
	if role != "vendor_owner" {
		filter["user.id"] = userID
	}
	return s.queryActivity(ctx, filter)
}

// GetCPanelActivity returns recent audit log entries for a specific user.
func (s *DashboardService) GetCPanelActivity(ctx context.Context, userID string) ([]DashboardActivity, error) {
	return s.queryActivity(ctx, bson.M{"user.id": userID})
}

func (s *DashboardService) queryActivity(ctx context.Context, filter bson.M) ([]DashboardActivity, error) {
	col := s.db.Collection(database.ColAuditLogs)
	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}}).SetLimit(10)

	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return []DashboardActivity{}, nil
	}
	defer cursor.Close(ctx)

	var results []DashboardActivity
	for cursor.Next(ctx) {
		var entry struct {
			ID           primitive.ObjectID `bson:"_id"`
			Action       string             `bson:"action"`
			ResourceType string             `bson:"resource_type"`
			Timestamp    time.Time          `bson:"timestamp"`
			Status       string             `bson:"status"`
		}
		if err := cursor.Decode(&entry); err != nil {
			continue
		}
		status := entry.Status
		if status == "" {
			status = "success"
		}
		results = append(results, DashboardActivity{
			ID:        entry.ID.Hex(),
			Action:    entry.Action,
			Resource:  entry.ResourceType,
			Timestamp: entry.Timestamp.Format(time.RFC3339),
			Status:    status,
		})
	}
	if results == nil {
		results = []DashboardActivity{}
	}
	return results, nil
}

// GetServerStatus returns live CPU, memory, disk, and uptime metrics from the Linux host.
func (s *DashboardService) GetServerStatus() (*ServerStatus, error) {
	return &ServerStatus{
		CPUPercent:    getCPUPercent(),
		MemoryPercent: getMemoryPercent(),
		DiskPercent:   getDiskPercent(),
		UptimeString:  getUptime(),
	}, nil
}

func getCPUPercent() float64 {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return 0
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 5 {
		return 0
	}
	user, _ := strconv.ParseFloat(fields[1], 64)
	nice, _ := strconv.ParseFloat(fields[2], 64)
	system, _ := strconv.ParseFloat(fields[3], 64)
	idle, _ := strconv.ParseFloat(fields[4], 64)
	total := user + nice + system + idle
	if total == 0 {
		return 0
	}
	return math.Round((user + nice + system) / total * 100)
}

func getMemoryPercent() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	var memTotal, memAvailable float64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		val, _ := strconv.ParseFloat(fields[1], 64)
		switch fields[0] {
		case "MemTotal:":
			memTotal = val
		case "MemAvailable:":
			memAvailable = val
		}
	}
	if memTotal == 0 {
		return 0
	}
	return math.Round((memTotal - memAvailable) / memTotal * 100)
}

func getDiskPercent() float64 {
	out, err := exec.Command("df", "--output=pcent", "/").Output()
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0
	}
	pct := strings.TrimSpace(lines[1])
	pct = strings.TrimSuffix(pct, "%")
	val, _ := strconv.ParseFloat(pct, 64)
	return val
}

func getUptime() string {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "N/A"
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "N/A"
	}
	seconds, _ := strconv.ParseFloat(fields[0], 64)
	days := int(seconds) / 86400
	hours := (int(seconds) % 86400) / 3600
	return fmt.Sprintf("%d days, %dh", days, hours)
}
