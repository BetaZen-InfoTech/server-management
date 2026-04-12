package services

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type BackupService struct {
	db *mongo.Database
}

func NewBackupService(db *mongo.Database) *BackupService {
	return &BackupService{db: db}
}

// List returns a paginated list of all backups.
func (s *BackupService) List(ctx context.Context, page, limit int) ([]models.Backup, int64, error) {
	col := s.db.Collection(database.ColBackups)
	total, err := col.CountDocuments(ctx, bson.M{})
	if err != nil {
		return nil, 0, err
	}
	skip := int64((page - 1) * limit)
	opts := options.Find().SetSkip(skip).SetLimit(int64(limit)).SetSort(bson.D{{Key: "created_at", Value: -1}})
	cursor, err := col.Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var backups []models.Backup
	if err := cursor.All(ctx, &backups); err != nil {
		return nil, 0, err
	}
	if backups == nil {
		backups = []models.Backup{}
	}
	return backups, total, nil
}

// GetByID retrieves a single backup record by its ID.
func (s *BackupService) GetByID(ctx context.Context, id string) (*models.Backup, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid backup ID")
	}
	var backup models.Backup
	if err := s.db.Collection(database.ColBackups).FindOne(ctx, bson.M{"_id": oid}).Decode(&backup); err != nil {
		return nil, err
	}
	return &backup, nil
}

// Create initiates a new backup job for a domain.
func (s *BackupService) Create(ctx context.Context, req *models.CreateBackupRequest) (*models.Backup, error) {
	timestamp := time.Now().Format("20060102-150405")
	backupDir := fmt.Sprintf("/home/%s/backups", req.User)

	// Ensure backup directory exists
	agent.RunCommand(ctx, "mkdir", "-p", backupDir)

	backup := models.Backup{
		Type:        req.Type,
		Domain:      req.Domain,
		User:        req.User,
		Storage:     req.Storage,
		Status:      "in_progress",
		Compression: req.Compression,
		CreatedAt:   time.Now(),
	}

	if req.RemoteDestination != nil {
		backup.RemoteDestination = req.RemoteDestination
	}

	outputPath := fmt.Sprintf("%s/%s-%s.tar.gz", backupDir, req.Domain, timestamp)
	backup.Path = outputPath

	var backupErr error
	switch req.Type {
	case "full":
		// Full backup bundles files + DB + email + config into sibling archives.
		// The main outputPath holds the file archive and is what gets transferred
		// to remote storage; side archives live alongside it in backupDir.
		if err := agent.BackupFiles(ctx, req.User, outputPath); err != nil {
			backupErr = err
			break
		}
		dbPath := fmt.Sprintf("%s/%s-db-%s.gz", backupDir, req.Domain, timestamp)
		agent.BackupMongoDB(ctx, req.Domain, dbPath)
		emailPath := fmt.Sprintf("%s/%s-email-%s.tar.gz", backupDir, req.Domain, timestamp)
		agent.BackupEmail(ctx, req.Domain, emailPath)
		configPath := fmt.Sprintf("%s/%s-config-%s.tar.gz", backupDir, req.Domain, timestamp)
		s.backupConfig(ctx, req.User, req.Domain, configPath)
	case "files":
		backupErr = agent.BackupFiles(ctx, req.User, outputPath)
	case "database":
		backupErr = agent.BackupMongoDB(ctx, req.Domain, outputPath)
	case "email":
		backupErr = agent.BackupEmail(ctx, req.Domain, outputPath)
	case "config":
		backupErr = s.backupConfig(ctx, req.User, req.Domain, outputPath)
	default:
		backupErr = agent.BackupFiles(ctx, req.User, outputPath)
	}

	if backupErr != nil {
		backup.Status = "failed"
	} else {
		backup.Status = "completed"
		now := time.Now()
		backup.CompletedAt = &now
		// Get file size
		if result, err := agent.RunCommand(ctx, "stat", "--format=%s", outputPath); err == nil {
			sizeBytes, _ := strconv.ParseFloat(strings.TrimSpace(result.Output), 64)
			backup.SizeMB = sizeBytes / (1024 * 1024)
		}
	}

	// Transfer to remote if requested
	if backupErr == nil && (req.Storage == "remote" || req.Storage == "both") && req.RemoteDestination != nil {
		rd := req.RemoteDestination
		if rd.Port == 0 {
			switch rd.Protocol {
			case "sftp", "scp":
				rd.Port = 22
			case "ftp":
				rd.Port = 21
			}
		}
		transferErr := s.transferToRemote(ctx, outputPath, rd)
		if transferErr != nil {
			backup.Status = "failed"
			backupErr = transferErr
		}
		// If storage is remote only, remove local file after transfer
		if req.Storage == "remote" && transferErr == nil {
			os.Remove(outputPath)
			backup.Path = ""
		}
	}

	result, err := s.db.Collection(database.ColBackups).InsertOne(ctx, backup)
	if err != nil {
		return nil, err
	}
	backup.ID = result.InsertedID.(primitive.ObjectID)
	return &backup, backupErr
}

// transferToRemote sends a backup file to a remote destination.
func (s *BackupService) transferToRemote(ctx context.Context, localPath string, rd *models.RemoteDestination) error {
	switch rd.Protocol {
	case "sftp":
		return agent.TransferViaSFTP(ctx, localPath, rd.Host, rd.Port, rd.Username, rd.Password, rd.Path)
	case "ftp":
		return agent.TransferViaFTP(ctx, localPath, rd.Host, rd.Port, rd.Username, rd.Password, rd.Path)
	case "scp":
		return agent.TransferViaSCP(ctx, localPath, rd.Host, rd.Port, rd.Username, rd.Password, rd.Path)
	default:
		return fmt.Errorf("unsupported protocol: %s", rd.Protocol)
	}
}

// downloadFromRemote downloads a backup file from a remote source.
func (s *BackupService) downloadFromRemote(ctx context.Context, localPath string, rd *models.RemoteDestination) error {
	switch rd.Protocol {
	case "sftp":
		return agent.DownloadViaSFTP(ctx, rd.Host, rd.Port, rd.Username, rd.Password, rd.Path, localPath)
	case "ftp":
		return agent.DownloadViaFTP(ctx, rd.Host, rd.Port, rd.Username, rd.Password, rd.Path, localPath)
	case "scp":
		return agent.DownloadViaSCP(ctx, rd.Host, rd.Port, rd.Username, rd.Password, rd.Path, localPath)
	default:
		return fmt.Errorf("unsupported protocol: %s", rd.Protocol)
	}
}

// Restore restores data from a backup (from server, uploaded file, or remote).
func (s *BackupService) Restore(ctx context.Context, req *models.RestoreRequest) error {
	switch req.Source {
	case "server":
		return s.restoreFromServer(ctx, req)
	case "upload":
		// File is already saved locally by the handler; req.BackupID holds the temp file path
		return s.restoreFromFile(ctx, req.BackupID, req.RestoreType, req.User, req.Domain)
	case "remote":
		return s.restoreFromRemote(ctx, req)
	default:
		// Fallback: treat as server restore for backward compatibility
		return s.restoreFromServer(ctx, req)
	}
}

func (s *BackupService) restoreFromServer(ctx context.Context, req *models.RestoreRequest) error {
	backup, err := s.GetByID(ctx, req.BackupID)
	if err != nil {
		return fmt.Errorf("backup not found: %w", err)
	}
	return s.restoreFromFile(ctx, backup.Path, req.RestoreType, backup.User, backup.Domain)
}

func (s *BackupService) restoreFromRemote(ctx context.Context, req *models.RestoreRequest) error {
	if req.RemoteDestination == nil {
		return fmt.Errorf("remote destination is required for remote restore")
	}
	rd := req.RemoteDestination
	if rd.Port == 0 {
		switch rd.Protocol {
		case "sftp", "scp":
			rd.Port = 22
		case "ftp":
			rd.Port = 21
		}
	}

	// Download to temp location
	tmpPath := fmt.Sprintf("/tmp/serverpanel-restore-%d.tar.gz", time.Now().Unix())
	if err := s.downloadFromRemote(ctx, tmpPath, rd); err != nil {
		return fmt.Errorf("failed to download from remote: %w", err)
	}
	defer os.Remove(tmpPath)

	return s.restoreFromFile(ctx, tmpPath, req.RestoreType, req.User, req.Domain)
}

func (s *BackupService) restoreFromFile(ctx context.Context, filePath, restoreType, user, domain string) error {
	switch restoreType {
	case "full", "files":
		if err := agent.RestoreFiles(ctx, user, filePath); err != nil {
			return fmt.Errorf("failed to restore files: %w", err)
		}
	case "database":
		if err := agent.RestoreMongoDB(ctx, domain, filePath); err != nil {
			return fmt.Errorf("failed to restore database: %w", err)
		}
	case "email":
		if err := agent.RestoreEmail(ctx, domain, filePath); err != nil {
			return fmt.Errorf("failed to restore email: %w", err)
		}
	case "config":
		if err := s.restoreConfig(ctx, user, domain, filePath); err != nil {
			return fmt.Errorf("failed to restore config: %w", err)
		}
	default:
		if err := agent.RestoreFiles(ctx, user, filePath); err != nil {
			return fmt.Errorf("failed to restore: %w", err)
		}
	}
	return nil
}

// TestConnection tests connectivity to a remote server.
func (s *BackupService) TestConnection(ctx context.Context, req *models.TestConnectionRequest) error {
	return agent.TestRemoteConnection(ctx, req.Protocol, req.Host, req.Port, req.Username, req.Password)
}

// Delete removes a backup archive and its record.
func (s *BackupService) Delete(ctx context.Context, id string) error {
	backup, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Remove file
	if backup.Path != "" {
		os.Remove(backup.Path)
	}

	oid, _ := primitive.ObjectIDFromHex(id)
	_, err = s.db.Collection(database.ColBackups).DeleteOne(ctx, bson.M{"_id": oid})
	return err
}

// GetDownloadPath returns the local file path for downloading a backup.
func (s *BackupService) GetDownloadPath(ctx context.Context, id string) (string, error) {
	backup, err := s.GetByID(ctx, id)
	if err != nil {
		return "", err
	}
	if backup.Path == "" {
		return "", fmt.Errorf("backup file not found")
	}
	return backup.Path, nil
}

// ListSchedules returns all configured backup schedules.
func (s *BackupService) ListSchedules(ctx context.Context) ([]models.BackupSchedule, error) {
	col := s.db.Collection(database.ColBackupSchedules)
	cursor, err := col.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var schedules []models.BackupSchedule
	if err := cursor.All(ctx, &schedules); err != nil {
		return nil, err
	}
	if schedules == nil {
		schedules = []models.BackupSchedule{}
	}
	return schedules, nil
}

// CreateSchedule sets up a new automated backup schedule.
func (s *BackupService) CreateSchedule(ctx context.Context, schedule *models.BackupSchedule) (*models.BackupSchedule, error) {
	schedule.CreatedAt = time.Now()
	schedule.UpdatedAt = time.Now()
	if !schedule.Enabled {
		schedule.Enabled = true
	}

	result, err := s.db.Collection(database.ColBackupSchedules).InsertOne(ctx, schedule)
	if err != nil {
		return nil, err
	}
	schedule.ID = result.InsertedID.(primitive.ObjectID)

	// Add cron entry for automated backup
	backupCmd := fmt.Sprintf("/opt/serverpanel/backend/scripts/backup.sh %s %s %s", schedule.Domain, schedule.User, schedule.Type)
	agent.WriteCrontab(ctx, "root", schedule.Schedule, backupCmd)

	return schedule, nil
}

// DeleteSchedule removes an automated backup schedule.
func (s *BackupService) DeleteSchedule(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid schedule ID")
	}
	_, err = s.db.Collection(database.ColBackupSchedules).DeleteOne(ctx, bson.M{"_id": oid})
	return err
}

// backupConfig builds a tar.gz snapshot of server-side configuration that is
// otherwise lost on a file-only restore: DNS zone export, SSL certs, nginx
// vhost, PHP-FPM pool, the owning user's crontab, and JSON metadata for the
// MongoDB user+domain+DNS+cron records. Mirrors what restoreConfig reads back.
func (s *BackupService) backupConfig(ctx context.Context, user, domain, outputPath string) error {
	stageDir := fmt.Sprintf("/tmp/serverpanel-cfgbackup-%d", time.Now().UnixNano())
	defer agent.RunCommand(ctx, "rm", "-rf", stageDir)

	if _, err := agent.RunCommand(ctx, "mkdir", "-p",
		stageDir+"/dns",
		stageDir+"/ssl",
		stageDir+"/nginx",
		stageDir+"/php-fpm",
		stageDir+"/cron",
		stageDir+"/metadata",
	); err != nil {
		return fmt.Errorf("failed to stage config backup: %w", err)
	}

	// DNS zone (BIND-format export from PowerDNS).
	agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("pdnsutil list-zone %s > %s/dns/%s.zone 2>/dev/null || true", domain, stageDir, domain))

	// SSL certs — dereference symlinks so the archive holds real cert files.
	agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("if [ -d /etc/letsencrypt/live/%s ]; then cp -rL /etc/letsencrypt/live/%s %s/ssl/; fi", domain, domain, stageDir))

	// Nginx vhost.
	agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("cp /etc/nginx/sites-available/%s %s/nginx/ 2>/dev/null || true", domain, stageDir))

	// PHP-FPM pool across every installed PHP version.
	agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("find /etc/php -name '%s.conf' -exec cp {} %s/php-fpm/ \\; 2>/dev/null || true", domain, stageDir))

	// Owning user's crontab.
	agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("crontab -u %s -l > %s/cron/%s.crontab 2>/dev/null || true", user, stageDir, user))

	// Metadata: user + domain + DNS records + cron jobs as JSON.
	s.writeMetadataJSON(ctx, user, domain, stageDir+"/metadata")

	if _, err := agent.RunCommand(ctx, "tar", "-czf", outputPath, "-C", stageDir, "."); err != nil {
		return fmt.Errorf("failed to archive config: %w", err)
	}
	return nil
}

// writeMetadataJSON exports MongoDB records tied to this domain/user as JSON
// files inside metaDir so the restore side can inspect or reimport them
// without a live DB connection.
func (s *BackupService) writeMetadataJSON(ctx context.Context, user, domain, metaDir string) {
	writeDoc := func(name string, doc interface{}) {
		if doc == nil {
			return
		}
		data, err := bson.MarshalExtJSON(doc, false, false)
		if err != nil {
			return
		}
		path := fmt.Sprintf("%s/%s.json", metaDir, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to write %s metadata: %v\n", name, err)
		}
	}

	var userDoc bson.M
	s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"username": user}).Decode(&userDoc)
	writeDoc("user", userDoc)

	var domDoc bson.M
	s.db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": domain}).Decode(&domDoc)
	writeDoc("domain", domDoc)

	var zoneDoc bson.M
	s.db.Collection(database.ColDNSZones).FindOne(ctx, bson.M{"domain": domain}).Decode(&zoneDoc)
	writeDoc("dns_zone", zoneDoc)

	if zoneDoc != nil {
		if zoneID, ok := zoneDoc["_id"]; ok {
			cur, err := s.db.Collection(database.ColDNSRecords).Find(ctx, bson.M{"zone_id": zoneID})
			if err == nil {
				var recs []bson.M
				if decodeErr := cur.All(ctx, &recs); decodeErr == nil {
					writeDoc("dns_records", recs)
				}
				cur.Close(ctx)
			}
		}
	}

	cur, err := s.db.Collection(database.ColCronJobs).Find(ctx, bson.M{"user": user})
	if err == nil {
		var jobs []bson.M
		if decodeErr := cur.All(ctx, &jobs); decodeErr == nil {
			writeDoc("cron_jobs", jobs)
		}
		cur.Close(ctx)
	}
}

// restoreConfig extracts a config-backup tarball and reinstalls the pieces:
// SSL certs, nginx vhost, PHP-FPM pool, crontab, and DNS zone. MongoDB
// metadata JSON is left extracted in the staging dir (and cleaned afterwards)
// so operators can inspect it, but is not auto-reinserted.
func (s *BackupService) restoreConfig(ctx context.Context, user, domain, archivePath string) error {
	stageDir := fmt.Sprintf("/tmp/serverpanel-cfgrestore-%d", time.Now().UnixNano())
	defer agent.RunCommand(ctx, "rm", "-rf", stageDir)

	if _, err := agent.RunCommand(ctx, "mkdir", "-p", stageDir); err != nil {
		return fmt.Errorf("failed to stage config restore: %w", err)
	}
	if _, err := agent.RunCommand(ctx, "tar", "-xzf", archivePath, "-C", stageDir); err != nil {
		return fmt.Errorf("failed to extract config archive: %w", err)
	}

	// SSL certs.
	agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("if [ -d %s/ssl/%s ]; then mkdir -p /etc/letsencrypt/live/%s && cp -rL %s/ssl/%s/. /etc/letsencrypt/live/%s/; fi",
			stageDir, domain, domain, stageDir, domain, domain))

	// Nginx vhost.
	agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("if [ -f %s/nginx/%s ]; then cp %s/nginx/%s /etc/nginx/sites-available/%s && ln -sf /etc/nginx/sites-available/%s /etc/nginx/sites-enabled/%s; fi",
			stageDir, domain, stageDir, domain, domain, domain, domain))

	// PHP-FPM pool — drop into every installed PHP version and reload.
	agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf(`for f in %s/php-fpm/%s.conf; do
			[ -f "$f" ] || continue
			for ver in $(ls /etc/php 2>/dev/null); do
				cp "$f" /etc/php/$ver/fpm/pool.d/%s.conf 2>/dev/null || true
				systemctl reload php$ver-fpm 2>/dev/null || true
			done
		done`, stageDir, domain, domain))

	// Fail loudly on a broken nginx config — a silent half-restore is worse
	// than a visible error.
	if _, err := agent.RunCommand(ctx, "nginx", "-t"); err != nil {
		return fmt.Errorf("nginx config test failed after restore: %w", err)
	}
	agent.ReloadNginx(ctx)

	// DNS zone.
	agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf(`if [ -f %s/dns/%s.zone ]; then
			pdnsutil delete-zone %s 2>/dev/null || true
			pdnsutil load-zone %s %s/dns/%s.zone 2>/dev/null || true
			pdns_control reload 2>/dev/null || true
		fi`, stageDir, domain, domain, domain, stageDir, domain))

	// User crontab.
	agent.RunCommand(ctx, "bash", "-c",
		fmt.Sprintf("if [ -f %s/cron/%s.crontab ]; then crontab -u %s %s/cron/%s.crontab; fi",
			stageDir, user, user, stageDir, user))

	return nil
}
