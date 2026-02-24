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

type DeployService struct {
	db *mongo.Database
}

func NewDeployService(db *mongo.Database) *DeployService {
	return &DeployService{db: db}
}

func (s *DeployService) List(ctx context.Context, page, limit int) ([]models.GitHubDeploy, int64, error) {
	col := s.db.Collection(database.ColGitHubDeploys)
	filter := bson.M{}

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

	var deploys []models.GitHubDeploy
	if err := cursor.All(ctx, &deploys); err != nil {
		return nil, 0, err
	}
	if deploys == nil {
		deploys = []models.GitHubDeploy{}
	}
	return deploys, total, nil
}

func (s *DeployService) GetByID(ctx context.Context, id string) (*models.GitHubDeploy, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid deploy ID")
	}
	col := s.db.Collection(database.ColGitHubDeploys)
	var deploy models.GitHubDeploy
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&deploy); err != nil {
		return nil, err
	}
	return &deploy, nil
}

func (s *DeployService) Create(ctx context.Context, req *models.CreateGitHubDeployRequest) (*models.GitHubDeploy, error) {
	deployDir := fmt.Sprintf("/home/deploys/%s", req.Domain)
	os.MkdirAll(deployDir, 0755)

	// Clone repository
	token := ""
	if req.EnvVars != nil {
		token = req.EnvVars["GITHUB_TOKEN"]
	}
	if err := agent.GitClone(ctx, req.Repo, req.Branch, deployDir, token); err != nil {
		return nil, fmt.Errorf("failed to clone repository: %w", err)
	}

	// Run build command if specified
	if req.BuildCommand != "" {
		_, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("cd %s && %s", deployDir, req.BuildCommand))
		if err != nil {
			return nil, fmt.Errorf("build failed: %w", err)
		}
	}

	// Get current commit info
	commitHash := ""
	commitMsg := ""
	commitAuthor := ""
	if result, err := agent.RunCommand(ctx, "git", "-C", deployDir, "log", "-1", "--format=%H|||%s|||%an"); err == nil {
		parts := strings.SplitN(strings.TrimSpace(result.Output), "|||", 3)
		if len(parts) == 3 {
			commitHash = parts[0][:8]
			commitMsg = parts[1]
			commitAuthor = parts[2]
		}
	}

	// Start the application if it has a start command
	serviceName := "sp-deploy-" + req.Domain
	if req.StartCommand != "" {
		workDir := deployDir
		if req.RootDir != "" {
			workDir = fmt.Sprintf("%s/%s", deployDir, req.RootDir)
		}
		agent.CreateSystemdService(ctx, serviceName, "root", workDir, req.StartCommand, req.EnvVars)

		// Create reverse proxy if domain is specified
		agent.CreateReverseProxy(ctx, &agent.VhostConfig{
			Domain: req.Domain,
			Port:   8080, // Default port
		})
	}

	now := time.Now()
	deploy := models.GitHubDeploy{
		Domain:          req.Domain,
		Repo:            req.Repo,
		Branch:          req.Branch,
		AppType:         req.AppType,
		AutoDeploy:      req.AutoDeploy,
		BuildCommand:    req.BuildCommand,
		StartCommand:    req.StartCommand,
		EnvVars:         req.EnvVars,
		NodeVersion:     req.NodeVersion,
		RootDir:         req.RootDir,
		PreDeployScript: req.PreDeployScript,
		PostDeployScript: req.PostDeployScript,
		Status:          "active",
		CurrentCommit:   commitHash,
		CommitMessage:   commitMsg,
		CommitAuthor:    commitAuthor,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	col := s.db.Collection(database.ColGitHubDeploys)
	result, err := col.InsertOne(ctx, deploy)
	if err != nil {
		return nil, err
	}
	deploy.ID = result.InsertedID.(primitive.ObjectID)

	// Create initial release record
	release := models.DeployRelease{
		DeployID:      deploy.ID,
		Commit:        commitHash,
		CommitMessage: commitMsg,
		Author:        commitAuthor,
		Branch:        req.Branch,
		Status:        "success",
		Trigger:       "manual",
		DeployedAt:    now,
	}
	s.db.Collection(database.ColDeployments).InsertOne(ctx, release)

	return &deploy, nil
}

func (s *DeployService) Redeploy(ctx context.Context, id string) (*models.DeployRelease, error) {
	deploy, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	startTime := time.Now()
	deployDir := fmt.Sprintf("/home/deploys/%s", deploy.Domain)
	logs := []string{}

	// Pull latest code
	logs = append(logs, "Pulling latest code...")
	if err := agent.GitPull(ctx, deployDir, deploy.Branch); err != nil {
		return nil, fmt.Errorf("git pull failed: %w", err)
	}

	// Run build command
	if deploy.BuildCommand != "" {
		logs = append(logs, fmt.Sprintf("Running build: %s", deploy.BuildCommand))
		result, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("cd %s && %s", deployDir, deploy.BuildCommand))
		if err != nil {
			return nil, fmt.Errorf("build failed: %w", err)
		}
		if result != nil {
			logs = append(logs, result.Output)
		}
	}

	// Restart service
	serviceName := "sp-deploy-" + deploy.Domain
	logs = append(logs, "Restarting service...")
	agent.ServiceAction(ctx, serviceName, "restart")

	// Get commit info
	commitHash := ""
	commitMsg := ""
	commitAuthor := ""
	if result, err := agent.RunCommand(ctx, "git", "-C", deployDir, "log", "-1", "--format=%H|||%s|||%an"); err == nil {
		parts := strings.SplitN(strings.TrimSpace(result.Output), "|||", 3)
		if len(parts) == 3 {
			commitHash = parts[0][:8]
			commitMsg = parts[1]
			commitAuthor = parts[2]
		}
	}

	duration := int(time.Since(startTime).Seconds())
	now := time.Now()

	// Update deploy record
	s.db.Collection(database.ColGitHubDeploys).UpdateOne(ctx, bson.M{"_id": deploy.ID}, bson.M{
		"$set": bson.M{
			"current_commit": commitHash,
			"commit_message": commitMsg,
			"commit_author":  commitAuthor,
			"status":         "active",
			"updated_at":     now,
		},
	})

	// Create release record
	release := models.DeployRelease{
		DeployID:        deploy.ID,
		Commit:          commitHash,
		CommitMessage:   commitMsg,
		Author:          commitAuthor,
		Branch:          deploy.Branch,
		Status:          "success",
		Trigger:         "manual",
		DurationSeconds: duration,
		Logs:            logs,
		DeployedAt:      now,
	}
	result, err := s.db.Collection(database.ColDeployments).InsertOne(ctx, release)
	if err != nil {
		return nil, err
	}
	release.ID = result.InsertedID.(primitive.ObjectID)

	return &release, nil
}

func (s *DeployService) Rollback(ctx context.Context, id string, targetCommit string) (*models.DeployRelease, error) {
	deploy, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	deployDir := fmt.Sprintf("/home/deploys/%s", deploy.Domain)

	// Checkout target commit
	if _, err := agent.RunCommand(ctx, "git", "-C", deployDir, "checkout", targetCommit); err != nil {
		return nil, fmt.Errorf("failed to checkout commit %s: %w", targetCommit, err)
	}

	// Rebuild
	if deploy.BuildCommand != "" {
		if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("cd %s && %s", deployDir, deploy.BuildCommand)); err != nil {
			return nil, fmt.Errorf("rebuild failed: %w", err)
		}
	}

	// Restart service
	serviceName := "sp-deploy-" + deploy.Domain
	agent.ServiceAction(ctx, serviceName, "restart")

	now := time.Now()

	// Update deploy record
	s.db.Collection(database.ColGitHubDeploys).UpdateOne(ctx, bson.M{"_id": deploy.ID}, bson.M{
		"$set": bson.M{"current_commit": targetCommit, "updated_at": now},
	})

	release := models.DeployRelease{
		DeployID:      deploy.ID,
		Commit:        targetCommit,
		CommitMessage: "Rollback to " + targetCommit,
		Branch:        deploy.Branch,
		Status:        "success",
		Trigger:       "rollback",
		DeployedAt:    now,
	}
	result, err := s.db.Collection(database.ColDeployments).InsertOne(ctx, release)
	if err != nil {
		return nil, err
	}
	release.ID = result.InsertedID.(primitive.ObjectID)
	return &release, nil
}

func (s *DeployService) Cancel(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid deploy ID")
	}
	_, err = s.db.Collection(database.ColGitHubDeploys).UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{"status": "cancelled", "updated_at": time.Now()},
	})
	return err
}

func (s *DeployService) Delete(ctx context.Context, id string) error {
	deploy, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}

	// Stop and delete service
	serviceName := "sp-deploy-" + deploy.Domain
	agent.DeleteSystemdService(ctx, serviceName)

	// Delete nginx vhost
	agent.DeleteVhost(ctx, deploy.Domain)

	// Remove deploy directory
	os.RemoveAll(fmt.Sprintf("/home/deploys/%s", deploy.Domain))

	// Delete release history
	s.db.Collection(database.ColDeployments).DeleteMany(ctx, bson.M{"deploy_id": deploy.ID})

	// Delete deploy config
	_, err = s.db.Collection(database.ColGitHubDeploys).DeleteOne(ctx, bson.M{"_id": deploy.ID})
	return err
}

func (s *DeployService) GetLogs(ctx context.Context, id string, releaseID string) ([]string, error) {
	deploy, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if releaseID != "" {
		oid, err := primitive.ObjectIDFromHex(releaseID)
		if err == nil {
			var release models.DeployRelease
			if err := s.db.Collection(database.ColDeployments).FindOne(ctx, bson.M{"_id": oid}).Decode(&release); err == nil {
				return release.Logs, nil
			}
		}
	}

	// Fall back to journalctl
	serviceName := "sp-deploy-" + deploy.Domain
	result, err := agent.RunCommand(ctx, "journalctl", "-u", serviceName, "-n", "100", "--no-pager")
	if err != nil {
		return []string{}, nil
	}
	lines := strings.Split(strings.TrimSpace(result.Output), "\n")
	return lines, nil
}

func (s *DeployService) History(ctx context.Context, id string, page, limit int) ([]models.DeployRelease, int64, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid deploy ID")
	}

	col := s.db.Collection(database.ColDeployments)
	filter := bson.M{"deploy_id": oid}

	total, err := col.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	skip := int64((page - 1) * limit)
	opts := options.Find().SetSkip(skip).SetLimit(int64(limit)).SetSort(bson.D{{Key: "deployed_at", Value: -1}})
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var releases []models.DeployRelease
	if err := cursor.All(ctx, &releases); err != nil {
		return nil, 0, err
	}
	if releases == nil {
		releases = []models.DeployRelease{}
	}
	return releases, total, nil
}

func (s *DeployService) HandleGitHubWebhook(ctx context.Context, deployID string, payload map[string]interface{}) error {
	deploy, err := s.GetByID(ctx, deployID)
	if err != nil {
		return fmt.Errorf("deploy config not found: %w", err)
	}

	if deploy.Paused {
		return fmt.Errorf("auto-deploy is paused for this configuration")
	}

	// Extract ref from webhook payload to verify it matches the configured branch
	ref, _ := payload["ref"].(string)
	expectedRef := "refs/heads/" + deploy.Branch
	if ref != "" && ref != expectedRef {
		return nil // Ignore pushes to other branches
	}

	// Trigger redeploy
	_, err = s.Redeploy(ctx, deployID)
	return err
}

func (s *DeployService) Pause(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid deploy ID")
	}
	_, err = s.db.Collection(database.ColGitHubDeploys).UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{"paused": true, "updated_at": time.Now()},
	})
	return err
}

func (s *DeployService) Resume(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid deploy ID")
	}
	_, err = s.db.Collection(database.ColGitHubDeploys).UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{"paused": false, "updated_at": time.Now()},
	})
	return err
}
