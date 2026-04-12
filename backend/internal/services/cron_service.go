package services

import (
	"context"
	"fmt"
	"os"
	"regexp"
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

const (
	managedBeginMarker = "# BEGIN ServerPanel managed - DO NOT EDIT"
	managedEndMarker   = "# END ServerPanel managed"
)

var (
	envVarRegex   = regexp.MustCompile(`^\s*[A-Za-z_][A-Za-z0-9_]*\s*=`)
	cronFieldRegex = regexp.MustCompile(`^[\d\*\/\,\-A-Za-z?LW#]+$`)
)

// parseCronLine extracts (schedule, command) from a single crontab line.
// Returns ok=false for blank lines, comments, env vars, and malformed entries.
// Supports both 5-field schedules and @-shortcuts (@daily, @reboot, etc.).
func parseCronLine(line string) (schedule, command string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	if envVarRegex.MatchString(trimmed) {
		return "", "", false
	}
	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return "", "", false
	}
	if strings.HasPrefix(fields[0], "@") {
		return fields[0], strings.Join(fields[1:], " "), true
	}
	if len(fields) < 6 {
		return "", "", false
	}
	for _, f := range fields[:5] {
		if !cronFieldRegex.MatchString(f) {
			return "", "", false
		}
	}
	return strings.Join(fields[:5], " "), strings.Join(fields[5:], " "), true
}

// List returns all cron jobs, optionally filtered by domain or user. Before
// reading from the database, system crontabs are synced so any entries added
// outside the panel become visible and manageable.
func (s *CronService) List(ctx context.Context, domain, user string) ([]models.CronJob, error) {
	if user != "" {
		s.syncSystemCron(ctx, user)
	} else {
		users, _ := agent.ListCrontabUsers(ctx)
		for _, u := range users {
			s.syncSystemCron(ctx, u)
		}
	}

	col := s.db.Collection(database.ColCronJobs)
	filter := bson.M{}
	if domain != "" {
		filter["domain"] = domain
	}
	if user != "" {
		filter["user"] = user
	}
	if scope := GetCallerScope(ctx); scope != nil {
		filter = scope.ApplyTo(ctx, s.db, "user", filter)
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

// syncSystemCron reads a user's crontab and inserts any entries not already
// tracked in the database. Matching is done by (user, schedule, command).
func (s *CronService) syncSystemCron(ctx context.Context, user string) {
	content, err := agent.ReadCrontab(ctx, user)
	if err != nil || content == "" {
		return
	}
	col := s.db.Collection(database.ColCronJobs)
	for _, line := range strings.Split(content, "\n") {
		schedule, command, ok := parseCronLine(line)
		if !ok {
			continue
		}
		var existing models.CronJob
		err := col.FindOne(ctx, bson.M{
			"user":     user,
			"schedule": schedule,
			"command":  command,
		}).Decode(&existing)
		if err == nil {
			continue
		}
		now := time.Now()
		job := models.CronJob{
			User:        user,
			Schedule:    schedule,
			Command:     command,
			Description: "Imported from system crontab",
			Enabled:     true,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		col.InsertOne(ctx, job)
	}
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

// Create adds a new cron job: insert the DB record first, then rewrite the
// user's crontab so external entries are preserved.
func (s *CronService) Create(ctx context.Context, req *models.CreateCronRequest) (*models.CronJob, error) {
	if req.User == "" {
		req.User = "root"
	}
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

	result, err := s.db.Collection(database.ColCronJobs).InsertOne(ctx, job)
	if err != nil {
		return nil, err
	}
	job.ID = result.InsertedID.(primitive.ObjectID)

	if err := s.rewriteCrontab(ctx, req.User); err != nil {
		return nil, fmt.Errorf("failed to write crontab: %w", err)
	}
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

// rewriteCrontab regenerates a user's crontab non-destructively. External
// content (env vars, comments, blank lines, and any cron entries outside the
// managed block) is preserved verbatim. Inside the managed block, all enabled
// DB-tracked entries for the user are written. Stale managed-block content is
// stripped first.
func (s *CronService) rewriteCrontab(ctx context.Context, user string) error {
	current, _ := agent.ReadCrontab(ctx, user)

	// Strip the existing managed block (if any) so we can rewrite it.
	preserved := stripManagedBlock(current)

	// Collect known managed jobs from DB so we can also strip duplicates that
	// may exist as plain entries (e.g., on first import after sync).
	col := s.db.Collection(database.ColCronJobs)
	dbCursor, err := col.Find(ctx, bson.M{"user": user})
	if err != nil {
		return err
	}
	var allJobs []models.CronJob
	if err := dbCursor.All(ctx, &allJobs); err != nil {
		dbCursor.Close(ctx)
		return err
	}
	dbCursor.Close(ctx)

	managedKeys := make(map[string]bool, len(allJobs))
	for _, j := range allJobs {
		managedKeys[j.Schedule+"\x00"+j.Command] = true
	}

	// Walk preserved lines: drop any plain cron entry that matches a known
	// managed (schedule, command) — these are duplicates left over from the
	// initial system-crontab import.
	var kept []string
	for _, line := range strings.Split(preserved, "\n") {
		if schedule, command, ok := parseCronLine(line); ok {
			if managedKeys[schedule+"\x00"+command] {
				continue
			}
		}
		kept = append(kept, line)
	}
	// Trim trailing blank lines.
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}

	// Build the managed block from enabled jobs.
	var managed []string
	for _, j := range allJobs {
		if j.Enabled {
			managed = append(managed, fmt.Sprintf("%s %s", j.Schedule, j.Command))
		}
	}

	var out []string
	out = append(out, kept...)
	if len(managed) > 0 {
		if len(kept) > 0 {
			out = append(out, "")
		}
		out = append(out, managedBeginMarker)
		out = append(out, managed...)
		out = append(out, managedEndMarker)
	}
	content := strings.Join(out, "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	tmpFile := fmt.Sprintf("/tmp/crontab_%s_%d", user, os.Getpid())
	if err := os.WriteFile(tmpFile, []byte(content), 0600); err != nil {
		return err
	}
	defer os.Remove(tmpFile)

	if content == "" {
		// Empty crontab — remove it instead of installing an empty file.
		_, err := agent.RunCommand(ctx, "crontab", "-u", user, "-r")
		return err
	}
	_, err = agent.RunCommand(ctx, "crontab", "-u", user, tmpFile)
	return err
}

// stripManagedBlock removes any content between BEGIN/END markers (inclusive).
func stripManagedBlock(content string) string {
	lines := strings.Split(content, "\n")
	var out []string
	skipping := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !skipping && trimmed == managedBeginMarker {
			skipping = true
			continue
		}
		if skipping {
			if trimmed == managedEndMarker {
				skipping = false
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
