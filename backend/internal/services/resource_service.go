package services

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type ResourceService struct {
	db *mongo.Database
}

func NewResourceService(db *mongo.Database) *ResourceService {
	return &ResourceService{db: db}
}

// DiskQuota represents a single filesystem mount point's usage.
type DiskQuota struct {
	Path    string  `json:"path"`
	Used    float64 `json:"used"`
	Total   float64 `json:"total"`
	Percent float64 `json:"percent"`
}

// DomainBandwidthEntry represents bandwidth usage for a single domain.
type DomainBandwidthEntry struct {
	Domain        string  `json:"domain"`
	BytesIn       string  `json:"bytesIn"`
	BytesOut      string  `json:"bytesOut"`
	TotalTransfer string  `json:"totalTransfer"`
	Percent       float64 `json:"percent"`
}

func bytesToHuman(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	val := float64(bytes)
	for _, unit := range units {
		val /= 1024
		if val < 1024 || unit == "TB" {
			return fmt.Sprintf("%.1f %s", val, unit)
		}
	}
	return fmt.Sprintf("%.1f TB", val)
}

// Summary returns disk quotas as an array of mount point usage.
func (s *ResourceService) Summary(ctx context.Context) ([]DiskQuota, error) {
	var quotas []DiskQuota

	// Get all relevant mount points via df
	result, err := agent.RunCommand(ctx, "df", "-BG")
	if err != nil {
		return quotas, fmt.Errorf("failed to get disk usage: %w", err)
	}

	lines := strings.Split(result.Output, "\n")
	for i, line := range lines {
		if i == 0 { // skip header
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		mountPoint := fields[5]

		// Only include relevant mount points
		relevant := false
		relevantPaths := []string{"/", "/var/www", "/var/lib/mongodb", "/var/mail", "/tmp", "/home", "/var/log"}
		for _, rp := range relevantPaths {
			if mountPoint == rp {
				relevant = true
				break
			}
		}
		if !relevant {
			continue
		}

		totalStr := strings.TrimSuffix(fields[1], "G")
		usedStr := strings.TrimSuffix(fields[2], "G")
		total, _ := strconv.ParseFloat(totalStr, 64)
		used, _ := strconv.ParseFloat(usedStr, 64)

		percent := 0.0
		if total > 0 {
			percent = math.Round(used / total * 100)
		}

		quotas = append(quotas, DiskQuota{
			Path:    mountPoint,
			Used:    used,
			Total:   total,
			Percent: percent,
		})
	}

	// If no specific mount points found, at least include root
	if len(quotas) == 0 {
		if result, err := agent.RunCommand(ctx, "df", "-BG", "/"); err == nil {
			lines := strings.Split(result.Output, "\n")
			if len(lines) >= 2 {
				fields := strings.Fields(lines[1])
				if len(fields) >= 5 {
					totalStr := strings.TrimSuffix(fields[1], "G")
					usedStr := strings.TrimSuffix(fields[2], "G")
					total, _ := strconv.ParseFloat(totalStr, 64)
					used, _ := strconv.ParseFloat(usedStr, 64)
					percent := 0.0
					if total > 0 {
						percent = math.Round(used / total * 100)
					}
					quotas = append(quotas, DiskQuota{
						Path:    "/",
						Used:    used,
						Total:   total,
						Percent: percent,
					})
				}
			}
		}
	}

	// Also add specific directory sizes for paths not on separate partitions
	dirSizes := map[string]string{
		"/var/www":         "/var/www",
		"/var/lib/mongodb": "/var/lib/mongodb",
		"/var/mail":        "/var/mail",
		"/tmp":             "/tmp",
	}

	// Check which paths already have dedicated mount points
	existingPaths := make(map[string]bool)
	for _, q := range quotas {
		existingPaths[q.Path] = true
	}

	// Get root total for calculating percentages of directory sizes
	var rootTotal float64
	for _, q := range quotas {
		if q.Path == "/" {
			rootTotal = q.Total
			break
		}
	}

	for label, dir := range dirSizes {
		if existingPaths[label] {
			continue
		}
		// Get directory size using du
		duResult, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("du -sB1 %s 2>/dev/null | head -1", dir))
		if err != nil {
			continue
		}
		duFields := strings.Fields(duResult.Output)
		if len(duFields) < 1 {
			continue
		}
		usedBytes, _ := strconv.ParseInt(duFields[0], 10, 64)
		usedGB := math.Round(float64(usedBytes)/1073741824*10) / 10

		// Use a reasonable total (portion of root or a default)
		totalGB := rootTotal
		if totalGB <= 0 {
			totalGB = 100
		}
		percent := 0.0
		if totalGB > 0 {
			percent = math.Round(usedGB / totalGB * 100)
		}

		quotas = append(quotas, DiskQuota{
			Path:    label,
			Used:    usedGB,
			Total:   totalGB,
			Percent: percent,
		})
	}

	return quotas, nil
}

// DomainUsage returns detailed resource usage for a specific domain.
func (s *ResourceService) DomainUsage(ctx context.Context, domain string) (map[string]interface{}, error) {
	// Find domain record
	col := s.db.Collection(database.ColDomains)
	var domainDoc bson.M
	if err := col.FindOne(ctx, bson.M{"domain": domain}).Decode(&domainDoc); err != nil {
		return nil, fmt.Errorf("domain not found: %s", domain)
	}

	user, _ := domainDoc["user"].(string)
	usage := make(map[string]interface{})
	usage["domain"] = domain
	usage["user"] = user

	// Disk usage for user home
	if user != "" {
		if result, err := agent.RunCommand(ctx, "du", "-sb", fmt.Sprintf("/home/%s/", user)); err == nil {
			fields := strings.Fields(result.Output)
			if len(fields) >= 1 {
				usage["disk_bytes"], _ = strconv.ParseInt(fields[0], 10, 64)
			}
		}
	}

	// Count sub-resources
	appCount, _ := s.db.Collection(database.ColApps).CountDocuments(ctx, bson.M{"domain": domain})
	dbCount, _ := s.db.Collection(database.ColDatabases).CountDocuments(ctx, bson.M{"domain": domain})
	mailboxCount, _ := s.db.Collection(database.ColMailboxes).CountDocuments(ctx, bson.M{"domain": domain})

	usage["apps"] = appCount
	usage["databases"] = dbCount
	usage["mailboxes"] = mailboxCount

	return usage, nil
}

// Bandwidth returns per-domain bandwidth statistics from nginx access logs.
func (s *ResourceService) Bandwidth(ctx context.Context, period, interval string) ([]DomainBandwidthEntry, error) {
	var entries []DomainBandwidthEntry

	// Get all domains from the database
	col := s.db.Collection(database.ColDomains)
	cursor, err := col.Find(ctx, bson.M{})
	if err != nil {
		return entries, fmt.Errorf("failed to list domains: %w", err)
	}
	defer cursor.Close(ctx)

	var domains []bson.M
	if err := cursor.All(ctx, &domains); err != nil {
		return entries, fmt.Errorf("failed to decode domains: %w", err)
	}

	// Get total server bandwidth for percent calculation
	var serverTotalBytes int64
	if result, err := agent.RunCommand(ctx, "bash", "-c", "awk '{sum+=$10} END {print sum}' /var/log/nginx/access.log 2>/dev/null"); err == nil {
		serverTotalBytes, _ = strconv.ParseInt(strings.TrimSpace(result.Output), 10, 64)
	}
	if serverTotalBytes <= 0 {
		serverTotalBytes = 1 // avoid divide by zero
	}

	for _, d := range domains {
		domainName, _ := d["domain"].(string)
		if domainName == "" {
			continue
		}

		// Parse nginx access log for this domain
		logFile := fmt.Sprintf("/var/log/nginx/%s-access.log", domainName)

		// Get total bytes (request body size ≈ bytes in, response size ≈ bytes out)
		var bytesOut int64
		cmd := fmt.Sprintf("awk '{sum+=$10} END {print sum}' %s 2>/dev/null", logFile)
		if result, err := agent.RunCommand(ctx, "bash", "-c", cmd); err == nil {
			bytesOut, _ = strconv.ParseInt(strings.TrimSpace(result.Output), 10, 64)
		}

		var bytesIn int64
		cmd = fmt.Sprintf("awk '{sum+=$11} END {print sum}' %s 2>/dev/null || echo 0", logFile)
		if result, err := agent.RunCommand(ctx, "bash", "-c", cmd); err == nil {
			val, _ := strconv.ParseInt(strings.TrimSpace(result.Output), 10, 64)
			if val > 0 {
				bytesIn = val
			} else {
				// Estimate bytes in as ~15% of bytes out for typical web traffic
				bytesIn = bytesOut * 15 / 100
			}
		}

		totalBytes := bytesIn + bytesOut
		if totalBytes == 0 {
			continue // skip domains with no traffic
		}

		percent := math.Round(float64(totalBytes) / float64(serverTotalBytes) * 100)
		if percent > 100 {
			percent = 100
		}

		entries = append(entries, DomainBandwidthEntry{
			Domain:        domainName,
			BytesIn:       bytesToHuman(bytesIn),
			BytesOut:      bytesToHuman(bytesOut),
			TotalTransfer: bytesToHuman(totalBytes),
			Percent:       percent,
		})
	}

	return entries, nil
}

// BandwidthByDomain returns bandwidth usage for a specific domain.
func (s *ResourceService) BandwidthByDomain(ctx context.Context, domain string) (map[string]interface{}, error) {
	usage := map[string]interface{}{
		"domain": domain,
	}

	logFile := fmt.Sprintf("/var/log/nginx/%s-access.log", domain)
	cmd := fmt.Sprintf("awk '{sum+=$10} END {print sum}' %s 2>/dev/null", logFile)
	if result, err := agent.RunCommand(ctx, "bash", "-c", cmd); err == nil {
		totalBytes, _ := strconv.ParseInt(strings.TrimSpace(result.Output), 10, 64)
		usage["total_bytes"] = totalBytes
	}

	// Request count
	cmd = fmt.Sprintf("wc -l < %s 2>/dev/null", logFile)
	if result, err := agent.RunCommand(ctx, "bash", "-c", cmd); err == nil {
		count, _ := strconv.ParseInt(strings.TrimSpace(result.Output), 10, 64)
		usage["request_count"] = count
	}

	return usage, nil
}

// UpdateLimits updates resource limits (disk quota, bandwidth, etc.) for a domain.
func (s *ResourceService) UpdateLimits(ctx context.Context, domain string, limits map[string]interface{}) error {
	col := s.db.Collection(database.ColDomains)
	var domainDoc bson.M
	if err := col.FindOne(ctx, bson.M{"domain": domain}).Decode(&domainDoc); err != nil {
		return fmt.Errorf("domain not found: %s", domain)
	}

	user, _ := domainDoc["user"].(string)

	// Apply disk quota if specified
	if quotaMB, ok := limits["disk_quota_mb"]; ok {
		if mb, ok := quotaMB.(float64); ok && user != "" {
			if err := agent.SetDiskQuota(ctx, user, int(mb)); err != nil {
				return fmt.Errorf("failed to set disk quota: %w", err)
			}
		}
	}

	// Update domain record with new limits
	_, err := col.UpdateOne(ctx, bson.M{"domain": domain}, bson.M{"$set": bson.M{"limits": limits}})
	return err
}
