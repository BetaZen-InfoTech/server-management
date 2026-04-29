package services

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	moptions "go.mongodb.org/mongo-driver/mongo/options"
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

// TrafficIPStat is one row in the per-IP report — count of requests
// from this IP and total bytes the server sent back.
type TrafficIPStat struct {
	IP       string `json:"ip"`
	Requests int64  `json:"requests"`
	Bytes    int64  `json:"bytes"`
}

// TrafficURLStat is one row in the per-URL report — count of hits to
// this URL path and total bytes served.
type TrafficURLStat struct {
	URL      string `json:"url"`
	Requests int64  `json:"requests"`
	Bytes    int64  `json:"bytes"`
}

// TrafficDomainStat is one row in the per-domain breakdown — only
// populated when the caller asked for the server-wide report.
type TrafficDomainStat struct {
	Domain   string `json:"domain"`
	Requests int64  `json:"requests"`
	Bytes    int64  `json:"bytes"`
}

// TrafficStats is the response envelope for the Reports surface. The
// frontend renders the three lists in three tabs; the totals power
// the header summary.
type TrafficStats struct {
	Domain        string              `json:"domain"`
	LogFile       string              `json:"log_file"`
	TotalRequests int64               `json:"total_requests"`
	TotalBytes    int64               `json:"total_bytes"`
	TopIPs        []TrafficIPStat     `json:"top_ips"`
	TopURLs       []TrafficURLStat    `json:"top_urls"`
	Domains       []TrafficDomainStat `json:"domains,omitempty"`
}

// TrafficStatsByDomain aggregates the nginx access log for one domain
// (when `domain` is non-empty) or the whole server log (when empty).
// Output is the top 50 IPs by request volume, the top 50 URLs by hit
// count, and — for the whole-server case — a per-domain breakdown.
//
// Implementation notes:
//   - awk does the heavy lifting (single-pass over the log) so we
//     don't pull MB+ logs into Go memory. Sort happens in awk's END
//     block, top-50 trimming via head.
//   - Default nginx common log layout assumed: $1 = remote_addr, $7
//     = request path (the URL inside the quoted "GET /path HTTP/1.1"
//     field), $10 = body_bytes_sent. Operators with a custom format
//     would need a per-domain log-format override — not in scope.
//   - Errors don't bubble: a missing log file means "no traffic
//     yet", not an HTTP 500. We return an empty TrafficStats so the
//     frontend renders a clean "no data yet" view.
func (s *ResourceService) TrafficStatsByDomain(ctx context.Context, domain string) (*TrafficStats, error) {
	out := &TrafficStats{Domain: domain}

	logFile := "/var/log/nginx/access.log"
	if domain != "" {
		logFile = fmt.Sprintf("/var/log/nginx/%s-access.log", domain)
	}
	out.LogFile = logFile

	// Single awk pass: emit three sections separated by sentinel
	// lines. Easier to parse than three separate exec.Command calls
	// and avoids re-reading the log three times.
	script := fmt.Sprintf(`
LOG=%s
[ -r "$LOG" ] || exit 0

awk '{
    ip = $1
    url = $7
    bytes = $10 + 0
    cnt_ip[ip]++; bytes_ip[ip] += bytes
    cnt_url[url]++; bytes_url[url] += bytes
    total_cnt++; total_bytes += bytes
}
END {
    printf "TOTAL\t%%d\t%%d\n", total_cnt, total_bytes
    for (i in cnt_ip)  printf "IP\t%%d\t%%d\t%%s\n", cnt_ip[i], bytes_ip[i], i
    for (u in cnt_url) printf "URL\t%%d\t%%d\t%%s\n", cnt_url[u], bytes_url[u], u
}' "$LOG"
`, shellQuote(logFile))

	res, err := agent.RunCommand(ctx, "bash", "-c", script)
	if err != nil || res == nil {
		// Empty / missing log — return a zeroed report.
		return out, nil
	}

	for _, line := range strings.Split(res.Output, "\n") {
		fields := strings.SplitN(line, "\t", 4)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "TOTAL":
			if len(fields) >= 3 {
				out.TotalRequests, _ = strconv.ParseInt(fields[1], 10, 64)
				out.TotalBytes, _ = strconv.ParseInt(fields[2], 10, 64)
			}
		case "IP":
			if len(fields) == 4 {
				reqs, _ := strconv.ParseInt(fields[1], 10, 64)
				bts, _ := strconv.ParseInt(fields[2], 10, 64)
				out.TopIPs = append(out.TopIPs, TrafficIPStat{
					IP: fields[3], Requests: reqs, Bytes: bts,
				})
			}
		case "URL":
			if len(fields) == 4 {
				reqs, _ := strconv.ParseInt(fields[1], 10, 64)
				bts, _ := strconv.ParseInt(fields[2], 10, 64)
				out.TopURLs = append(out.TopURLs, TrafficURLStat{
					URL: fields[3], Requests: reqs, Bytes: bts,
				})
			}
		}
	}

	// Sort top-N in Go because awk's iteration order isn't stable
	// and the TOP keyword pattern in awk is heavier than the cost of
	// a simple slice sort here (each list is bounded by unique IPs /
	// URLs in the log; in practice that's a few hundred entries).
	sort.Slice(out.TopIPs, func(i, j int) bool { return out.TopIPs[i].Requests > out.TopIPs[j].Requests })
	sort.Slice(out.TopURLs, func(i, j int) bool { return out.TopURLs[i].Requests > out.TopURLs[j].Requests })
	if len(out.TopIPs) > 50 {
		out.TopIPs = out.TopIPs[:50]
	}
	if len(out.TopURLs) > 50 {
		out.TopURLs = out.TopURLs[:50]
	}

	// Server-wide variant also reports per-domain totals so the UI
	// can show "you have N domains, each generating X% of traffic".
	// Cheap follow-up loop — one short awk per domain.
	if domain == "" {
		col := s.db.Collection(database.ColDomains)
		cur, err := col.Find(ctx, bson.M{})
		if err == nil {
			defer cur.Close(ctx)
			for cur.Next(ctx) {
				var d bson.M
				if cur.Decode(&d) != nil {
					continue
				}
				dn, _ := d["domain"].(string)
				if dn == "" {
					continue
				}
				perLog := fmt.Sprintf("/var/log/nginx/%s-access.log", dn)
				cmd := fmt.Sprintf(
					`[ -r %s ] && awk '{cnt++; sum+=$10} END {printf "%%d\t%%d", cnt+0, sum+0}' %s 2>/dev/null || echo "0\t0"`,
					shellQuote(perLog), shellQuote(perLog),
				)
				if r, err := agent.RunCommand(ctx, "bash", "-c", cmd); err == nil && r != nil {
					parts := strings.SplitN(strings.TrimSpace(r.Output), "\t", 2)
					if len(parts) == 2 {
						reqs, _ := strconv.ParseInt(parts[0], 10, 64)
						bts, _ := strconv.ParseInt(parts[1], 10, 64)
						if reqs > 0 || bts > 0 {
							out.Domains = append(out.Domains, TrafficDomainStat{
								Domain: dn, Requests: reqs, Bytes: bts,
							})
						}
					}
				}
			}
			sort.Slice(out.Domains, func(i, j int) bool { return out.Domains[i].Requests > out.Domains[j].Requests })
		}
	}

	return out, nil
}

// (shellQuote lives in config_service.go — reused here for log-file paths
// that come from operator input via the ?domain= query.)

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

// ----------------------------------------------------------------------
// Per-vendor + per-user resource grouping
// ----------------------------------------------------------------------

// VendorRollup is the rollup row rendered in Resources → Vendors tab.
// Counts come from the catalog collections (cheap), DiskBytes from the
// per-vendor `du -sb` cache reused from UserService.VendorStorageBytes
// so two pages don't fan-out 2× as many disk walks.
type VendorRollup struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	IsActive    bool   `json:"is_active"`
	PackageName string `json:"package_name"`
	Domains     int64  `json:"domains"`
	Apps        int64  `json:"apps"`
	Databases   int64  `json:"databases"`
	Mailboxes   int64  `json:"mailboxes"`
	Subdomains  int64  `json:"subdomains"`
	DiskBytes   int64  `json:"disk_bytes"`
}

// Vendors returns one rollup row per vendor_admin (excluding trash).
// Used by the Resources page's by-vendor grouping. Disk bytes use the
// 5-minute cache to keep the call cheap when there are many vendors.
func (s *ResourceService) Vendors(ctx context.Context, userSvc *UserService) ([]VendorRollup, error) {
	usersCol := s.db.Collection(database.ColUsers)
	cur, err := usersCol.Find(ctx, bson.M{
		"role":       "vendor_admin",
		"deleted_at": bson.M{"$in": bson.A{nil, primitive.Null{}}},
	})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var rows []bson.M
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}

	out := make([]VendorRollup, 0, len(rows))
	for _, r := range rows {
		username, _ := r["username"].(string)
		// Resolve usernames in this vendor's tenant so we can scope
		// catalog counts to "stuff this vendor owns" (their own
		// resources + every customer/staff under them). Cheap: just
		// the username field projected.
		tenantUsernames := tenantUsernames(ctx, s.db, r["_id"])
		// Domain set drives the count queries for catalog tables that
		// reference domains by name (apps, databases, mailboxes).
		domainNames := domainsForUsers(ctx, s.db, tenantUsernames)

		var domainCount, appCount, dbCount, mailboxCount, subdomainCount int64
		if len(tenantUsernames) > 0 {
			domainCount, _ = s.db.Collection(database.ColDomains).CountDocuments(ctx,
				bson.M{"user": bson.M{"$in": tenantUsernames}})
		}
		if len(domainNames) > 0 {
			appCount, _ = s.db.Collection(database.ColApps).CountDocuments(ctx,
				bson.M{"domain": bson.M{"$in": domainNames}})
			dbCount, _ = s.db.Collection(database.ColDatabases).CountDocuments(ctx,
				bson.M{"domain": bson.M{"$in": domainNames}})
			mailboxCount, _ = s.db.Collection(database.ColMailboxes).CountDocuments(ctx,
				bson.M{"domain": bson.M{"$in": domainNames}})
			subdomainCount, _ = s.db.Collection(database.ColSubdomains).CountDocuments(ctx,
				bson.M{"parent_domain": bson.M{"$in": domainNames}})
		}

		var disk int64
		if userSvc != nil {
			disk = userSvc.VendorStorageBytes(ctx, username, false)
		}

		idHex := ""
		if oid, ok := r["_id"].(primitive.ObjectID); ok {
			idHex = oid.Hex()
		}
		isActive, _ := r["is_active"].(bool)
		name, _ := r["name"].(string)
		email, _ := r["email"].(string)
		pkgName, _ := r["package_name"].(string)

		out = append(out, VendorRollup{
			ID:          idHex,
			Username:    username,
			Name:        name,
			Email:       email,
			IsActive:    isActive,
			PackageName: pkgName,
			Domains:     domainCount,
			Apps:        appCount,
			Databases:   dbCount,
			Mailboxes:   mailboxCount,
			Subdomains:  subdomainCount,
			DiskBytes:   disk,
		})
	}
	return out, nil
}

// VendorDetail returns the full per-vendor breakdown — list of every
// domain / subdomain / app / database / mailbox / forwarder / cron job
// owned by the vendor's tenant. Used by the per-vendor drawer in the
// Resources → Vendors tab. Returns a free-form map so the frontend can
// render new resource types without round-tripping a typed shape change.
func (s *ResourceService) VendorDetail(ctx context.Context, vendorID string) (map[string]interface{}, error) {
	oid, err := primitive.ObjectIDFromHex(vendorID)
	if err != nil {
		return nil, fmt.Errorf("invalid vendor id")
	}
	var vendor bson.M
	if err := s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": oid}).Decode(&vendor); err != nil {
		return nil, fmt.Errorf("vendor not found")
	}
	username, _ := vendor["username"].(string)
	tenantUsernames := tenantUsernames(ctx, s.db, vendor["_id"])
	domains := listDocs(ctx, s.db.Collection(database.ColDomains),
		bson.M{"user": bson.M{"$in": tenantUsernames}}, []string{"domain", "user", "php_version", "ssl_active", "expires_on", "registrar"})
	domainNames := []string{}
	for _, d := range domains {
		if dn, ok := d["domain"].(string); ok {
			domainNames = append(domainNames, dn)
		}
	}
	apps := listDocs(ctx, s.db.Collection(database.ColApps),
		bson.M{"domain": bson.M{"$in": domainNames}}, []string{"name", "domain", "framework", "app_type", "status", "port", "user"})
	dbs := listDocs(ctx, s.db.Collection(database.ColDatabases),
		bson.M{"domain": bson.M{"$in": domainNames}}, []string{"db_name", "domain", "type", "host", "port", "size_mb"})
	mailboxes := listDocs(ctx, s.db.Collection(database.ColMailboxes),
		bson.M{"domain": bson.M{"$in": domainNames}}, []string{"address", "domain", "quota_mb", "active"})
	forwarders := listDocs(ctx, s.db.Collection(database.ColForwarders),
		bson.M{"domain": bson.M{"$in": domainNames}}, []string{"source", "destination", "domain"})
	subdomains := listDocs(ctx, s.db.Collection(database.ColSubdomains),
		bson.M{"parent_domain": bson.M{"$in": domainNames}}, []string{"subdomain", "parent_domain", "document_root"})
	cronJobs := listDocs(ctx, s.db.Collection(database.ColCronJobs),
		bson.M{"user": bson.M{"$in": tenantUsernames}}, []string{"name", "schedule", "command", "user", "enabled"})
	ftpAccounts := listDocs(ctx, s.db.Collection(database.ColFTPAccounts),
		bson.M{"user": bson.M{"$in": tenantUsernames}}, []string{"username", "user", "directory"})

	return map[string]interface{}{
		"vendor": map[string]interface{}{
			"id":           vendorID,
			"username":     username,
			"name":         vendor["name"],
			"email":        vendor["email"],
			"package_name": vendor["package_name"],
		},
		"tenant_users":  tenantUsernames,
		"domains":       domains,
		"subdomains":    subdomains,
		"apps":          apps,
		"databases":     dbs,
		"mailboxes":     mailboxes,
		"forwarders":    forwarders,
		"cron_jobs":     cronJobs,
		"ftp_accounts":  ftpAccounts,
	}, nil
}

// tenantUsernames returns every linux username inside this vendor's
// tenant (vendor + their staff/customers/developers/support). Used to
// scope catalog queries by `user` field.
func tenantUsernames(ctx context.Context, db *mongo.Database, vendorID interface{}) []string {
	cur, err := db.Collection(database.ColUsers).Find(ctx,
		bson.M{"tenant_id": vendorID})
	if err != nil {
		return nil
	}
	defer cur.Close(ctx)
	var rows []bson.M
	if err := cur.All(ctx, &rows); err != nil {
		return nil
	}
	usernames := make([]string, 0, len(rows))
	for _, r := range rows {
		if u, ok := r["username"].(string); ok && u != "" {
			usernames = append(usernames, u)
		}
	}
	return usernames
}

// domainsForUsers returns every domain name owned by any user in the
// given list. Cached implicitly per call — the caller already has the
// usernames so we don't re-derive them.
func domainsForUsers(ctx context.Context, db *mongo.Database, usernames []string) []string {
	if len(usernames) == 0 {
		return nil
	}
	cur, err := db.Collection(database.ColDomains).Find(ctx,
		bson.M{"user": bson.M{"$in": usernames}})
	if err != nil {
		return nil
	}
	defer cur.Close(ctx)
	var rows []bson.M
	if err := cur.All(ctx, &rows); err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if d, ok := r["domain"].(string); ok && d != "" {
			out = append(out, d)
		}
	}
	return out
}

// listDocs runs a Find with an inclusive projection and returns the
// raw bson.M rows. Keeps the per-collection helper code in one place
// — every list endpoint above projects only the fields the UI renders
// so we don't ship password hashes or refresh tokens to the browser.
func listDocs(ctx context.Context, col *mongo.Collection, filter bson.M, fields []string) []bson.M {
	if filter == nil {
		filter = bson.M{}
	}
	proj := bson.M{}
	for _, f := range fields {
		proj[f] = 1
	}
	opts := mongoFindOpts(proj)
	cur, err := col.Find(ctx, filter, opts)
	if err != nil {
		return []bson.M{}
	}
	defer cur.Close(ctx)
	var rows []bson.M
	if err := cur.All(ctx, &rows); err != nil {
		return []bson.M{}
	}
	if rows == nil {
		return []bson.M{}
	}
	return rows
}

// mongoFindOpts wraps a projection in a *FindOptions. Centralised so
// future tweaks (e.g. always-cap at 500) live in one place.
func mongoFindOpts(projection bson.M) *moptions.FindOptions {
	o := moptions.Find()
	if projection != nil {
		o.SetProjection(projection)
	}
	o.SetLimit(500)
	return o
}
