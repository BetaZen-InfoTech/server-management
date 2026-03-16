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
