package services

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
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

// GetWHMStats returns aggregate counts across all collections (admin-level).
func (s *DashboardService) GetWHMStats(ctx context.Context) (*WHMDashboardStats, error) {
	stats := &WHMDashboardStats{}

	totalDomains, err := s.db.Collection(database.ColDomains).CountDocuments(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	stats.TotalDomains = totalDomains

	activeApps, err := s.db.Collection(database.ColApps).CountDocuments(ctx, bson.M{"status": "running"})
	if err != nil {
		// Fallback: count all apps
		activeApps, _ = s.db.Collection(database.ColApps).CountDocuments(ctx, bson.M{})
	}
	stats.ActiveApps = activeApps

	databases, err := s.db.Collection(database.ColDatabases).CountDocuments(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	stats.Databases = databases

	sslCerts, err := s.db.Collection(database.ColSSLCerts).CountDocuments(ctx, bson.M{})
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

	userDomains, err := s.getUserDomains(ctx, userID)
	if err != nil || len(userDomains) == 0 {
		return stats, nil
	}

	domainFilter := bson.M{"domain": bson.M{"$in": userDomains}}

	stats.Domains = int64(len(userDomains))
	stats.Apps, _ = s.db.Collection(database.ColApps).CountDocuments(ctx, domainFilter)
	stats.Databases, _ = s.db.Collection(database.ColDatabases).CountDocuments(ctx, domainFilter)
	stats.EmailAccounts, _ = s.db.Collection(database.ColMailboxes).CountDocuments(ctx, domainFilter)
	stats.SSLCerts, _ = s.db.Collection(database.ColSSLCerts).CountDocuments(ctx, domainFilter)

	return stats, nil
}

func (s *DashboardService) getUserDomains(ctx context.Context, userID string) ([]string, error) {
	objectID, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return nil, err
	}
	var user struct {
		Domains []string `bson:"domains"`
	}
	err = s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": objectID}).Decode(&user)
	if err != nil {
		return nil, err
	}
	return user.Domains, nil
}

// GetWHMActivity returns recent audit log entries (all users).
func (s *DashboardService) GetWHMActivity(ctx context.Context) ([]DashboardActivity, error) {
	return s.queryActivity(ctx, bson.M{})
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
