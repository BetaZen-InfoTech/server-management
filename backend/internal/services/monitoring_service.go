package services

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MonitoringService struct {
	db *mongo.Database
}

func NewMonitoringService(db *mongo.Database) *MonitoringService {
	return &MonitoringService{db: db}
}

// SystemInfo returns static system information (hostname, OS, CPU, RAM, disks).
func (s *MonitoringService) SystemInfo(ctx context.Context) (map[string]interface{}, error) {
	info := make(map[string]interface{})

	if result, err := agent.RunCommand(ctx, "hostname"); err == nil {
		info["hostname"] = strings.TrimSpace(result.Output)
	}
	if result, err := agent.RunCommand(ctx, "hostname", "-I"); err == nil {
		parts := strings.Fields(strings.TrimSpace(result.Output))
		if len(parts) > 0 {
			info["ip"] = parts[0]
		}
	}
	if result, err := agent.RunCommand(ctx, "uname", "-r"); err == nil {
		info["kernel"] = strings.TrimSpace(result.Output)
	}
	if result, err := agent.RunCommand(ctx, "bash", "-c", "cat /etc/os-release | grep PRETTY_NAME | cut -d'\"' -f2"); err == nil {
		info["os"] = strings.TrimSpace(result.Output)
	}
	if result, err := agent.RunCommand(ctx, "nproc"); err == nil {
		info["cpu_count"], _ = strconv.Atoi(strings.TrimSpace(result.Output))
	}
	if result, err := agent.RunCommand(ctx, "bash", "-c", "cat /proc/cpuinfo | grep 'model name' | head -1 | cut -d':' -f2"); err == nil {
		info["cpu_model"] = strings.TrimSpace(result.Output)
	}
	if result, err := agent.RunCommand(ctx, "free", "-b"); err == nil {
		for _, line := range strings.Split(result.Output, "\n") {
			if strings.HasPrefix(line, "Mem:") {
				fields := strings.Fields(line)
				if len(fields) >= 3 {
					info["ram_total"], _ = strconv.ParseInt(fields[1], 10, 64)
					info["ram_used"], _ = strconv.ParseInt(fields[2], 10, 64)
				}
			}
		}
	}
	if result, err := agent.RunCommand(ctx, "df", "-B1", "/"); err == nil {
		lines := strings.Split(result.Output, "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[1])
			if len(fields) >= 4 {
				info["disk_total"], _ = strconv.ParseInt(fields[1], 10, 64)
				info["disk_used"], _ = strconv.ParseInt(fields[2], 10, 64)
				info["disk_available"], _ = strconv.ParseInt(fields[3], 10, 64)
			}
		}
	}
	if result, err := agent.RunCommand(ctx, "uptime", "-p"); err == nil {
		info["uptime"] = strings.TrimSpace(result.Output)
	}
	return info, nil
}

// LiveMetrics returns real-time CPU, memory, disk, and network usage metrics.
func (s *MonitoringService) LiveMetrics(ctx context.Context) (map[string]interface{}, error) {
	metrics := make(map[string]interface{})

	if result, err := agent.RunCommand(ctx, "bash", "-c", "top -bn1 | grep 'Cpu(s)' | awk '{print $2}'"); err == nil {
		metrics["cpu_percent"], _ = strconv.ParseFloat(strings.TrimSpace(result.Output), 64)
	}
	if result, err := agent.RunCommand(ctx, "free", "-b"); err == nil {
		for _, line := range strings.Split(result.Output, "\n") {
			if strings.HasPrefix(line, "Mem:") {
				fields := strings.Fields(line)
				if len(fields) >= 7 {
					total, _ := strconv.ParseInt(fields[1], 10, 64)
					used, _ := strconv.ParseInt(fields[2], 10, 64)
					available, _ := strconv.ParseInt(fields[6], 10, 64)
					metrics["memory"] = map[string]interface{}{
						"total": total, "used": used, "available": available,
						"percent": float64(used) / float64(total) * 100,
					}
				}
			}
			if strings.HasPrefix(line, "Swap:") {
				fields := strings.Fields(line)
				if len(fields) >= 3 {
					total, _ := strconv.ParseInt(fields[1], 10, 64)
					used, _ := strconv.ParseInt(fields[2], 10, 64)
					metrics["swap"] = map[string]interface{}{"total": total, "used": used}
				}
			}
		}
	}
	if result, err := agent.RunCommand(ctx, "df", "-B1", "/"); err == nil {
		lines := strings.Split(result.Output, "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[1])
			if len(fields) >= 5 {
				total, _ := strconv.ParseInt(fields[1], 10, 64)
				used, _ := strconv.ParseInt(fields[2], 10, 64)
				metrics["disk"] = map[string]interface{}{
					"total": total, "used": used, "percent": strings.TrimSuffix(fields[4], "%"),
				}
			}
		}
	}
	if result, err := agent.RunCommand(ctx, "cat", "/proc/loadavg"); err == nil {
		fields := strings.Fields(result.Output)
		if len(fields) >= 3 {
			metrics["load_average"] = map[string]interface{}{"1m": fields[0], "5m": fields[1], "15m": fields[2]}
		}
	}
	if result, err := agent.RunCommand(ctx, "bash", "-c", "cat /proc/net/dev | grep -v lo | tail -n +3 | head -1"); err == nil {
		fields := strings.Fields(result.Output)
		if len(fields) >= 10 {
			rxBytes, _ := strconv.ParseInt(fields[1], 10, 64)
			txBytes, _ := strconv.ParseInt(fields[9], 10, 64)
			metrics["network"] = map[string]interface{}{"rx_bytes": rxBytes, "tx_bytes": txBytes}
		}
	}
	metrics["timestamp"] = time.Now()
	return metrics, nil
}

// ServiceStatus returns the running status of all managed services.
func (s *MonitoringService) ServiceStatus(ctx context.Context) ([]map[string]interface{}, error) {
	managed := []string{"nginx", "mongod", "postfix", "dovecot", "fail2ban", "ufw"}

	if result, err := agent.RunCommand(ctx, "bash", "-c", "systemctl list-units --type=service --all --no-pager --plain | grep php | awk '{print $1}'"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(result.Output), "\n") {
			if line != "" {
				managed = append(managed, strings.TrimSuffix(line, ".service"))
			}
		}
	}

	var statuses []map[string]interface{}
	for _, svc := range managed {
		entry := map[string]interface{}{"name": svc, "active": false, "status": "unknown"}
		if result, err := agent.RunCommand(ctx, "systemctl", "is-active", svc); err == nil {
			st := strings.TrimSpace(result.Output)
			entry["status"] = st
			entry["active"] = st == "active"
		} else {
			entry["status"] = "inactive"
		}
		if result, err := agent.RunCommand(ctx, "systemctl", "is-enabled", svc); err == nil {
			entry["enabled"] = strings.TrimSpace(result.Output) == "enabled"
		}
		statuses = append(statuses, entry)
	}
	if statuses == nil {
		statuses = []map[string]interface{}{}
	}
	return statuses, nil
}

// HistoricalMetrics returns time-series data for a specific metric over a given period.
func (s *MonitoringService) HistoricalMetrics(ctx context.Context, metric, period, interval string) ([]map[string]interface{}, error) {
	col := s.db.Collection(database.ColMetrics)
	since := time.Now()
	switch period {
	case "1h":
		since = since.Add(-1 * time.Hour)
	case "6h":
		since = since.Add(-6 * time.Hour)
	case "24h":
		since = since.Add(-24 * time.Hour)
	case "7d":
		since = since.Add(-7 * 24 * time.Hour)
	case "30d":
		since = since.Add(-30 * 24 * time.Hour)
	default:
		since = since.Add(-24 * time.Hour)
	}
	filter := bson.M{"timestamp": bson.M{"$gte": since}}
	if metric != "" {
		filter["metric"] = metric
	}
	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: 1}}).SetLimit(500)
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var results []map[string]interface{}
	if err := cursor.All(ctx, &results); err != nil {
		return nil, err
	}
	if results == nil {
		results = []map[string]interface{}{}
	}
	return results, nil
}

// StartMetricsCollector starts a background goroutine that periodically collects
// and stores system metrics for historical charting.
func (s *MonitoringService) StartMetricsCollector(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Collect immediately on start
		s.collectAndStoreMetrics(ctx)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.collectAndStoreMetrics(ctx)
			}
		}
	}()
}

func (s *MonitoringService) collectAndStoreMetrics(ctx context.Context) {
	col := s.db.Collection(database.ColMetrics)
	now := time.Now()

	// CPU
	if result, err := agent.RunCommand(ctx, "bash", "-c", "top -bn1 | grep 'Cpu(s)' | awk '{print $2}'"); err == nil {
		cpuPercent, _ := strconv.ParseFloat(strings.TrimSpace(result.Output), 64)
		col.InsertOne(ctx, bson.M{
			"metric": "cpu", "value": cpuPercent, "timestamp": now,
		})
	}

	// Memory
	if result, err := agent.RunCommand(ctx, "free", "-b"); err == nil {
		for _, line := range strings.Split(result.Output, "\n") {
			if strings.HasPrefix(line, "Mem:") {
				fields := strings.Fields(line)
				if len(fields) >= 7 {
					total, _ := strconv.ParseInt(fields[1], 10, 64)
					used, _ := strconv.ParseInt(fields[2], 10, 64)
					percent := float64(used) / float64(total) * 100
					col.InsertOne(ctx, bson.M{
						"metric": "memory", "value": percent,
						"used": used, "total": total, "timestamp": now,
					})
				}
			}
		}
	}

	// Disk
	if result, err := agent.RunCommand(ctx, "df", "-B1", "/"); err == nil {
		lines := strings.Split(result.Output, "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[1])
			if len(fields) >= 5 {
				total, _ := strconv.ParseInt(fields[1], 10, 64)
				used, _ := strconv.ParseInt(fields[2], 10, 64)
				percent := float64(used) / float64(total) * 100
				col.InsertOne(ctx, bson.M{
					"metric": "disk", "value": percent,
					"used": used, "total": total, "timestamp": now,
				})
			}
		}
	}

	// Clean up old metrics (keep 7 days)
	cutoff := now.Add(-7 * 24 * time.Hour)
	col.DeleteMany(ctx, bson.M{"timestamp": bson.M{"$lt": cutoff}})
}

// GetAlertsConfig returns the current alert threshold configuration.
func (s *MonitoringService) GetAlertsConfig(ctx context.Context) (map[string]interface{}, error) {
	col := s.db.Collection(database.ColServerConfig)
	var config bson.M
	err := col.FindOne(ctx, bson.M{"key": "alerts"}).Decode(&config)
	if err == mongo.ErrNoDocuments {
		return map[string]interface{}{
			"cpu_threshold": 90, "memory_threshold": 90, "disk_threshold": 85,
			"email_enabled": false, "slack_enabled": false,
		}, nil
	}
	if err != nil {
		return nil, err
	}
	if value, ok := config["value"]; ok {
		if m, ok := value.(bson.M); ok {
			result := make(map[string]interface{})
			for k, v := range m {
				result[k] = v
			}
			return result, nil
		}
	}
	return map[string]interface{}{}, nil
}

// UpdateAlertsConfig updates the alert threshold configuration.
func (s *MonitoringService) UpdateAlertsConfig(ctx context.Context, config map[string]interface{}) error {
	col := s.db.Collection(database.ColServerConfig)
	_, err := col.UpdateOne(ctx,
		bson.M{"key": "alerts"},
		bson.M{"$set": bson.M{"key": "alerts", "value": config, "updated_at": time.Now()}},
		options.Update().SetUpsert(true),
	)
	return err
}

// ServerDetails mirrors the WHM "Server Information" page. Backs a
// read-only section-by-section view of static system facts plus the
// current `free` / `df` output. Everything here is safe to hand to a
// tenant operator — no secrets, no per-user data.
type ProcessorDetail struct {
	Index  int    `json:"index"`
	Vendor string `json:"vendor"`
	Name   string `json:"name"`
	Speed  string `json:"speed"` // MHz as string from /proc/cpuinfo (it's a float)
	Cache  string `json:"cache"`
}

type DiskInfo struct {
	Device     string `json:"device"`
	Size       string `json:"size"`
	Used       string `json:"used"`
	Avail      string `json:"avail"`
	UsePercent int    `json:"use_percent"`
	MountPoint string `json:"mount_point"`
}

type ServerDetails struct {
	Processors       []ProcessorDetail `json:"processors"`
	MemoryBoot       string            `json:"memory_boot"` // dmesg boot line
	System           string            `json:"system"`      // uname -a
	PhysicalDisks    []string          `json:"physical_disks"`
	CurrentMemoryRaw string            `json:"current_memory_raw"` // raw `free` output
	CurrentDisks     []DiskInfo        `json:"current_disks"`
}

// ServerInformation returns the full WHM-style server info payload. Each
// block is independently best-effort: a missing kernel log or an OS
// where dmesg is privileged-only still yields a usable response, just
// with that block empty.
func (s *MonitoringService) ServerInformation(ctx context.Context) (*ServerDetails, error) {
	out := &ServerDetails{
		Processors:    []ProcessorDetail{},
		PhysicalDisks: []string{},
		CurrentDisks:  []DiskInfo{},
	}

	// /proc/cpuinfo is the canonical source for per-CPU detail. Parse it
	// into one ProcessorDetail per "processor :" entry.
	if r, err := agent.RunCommand(ctx, "cat", "/proc/cpuinfo"); err == nil {
		var cur ProcessorDetail
		flush := func() {
			if cur.Name != "" || cur.Vendor != "" {
				out.Processors = append(out.Processors, cur)
			}
			cur = ProcessorDetail{}
		}
		for _, line := range strings.Split(r.Output, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				flush()
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			switch key {
			case "processor":
				cur.Index, _ = strconv.Atoi(val)
			case "vendor_id":
				cur.Vendor = val
			case "model name":
				cur.Name = val
			case "cpu MHz":
				cur.Speed = val + " MHz"
			case "cache size":
				cur.Cache = val
			}
		}
		flush()
	}

	// Memory boot line — dmesg prints a "Memory: … available" line on
	// boot that mirrors what WHM shows. Not every host lets unprivileged
	// processes read the kernel log, so fall back silently.
	if r, err := agent.RunCommand(ctx, "bash", "-c", "dmesg 2>/dev/null | grep -m1 'Memory:' || true"); err == nil {
		out.MemoryBoot = strings.TrimSpace(r.Output)
	}

	// `uname -a` — one line system description.
	if r, err := agent.RunCommand(ctx, "uname", "-a"); err == nil {
		out.System = strings.TrimSpace(r.Output)
	}

	// Physical disks — the dmesg lines starting with "sd " or "sda:". If
	// dmesg is privileged, fall back to a short lsblk summary instead.
	if r, err := agent.RunCommand(ctx, "bash", "-c",
		"dmesg 2>/dev/null | grep -E 'sd [0-9]:|sd[a-z]:' | head -20 || true",
	); err == nil && strings.TrimSpace(r.Output) != "" {
		for _, ln := range strings.Split(strings.TrimSpace(r.Output), "\n") {
			out.PhysicalDisks = append(out.PhysicalDisks, ln)
		}
	} else if r2, err2 := agent.RunCommand(ctx, "lsblk", "-d", "-o", "NAME,SIZE,MODEL,TYPE"); err2 == nil {
		for _, ln := range strings.Split(strings.TrimSpace(r2.Output), "\n") {
			out.PhysicalDisks = append(out.PhysicalDisks, ln)
		}
	}

	// Raw `free` output — the Server Information page reproduces this
	// verbatim (total / used / free / shared / buff/cache / available).
	if r, err := agent.RunCommand(ctx, "free"); err == nil {
		out.CurrentMemoryRaw = strings.TrimRight(r.Output, "\n")
	}

	// Disk table — `df -h -x tmpfs -x devtmpfs -x overlay` so we only
	// surface real filesystems the operator cares about.
	if r, err := agent.RunCommand(ctx, "df", "-hT", "-x", "tmpfs", "-x", "devtmpfs", "-x", "overlay", "-x", "squashfs"); err == nil {
		lines := strings.Split(strings.TrimSpace(r.Output), "\n")
		for i, ln := range lines {
			if i == 0 {
				continue
			}
			fields := strings.Fields(ln)
			// df -hT columns: FS Type Size Used Avail Use% Mounted-on
			if len(fields) < 7 {
				continue
			}
			usePct, _ := strconv.Atoi(strings.TrimSuffix(fields[5], "%"))
			out.CurrentDisks = append(out.CurrentDisks, DiskInfo{
				Device:     fields[0],
				Size:       fields[2],
				Used:       fields[3],
				Avail:      fields[4],
				UsePercent: usePct,
				MountPoint: fields[6],
			})
		}
	}

	return out, nil
}

// ServiceSummary mirrors the WHM "Service Status" page — per-service
// name + version + running state, plus the top-level Server Load /
// Memory / Swap / per-mount disk rollup that lives at the bottom of
// that page.
type ServiceRow struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Active  bool   `json:"active"`
	Status  string `json:"status"`
}

type ServiceSummary struct {
	Services    []ServiceRow `json:"services"`
	LoadAverage []float64    `json:"load_average"` // 1m, 5m, 15m
	CPUCount    int          `json:"cpu_count"`
	MemoryTotal int64        `json:"memory_total"`
	MemoryUsed  int64        `json:"memory_used"`
	SwapTotal   int64        `json:"swap_total"`
	SwapUsed    int64        `json:"swap_used"`
	Disks       []DiskInfo   `json:"disks"`
}

// serviceVersionProbes maps a systemd unit to the shell command whose
// first-line output looks like a version number. `bash -c "<probe> 2>&1 | head -1"`
// so we never hang on tools that want TTY input; a missing binary just
// leaves the version column blank.
var serviceVersionProbes = map[string]string{
	"nginx":        "nginx -v 2>&1 | head -1",
	"httpd":        "httpd -v 2>/dev/null | head -1 || apache2 -v 2>/dev/null | head -1",
	"mongod":       "mongod --version 2>/dev/null | head -1",
	"mariadb":      "mariadbd --version 2>/dev/null | head -1 || mysqld --version 2>/dev/null | head -1",
	"postfix":      "postconf mail_version 2>/dev/null | awk '{print $3}'",
	"dovecot":      "dovecot --version 2>/dev/null | head -1",
	"fail2ban":     "fail2ban-client --version 2>/dev/null | head -1",
	"exim":         "exim -bV 2>/dev/null | head -1",
	"sshd":         "sshd -V 2>&1 | head -1",
	"crond":        "crond -V 2>&1 | head -1",
	"rsyslogd":     "rsyslogd -v 2>/dev/null | head -1",
	"named":        "named -v 2>/dev/null",
	"powerdns":     "pdns_server --version 2>&1 | head -1",
	"redis-server": "redis-server --version 2>/dev/null | head -1",
}

// ServiceStatusSummary gathers the data for WHM's Service Status page.
// Services are discovered from the same managed list as ServiceStatus()
// plus any php*-fpm units, then decorated with a best-effort version
// string and a rollup of load / memory / swap / disks at the bottom.
func (s *MonitoringService) ServiceStatusSummary(ctx context.Context) (*ServiceSummary, error) {
	// Seed list mirrors ServiceStatus() so the two pages stay in sync.
	managed := []string{"nginx", "httpd", "mongod", "mariadb", "postfix", "dovecot", "fail2ban", "ufw", "exim", "sshd", "crond", "rsyslogd", "named", "powerdns", "redis-server"}
	if result, err := agent.RunCommand(ctx, "bash", "-c",
		"systemctl list-units --type=service --all --no-pager --plain 2>/dev/null | grep -oE 'php[0-9.]*-fpm' | sort -u",
	); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(result.Output), "\n") {
			if line != "" {
				managed = append(managed, line)
			}
		}
	}

	out := &ServiceSummary{Services: []ServiceRow{}, Disks: []DiskInfo{}}

	for _, svc := range managed {
		row := ServiceRow{Name: svc, Status: "unknown"}
		if r, err := agent.RunCommand(ctx, "systemctl", "is-active", svc); err == nil {
			st := strings.TrimSpace(r.Output)
			row.Status = st
			row.Active = st == "active"
		} else {
			row.Status = "inactive"
		}
		if probe, ok := serviceVersionProbes[svc]; ok {
			if r, err := agent.RunCommand(ctx, "bash", "-c", probe+" 2>&1"); err == nil {
				row.Version = strings.TrimSpace(r.Output)
			}
		} else if strings.HasSuffix(svc, "-fpm") {
			// php8.2-fpm → php8.2 -v → version line
			bin := strings.TrimSuffix(svc, "-fpm")
			if r, err := agent.RunCommand(ctx, "bash", "-c", bin+" -v 2>/dev/null | head -1"); err == nil {
				row.Version = strings.TrimSpace(r.Output)
			}
		}
		out.Services = append(out.Services, row)
	}

	// Load average — /proc/loadavg fields: 1m 5m 15m runnable pid
	if r, err := agent.RunCommand(ctx, "cat", "/proc/loadavg"); err == nil {
		fields := strings.Fields(strings.TrimSpace(r.Output))
		if len(fields) >= 3 {
			for _, f := range fields[:3] {
				v, _ := strconv.ParseFloat(f, 64)
				out.LoadAverage = append(out.LoadAverage, v)
			}
		}
	}
	if r, err := agent.RunCommand(ctx, "nproc"); err == nil {
		out.CPUCount, _ = strconv.Atoi(strings.TrimSpace(r.Output))
	}

	// Memory + swap (bytes) via free -b.
	if r, err := agent.RunCommand(ctx, "free", "-b"); err == nil {
		for _, line := range strings.Split(r.Output, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			switch fields[0] {
			case "Mem:":
				out.MemoryTotal, _ = strconv.ParseInt(fields[1], 10, 64)
				out.MemoryUsed, _ = strconv.ParseInt(fields[2], 10, 64)
			case "Swap:":
				out.SwapTotal, _ = strconv.ParseInt(fields[1], 10, 64)
				out.SwapUsed, _ = strconv.ParseInt(fields[2], 10, 64)
			}
		}
	}

	// Disk table (same shape as Server Information page).
	if r, err := agent.RunCommand(ctx, "df", "-hT", "-x", "tmpfs", "-x", "devtmpfs", "-x", "overlay", "-x", "squashfs"); err == nil {
		lines := strings.Split(strings.TrimSpace(r.Output), "\n")
		for i, ln := range lines {
			if i == 0 {
				continue
			}
			fields := strings.Fields(ln)
			if len(fields) < 7 {
				continue
			}
			usePct, _ := strconv.Atoi(strings.TrimSuffix(fields[5], "%"))
			out.Disks = append(out.Disks, DiskInfo{
				Device:     fields[0],
				Size:       fields[2],
				Used:       fields[3],
				Avail:      fields[4],
				UsePercent: usePct,
				MountPoint: fields[6],
			})
		}
	}

	return out, nil
}
