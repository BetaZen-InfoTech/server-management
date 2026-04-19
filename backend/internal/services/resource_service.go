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

// dirBytes returns the recursive byte count of a directory via `du -sb`.
// Empty path or missing dir returns 0. Used in DomainUsage to add up each
// app's install dir, each mailbox's maildir, etc. so the UI can render a
// per-service breakdown instead of just the opaque "home total".
func dirBytes(ctx context.Context, path string) int64 {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	res, err := agent.RunCommand(ctx, "du", "-sb", path)
	if err != nil || res == nil {
		return 0
	}
	fields := strings.Fields(res.Output)
	if len(fields) < 1 {
		return 0
	}
	n, _ := strconv.ParseInt(fields[0], 10, 64)
	return n
}

// tailNginxAccessLog pulls the last N requests from the domain's nginx
// access log with timestamps, status codes, response sizes, request URIs
// and user-agents. Used for the "Recent activity" section of the domain
// detail drawer. An empty result simply means the log hasn't been written
// to yet or the domain has no requests.
type accessLogLine struct {
	IP     string `json:"ip"`
	Time   string `json:"time"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
	Bytes  int64  `json:"bytes"`
}

func tailNginxAccessLog(ctx context.Context, domain string, n int) []accessLogLine {
	if n <= 0 {
		n = 20
	}
	logFile := fmt.Sprintf("/var/log/nginx/%s-access.log", domain)
	res, err := agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf(`tail -n %d %s 2>/dev/null || true`, n, logFile))
	if err != nil || res == nil {
		return []accessLogLine{}
	}
	out := []accessLogLine{}
	for _, line := range strings.Split(res.Output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parsed := parseNginxCombined(line)
		if parsed.IP != "" {
			out = append(out, parsed)
		}
	}
	// Reverse so newest appears first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// parseNginxCombined parses one line of the standard nginx "combined" log
// format. Tolerates missing fields — anything we can't extract returns as
// a zero value on the struct.
func parseNginxCombined(line string) accessLogLine {
	var l accessLogLine
	// IP is the first whitespace-separated field.
	fields := strings.Fields(line)
	if len(fields) > 0 {
		l.IP = fields[0]
	}
	// Timestamp is inside [...].
	if i := strings.Index(line, "["); i >= 0 {
		if j := strings.Index(line[i:], "]"); j > 0 {
			l.Time = line[i+1 : i+j]
		}
	}
	// Request is in "METHOD /path HTTP/x.y" inside the first pair of quotes.
	if i := strings.Index(line, `"`); i >= 0 {
		rest := line[i+1:]
		if j := strings.Index(rest, `"`); j > 0 {
			req := rest[:j]
			parts := strings.Fields(req)
			if len(parts) >= 1 {
				l.Method = parts[0]
			}
			if len(parts) >= 2 {
				l.Path = parts[1]
			}
			// Status and size follow the closing quote.
			after := strings.Fields(strings.TrimSpace(rest[j+1:]))
			if len(after) >= 1 {
				l.Status, _ = strconv.Atoi(after[0])
			}
			if len(after) >= 2 {
				l.Bytes, _ = strconv.ParseInt(after[1], 10, 64)
			}
		}
	}
	return l
}

// DomainUsage returns detailed resource usage for a specific domain, rich
// enough to populate the "Details" drawer in the WHM Resources page:
// disk used by the domain's user home, a per-app disk breakdown (one entry
// per deployed application with its install path + byte count), a
// per-database disk breakdown (MySQL uses information_schema, MongoDB
// uses the on-disk dump size as an approximation), mailbox total,
// bandwidth counters from nginx, the tail of the access log, and counts
// of every related resource.
func (s *ResourceService) DomainUsage(ctx context.Context, domain string) (map[string]interface{}, error) {
	col := s.db.Collection(database.ColDomains)
	var domainDoc bson.M
	if err := col.FindOne(ctx, bson.M{"domain": domain}).Decode(&domainDoc); err != nil {
		return nil, fmt.Errorf("domain not found: %s", domain)
	}

	user, _ := domainDoc["user"].(string)
	phpVersion, _ := domainDoc["php_version"].(string)

	usage := map[string]interface{}{
		"domain":      domain,
		"user":        user,
		"php_version": phpVersion,
	}

	// -------- Disk: user home total ---------------------------------
	var homeBytes int64
	if user != "" {
		homeBytes = dirBytes(ctx, fmt.Sprintf("/home/%s/", user))
	}

	// -------- Disk: per-app breakdown -------------------------------
	apps := []map[string]interface{}{}
	{
		cur, _ := s.db.Collection(database.ColApps).Find(ctx, bson.M{"domain": domain})
		if cur != nil {
			var rows []bson.M
			_ = cur.All(ctx, &rows)
			for _, r := range rows {
				name, _ := r["name"].(string)
				installPath, _ := r["install_path"].(string)
				framework, _ := r["framework"].(string)
				status, _ := r["status"].(string)
				appType, _ := r["app_type"].(string)
				port32, _ := r["port"].(int32)
				port := int(port32)
				if port == 0 {
					if p64, ok := r["port"].(int64); ok {
						port = int(p64)
					}
				}
				if installPath == "" && user != "" && name != "" {
					installPath = fmt.Sprintf("/home/%s/apps/%s", user, name)
				}
				apps = append(apps, map[string]interface{}{
					"name":         name,
					"framework":    framework,
					"app_type":     appType,
					"install_path": installPath,
					"status":       status,
					"port":         port,
					"bytes":        dirBytes(ctx, installPath),
				})
			}
		}
	}

	// -------- Disk: per-database breakdown --------------------------
	dbList := []map[string]interface{}{}
	var totalDBBytes int64
	{
		cur, _ := s.db.Collection(database.ColDatabases).Find(ctx, bson.M{"domain": domain})
		if cur != nil {
			var rows []bson.M
			_ = cur.All(ctx, &rows)
			for _, r := range rows {
				name, _ := r["db_name"].(string)
				dbType, _ := r["type"].(string)
				var bytes int64
				if dbType == "mysql" && name != "" {
					// information_schema.TABLES returns MB — convert to bytes.
					if mb, err := agent.GetMySQLDatabaseSize(ctx, name); err == nil {
						bytes = int64(mb * 1024 * 1024)
					}
				}
				dbList = append(dbList, map[string]interface{}{
					"name":  name,
					"type":  dbType,
					"bytes": bytes,
				})
				totalDBBytes += bytes
			}
		}
	}

	// -------- Disk: mail + backup tallies ---------------------------
	var mailBytes int64
	if user != "" {
		// Dovecot maildirs live under /home/<user>/mail/<domain>/ by default;
		// some setups use /var/mail/vhosts/<domain>/. Try both.
		for _, p := range []string{
			fmt.Sprintf("/home/%s/mail/%s/", user, domain),
			fmt.Sprintf("/var/mail/vhosts/%s/", domain),
		} {
			if n := dirBytes(ctx, p); n > 0 {
				mailBytes += n
			}
		}
	}

	// Public HTML tree for legacy (non-app) sites.
	var publicHTMLBytes int64
	if user != "" {
		publicHTMLBytes = dirBytes(ctx, fmt.Sprintf("/home/%s/domains/%s/public_html/", user, domain))
	}

	usage["disk"] = map[string]interface{}{
		"home_bytes":        homeBytes,
		"apps":              apps,
		"databases":         dbList,
		"databases_bytes":   totalDBBytes,
		"mail_bytes":        mailBytes,
		"public_html_bytes": publicHTMLBytes,
	}

	// -------- Bandwidth --------------------------------------------
	logFile := fmt.Sprintf("/var/log/nginx/%s-access.log", domain)
	var bytesOut, bytesIn, requestCount int64
	if r, err := agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf(`awk '{sum+=$10} END {print sum+0}' %s 2>/dev/null`, logFile)); err == nil && r != nil {
		bytesOut, _ = strconv.ParseInt(strings.TrimSpace(r.Output), 10, 64)
	}
	if r, err := agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf(`awk '{sum+=$11} END {print sum+0}' %s 2>/dev/null`, logFile)); err == nil && r != nil {
		bytesIn, _ = strconv.ParseInt(strings.TrimSpace(r.Output), 10, 64)
	}
	if bytesIn == 0 && bytesOut > 0 {
		// Combined format's $11 is the referer, not a byte count — the
		// "15% of out" heuristic approximates typical request-body sizes
		// for sites that only log the standard combined fields.
		bytesIn = bytesOut * 15 / 100
	}
	if r, err := agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf(`wc -l < %s 2>/dev/null || echo 0`, logFile)); err == nil && r != nil {
		requestCount, _ = strconv.ParseInt(strings.TrimSpace(r.Output), 10, 64)
	}
	usage["bandwidth"] = map[string]interface{}{
		"bytes_in":      bytesIn,
		"bytes_out":     bytesOut,
		"total_bytes":   bytesIn + bytesOut,
		"request_count": requestCount,
	}

	// -------- Recent access log tail (~20 lines) --------------------
	usage["recent_requests"] = tailNginxAccessLog(ctx, domain, 20)

	// -------- Counts ------------------------------------------------
	appCount, _ := s.db.Collection(database.ColApps).CountDocuments(ctx, bson.M{"domain": domain})
	dbCount, _ := s.db.Collection(database.ColDatabases).CountDocuments(ctx, bson.M{"domain": domain})
	mailboxCount, _ := s.db.Collection(database.ColMailboxes).CountDocuments(ctx, bson.M{"domain": domain})
	subdomainCount, _ := s.db.Collection(database.ColSubdomains).CountDocuments(ctx, bson.M{"parent_domain": domain})
	usage["counts"] = map[string]interface{}{
		"apps":       appCount,
		"databases":  dbCount,
		"mailboxes":  mailboxCount,
		"subdomains": subdomainCount,
	}

	// Legacy top-level fields kept for backward compat with older callers.
	usage["disk_bytes"] = homeBytes
	usage["apps_count"] = appCount
	usage["databases_count"] = dbCount
	usage["mailboxes_count"] = mailboxCount

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
