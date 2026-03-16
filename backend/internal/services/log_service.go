package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"go.mongodb.org/mongo-driver/mongo"
)

type LogService struct {
	db *mongo.Database
}

func NewLogService(db *mongo.Database) *LogService {
	return &LogService{db: db}
}

var logTypePaths = map[string]string{
	"access":  "/var/log/nginx/access.log",
	"error":   "/var/log/nginx/error.log",
	"mail":    "/var/log/mail.log",
	"system":  "/var/log/syslog",
	"auth":    "/var/log/auth.log",
	"mongodb": "/var/log/mongodb/mongod.log",
}

// ViewLogs retrieves log entries of a specific type with line limit and time range.
func (s *LogService) ViewLogs(ctx context.Context, logType string, lines int, since, until string) ([]map[string]interface{}, error) {
	logPath, ok := logTypePaths[logType]
	if !ok {
		return nil, fmt.Errorf("unknown log type: %s", logType)
	}

	if lines <= 0 {
		lines = 100
	}
	if lines > 1000 {
		lines = 1000
	}

	cmd := fmt.Sprintf("tail -n %d %s 2>/dev/null", lines, logPath)
	result, err := agent.RunCommand(ctx, "bash", "-c", cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to read log: %w", err)
	}

	var entries []map[string]interface{}
	for i, line := range strings.Split(strings.TrimSpace(result.Output), "\n") {
		if line == "" {
			continue
		}
		entries = append(entries, map[string]interface{}{
			"line":    i + 1,
			"message": line,
		})
	}
	if entries == nil {
		entries = []map[string]interface{}{}
	}
	return entries, nil
}

// SearchLogs searches within logs of a specific type for a query string.
func (s *LogService) SearchLogs(ctx context.Context, logType string, query string, lines int) ([]map[string]interface{}, error) {
	logPath, ok := logTypePaths[logType]
	if !ok {
		return nil, fmt.Errorf("unknown log type: %s", logType)
	}

	if lines <= 0 {
		lines = 100
	}
	if lines > 1000 {
		lines = 1000
	}

	// Sanitize query to prevent command injection
	safeQuery := strings.ReplaceAll(query, "'", "'\\''")
	cmd := fmt.Sprintf("grep -i '%s' %s 2>/dev/null | tail -n %d", safeQuery, logPath, lines)
	result, err := agent.RunCommand(ctx, "bash", "-c", cmd)
	if err != nil {
		// grep returns exit code 1 when no matches; that's ok
		return []map[string]interface{}{}, nil
	}

	var entries []map[string]interface{}
	for i, line := range strings.Split(strings.TrimSpace(result.Output), "\n") {
		if line == "" {
			continue
		}
		entries = append(entries, map[string]interface{}{
			"line":    i + 1,
			"message": line,
		})
	}
	if entries == nil {
		entries = []map[string]interface{}{}
	}
	return entries, nil
}

// DownloadLog prepares a log file for download in the requested format.
func (s *LogService) DownloadLog(ctx context.Context, logType string, format string) (string, error) {
	logPath, ok := logTypePaths[logType]
	if !ok {
		return "", fmt.Errorf("unknown log type: %s", logType)
	}
	return logPath, nil
}

// ListLogFiles returns metadata about all available log files on the server.
func (s *LogService) ListLogFiles(ctx context.Context) ([]map[string]interface{}, error) {
	var files []map[string]interface{}

	for logType, logPath := range logTypePaths {
		entry := map[string]interface{}{
			"type": logType,
			"path": logPath,
		}

		cmd := fmt.Sprintf("stat --format='%%s %%Y' %s 2>/dev/null", logPath)
		if result, err := agent.RunCommand(ctx, "bash", "-c", cmd); err == nil {
			fields := strings.Fields(strings.TrimSpace(result.Output))
			if len(fields) >= 2 {
				size, _ := strconv.ParseInt(fields[0], 10, 64)
				entry["size_bytes"] = size
				entry["modified"] = fields[1]
			}
		}
		files = append(files, entry)
	}

	if files == nil {
		files = []map[string]interface{}{}
	}
	return files, nil
}
