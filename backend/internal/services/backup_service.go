package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	filter := bson.M{}
	if scope := GetCallerScope(ctx); scope != nil {
		filter = scope.ApplyTo(ctx, s.db, "user", filter)
	}
	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	skip := int64((page - 1) * limit)
	opts := options.Find().SetSkip(skip).SetLimit(int64(limit)).SetSort(bson.D{{Key: "created_at", Value: -1}})
	cursor, err := col.Find(ctx, filter, opts)
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
	// Tenant isolation. List is already scoped, but every by-id op — Get,
	// GetDownloadPath (→ download), Delete, and restoreFromServer — funnels
	// through here, and previously resolved the backup by raw _id with NO scope
	// check. That made the backup id a global handle: any tenant-scoped caller
	// (down to a `customer`) could read/download/delete/restore another tenant's
	// backup by guessing the ObjectID. AssertOwns is a no-op for the platform
	// owner and internal (nil-scope) callers, so only cross-tenant HTTP access
	// is blocked. 404-style message avoids leaking that the id exists.
	if scope := GetCallerScope(ctx); scope != nil {
		if err := scope.AssertOwns(ctx, s.db, backup.User); err != nil {
			return nil, fmt.Errorf("backup not found")
		}
	}
	return &backup, nil
}

// Create initiates a new backup job for a domain.
func (s *BackupService) Create(ctx context.Context, req *models.CreateBackupRequest) (*models.Backup, error) {
	// Tenant isolation: req.User/req.Domain come straight from the request body.
	// Without this a tenant-scoped caller could back up (tar /home + dump the
	// databases of) any other tenant's account by naming their linux user, then
	// download the archive. Owner / internal callers are unaffected (no-op).
	if scope := GetCallerScope(ctx); scope != nil {
		if err := scope.AssertOwns(ctx, s.db, req.User); err != nil {
			return nil, fmt.Errorf("user %q is not in your tenant", req.User)
		}
		if req.Domain != "" {
			if err := scope.AssertOwnsDomain(ctx, s.db, strings.ToLower(strings.TrimSpace(req.Domain))); err != nil {
				return nil, fmt.Errorf("domain %q is not in your tenant", req.Domain)
			}
		}
	}
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

	// produced maps component → local archive path. Remote transfer,
	// encryption and retention all operate over every entry, so a "full"
	// backup ships ALL its archives off-site instead of just the files one.
	produced := map[string]string{}

	var backupErr error
	switch req.Type {
	case "full":
		// Full backup = files + DB + email + config as sibling archives. Each
		// side-archive error is collected (pre-fix they were silently
		// discarded while the backup was still marked "completed").
		var errs []string
		if err := agent.BackupFiles(ctx, req.User, outputPath); err != nil {
			errs = append(errs, "files: "+err.Error())
		} else {
			produced["files"] = outputPath
		}
		dbPath := fmt.Sprintf("%s/%s-db-%s.tar.gz", backupDir, req.Domain, timestamp)
		if err := s.backupDatabases(ctx, req.User, req.Domain, dbPath); err != nil {
			errs = append(errs, "database: "+err.Error())
		} else {
			produced["database"] = dbPath
			backup.DatabasePath = dbPath
		}
		emailPath := fmt.Sprintf("%s/%s-email-%s.tar.gz", backupDir, req.Domain, timestamp)
		if err := agent.BackupEmail(ctx, req.User, req.Domain, emailPath); err != nil {
			errs = append(errs, "email: "+err.Error())
		} else {
			produced["email"] = emailPath
			backup.EmailPath = emailPath
		}
		configPath := fmt.Sprintf("%s/%s-config-%s.tar.gz", backupDir, req.Domain, timestamp)
		if err := s.backupConfig(ctx, req.User, req.Domain, configPath); err != nil {
			errs = append(errs, "config: "+err.Error())
		} else {
			produced["config"] = configPath
			backup.ConfigPath = configPath
		}
		if len(errs) > 0 {
			backupErr = fmt.Errorf("full backup incomplete — %s", strings.Join(errs, "; "))
		}
	case "files":
		if backupErr = agent.BackupFiles(ctx, req.User, outputPath); backupErr == nil {
			produced["files"] = outputPath
		}
	case "database":
		if backupErr = s.backupDatabases(ctx, req.User, req.Domain, outputPath); backupErr == nil {
			produced["database"] = outputPath
		}
	case "email":
		if backupErr = agent.BackupEmail(ctx, req.User, req.Domain, outputPath); backupErr == nil {
			produced["email"] = outputPath
		}
	case "config":
		if backupErr = s.backupConfig(ctx, req.User, req.Domain, outputPath); backupErr == nil {
			produced["config"] = outputPath
		}
	default:
		if backupErr = agent.BackupFiles(ctx, req.User, outputPath); backupErr == nil {
			produced["files"] = outputPath
		}
	}

	// Optional encryption — honour an explicit EncryptionPassword, else the
	// server-wide BACKUP_ENCRYPTION_KEY. Encrypts every produced archive
	// (<f> → <f>.enc) and re-points the stored paths. (The model exposed
	// EncryptionPassword for releases but never used it — the "AES-256-CBC"
	// claim in the docs was false until this.)
	if backupErr == nil {
		if key := s.encryptionKey(req.EncryptionPassword); key != "" {
			if err := s.encryptProduced(ctx, key, produced, &backup); err != nil {
				backupErr = fmt.Errorf("encryption failed: %w", err)
			} else {
				backup.Encrypted = true
				outputPath = backup.Path
			}
		}
	}

	if backupErr != nil {
		backup.Status = "failed"
	} else {
		backup.Status = "completed"
		now := time.Now()
		backup.CompletedAt = &now
		if result, err := agent.RunCommand(ctx, "stat", "--format=%s", backup.Path); err == nil {
			sizeBytes, _ := strconv.ParseFloat(strings.TrimSpace(result.Output), 64)
			backup.SizeMB = sizeBytes / (1024 * 1024)
		}
	}

	// Off-site transfer. Pre-fix this only ran for storage=remote/both and
	// only shipped the single files archive; s3 was validated but dead.
	if backupErr == nil && req.Storage != "local" && req.Storage != "" {
		var transferErr error
		switch req.Storage {
		case "s3":
			transferErr = s.transferAllToS3(ctx, produced, req)
		case "remote", "both":
			if req.RemoteDestination == nil {
				transferErr = fmt.Errorf("remote storage requested but no destination supplied")
			} else {
				rd := req.RemoteDestination
				if rd.Port == 0 {
					switch rd.Protocol {
					case "sftp", "scp":
						rd.Port = 22
					case "ftp":
						rd.Port = 21
					}
				}
				transferErr = s.transferAllToRemote(ctx, produced, rd)
			}
		}
		if transferErr != nil {
			backup.Status = "failed"
			backupErr = transferErr
		} else if req.Storage == "remote" || req.Storage == "s3" {
			// Remote-only: drop the local copies once they're safely off-site.
			for _, p := range produced {
				os.Remove(p)
			}
			backup.Path, backup.DatabasePath, backup.EmailPath, backup.ConfigPath = "", "", "", ""
		}
	}

	result, err := s.db.Collection(database.ColBackups).InsertOne(ctx, backup)
	if err != nil {
		return nil, err
	}
	backup.ID = result.InsertedID.(primitive.ObjectID)
	return &backup, backupErr
}

// encryptionKey resolves the key for at-rest backup encryption: an explicit
// per-request password wins, else the server-wide BACKUP_ENCRYPTION_KEY from
// the environment. A blank or placeholder key means "no encryption".
func (s *BackupService) encryptionKey(reqPass string) string {
	if k := strings.TrimSpace(reqPass); k != "" {
		return k
	}
	k := strings.TrimSpace(os.Getenv("BACKUP_ENCRYPTION_KEY"))
	if k == "" || k == "change-me-to-a-random-encryption-key" {
		return ""
	}
	return k
}

// encryptProduced encrypts each produced archive in place and updates both
// the produced map and the backup record's path fields to the .enc paths.
func (s *BackupService) encryptProduced(ctx context.Context, key string, produced map[string]string, backup *models.Backup) error {
	for comp, p := range produced {
		enc := p + ".enc"
		if err := agent.EncryptFile(ctx, p, enc, key); err != nil {
			return err
		}
		os.Remove(p)
		produced[comp] = enc
		// For a single-component backup the component archive IS the main
		// Path; keep Path pointing at the encrypted file so Download/size work.
		if p == backup.Path {
			backup.Path = enc
		}
		switch comp {
		case "database":
			backup.DatabasePath = enc
		case "email":
			backup.EmailPath = enc
		case "config":
			backup.ConfigPath = enc
		}
	}
	return nil
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

// transferAllToRemote ships every produced archive to an FTP/SFTP/SCP
// destination, treating rd.Path as a directory and appending each archive's
// basename. Pre-fix a "full" backup transferred only the files archive, so a
// remote-only full backup silently dropped DB/email/config.
func (s *BackupService) transferAllToRemote(ctx context.Context, produced map[string]string, rd *models.RemoteDestination) error {
	dir := strings.TrimSuffix(rd.Path, "/")
	for _, local := range produced {
		perFile := *rd
		perFile.Path = dir + "/" + filepath.Base(local)
		if err := s.transferToRemote(ctx, local, &perFile); err != nil {
			return fmt.Errorf("transfer %s: %w", filepath.Base(local), err)
		}
	}
	return nil
}

// transferAllToS3 uploads every produced archive to S3 (or any S3-compatible
// store) via an rclone on-the-fly connection string built from the request's
// credentials. This is what makes storage=s3 real — it was a validated but
// completely unimplemented path before.
func (s *BackupService) transferAllToS3(ctx context.Context, produced map[string]string, req *models.CreateBackupRequest) error {
	if req.S3Bucket == "" || req.S3AccessKey == "" || req.S3SecretKey == "" {
		return fmt.Errorf("s3 storage requires bucket, access key and secret key")
	}
	spec := fmt.Sprintf(":s3,access_key_id=%s,secret_access_key=%s", req.S3AccessKey, req.S3SecretKey)
	if req.S3Region != "" {
		spec += ",region=" + req.S3Region
	}
	if req.S3Endpoint != "" {
		spec += ",endpoint=" + req.S3Endpoint
	}
	spec += ":" + strings.TrimSuffix(req.S3Bucket, "/") + "/"
	for _, local := range produced {
		if err := agent.UploadViaRclone(ctx, local, spec); err != nil {
			return fmt.Errorf("s3 upload %s: %w", filepath.Base(local), err)
		}
	}
	return nil
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
	encKey := s.encryptionKey(req.EncryptionPassword)
	// Tenant isolation for the body-driven restore sources. "server" restores
	// resolve the target user from the stored backup record via GetByID (already
	// tenant-gated), but "upload" and "remote" take req.User/req.Domain straight
	// from the request — without a check a tenant-scoped caller could restore an
	// arbitrary archive OVER another tenant's /home + databases. Owner/internal
	// callers no-op.
	if scope := GetCallerScope(ctx); scope != nil && (req.Source == "upload" || req.Source == "remote") {
		if err := scope.AssertOwns(ctx, s.db, req.User); err != nil {
			return fmt.Errorf("user %q is not in your tenant", req.User)
		}
		if req.Domain != "" {
			if err := scope.AssertOwnsDomain(ctx, s.db, strings.ToLower(strings.TrimSpace(req.Domain))); err != nil {
				return fmt.Errorf("domain %q is not in your tenant", req.Domain)
			}
		}
	}
	switch req.Source {
	case "server":
		return s.restoreFromServer(ctx, req, encKey)
	case "upload":
		// File is already saved locally by the handler; req.BackupID holds the temp file path
		return s.restoreFromFile(ctx, req.BackupID, req.RestoreType, req.User, req.Domain, encKey)
	case "remote":
		return s.restoreFromRemote(ctx, req, encKey)
	default:
		// Fallback: treat as server restore for backward compatibility
		return s.restoreFromServer(ctx, req, encKey)
	}
}

func (s *BackupService) restoreFromServer(ctx context.Context, req *models.RestoreRequest, encKey string) error {
	backup, err := s.GetByID(ctx, req.BackupID)
	if err != nil {
		return fmt.Errorf("backup not found: %w", err)
	}
	user, domain := backup.User, backup.Domain
	// A full restore of a full backup reinstates every captured component,
	// not just files — the cPanel "full" restore previously restored files
	// only and silently skipped DB/email/config.
	if req.RestoreType == "full" && backup.Type == "full" {
		return s.restoreFull(ctx, backup, user, domain, encKey)
	}
	// Single-component restore reads the matching sibling archive when present.
	path := backup.Path
	switch req.RestoreType {
	case "database":
		if backup.DatabasePath != "" {
			path = backup.DatabasePath
		}
	case "email":
		if backup.EmailPath != "" {
			path = backup.EmailPath
		}
	case "config":
		if backup.ConfigPath != "" {
			path = backup.ConfigPath
		}
	}
	return s.restoreFromFile(ctx, path, req.RestoreType, user, domain, encKey)
}

// restoreFull reinstates every component of a full backup, accumulating
// per-component errors instead of stopping at the first failure.
func (s *BackupService) restoreFull(ctx context.Context, b *models.Backup, user, domain, encKey string) error {
	var errs []string
	restore := func(comp, path, rtype string) {
		if path == "" {
			return
		}
		if err := s.restoreFromFile(ctx, path, rtype, user, domain, encKey); err != nil {
			errs = append(errs, comp+": "+err.Error())
		}
	}
	restore("files", b.Path, "files")
	restore("database", b.DatabasePath, "database")
	restore("email", b.EmailPath, "email")
	restore("config", b.ConfigPath, "config")
	if len(errs) > 0 {
		return fmt.Errorf("full restore had errors — %s", strings.Join(errs, "; "))
	}
	return nil
}

func (s *BackupService) restoreFromRemote(ctx context.Context, req *models.RestoreRequest, encKey string) error {
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

	return s.restoreFromFile(ctx, tmpPath, req.RestoreType, req.User, req.Domain, encKey)
}

func (s *BackupService) restoreFromFile(ctx context.Context, filePath, restoreType, user, domain, encKey string) error {
	if filePath == "" {
		return fmt.Errorf("no %s archive is available for this backup", restoreType)
	}
	// Encrypted archives are decrypted to a temp file first.
	if strings.HasSuffix(filePath, ".enc") {
		if encKey == "" {
			return fmt.Errorf("backup is encrypted but no encryption key/password was provided")
		}
		dec := fmt.Sprintf("/tmp/serverpanel-dec-%d-%s", time.Now().UnixNano(),
			strings.TrimSuffix(filepath.Base(filePath), ".enc"))
		if err := agent.DecryptFile(ctx, filePath, dec, encKey); err != nil {
			return fmt.Errorf("failed to decrypt backup: %w", err)
		}
		defer os.Remove(dec)
		filePath = dec
	}
	switch restoreType {
	case "full", "files":
		if err := agent.RestoreFiles(ctx, user, filePath); err != nil {
			return fmt.Errorf("failed to restore files: %w", err)
		}
	case "database":
		if err := s.restoreDatabases(ctx, user, domain, filePath); err != nil {
			return fmt.Errorf("failed to restore database: %w", err)
		}
	case "email":
		if err := agent.RestoreEmailLocal(ctx, user, domain, filePath); err != nil {
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

// CreateSchedule sets up a new automated backup schedule and installs a cron
// entry that invokes `bzpanel backup-run <id>`. Pre-fix the cron called
// /opt/serverpanel/backend/scripts/backup.sh — a script that does not exist
// anywhere in the repo, so every scheduled backup was a silent no-op. The cron
// line carries a `# bzpanel-schedule:<id>` marker so DeleteSchedule can remove
// exactly this entry.
func (s *BackupService) CreateSchedule(ctx context.Context, schedule *models.BackupSchedule) (*models.BackupSchedule, error) {
	schedule.CreatedAt = time.Now()
	schedule.UpdatedAt = time.Now()
	schedule.Enabled = true

	result, err := s.db.Collection(database.ColBackupSchedules).InsertOne(ctx, schedule)
	if err != nil {
		return nil, err
	}
	schedule.ID = result.InsertedID.(primitive.ObjectID)

	id := schedule.ID.Hex()
	backupCmd := fmt.Sprintf("%s backup-run %s # %s", bzpanelBinary(), id, scheduleMarker(id))
	if err := agent.WriteCrontab(ctx, "root", cronExpr(schedule.Schedule, schedule.Time), backupCmd); err != nil {
		// Roll back so we never leave a schedule record with no working cron.
		s.db.Collection(database.ColBackupSchedules).DeleteOne(ctx, bson.M{"_id": schedule.ID})
		return nil, fmt.Errorf("install cron: %w", err)
	}
	return schedule, nil
}

// DeleteSchedule removes an automated backup schedule AND its cron line.
// Pre-fix this deleted only the Mongo record, leaving an orphaned cron entry
// that kept firing the (no-op) backup forever.
func (s *BackupService) DeleteSchedule(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid schedule ID")
	}
	agent.RemoveCrontabMatching(ctx, "root", scheduleMarker(id))
	_, err = s.db.Collection(database.ColBackupSchedules).DeleteOne(ctx, bson.M{"_id": oid})
	return err
}

// RunScheduled executes the backup described by a schedule, then prunes old
// backups for that user/domain beyond RetentionCount. Invoked by
// `bzpanel backup-run <id>` from cron.
func (s *BackupService) RunScheduled(ctx context.Context, scheduleID string) error {
	oid, err := primitive.ObjectIDFromHex(scheduleID)
	if err != nil {
		return fmt.Errorf("invalid schedule ID")
	}
	var sched models.BackupSchedule
	if err := s.db.Collection(database.ColBackupSchedules).FindOne(ctx, bson.M{"_id": oid}).Decode(&sched); err != nil {
		return fmt.Errorf("schedule not found: %w", err)
	}
	if !sched.Enabled {
		return nil
	}
	req := &models.CreateBackupRequest{
		Type:    sched.Type,
		User:    sched.User,
		Domain:  sched.Domain,
		Storage: sched.Storage,
	}
	if req.Storage == "" {
		req.Storage = "local"
	}
	if sched.Storage == "s3" {
		req.S3Bucket = sched.S3Bucket
		req.S3Region = sched.S3Region
		req.S3AccessKey = sched.S3AccessKey
		req.S3SecretKey = sched.S3SecretKey
	}
	if _, err := s.Create(ctx, req); err != nil {
		return err
	}
	if sched.RetentionCount > 0 {
		s.enforceRetention(ctx, sched.User, sched.Domain, sched.RetentionCount)
	}
	return nil
}

// enforceRetention deletes the oldest backups for a user/domain beyond keep,
// removing both the archive files and the Mongo records. Honours
// BackupSchedule.RetentionCount, which the model stored but nothing ever
// acted on (backups accumulated on disk indefinitely).
func (s *BackupService) enforceRetention(ctx context.Context, user, domain string, keep int) {
	col := s.db.Collection(database.ColBackups)
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetSkip(int64(keep))
	cur, err := col.Find(ctx, bson.M{"user": user, "domain": domain}, opts)
	if err != nil {
		return
	}
	var old []models.Backup
	if err := cur.All(ctx, &old); err != nil {
		return
	}
	for _, b := range old {
		for _, p := range []string{b.Path, b.DatabasePath, b.EmailPath, b.ConfigPath} {
			if p != "" {
				os.Remove(p)
			}
		}
		col.DeleteOne(ctx, bson.M{"_id": b.ID})
	}
}

func scheduleMarker(id string) string { return "bzpanel-schedule:" + id }

// bzpanelBinary returns the path cron should use to invoke the CLI.
func bzpanelBinary() string {
	for _, p := range []string{"/usr/local/bin/bzpanel", "/opt/serverpanel/bin/bzpanel"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/usr/local/bin/bzpanel"
}

// cronExpr turns a schedule's (period, "HH:MM") into a 5-field cron
// expression. If period already looks like a raw cron expression (it contains
// a space), it's used verbatim so power users can supply their own.
func cronExpr(period, hhmm string) string {
	if strings.Contains(strings.TrimSpace(period), " ") {
		return strings.TrimSpace(period)
	}
	min, hour := 0, 3
	if parts := strings.SplitN(strings.TrimSpace(hhmm), ":", 2); len(parts) == 2 {
		if h, err := strconv.Atoi(parts[0]); err == nil {
			hour = h
		}
		if m, err := strconv.Atoi(parts[1]); err == nil {
			min = m
		}
	}
	switch strings.ToLower(strings.TrimSpace(period)) {
	case "hourly":
		return fmt.Sprintf("%d * * * *", min)
	case "weekly":
		return fmt.Sprintf("%d %d * * 0", min, hour)
	case "monthly":
		return fmt.Sprintf("%d %d 1 * *", min, hour)
	default: // daily / unspecified
		return fmt.Sprintf("%d %d * * *", min, hour)
	}
}

// backupDatabases dumps every MySQL/Mongo database the panel provisioned for
// this domain (or owner) into a single tar.gz. Replaces the pre-fix behaviour
// of `mongodump --db <domain>` — which dumped a MongoDB database named after
// the domain, a database that never exists (hosted site data lives in MySQL,
// and the panel's own data lives in `serverpanel`, not a per-domain DB).
func (s *BackupService) backupDatabases(ctx context.Context, user, domain, outputPath string) error {
	filter := bson.M{"$or": bson.A{bson.M{"domain": domain}, bson.M{"owner": user}}}
	cur, err := s.db.Collection(database.ColDatabases).Find(ctx, filter)
	if err != nil {
		return fmt.Errorf("list databases: %w", err)
	}
	var dbs []models.Database
	if err := cur.All(ctx, &dbs); err != nil {
		return fmt.Errorf("decode databases: %w", err)
	}

	stage := fmt.Sprintf("/tmp/serverpanel-dbbackup-%d", time.Now().UnixNano())
	defer agent.RunCommand(ctx, "rm", "-rf", stage)
	if _, err := agent.RunCommand(ctx, "mkdir", "-p", stage+"/mysql", stage+"/mongo"); err != nil {
		return fmt.Errorf("stage db backup: %w", err)
	}
	for _, d := range dbs {
		if d.DBName == "" {
			continue
		}
		switch strings.ToLower(d.Type) {
		case "mysql", "mariadb":
			agent.MySQLDump(ctx, d.DBName, fmt.Sprintf("%s/mysql/%s.sql.gz", stage, d.DBName))
		case "mongodb", "mongo":
			agent.BackupMongoDB(ctx, d.DBName, fmt.Sprintf("%s/mongo/%s.archive.gz", stage, d.DBName))
		}
	}
	// Always emit a valid archive — a domain with no databases must not fail
	// the surrounding "full" backup.
	if _, err := agent.RunCommand(ctx, "tar", "-czf", outputPath, "-C", stage, "."); err != nil {
		return fmt.Errorf("archive databases: %w", err)
	}
	return nil
}

// restoreDatabases extracts a database archive and reloads each dump. MySQL
// dumps are self-contained (--databases → CREATE DATABASE + USE); mongo
// archives restore with --drop. Per-file errors are tolerated so one bad DB
// doesn't abort the rest of the restore.
func (s *BackupService) restoreDatabases(ctx context.Context, user, domain, archivePath string) error {
	stage := fmt.Sprintf("/tmp/serverpanel-dbrestore-%d", time.Now().UnixNano())
	defer agent.RunCommand(ctx, "rm", "-rf", stage)
	if _, err := agent.RunCommand(ctx, "mkdir", "-p", stage); err != nil {
		return fmt.Errorf("stage db restore: %w", err)
	}
	if _, err := agent.RunCommand(ctx, "tar", "-xzf", archivePath, "-C", stage); err != nil {
		return fmt.Errorf("extract db archive: %w", err)
	}
	_, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(`
for f in %[1]s/mysql/*.sql.gz; do [ -e "$f" ] || continue; gunzip -c "$f" | mysql 2>/dev/null || true; done
for f in %[1]s/mongo/*.archive.gz; do [ -e "$f" ] || continue; mongorestore --gzip --drop --archive="$f" 2>/dev/null || true; done
true`, stage))
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
