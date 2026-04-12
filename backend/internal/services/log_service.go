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

type logSource struct {
	// file is the primary on-disk log path; empty means journal-only.
	file string
	// journalUnit is a systemd unit to fall back to (or primary source) via journalctl.
	journalUnit string
	// label is used for download filenames and error messages.
	label string
}

// logSources maps the frontend log-type slug to the actual source on the server.
// Frontend (LogsPage.tsx) sends these slugs verbatim — keep them in sync.
var logSources = map[string]logSource{
	"nginx-access": {file: "/var/log/nginx/access.log", label: "nginx-access"},
	"nginx-error":  {file: "/var/log/nginx/error.log", label: "nginx-error"},
	"app":          {journalUnit: "serverpanel", label: "serverpanel"},
	"system":       {file: "/var/log/syslog", journalUnit: "", label: "system"},
	"auth":         {file: "/var/log/auth.log", label: "auth"},
	"mail":         {file: "/var/log/mail.log", label: "mail"},
	"mongodb":      {file: "/var/log/mongodb/mongod.log", label: "mongodb"},

	// Back-compat aliases (old frontends / scripts may still use these).
	"access": {file: "/var/log/nginx/access.log", label: "nginx-access"},
	"error":  {file: "/var/log/nginx/error.log", label: "nginx-error"},
}

func clampLines(lines int) int {
	if lines <= 0 {
		return 100
	}
	if lines > 5000 {
		return 5000
	}
	return lines
}

// readSource fetches the last N lines from a log source, preferring the file
// but falling back to journalctl when the file is missing, empty, or unreadable.
func (s *LogService) readSource(ctx context.Context, src logSource, lines int) (string, error) {
	lines = clampLines(lines)

	if src.file != "" {
		// sudo is unnecessary since serverpanel runs as root in production,
		// but tail still exits non-zero if the file is missing — swallow that
		// and fall through to the journal fallback below.
		cmd := fmt.Sprintf("tail -n %d %s 2>/dev/null", lines, src.file)
		if result, err := agent.RunCommand(ctx, "bash", "-c", cmd); err == nil {
			out := strings.TrimRight(result.Output, "\n")
			if out != "" {
				return out, nil
			}
		}
	}

	if src.journalUnit != "" {
		cmd := fmt.Sprintf("journalctl -u %s -n %d --no-pager --output=short-iso 2>/dev/null", src.journalUnit, lines)
		if result, err := agent.RunCommand(ctx, "bash", "-c", cmd); err == nil {
			return strings.TrimRight(result.Output, "\n"), nil
		}
	}

	// Last-ditch: for system logs, try journalctl without a unit filter.
	if src.file == "/var/log/syslog" {
		cmd := fmt.Sprintf("journalctl -n %d --no-pager --output=short-iso 2>/dev/null", lines)
		if result, err := agent.RunCommand(ctx, "bash", "-c", cmd); err == nil {
			return strings.TrimRight(result.Output, "\n"), nil
		}
	}

	return "", nil
}

// ViewLogs retrieves log entries for the given slug, returning a payload the
// frontend can render directly: {content, lines, path, type}.
func (s *LogService) ViewLogs(ctx context.Context, logType string, lines int, since, until string) (map[string]interface{}, error) {
	src, ok := logSources[logType]
	if !ok {
		return nil, fmt.Errorf("unknown log type: %s", logType)
	}

	content, err := s.readSource(ctx, src, lines)
	if err != nil {
		return nil, fmt.Errorf("failed to read log: %w", err)
	}

	var lineList []string
	if content != "" {
		lineList = strings.Split(content, "\n")
	} else {
		lineList = []string{}
	}

	return map[string]interface{}{
		"type":    logType,
		"label":   src.label,
		"path":    src.file,
		"unit":    src.journalUnit,
		"lines":   lineList,
		"count":   len(lineList),
		"content": content,
	}, nil
}

// SearchLogs searches within the given log source for a query string.
func (s *LogService) SearchLogs(ctx context.Context, logType string, query string, lines int) (map[string]interface{}, error) {
	src, ok := logSources[logType]
	if !ok {
		return nil, fmt.Errorf("unknown log type: %s", logType)
	}
	lines = clampLines(lines)

	// Sanitize query to prevent command injection via single-quote escape.
	safeQuery := strings.ReplaceAll(query, "'", "'\\''")

	var cmd string
	if src.file != "" {
		cmd = fmt.Sprintf("grep -i -- '%s' %s 2>/dev/null | tail -n %d", safeQuery, src.file, lines)
	} else if src.journalUnit != "" {
		cmd = fmt.Sprintf("journalctl -u %s --no-pager --output=short-iso 2>/dev/null | grep -i -- '%s' | tail -n %d", src.journalUnit, safeQuery, lines)
	} else {
		return map[string]interface{}{"content": "", "lines": []string{}, "count": 0}, nil
	}

	result, err := agent.RunCommand(ctx, "bash", "-c", cmd)
	if err != nil {
		// grep exits 1 on no matches; treat as empty, not failure.
		return map[string]interface{}{"content": "", "lines": []string{}, "count": 0}, nil
	}

	content := strings.TrimRight(result.Output, "\n")
	var lineList []string
	if content != "" {
		lineList = strings.Split(content, "\n")
	} else {
		lineList = []string{}
	}
	return map[string]interface{}{
		"content": content,
		"lines":   lineList,
		"count":   len(lineList),
	}, nil
}

// DownloadLog returns the on-disk path for a log type (journal-only sources
// cannot be downloaded directly — callers should fall back to ViewLogs).
func (s *LogService) DownloadLog(ctx context.Context, logType string, format string) (string, error) {
	src, ok := logSources[logType]
	if !ok {
		return "", fmt.Errorf("unknown log type: %s", logType)
	}
	if src.file == "" {
		return "", fmt.Errorf("log type %s has no downloadable file", logType)
	}
	return src.file, nil
}

// ListLogFiles returns metadata about every known log source on the server.
func (s *LogService) ListLogFiles(ctx context.Context) ([]map[string]interface{}, error) {
	var files []map[string]interface{}

	for logType, src := range logSources {
		entry := map[string]interface{}{
			"type":  logType,
			"label": src.label,
			"path":  src.file,
			"unit":  src.journalUnit,
		}

		if src.file != "" {
			cmd := fmt.Sprintf("stat --format='%%s %%Y' %s 2>/dev/null", src.file)
			if result, err := agent.RunCommand(ctx, "bash", "-c", cmd); err == nil {
				fields := strings.Fields(strings.TrimSpace(result.Output))
				if len(fields) >= 2 {
					size, _ := strconv.ParseInt(fields[0], 10, 64)
					entry["size_bytes"] = size
					entry["modified"] = fields[1]
					entry["exists"] = true
				} else {
					entry["exists"] = false
				}
			}
		}
		files = append(files, entry)
	}

	if files == nil {
		files = []map[string]interface{}{}
	}
	return files, nil
}
