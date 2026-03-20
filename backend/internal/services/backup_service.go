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
		if err := agent.BackupFiles(ctx, req.User, outputPath); err != nil {
			backupErr = err
			break
		}
		dbPath := fmt.Sprintf("%s/%s-db-%s.gz", backupDir, req.Domain, timestamp)
		agent.BackupMongoDB(ctx, req.Domain, dbPath)
		emailPath := fmt.Sprintf("%s/%s-email-%s.tar.gz", backupDir, req.Domain, timestamp)
		agent.BackupEmail(ctx, req.Domain, emailPath)
	case "files":
		backupErr = agent.BackupFiles(ctx, req.User, outputPath)
	case "database":
		backupErr = agent.BackupMongoDB(ctx, req.Domain, outputPath)
	case "email":
		backupErr = agent.BackupEmail(ctx, req.Domain, outputPath)
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
