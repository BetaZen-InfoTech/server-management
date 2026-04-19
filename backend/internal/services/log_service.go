package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
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

// tenantScopedLogTypes lists the log slugs a tenant-scoped caller is
// allowed to view. Everything else (system / auth / mail / mongodb / app)
// is reserved for the platform operator — those logs contain every
// tenant's data mingled together, and letting a vendor_admin read them
// would leak activity from unrelated tenants.
var tenantScopedLogTypes = map[string]bool{
	"nginx-access": true,
	"nginx-error":  true,
	"access":       true, // back-compat alias
	"error":        true, // back-compat alias
	"all":          true, // renders a tenant-filtered merge
}

// viewTenantScoped returns the log tail for a tenant-scoped caller:
// nginx-access and nginx-error concat only the per-domain files that
// belong to the caller's tenant; everything else returns an empty
// payload with an informational banner so the UI can explain why the
// tab is blank rather than silently returning a flat list.
func (s *LogService) viewTenantScoped(ctx context.Context, logType string, lines int, scope *CallerScope) (map[string]interface{}, error) {
	domains, _ := scope.TenantDomains(ctx, s.db)
	if !tenantScopedLogTypes[logType] {
		return map[string]interface{}{
			"type":  logType,
			"label": "restricted",
			"lines": []string{},
			"count": 0,
			"content": "# This log type isn't available for your account.\n" +
				"# System, application, authentication, mail, and database logs are\n" +
				"# server-wide and can only be viewed by the platform operator.\n" +
				"# You can still view per-domain nginx access + error logs via the\n" +
				"# Nginx Access / Nginx Error tabs.",
			"restricted": true,
		}, nil
	}
	// Map the two wildcards to the concrete slug we know how to read.
	base := logType
	if base == "access" {
		base = "nginx-access"
	} else if base == "error" {
		base = "nginx-error"
	}

	// For "all" we concat both nginx-access and nginx-error across every
	// tenant domain; other types read a single per-file suffix.
	var suffixes []string
	switch base {
	case "nginx-access":
		suffixes = []string{"-access.log"}
	case "nginx-error":
		suffixes = []string{"-error.log"}
	case "all":
		suffixes = []string{"-access.log", "-error.log"}
	}

	lines = clampLines(lines)
	if len(domains) == 0 {
		return map[string]interface{}{
			"type":  logType,
			"label": "tenant",
			"lines": []string{},
			"count": 0,
			"content": "# No domains are registered under your account yet.\n" +
				"# Once you create a domain, its nginx access + error logs will appear here.",
		}, nil
	}

	// Split the line budget evenly across the (domains × suffixes) sources.
	slots := len(domains) * len(suffixes)
	if slots < 1 {
		slots = 1
	}
	per := lines / slots
	if per < 10 {
		per = 10
	}

	var blocks []string
	var all []string
	for _, domain := range domains {
		for _, suffix := range suffixes {
			path := fmt.Sprintf("/var/log/nginx/%s%s", domain, suffix)
			cmd := fmt.Sprintf("tail -n %d %s 2>/dev/null", per, path)
			res, err := agent.RunCommand(ctx, "bash", "-c", cmd)
			if err != nil || res == nil {
				continue
			}
			out := strings.TrimRight(res.Output, "\n")
			if out == "" {
				continue
			}
			header := fmt.Sprintf("=== %s%s (last %d) ===", domain, suffix, per)
			blocks = append(blocks, header+"\n"+out)
			for _, ln := range strings.Split(out, "\n") {
				if ln == "" {
					continue
				}
				all = append(all, fmt.Sprintf("[%s%s] %s", domain, suffix, ln))
			}
		}
	}

	return map[string]interface{}{
		"type":    logType,
		"label":   "tenant",
		"lines":   all,
		"count":   len(all),
		"content": strings.Join(blocks, "\n\n"),
	}, nil
}

// ViewLogs retrieves log entries for the given slug, returning a payload the
// frontend can render directly: {content, lines, path, type}.
// The special slug "all" returns a merged feed across every known log
// source, with each line prefixed by its source label.
//
// Tenant scoping: vendor_admin / vendor_staff / developer / support only
// ever see per-domain nginx logs for their own tenant's domains. Every
// other log slug returns an empty payload with an informational banner.
// Platform operators (vendor_owner) bypass this filter entirely.
func (s *LogService) ViewLogs(ctx context.Context, logType string, lines int, since, until string) (map[string]interface{}, error) {
	if scope := GetCallerScope(ctx); scope != nil && constants.IsTenantScoped(scope.Role) {
		return s.viewTenantScoped(ctx, logType, lines, scope)
	}

	if logType == "all" {
		return s.viewAll(ctx, lines)
	}

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

// viewAll reads the tail of every known log source and returns them as a
// single grouped feed with a `=== source ===` banner before each block.
// We don't try to merge-sort by timestamp — log formats differ enough
// (nginx "2026/04/19 00:53:52", syslog "Apr 19 00:53:52", journalctl ISO)
// that any naive sort would get it wrong most of the time. Grouped with
// clear headers is what operators actually want for a "show me everything"
// view, and the browser's find-in-page still works across the whole blob.
func (s *LogService) viewAll(ctx context.Context, lines int) (map[string]interface{}, error) {
	lines = clampLines(lines)
	// Split the budget across real sources (skip back-compat aliases so the
	// same lines don't appear twice).
	canonical := []string{"app", "nginx-access", "nginx-error", "system", "auth", "mail", "mongodb"}
	per := lines / len(canonical)
	if per < 10 {
		per = 10
	}

	var blocks []string
	var all []string
	for _, key := range canonical {
		src, ok := logSources[key]
		if !ok {
			continue
		}
		content, err := s.readSource(ctx, src, per)
		if err != nil || strings.TrimSpace(content) == "" {
			continue
		}
		header := fmt.Sprintf("=== %s (last %d) ===", src.label, per)
		blocks = append(blocks, header+"\n"+content)
		// Tag each line with its source so the "lines" array is still useful
		// for filtering on the frontend side.
		for _, line := range strings.Split(content, "\n") {
			if line == "" {
				continue
			}
			all = append(all, fmt.Sprintf("[%s] %s", src.label, line))
		}
	}

	return map[string]interface{}{
		"type":    "all",
		"label":   "all",
		"path":    "",
		"unit":    "",
		"lines":   all,
		"count":   len(all),
		"content": strings.Join(blocks, "\n\n"),
	}, nil
}

// SearchLogs searches within the given log source for a query string.
// Tenant-scoped callers can only search across their own domains' nginx
// files (grep -i across the per-domain -access.log / -error.log). Other
// slugs return empty for them, matching ViewLogs' behavior.
func (s *LogService) SearchLogs(ctx context.Context, logType string, query string, lines int) (map[string]interface{}, error) {
	if scope := GetCallerScope(ctx); scope != nil && constants.IsTenantScoped(scope.Role) {
		if !tenantScopedLogTypes[logType] {
			return map[string]interface{}{"content": "", "lines": []string{}, "count": 0, "restricted": true}, nil
		}
		return s.searchTenantScoped(ctx, logType, query, lines, scope)
	}
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

// searchTenantScoped runs grep -i across the caller's per-domain nginx
// log files. Returns the merged, truncated output matching ViewLogs'
// tenant-scoped shape so the frontend treats both responses identically.
func (s *LogService) searchTenantScoped(ctx context.Context, logType, query string, lines int, scope *CallerScope) (map[string]interface{}, error) {
	domains, _ := scope.TenantDomains(ctx, s.db)
	if len(domains) == 0 {
		return map[string]interface{}{"content": "", "lines": []string{}, "count": 0}, nil
	}
	lines = clampLines(lines)
	safeQuery := strings.ReplaceAll(query, "'", "'\\''")

	var suffix string
	switch logType {
	case "nginx-error", "error":
		suffix = "-error.log"
	default:
		suffix = "-access.log"
	}

	var paths []string
	for _, d := range domains {
		paths = append(paths, fmt.Sprintf("/var/log/nginx/%s%s", d, suffix))
	}
	// Build a multi-file grep. Using /dev/null as the extra arg forces grep
	// to include the filename prefix even when only one path exists.
	cmd := fmt.Sprintf("grep -i -H -- '%s' %s /dev/null 2>/dev/null | tail -n %d",
		safeQuery, strings.Join(paths, " "), lines)
	res, err := agent.RunCommand(ctx, "bash", "-c", cmd)
	if err != nil || res == nil {
		return map[string]interface{}{"content": "", "lines": []string{}, "count": 0}, nil
	}
	content := strings.TrimRight(res.Output, "\n")
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
// Tenant-scoped callers can't download any server-wide log; they have to
// use the panel's per-domain view instead. Letting them grab
// /var/log/nginx/access.log would leak every other tenant's requests.
func (s *LogService) DownloadLog(ctx context.Context, logType string, format string) (string, error) {
	if scope := GetCallerScope(ctx); scope != nil && constants.IsTenantScoped(scope.Role) {
		return "", fmt.Errorf("download not available for tenant-scoped accounts")
	}
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
// Tenant-scoped callers only see the slugs they're allowed to view, so the
// frontend can hide the system / auth / mail / mongodb tabs entirely
// instead of rendering them and handing back an empty "restricted" payload
// when clicked.
func (s *LogService) ListLogFiles(ctx context.Context) ([]map[string]interface{}, error) {
	var files []map[string]interface{}
	tenantScoped := false
	if scope := GetCallerScope(ctx); scope != nil && constants.IsTenantScoped(scope.Role) {
		tenantScoped = true
	}

	for logType, src := range logSources {
		if tenantScoped && !tenantScopedLogTypes[logType] {
			continue
		}
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
