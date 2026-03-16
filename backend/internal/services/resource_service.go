package services

import (
	"context"
	"fmt"
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

// Summary returns an overall resource usage summary for the server.
func (s *ResourceService) Summary(ctx context.Context) (map[string]interface{}, error) {
	summary := make(map[string]interface{})

	// Disk usage
	if result, err := agent.RunCommand(ctx, "df", "-B1", "/"); err == nil {
		lines := strings.Split(result.Output, "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[1])
			if len(fields) >= 5 {
				total, _ := strconv.ParseInt(fields[1], 10, 64)
				used, _ := strconv.ParseInt(fields[2], 10, 64)
				available, _ := strconv.ParseInt(fields[3], 10, 64)
				summary["disk"] = map[string]interface{}{
					"total":     total,
					"used":      used,
					"available": available,
					"percent":   strings.TrimSuffix(fields[4], "%"),
				}
			}
		}
	}

	// Count resources
	domainCount, _ := s.db.Collection(database.ColDomains).CountDocuments(ctx, bson.M{})
	dbCount, _ := s.db.Collection(database.ColDatabases).CountDocuments(ctx, bson.M{})
	mailboxCount, _ := s.db.Collection(database.ColMailboxes).CountDocuments(ctx, bson.M{})
	appCount, _ := s.db.Collection(database.ColApps).CountDocuments(ctx, bson.M{})
	userCount, _ := s.db.Collection(database.ColUsers).CountDocuments(ctx, bson.M{})

	summary["domains"] = domainCount
	summary["databases"] = dbCount
	summary["mailboxes"] = mailboxCount
	summary["apps"] = appCount
	summary["users"] = userCount

	// Memory
	if result, err := agent.RunCommand(ctx, "free", "-b"); err == nil {
		for _, line := range strings.Split(result.Output, "\n") {
			if strings.HasPrefix(line, "Mem:") {
				fields := strings.Fields(line)
				if len(fields) >= 3 {
					total, _ := strconv.ParseInt(fields[1], 10, 64)
					used, _ := strconv.ParseInt(fields[2], 10, 64)
					summary["memory"] = map[string]interface{}{
						"total":   total,
						"used":    used,
						"percent": float64(used) / float64(total) * 100,
					}
				}
			}
		}
	}

	return summary, nil
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

// Bandwidth returns server-wide bandwidth statistics for a period and interval.
func (s *ResourceService) Bandwidth(ctx context.Context, period, interval string) (map[string]interface{}, error) {
	bandwidth := make(map[string]interface{})

	// Try vnstat first
	if result, err := agent.RunCommand(ctx, "vnstat", "--json"); err == nil {
		bandwidth["source"] = "vnstat"
		bandwidth["data"] = result.Output
	} else {
		// Fallback: parse nginx access log for bytes
		if result, err := agent.RunCommand(ctx, "bash", "-c", "awk '{sum+=$10} END {print sum}' /var/log/nginx/access.log 2>/dev/null"); err == nil {
			totalBytes, _ := strconv.ParseInt(strings.TrimSpace(result.Output), 10, 64)
			bandwidth["total_bytes"] = totalBytes
			bandwidth["source"] = "nginx_logs"
		}
	}

	bandwidth["period"] = period
	bandwidth["interval"] = interval
	return bandwidth, nil
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
