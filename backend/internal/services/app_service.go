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

type AppService struct {
	db *mongo.Database
}

func NewAppService(db *mongo.Database) *AppService {
	return &AppService{db: db}
}

func (s *AppService) List(ctx context.Context, page, limit int) ([]models.App, int64, error) {
	col := s.db.Collection(database.ColApps)
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

	var apps []models.App
	if err := cursor.All(ctx, &apps); err != nil {
		return nil, 0, err
	}

	// Check live status for each app
	for i := range apps {
		serviceName := "sp-app-" + apps[i].Name
		result, err := agent.RunCommand(ctx, "systemctl", "is-active", serviceName)
		if err == nil && strings.TrimSpace(result.Output) == "active" {
			apps[i].Status = "running"
		} else if apps[i].Status != "deploying" {
			apps[i].Status = "stopped"
		}
	}

	if apps == nil {
		apps = []models.App{}
	}
	return apps, total, nil
}

func (s *AppService) GetByName(ctx context.Context, name string) (*models.App, error) {
	col := s.db.Collection(database.ColApps)
	var app models.App
	if err := col.FindOne(ctx, bson.M{"name": name}).Decode(&app); err != nil {
		return nil, err
	}

	// Check live status
	serviceName := "sp-app-" + app.Name
	result, err := agent.RunCommand(ctx, "systemctl", "is-active", serviceName)
	if err == nil && strings.TrimSpace(result.Output) == "active" {
		app.Status = "running"
	} else if app.Status != "deploying" {
		app.Status = "stopped"
	}

	return &app, nil
}

func (s *AppService) Deploy(ctx context.Context, req *models.DeployAppRequest) (*models.App, error) {
	appDir := fmt.Sprintf("/home/%s/apps/%s", req.User, req.Name)
	os.MkdirAll(appDir, 0755)

	// Clone repository if git deploy
	if req.DeployMethod == "git" && req.GitURL != "" {
		branch := req.GitBranch
		if branch == "" {
			branch = "main"
		}
		if err := agent.GitClone(ctx, req.GitURL, branch, appDir, req.GitToken); err != nil {
			return nil, fmt.Errorf("failed to clone repository: %w", err)
		}
	}

	// Run build command
	if req.BuildCmd != "" {
		if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("cd %s && %s", appDir, req.BuildCmd)); err != nil {
			return nil, fmt.Errorf("build failed: %w", err)
		}
	}

	// Write .env file
	if len(req.EnvVars) > 0 {
		var envLines []string
		for k, v := range req.EnvVars {
			envLines = append(envLines, fmt.Sprintf("%s=%s", k, v))
		}
		os.WriteFile(fmt.Sprintf("%s/.env", appDir), []byte(strings.Join(envLines, "\n")+"\n"), 0600)
	}

	// Create systemd service
	if req.StartCmd != "" {
		if err := agent.CreateSystemdService(ctx, req.Name, req.User, appDir, req.StartCmd, req.EnvVars); err != nil {
			return nil, fmt.Errorf("failed to create service: %w", err)
		}
	}

	// Create reverse proxy for the domain
	if req.Domain != "" && req.Port > 0 {
		if err := agent.CreateReverseProxy(ctx, &agent.VhostConfig{
			Domain: req.Domain,
			Port:   req.Port,
		}); err != nil {
			return nil, fmt.Errorf("failed to create reverse proxy: %w", err)
		}
	}

	now := time.Now()
	app := models.App{
		Name:            req.Name,
		Domain:          req.Domain,
		AppType:         req.AppType,
		DeployMethod:    req.DeployMethod,
		User:            req.User,
		Port:            req.Port,
		GitURL:          req.GitURL,
		GitBranch:       req.GitBranch,
		GitToken:        req.GitToken,
		DockerImage:     req.DockerImage,
		DockerVolumes:   req.DockerVolumes,
		DockerNetwork:   req.DockerNetwork,
		BuildCmd:        req.BuildCmd,
		StartCmd:        req.StartCmd,
		HealthCheckPath: req.HealthCheckPath,
		MinInstances:    req.MinInstances,
		MaxInstances:    req.MaxInstances,
		EnvVars:         req.EnvVars,
		Status:          "running",
		LastDeployed:    &now,
		DeploymentsCount: 1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	col := s.db.Collection(database.ColApps)
	result, err := col.InsertOne(ctx, app)
	if err != nil {
		return nil, err
	}
	app.ID = result.InsertedID.(primitive.ObjectID)
	return &app, nil
}

func (s *AppService) Redeploy(ctx context.Context, name string) (*models.App, error) {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("app not found: %w", err)
	}

	appDir := fmt.Sprintf("/home/%s/apps/%s", app.User, app.Name)

	// Pull latest code
	if app.DeployMethod == "git" && app.GitBranch != "" {
		if err := agent.GitPull(ctx, appDir, app.GitBranch); err != nil {
			return nil, fmt.Errorf("git pull failed: %w", err)
		}
	}

	// Rebuild
	if app.BuildCmd != "" {
		if _, err := agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("cd %s && %s", appDir, app.BuildCmd)); err != nil {
			return nil, fmt.Errorf("rebuild failed: %w", err)
		}
	}

	// Restart service
	serviceName := "sp-app-" + app.Name
	agent.ServiceAction(ctx, serviceName, "restart")

	now := time.Now()
	col := s.db.Collection(database.ColApps)
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var updated models.App
	err = col.FindOneAndUpdate(ctx, bson.M{"_id": app.ID}, bson.M{
		"$set": bson.M{"status": "running", "last_deployed": now, "updated_at": now},
		"$inc": bson.M{"deployments_count": 1},
	}, opts).Decode(&updated)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *AppService) Action(ctx context.Context, name string, action string) error {
	serviceName := "sp-app-" + name
	if err := agent.ServiceAction(ctx, serviceName, action); err != nil {
		return fmt.Errorf("failed to %s app: %w", action, err)
	}

	status := "running"
	if action == "stop" {
		status = "stopped"
	}

	s.db.Collection(database.ColApps).UpdateOne(ctx, bson.M{"name": name}, bson.M{
		"$set": bson.M{"status": status, "updated_at": time.Now()},
	})
	return nil
}

func (s *AppService) Delete(ctx context.Context, name string) error {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return fmt.Errorf("app not found: %w", err)
	}

	// Stop and delete systemd service
	serviceName := "sp-app-" + app.Name
	agent.DeleteSystemdService(ctx, serviceName)

	// Delete nginx vhost
	if app.Domain != "" {
		agent.DeleteVhost(ctx, app.Domain)
	}

	// Remove app directory
	os.RemoveAll(fmt.Sprintf("/home/%s/apps/%s", app.User, app.Name))

	// Delete from database
	_, err = s.db.Collection(database.ColApps).DeleteOne(ctx, bson.M{"_id": app.ID})
	return err
}

func (s *AppService) GetLogs(ctx context.Context, name string, lines int) ([]string, error) {
	if lines <= 0 {
		lines = 100
	}
	serviceName := "sp-app-" + name
	result, err := agent.RunCommand(ctx, "journalctl", "-u", serviceName, "-n", fmt.Sprint(lines), "--no-pager")
	if err != nil {
		return []string{}, nil
	}
	logLines := strings.Split(strings.TrimSpace(result.Output), "\n")
	return logLines, nil
}

func (s *AppService) UpdateEnv(ctx context.Context, name string, envVars map[string]string, restart bool) error {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return fmt.Errorf("app not found: %w", err)
	}

	appDir := fmt.Sprintf("/home/%s/apps/%s", app.User, app.Name)

	// Write .env file
	var envLines []string
	for k, v := range envVars {
		envLines = append(envLines, fmt.Sprintf("%s=%s", k, v))
	}
	if err := os.WriteFile(fmt.Sprintf("%s/.env", appDir), []byte(strings.Join(envLines, "\n")+"\n"), 0600); err != nil {
		return fmt.Errorf("failed to write .env: %w", err)
	}

	// Update database
	s.db.Collection(database.ColApps).UpdateOne(ctx, bson.M{"_id": app.ID}, bson.M{
		"$set": bson.M{"env_vars": envVars, "updated_at": time.Now()},
	})

	// Restart if requested
	if restart {
		serviceName := "sp-app-" + name
		agent.ServiceAction(ctx, serviceName, "restart")
	}

	return nil
}

func (s *AppService) Rollback(ctx context.Context, name string, deploymentID string) error {
	app, err := s.GetByName(ctx, name)
	if err != nil {
		return fmt.Errorf("app not found: %w", err)
	}

	appDir := fmt.Sprintf("/home/%s/apps/%s", app.User, app.Name)

	// Get the target deployment
	if deploymentID != "" {
		oid, err := primitive.ObjectIDFromHex(deploymentID)
		if err == nil {
			var deployment models.AppDeployment
			if err := s.db.Collection(database.ColDeployments).FindOne(ctx, bson.M{"_id": oid}).Decode(&deployment); err == nil {
				if deployment.GitCommit != "" {
					agent.RunCommand(ctx, "git", "-C", appDir, "checkout", deployment.GitCommit)
				}
			}
		}
	}

	// Rebuild and restart
	if app.BuildCmd != "" {
		agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("cd %s && %s", appDir, app.BuildCmd))
	}
	serviceName := "sp-app-" + name
	agent.ServiceAction(ctx, serviceName, "restart")

	return nil
}

func (s *AppService) ListByUser(ctx context.Context, userID string, page, limit int) ([]models.App, int64, error) {
	col := s.db.Collection(database.ColApps)
	filter := bson.M{"user": userID}

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

	var apps []models.App
	if err := cursor.All(ctx, &apps); err != nil {
		return nil, 0, err
	}
	if apps == nil {
		apps = []models.App{}
	}
	return apps, total, nil
}
