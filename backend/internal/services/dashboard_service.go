package services

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"runtime"
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

	// Absolute capacity + usage (bytes) so the UI can show "X of Y", not just a %.
	MemTotalBytes     uint64 `json:"memTotalBytes"`
	MemUsedBytes      uint64 `json:"memUsedBytes"`
	MemAvailableBytes uint64 `json:"memAvailableBytes"`
	SwapTotalBytes    uint64 `json:"swapTotalBytes"`
	SwapUsedBytes     uint64 `json:"swapUsedBytes"`
	DiskTotalBytes    uint64 `json:"diskTotalBytes"`
	DiskUsedBytes     uint64 `json:"diskUsedBytes"`
	DiskFreeBytes     uint64 `json:"diskFreeBytes"`

	// CPU detail.
	CPUCores    int     `json:"cpuCores"`
	LoadAvg1    float64 `json:"loadAvg1"`
	LoadAvg5    float64 `json:"loadAvg5"`
	LoadAvg15   float64 `json:"loadAvg15"`
	SwapPercent float64 `json:"swapPercent"`

	UptimeSeconds int64 `json:"uptimeSeconds"`
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

	// Active apps = running Deploy Software services (project_services). The
	// legacy `apps` collection is empty since the Deploy Software migration, so
	// counting it always returned 0. Owner sees every running service; other
	// roles are scoped to the projects their tenant owns.
	appFilter := bson.M{"status": "running"}
	if role != "vendor_owner" {
		projIDs := s.userProjectIDs(ctx, userID)
		if len(projIDs) == 0 {
			stats.ActiveApps = 0
		} else {
			appFilter["project_id"] = bson.M{"$in": projIDs}
			stats.ActiveApps, _ = s.db.Collection(database.ColProjectServices).CountDocuments(ctx, appFilter)
		}
	} else {
		stats.ActiveApps, _ = s.db.Collection(database.ColProjectServices).CountDocuments(ctx, appFilter)
	}

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
	st := &ServerStatus{
		CPUCores:     runtime.NumCPU(),
		CPUPercent:   getCPUPercent(),
		UptimeString: getUptime(),
	}

	// Memory + swap (bytes) from /proc/meminfo.
	memTotal, memAvail, swapTotal, swapFree := readMemInfo()
	st.MemTotalBytes = memTotal
	st.MemAvailableBytes = memAvail
	if memTotal >= memAvail {
		st.MemUsedBytes = memTotal - memAvail
	}
	if memTotal > 0 {
		st.MemoryPercent = math.Round(float64(st.MemUsedBytes) / float64(memTotal) * 100)
	}
	st.SwapTotalBytes = swapTotal
	if swapTotal >= swapFree {
		st.SwapUsedBytes = swapTotal - swapFree
	}
	if swapTotal > 0 {
		st.SwapPercent = math.Round(float64(st.SwapUsedBytes) / float64(swapTotal) * 100)
	}

	// Disk (bytes) for / via statfs.
	dTotal, dFree, dUsed := readDiskUsage("/")
	st.DiskTotalBytes = dTotal
	st.DiskFreeBytes = dFree
	st.DiskUsedBytes = dUsed
	if dTotal > 0 {
		st.DiskPercent = math.Round(float64(dUsed) / float64(dTotal) * 100)
	}

	// Load average.
	st.LoadAvg1, st.LoadAvg5, st.LoadAvg15 = readLoadAvg()

	// Uptime seconds.
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) > 0 {
			sec, _ := strconv.ParseFloat(fields[0], 64)
			st.UptimeSeconds = int64(sec)
		}
	}

	return st, nil
}

// getCPUPercent samples /proc/stat twice ~250ms apart and returns busy% over
// that interval — far more accurate than a single since-boot average.
func getCPUPercent() float64 {
	idle1, total1, ok1 := readCPUSample()
	if !ok1 {
		return 0
	}
	time.Sleep(250 * time.Millisecond)
	idle2, total2, ok2 := readCPUSample()
	if !ok2 {
		return 0
	}
	totalDelta := total2 - total1
	idleDelta := idle2 - idle1
	if totalDelta <= 0 {
		return 0
	}
	busy := (totalDelta - idleDelta) / totalDelta * 100
	if busy < 0 {
		busy = 0
	}
	if busy > 100 {
		busy = 100
	}
	return math.Round(busy)
}

// readCPUSample returns (idle, total) jiffies from the aggregate cpu line.
func readCPUSample() (idle, total float64, ok bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return 0, 0, false
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	for i := 1; i < len(fields); i++ {
		v, _ := strconv.ParseFloat(fields[i], 64)
		total += v
		// Fields: user nice system idle iowait irq softirq steal guest guest_nice
		// idle = field 4 (idle) + field 5 (iowait).
		if i == 4 || i == 5 {
			idle += v
		}
	}
	return idle, total, true
}

// readMemInfo returns MemTotal, MemAvailable, SwapTotal, SwapFree in bytes.
func readMemInfo() (memTotal, memAvail, swapTotal, swapFree uint64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, 0, 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		val, _ := strconv.ParseUint(fields[1], 10, 64)
		val *= 1024 // values are in kB
		switch fields[0] {
		case "MemTotal:":
			memTotal = val
		case "MemAvailable:":
			memAvail = val
		case "SwapTotal:":
			swapTotal = val
		case "SwapFree:":
			swapFree = val
		}
	}
	return
}

// readDiskUsage returns total, free, used bytes for the filesystem at path.
// Uses `df -B1` (byte blocks) so it stays portable across build targets.
func readDiskUsage(path string) (total, free, used uint64) {
	out, err := exec.Command("df", "-B1", "--output=size,used,avail", path).Output()
	if err != nil {
		return 0, 0, 0
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return 0, 0, 0
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 3 {
		return 0, 0, 0
	}
	total, _ = strconv.ParseUint(fields[0], 10, 64)
	used, _ = strconv.ParseUint(fields[1], 10, 64)
	free, _ = strconv.ParseUint(fields[2], 10, 64)
	return
}

// readLoadAvg returns the 1/5/15-minute load averages from /proc/loadavg.
func readLoadAvg() (l1, l5, l15 float64) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0
	}
	l1, _ = strconv.ParseFloat(fields[0], 64)
	l5, _ = strconv.ParseFloat(fields[1], 64)
	l15, _ = strconv.ParseFloat(fields[2], 64)
	return
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

// userProjectIDs returns the _ids of projects owned by the caller's tenant.
// For a tenant root (vendor_admin) userID == tenant_id, so this resolves their
// projects; staff whose own _id differs from the tenant id resolve nothing and
// fall back to 0 active apps rather than leaking cross-tenant counts.
func (s *DashboardService) userProjectIDs(ctx context.Context, userID string) []primitive.ObjectID {
	oid, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return nil
	}
	cur, err := s.db.Collection(database.ColProjects).Find(ctx,
		bson.M{"tenant_id": oid},
		options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil
	}
	defer cur.Close(ctx)
	var ids []primitive.ObjectID
	for cur.Next(ctx) {
		var doc struct {
			ID primitive.ObjectID `bson:"_id"`
		}
		if cur.Decode(&doc) == nil {
			ids = append(ids, doc.ID)
		}
	}
	return ids
}
