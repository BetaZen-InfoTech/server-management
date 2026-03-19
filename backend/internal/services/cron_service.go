package services

import (
	"context"
	"fmt"
	"os"
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

type CronService struct {
	db *mongo.Database
}

func NewCronService(db *mongo.Database) *CronService {
	return &CronService{db: db}
}

// List returns all cron jobs, optionally filtered by domain or user.
func (s *CronService) List(ctx context.Context, domain, user string) ([]models.CronJob, error) {
	col := s.db.Collection(database.ColCronJobs)
	filter := bson.M{}
	if domain != "" {
		filter["domain"] = domain
	}
	if user != "" {
		filter["user"] = user
	}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var jobs []models.CronJob
	if err := cursor.All(ctx, &jobs); err != nil {
		return nil, err
	}
	if jobs == nil {
		jobs = []models.CronJob{}
	}
	return jobs, nil
}

// GetByID retrieves a single cron job by its ID.
func (s *CronService) GetByID(ctx context.Context, id string) (*models.CronJob, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid cron job ID")
	}
	var job models.CronJob
	if err := s.db.Collection(database.ColCronJobs).FindOne(ctx, bson.M{"_id": oid}).Decode(&job); err != nil {
		return nil, err
	}
	return &job, nil
}

// Create adds a new cron job to the system crontab and stores its record.
func (s *CronService) Create(ctx context.Context, req *models.CreateCronRequest) (*models.CronJob, error) {
	job := models.CronJob{
		Domain:      req.Domain,
		User:        req.User,
		Command:     req.Command,
		Schedule:    req.Schedule,
		Description: req.Description,
		NotifyEmail: req.NotifyEmail,
		NotifyOn:    req.NotifyOn,
		Enabled:     true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := agent.WriteCrontab(ctx, req.User, req.Schedule, req.Command); err != nil {
		return nil, fmt.Errorf("failed to write crontab: %w", err)
	}

	result, err := s.db.Collection(database.ColCronJobs).InsertOne(ctx, job)
	if err != nil {
		return nil, err
	}
	job.ID = result.InsertedID.(primitive.ObjectID)
	return &job, nil
}

// CPanelCreate creates a cron job for a cPanel customer, auto-populating user/domain from user ID.
func (s *CronService) CPanelCreate(ctx context.Context, userID string, req *models.CreateCronRequest) (*models.CronJob, error) {
	username, domain, err := s.getUserInfo(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}
	req.User = username
	if req.Domain == "" {
		req.Domain = domain
	}
	return s.Create(ctx, req)
}

// ListByUser returns cron jobs for a specific cPanel user, looked up by user ID.
func (s *CronService) ListByUser(ctx context.Context, userID string) ([]models.CronJob, error) {
	username, _, err := s.getUserInfo(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.List(ctx, "", username)
}

// Update modifies an existing cron job's settings.
func (s *CronService) Update(ctx context.Context, id string, updates map[string]interface{}) (*models.CronJob, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid cron job ID")
	}

	updates["updated_at"] = time.Now()
	col := s.db.Collection(database.ColCronJobs)
	_, err = col.UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": updates})
	if err != nil {
		return nil, err
	}

	var job models.CronJob
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&job); err != nil {
		return nil, err
	}

	s.rewriteCrontab(ctx, job.User)
	return &job, nil
}

// Delete removes a cron job from the system and database.
func (s *CronService) Delete(ctx context.Context, id string) error {
	job, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	oid, _ := primitive.ObjectIDFromHex(id)
	if _, err := s.db.Collection(database.ColCronJobs).DeleteOne(ctx, bson.M{"_id": oid}); err != nil {
		return err
	}

	s.rewriteCrontab(ctx, job.User)
	return nil
}

// Toggle enables or disables a cron job.
func (s *CronService) Toggle(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid cron job ID")
	}

	var job models.CronJob
	col := s.db.Collection(database.ColCronJobs)
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&job); err != nil {
		return err
	}

	_, err = col.UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{"enabled": !job.Enabled, "updated_at": time.Now()},
	})
	if err != nil {
		return err
	}

	s.rewriteCrontab(ctx, job.User)
	return nil
}

// RunNow executes a cron job immediately and returns its output.
func (s *CronService) RunNow(ctx context.Context, id string) (*models.CronExecution, error) {
	job, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	result, cmdErr := agent.RunCommandAsUser(ctx, job.User, job.Command)
	duration := time.Since(start).Milliseconds()

	execution := models.CronExecution{
		CronJobID:  job.ID,
		ExecutedAt: start,
		DurationMS: duration,
		Status:     "completed",
	}

	if result != nil {
		execution.Output = result.Output
		execution.ExitCode = result.ExitCode
	}
	if cmdErr != nil {
		execution.Status = "failed"
		if result != nil {
			execution.Output = result.Output + "\n" + result.Error
		}
	}

	insertResult, err := s.db.Collection(database.ColCronHistory).InsertOne(ctx, execution)
	if err != nil {
		return nil, err
	}
	execution.ID = insertResult.InsertedID.(primitive.ObjectID)
	return &execution, nil
}

// History returns past execution records for a specific cron job.
func (s *CronService) History(ctx context.Context, id string) ([]models.CronExecution, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid cron job ID")
	}

	col := s.db.Collection(database.ColCronHistory)
	opts := options.Find().SetSort(bson.D{{Key: "executed_at", Value: -1}}).SetLimit(50)
	cursor, err := col.Find(ctx, bson.M{"cron_job_id": oid}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var history []models.CronExecution
	if err := cursor.All(ctx, &history); err != nil {
		return nil, err
	}
	if history == nil {
		history = []models.CronExecution{}
	}
	return history, nil
}

// getUserInfo looks up a user's linux username and primary domain from user ID.
func (s *CronService) getUserInfo(ctx context.Context, userID string) (username string, domain string, err error) {
	oid, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return "", "", fmt.Errorf("invalid user ID")
	}
	var user struct {
		Username string   `bson:"username"`
		Domains  []string `bson:"domains"`
	}
	if err := s.db.Collection("users").FindOne(ctx, bson.M{"_id": oid}).Decode(&user); err != nil {
		return "", "", err
	}
	if len(user.Domains) > 0 {
		domain = user.Domains[0]
	}
	return user.Username, domain, nil
}

func (s *CronService) rewriteCrontab(ctx context.Context, user string) {
	col := s.db.Collection(database.ColCronJobs)
	cursor, err := col.Find(ctx, bson.M{"user": user, "enabled": true})
	if err != nil {
		return
	}
	defer cursor.Close(ctx)

	var entries []string
	var jobs []models.CronJob
	if err := cursor.All(ctx, &jobs); err != nil {
		return
	}
	for _, job := range jobs {
		entries = append(entries, fmt.Sprintf("%s %s", job.Schedule, job.Command))
	}

	crontab := strings.Join(entries, "\n") + "\n"

	// Write to temp file to avoid shell injection
	tmpFile := fmt.Sprintf("/tmp/crontab_%s_%d", user, os.Getpid())
	if err := os.WriteFile(tmpFile, []byte(crontab), 0600); err != nil {
		return
	}
	defer os.Remove(tmpFile)

	agent.RunCommand(ctx, "crontab", "-u", user, tmpFile)
}
