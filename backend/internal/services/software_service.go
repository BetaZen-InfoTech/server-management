package services

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type SoftwareService struct {
	db        *mongo.Database
	installMu sync.Mutex
}

func NewSoftwareService(db *mongo.Database) *SoftwareService {
	return &SoftwareService{db: db}
}

// packageProbe defines how to detect a software package on the system.
type packageProbe struct {
	ID       string
	Name     string
	Category string
	Icon     string
	Command  string
	Args     []string
	Parser   func(output string) string
}

// ListInstalled detects installed software by running version commands on the host.
func (s *SoftwareService) ListInstalled(ctx context.Context) ([]map[string]interface{}, error) {
	probes := []packageProbe{
		{ID: "nginx", Name: "Nginx", Category: "Web Server", Icon: "nginx",
			Command: "nginx", Args: []string{"-v"},
			Parser: func(out string) string {
				if i := strings.Index(out, "nginx/"); i >= 0 {
					v := out[i+6:]
					if nl := strings.IndexByte(v, '\n'); nl > 0 {
						v = v[:nl]
					}
					return strings.TrimSpace(v)
				}
				return ""
			},
		},
		{ID: "php8.3", Name: "PHP 8.3", Category: "Runtime", Icon: "php",
			Command: "php8.3", Args: []string{"-v"}, Parser: parseGenericVersion},
		{ID: "php8.2", Name: "PHP 8.2", Category: "Runtime", Icon: "php",
			Command: "php8.2", Args: []string{"-v"}, Parser: parseGenericVersion},
		{ID: "php8.1", Name: "PHP 8.1", Category: "Runtime", Icon: "php",
			Command: "php8.1", Args: []string{"-v"}, Parser: parseGenericVersion},
		{ID: "mongodb", Name: "MongoDB", Category: "Database", Icon: "mongodb",
			Command: "mongod", Args: []string{"--version"},
			Parser: func(out string) string {
				if i := strings.Index(out, "db version v"); i >= 0 {
					v := out[i+12:]
					if nl := strings.IndexByte(v, '\n'); nl > 0 {
						v = v[:nl]
					}
					return strings.TrimSpace(v)
				}
				return parseGenericVersion(out)
			},
		},
		{ID: "nodejs", Name: "Node.js", Category: "Runtime", Icon: "nodejs",
			Command: "node", Args: []string{"-v"},
			Parser: func(out string) string {
				return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "v"))
			},
		},
		{ID: "redis", Name: "Redis", Category: "Cache", Icon: "cache",
			Command: "redis-server", Args: []string{"--version"},
			Parser: func(out string) string {
				if i := strings.Index(out, "v="); i >= 0 {
					v := out[i+2:]
					if sp := strings.IndexByte(v, ' '); sp > 0 {
						v = v[:sp]
					}
					return strings.TrimSpace(v)
				}
				return ""
			},
		},
		{ID: "certbot", Name: "Certbot", Category: "SSL", Icon: "ssl",
			Command: "certbot", Args: []string{"--version"},
			Parser: func(out string) string {
				parts := strings.Fields(strings.TrimSpace(out))
				if len(parts) >= 2 {
					return parts[len(parts)-1]
				}
				return strings.TrimSpace(out)
			},
		},
		{ID: "go", Name: "Go", Category: "Runtime", Icon: "go",
			Command: "go", Args: []string{"version"},
			Parser: func(out string) string {
				if i := strings.Index(out, "go1."); i >= 0 {
					v := out[i+2:]
					if sp := strings.IndexByte(v, ' '); sp > 0 {
						v = v[:sp]
					}
					return strings.TrimSpace(v)
				}
				return ""
			},
		},
		{ID: "git", Name: "Git", Category: "Dev Tools", Icon: "dev",
			Command: "git", Args: []string{"--version"},
			Parser: func(out string) string {
				parts := strings.Fields(strings.TrimSpace(out))
				if len(parts) >= 3 {
					return parts[2]
				}
				return ""
			},
		},
		{ID: "docker", Name: "Docker", Category: "Container", Icon: "docker",
			Command: "docker", Args: []string{"--version"},
			Parser: func(out string) string {
				if i := strings.Index(out, "version "); i >= 0 {
					v := out[i+8:]
					if c := strings.IndexByte(v, ','); c > 0 {
						v = v[:c]
					}
					return strings.TrimSpace(v)
				}
				return ""
			},
		},
		{ID: "ufw", Name: "UFW Firewall", Category: "Security", Icon: "firewall",
			Command: "ufw", Args: []string{"version"},
			Parser: func(out string) string {
				parts := strings.Fields(strings.TrimSpace(out))
				if len(parts) >= 2 {
					return parts[1]
				}
				return ""
			},
		},
		{ID: "postfix", Name: "Postfix", Category: "Mail Server", Icon: "mail",
			Command: "postconf", Args: []string{"-d", "mail_version"},
			Parser: func(out string) string {
				if i := strings.Index(out, "= "); i >= 0 {
					return strings.TrimSpace(out[i+2:])
				}
				return ""
			},
		},
		{ID: "dovecot", Name: "Dovecot", Category: "Mail Server", Icon: "mail",
			Command: "dovecot", Args: []string{"--version"},
			Parser: func(out string) string {
				parts := strings.Fields(strings.TrimSpace(out))
				if len(parts) >= 1 {
					return parts[0]
				}
				return ""
			},
		},
		{ID: "python3", Name: "Python 3", Category: "Runtime", Icon: "python",
			Command: "python3", Args: []string{"--version"},
			Parser: parseGenericVersion,
		},
		{ID: "mysql", Name: "MySQL", Category: "Database", Icon: "mongodb",
			Command: "mysql", Args: []string{"--version"},
			Parser: func(out string) string {
				if i := strings.Index(out, "Distrib "); i >= 0 {
					v := out[i+8:]
					if c := strings.IndexByte(v, ','); c > 0 {
						v = v[:c]
					}
					return strings.TrimSpace(v)
				}
				return parseGenericVersion(out)
			},
		},
	}

	var packages []map[string]interface{}
	for _, p := range probes {
		version := detectVersion(ctx, p)
		if version == "" {
			continue
		}
		packages = append(packages, map[string]interface{}{
			"id":            p.ID,
			"name":          p.Name,
			"version":       version,
			"latestVersion": version,
			"category":      p.Category,
			"icon":          p.Icon,
			"status":        "up-to-date",
		})
	}
	if packages == nil {
		packages = []map[string]interface{}{}
	}
	return packages, nil
}

func detectVersion(ctx context.Context, p packageProbe) string {
	result, err := agent.RunCommand(ctx, p.Command, p.Args...)
	if err != nil {
		// Some tools write version to stderr (e.g. nginx -v)
		if result != nil && result.Error != "" {
			if v := p.Parser(result.Error); v != "" {
				return v
			}
		}
		return ""
	}
	return p.Parser(result.Output)
}

func parseGenericVersion(out string) string {
	for _, word := range strings.Fields(out) {
		if len(word) > 0 && word[0] >= '0' && word[0] <= '9' && strings.Contains(word, ".") {
			return strings.TrimRight(word, ",-()[]")
		}
	}
	return ""
}

// Install installs a software package with the specified version.
func (s *SoftwareService) Install(ctx context.Context, software string, version string) error {
	pkg := software
	if version != "" {
		pkg = software + "=" + version
	}
	return agent.InstallPackages(ctx, pkg)
}

// Uninstall removes a software package from the server.
func (s *SoftwareService) Uninstall(ctx context.Context, software string) error {
	_, err := agent.RunCommand(ctx, "apt-get", "remove", "-y", software)
	return err
}

// CheckUpdates returns a list of packages with available updates.
func (s *SoftwareService) CheckUpdates(ctx context.Context) ([]map[string]interface{}, error) {
	result, err := agent.RunCommand(ctx, "apt", "list", "--upgradable")
	if err != nil {
		return []map[string]interface{}{}, nil
	}
	var updates []map[string]interface{}
	for _, line := range strings.Split(result.Output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Listing") {
			continue
		}
		parts := strings.SplitN(line, "/", 2)
		if len(parts) < 2 {
			continue
		}
		name := parts[0]
		rest := strings.Fields(parts[1])
		newVersion := ""
		if len(rest) >= 2 {
			newVersion = rest[1]
		}
		updates = append(updates, map[string]interface{}{
			"name":    name,
			"version": newVersion,
		})
	}
	if updates == nil {
		updates = []map[string]interface{}{}
	}
	return updates, nil
}

// InstallEmailServer initiates async email server installation.
func (s *SoftwareService) InstallEmailServer(ctx context.Context, config map[string]interface{}) (map[string]interface{}, error) {
	s.installMu.Lock()
	defer s.installMu.Unlock()

	// Check if already installing
	existing := s.getEmailConfig(ctx)
	if existing != nil && existing.Status == "installing" {
		return nil, fmt.Errorf("email server installation already in progress")
	}

	// Parse request
	req := s.parseInstallRequest(config)
	if req.Hostname == "" || req.Domain == "" {
		return nil, fmt.Errorf("hostname and domain are required")
	}

	now := time.Now()

	// Create/update EmailServerConfig (upsert singleton)
	emailConfig := &models.EmailServerConfig{
		Hostname:            req.Hostname,
		Domain:              req.Domain,
		PostfixEnabled:      true,
		DovecotEnabled:      true,
		SpamAssassinEnabled: req.SpamAssassinEnabled,
		OpenDKIMEnabled:     req.OpenDKIMEnabled,
		ClamAVEnabled:       req.ClamAVEnabled,
		Status:              "installing",
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	configID := s.upsertEmailConfig(ctx, emailConfig)

	// Build installation steps
	steps := s.buildInstallSteps(req)

	// Create EmailInstallation record
	installation := models.EmailInstallation{
		ConfigID:    configID,
		Status:      "pending",
		Steps:       steps,
		CurrentStep: 0,
		TotalSteps:  len(steps),
		CreatedAt:   now,
	}
	installResult, err := s.db.Collection(database.ColEmailInstallations).InsertOne(ctx, installation)
	if err != nil {
		return nil, fmt.Errorf("failed to create installation record: %w", err)
	}
	installID := installResult.InsertedID.(primitive.ObjectID)

	// Run installation in background
	go s.runInstallation(configID, installID, req)

	return map[string]interface{}{
		"installation_id": installID.Hex(),
		"config_id":       configID.Hex(),
		"status":          "installing",
		"total_steps":     len(steps),
		"message":         "Email server installation started",
	}, nil
}

// EmailServerStatus returns the current status of the email server components.
func (s *SoftwareService) EmailServerStatus(ctx context.Context) (map[string]interface{}, error) {
	config := s.getEmailConfig(ctx)
	if config == nil {
		return map[string]interface{}{
			"installed":  false,
			"status":     "not_installed",
			"components": []interface{}{},
			"config":     nil,
		}, nil
	}

	// Check live service status
	componentChecks := []struct {
		name    string
		service string
		pkg     string
		enabled bool
	}{
		{"Postfix", "postfix", "postfix", config.PostfixEnabled},
		{"Dovecot", "dovecot", "dovecot-core", config.DovecotEnabled},
		{"SpamAssassin", "spamassassin", "spamassassin", config.SpamAssassinEnabled},
		{"OpenDKIM", "opendkim", "opendkim", config.OpenDKIMEnabled},
		{"ClamAV", "clamav-daemon", "clamav-daemon", config.ClamAVEnabled},
	}

	components := make([]models.EmailComponentStatus, 0, len(componentChecks))
	for _, check := range componentChecks {
		installed, running, version := agent.CheckEmailServiceStatus(ctx, check.service, check.pkg)
		components = append(components, models.EmailComponentStatus{
			Name:      check.name,
			Installed: installed,
			Running:   running,
			Enabled:   check.enabled,
			Version:   version,
		})
	}

	// Get latest installation record
	var installation *models.EmailInstallation
	err := s.db.Collection(database.ColEmailInstallations).FindOne(ctx,
		bson.M{"config_id": config.ID},
		options.FindOne().SetSort(bson.M{"created_at": -1}),
	).Decode(&installation)
	if err != nil {
		installation = nil
	}

	return map[string]interface{}{
		"installed":    config.Status == "installed",
		"status":       config.Status,
		"components":   components,
		"config":       config,
		"installation": installation,
	}, nil
}

// GetEmailInstallation returns a specific installation by ID.
func (s *SoftwareService) GetEmailInstallation(ctx context.Context, id string) (*models.EmailInstallation, error) {
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid installation ID")
	}

	var installation models.EmailInstallation
	err = s.db.Collection(database.ColEmailInstallations).FindOne(ctx, bson.M{"_id": objID}).Decode(&installation)
	if err != nil {
		return nil, fmt.Errorf("installation not found")
	}

	return &installation, nil
}

// UpdateEmailSettings updates the email server component settings.
func (s *SoftwareService) UpdateEmailSettings(ctx context.Context, req models.UpdateEmailSettingsRequest) (map[string]interface{}, error) {
	config := s.getEmailConfig(ctx)
	if config == nil {
		return nil, fmt.Errorf("email server not installed")
	}

	update := bson.M{"updated_at": time.Now()}

	if req.Hostname != "" {
		update["hostname"] = req.Hostname
	}
	if req.Domain != "" {
		update["domain"] = req.Domain
	}
	if req.SpamAssassinEnabled != nil {
		update["spamassassin_enabled"] = *req.SpamAssassinEnabled
	}
	if req.OpenDKIMEnabled != nil {
		update["opendkim_enabled"] = *req.OpenDKIMEnabled
	}
	if req.ClamAVEnabled != nil {
		update["clamav_enabled"] = *req.ClamAVEnabled
	}

	_, err := s.db.Collection(database.ColEmailServerConfigs).UpdateByID(ctx, config.ID, bson.M{"$set": update})
	if err != nil {
		return nil, fmt.Errorf("failed to update settings: %w", err)
	}

	return map[string]interface{}{
		"message": "Email server settings updated",
	}, nil
}

// runInstallation executes each installation step sequentially in the background.
// Broadcasts real-time terminal output to all connected WebSocket clients.
func (s *SoftwareService) runInstallation(configID, installID primitive.ObjectID, req models.InstallEmailRequest) {
	ctx := context.Background()
	now := time.Now()
	hub := GetInstallHub()

	// Mark installation as running
	s.db.Collection(database.ColEmailInstallations).UpdateByID(ctx, installID, bson.M{
		"$set": bson.M{"status": "running", "started_at": now},
	})

	type stepExecutor struct {
		name        string
		description string
		execute     func(context.Context) (string, error)
	}

	// Build step executors
	executors := []stepExecutor{
		{"update_packages", "Update package lists", func(ctx context.Context) (string, error) {
			return agent.UpdatePackageLists(ctx)
		}},
		{"install_postfix", "Install Postfix SMTP server", func(ctx context.Context) (string, error) {
			return agent.InstallPostfix(ctx, req.Hostname, req.Domain)
		}},
		{"install_dovecot", "Install Dovecot IMAP/POP3 server", func(ctx context.Context) (string, error) {
			return agent.InstallDovecot(ctx, req.Domain)
		}},
		{"configure_postfix", "Configure Postfix submission and SMTPS ports", func(ctx context.Context) (string, error) {
			return agent.ConfigurePostfixMasterCf(ctx)
		}},
		{"configure_dovecot", "Configure Dovecot virtual mailboxes and authentication", func(ctx context.Context) (string, error) {
			return agent.ConfigureDovecot(ctx, req.Domain)
		}},
	}

	if req.SpamAssassinEnabled {
		executors = append(executors, stepExecutor{"install_spamassassin", "Install SpamAssassin spam filter", func(ctx context.Context) (string, error) {
			return agent.InstallSpamAssassin(ctx)
		}})
	}
	if req.OpenDKIMEnabled {
		executors = append(executors, stepExecutor{"install_opendkim", "Install OpenDKIM and generate signing keys", func(ctx context.Context) (string, error) {
			return agent.InstallOpenDKIM(ctx, req.Domain)
		}})
	}
	if req.ClamAVEnabled {
		executors = append(executors, stepExecutor{"install_clamav", "Install ClamAV antivirus", func(ctx context.Context) (string, error) {
			return agent.InstallClamAV(ctx)
		}})
	}

	executors = append(executors,
		stepExecutor{"open_firewall", "Open email firewall ports (25, 465, 587, 993, 995)", func(ctx context.Context) (string, error) {
			return agent.OpenEmailFirewallPorts(ctx)
		}},
		stepExecutor{"start_services", "Enable and start all email services", func(ctx context.Context) (string, error) {
			return agent.EnableEmailServices(ctx, req.SpamAssassinEnabled, req.OpenDKIMEnabled, req.ClamAVEnabled)
		}},
		stepExecutor{"verify_services", "Verify all services are running", func(ctx context.Context) (string, error) {
			return agent.VerifyEmailServices(ctx, req.SpamAssassinEnabled, req.OpenDKIMEnabled, req.ClamAVEnabled)
		}},
	)

	totalSteps := len(executors)

	for i, exec := range executors {
		// Mark step as running and broadcast
		stepNow := time.Now()
		s.updateStepStatus(ctx, installID, i, "running", "", "", &stepNow, nil)
		s.db.Collection(database.ColEmailInstallations).UpdateByID(ctx, installID, bson.M{
			"$set": bson.M{"current_step": i},
		})

		hub.Broadcast(TerminalMessage{
			Type:     "step_start",
			Step:     i,
			StepName: exec.description,
			Output:   fmt.Sprintf(">>> [%d/%d] %s...", i+1, totalSteps, exec.description),
			Total:    totalSteps,
		})

		output, err := exec.execute(ctx)

		if err != nil {
			completedAt := time.Now()
			s.updateStepStatus(ctx, installID, i, "failed", output, err.Error(), nil, &completedAt)
			s.db.Collection(database.ColEmailInstallations).UpdateByID(ctx, installID, bson.M{
				"$set": bson.M{
					"status":        "failed",
					"current_step":  i,
					"error_message": err.Error(),
					"completed_at":  completedAt,
				},
			})
			s.db.Collection(database.ColEmailServerConfigs).UpdateByID(ctx, configID, bson.M{
				"$set": bson.M{"status": "failed", "updated_at": completedAt},
			})

			hub.Broadcast(TerminalMessage{
				Type:     "step_error",
				Step:     i,
				StepName: exec.description,
				Output:   output,
				Error:    err.Error(),
				Total:    totalSteps,
			})
			hub.Broadcast(TerminalMessage{
				Type:   "install_failed",
				Step:   i,
				Output: fmt.Sprintf("Installation failed at step %d: %s", i+1, err.Error()),
				Error:  err.Error(),
				Total:  totalSteps,
			})
			return
		}

		completedAt := time.Now()
		s.updateStepStatus(ctx, installID, i, "completed", output, "", nil, &completedAt)

		hub.Broadcast(TerminalMessage{
			Type:     "step_complete",
			Step:     i,
			StepName: exec.description,
			Output:   output,
			Total:    totalSteps,
		})
	}

	// All steps completed
	completedAt := time.Now()
	s.db.Collection(database.ColEmailInstallations).UpdateByID(ctx, installID, bson.M{
		"$set": bson.M{
			"status":       "completed",
			"current_step": totalSteps,
			"completed_at": completedAt,
		},
	})
	s.db.Collection(database.ColEmailServerConfigs).UpdateByID(ctx, configID, bson.M{
		"$set": bson.M{
			"status":       "installed",
			"installed_at": completedAt,
			"updated_at":   completedAt,
		},
	})

	hub.Broadcast(TerminalMessage{
		Type:   "install_complete",
		Step:   totalSteps,
		Output: "Email server installation completed successfully!",
		Total:  totalSteps,
	})
}

// Helper methods

func (s *SoftwareService) getEmailConfig(ctx context.Context) *models.EmailServerConfig {
	var config models.EmailServerConfig
	err := s.db.Collection(database.ColEmailServerConfigs).FindOne(ctx, bson.M{}).Decode(&config)
	if err != nil {
		return nil
	}
	return &config
}

func (s *SoftwareService) upsertEmailConfig(ctx context.Context, config *models.EmailServerConfig) primitive.ObjectID {
	var existing models.EmailServerConfig
	err := s.db.Collection(database.ColEmailServerConfigs).FindOne(ctx, bson.M{}).Decode(&existing)
	if err == nil {
		// Update existing
		s.db.Collection(database.ColEmailServerConfigs).UpdateByID(ctx, existing.ID, bson.M{
			"$set": bson.M{
				"hostname":              config.Hostname,
				"domain":                config.Domain,
				"postfix_enabled":       config.PostfixEnabled,
				"dovecot_enabled":       config.DovecotEnabled,
				"spamassassin_enabled":  config.SpamAssassinEnabled,
				"opendkim_enabled":      config.OpenDKIMEnabled,
				"clamav_enabled":        config.ClamAVEnabled,
				"status":                config.Status,
				"updated_at":            config.UpdatedAt,
			},
		})
		return existing.ID
	}

	// Insert new
	result, _ := s.db.Collection(database.ColEmailServerConfigs).InsertOne(ctx, config)
	return result.InsertedID.(primitive.ObjectID)
}

func (s *SoftwareService) parseInstallRequest(config map[string]interface{}) models.InstallEmailRequest {
	req := models.InstallEmailRequest{}
	if v, ok := config["hostname"].(string); ok {
		req.Hostname = v
	}
	if v, ok := config["domain"].(string); ok {
		req.Domain = v
	}
	if v, ok := config["spamassassin_enabled"].(bool); ok {
		req.SpamAssassinEnabled = v
	}
	if v, ok := config["opendkim_enabled"].(bool); ok {
		req.OpenDKIMEnabled = v
	}
	if v, ok := config["clamav_enabled"].(bool); ok {
		req.ClamAVEnabled = v
	}
	return req
}

func (s *SoftwareService) buildInstallSteps(req models.InstallEmailRequest) []models.EmailInstallStep {
	steps := []models.EmailInstallStep{
		{Name: "update_packages", Description: "Update package lists", Status: "pending"},
		{Name: "install_postfix", Description: "Install Postfix SMTP server", Status: "pending"},
		{Name: "install_dovecot", Description: "Install Dovecot IMAP/POP3 server", Status: "pending"},
		{Name: "configure_postfix", Description: "Configure Postfix submission and SMTPS ports", Status: "pending"},
		{Name: "configure_dovecot", Description: "Configure Dovecot virtual mailboxes and authentication", Status: "pending"},
	}

	if req.SpamAssassinEnabled {
		steps = append(steps, models.EmailInstallStep{
			Name: "install_spamassassin", Description: "Install SpamAssassin spam filter", Status: "pending",
		})
	}
	if req.OpenDKIMEnabled {
		steps = append(steps, models.EmailInstallStep{
			Name: "install_opendkim", Description: "Install OpenDKIM and generate signing keys", Status: "pending",
		})
	}
	if req.ClamAVEnabled {
		steps = append(steps, models.EmailInstallStep{
			Name: "install_clamav", Description: "Install ClamAV antivirus", Status: "pending",
		})
	}

	steps = append(steps,
		models.EmailInstallStep{Name: "open_firewall", Description: "Open email firewall ports (25, 465, 587, 993, 995)", Status: "pending"},
		models.EmailInstallStep{Name: "start_services", Description: "Enable and start all email services", Status: "pending"},
		models.EmailInstallStep{Name: "verify_services", Description: "Verify all services are running", Status: "pending"},
	)

	return steps
}

func (s *SoftwareService) updateStepStatus(ctx context.Context, installID primitive.ObjectID, stepIndex int, status, output, errMsg string, startedAt, completedAt *time.Time) {
	update := bson.M{
		fmt.Sprintf("steps.%d.status", stepIndex): status,
	}
	if output != "" {
		update[fmt.Sprintf("steps.%d.output", stepIndex)] = output
	}
	if errMsg != "" {
		update[fmt.Sprintf("steps.%d.error", stepIndex)] = errMsg
	}
	if startedAt != nil {
		update[fmt.Sprintf("steps.%d.started_at", stepIndex)] = startedAt
	}
	if completedAt != nil {
		update[fmt.Sprintf("steps.%d.completed_at", stepIndex)] = completedAt
	}

	s.db.Collection(database.ColEmailInstallations).UpdateByID(ctx, installID, bson.M{"$set": update})
}

// ──────────────────────────────────────────────────────
// Runtime version management
// ──────────────────────────────────────────────────────

// ListRuntimeVersions returns available and installed versions for all runtimes.
func (s *SoftwareService) ListRuntimeVersions(ctx context.Context, runtime string) ([]map[string]interface{}, error) {
	switch strings.ToLower(runtime) {
	case "php":
		return agent.ListPHPVersions(ctx)
	case "nodejs", "node":
		return agent.ListNodeVersions(ctx)
	case "python":
		return agent.ListPythonVersions(ctx)
	case "ruby":
		return agent.ListRubyVersions(ctx)
	case "go", "golang":
		return agent.ListGoVersions(ctx)
	default:
		return nil, fmt.Errorf("unsupported runtime: %s", runtime)
	}
}

// ListAllRuntimes returns version info for all supported runtimes.
func (s *SoftwareService) ListAllRuntimes(ctx context.Context) (map[string]interface{}, error) {
	runtimes := map[string]interface{}{}

	php, _ := agent.ListPHPVersions(ctx)
	runtimes["php"] = php

	node, _ := agent.ListNodeVersions(ctx)
	runtimes["nodejs"] = node

	python, _ := agent.ListPythonVersions(ctx)
	runtimes["python"] = python

	ruby, _ := agent.ListRubyVersions(ctx)
	runtimes["ruby"] = ruby

	golang, _ := agent.ListGoVersions(ctx)
	runtimes["go"] = golang

	return runtimes, nil
}

// InstallRuntime installs a specific version of a runtime.
func (s *SoftwareService) InstallRuntime(ctx context.Context, runtime, version string) error {
	switch strings.ToLower(runtime) {
	case "php":
		return agent.InstallPHP(ctx, version)
	case "nodejs", "node":
		return agent.InstallNodeJS(ctx, version)
	case "python":
		return agent.InstallPython(ctx, version)
	case "ruby":
		return agent.InstallRuby(ctx, version)
	case "go", "golang":
		return agent.InstallGo(ctx, version)
	default:
		return fmt.Errorf("unsupported runtime: %s", runtime)
	}
}

// UninstallRuntime removes a specific version of a runtime.
func (s *SoftwareService) UninstallRuntime(ctx context.Context, runtime, version string) error {
	switch strings.ToLower(runtime) {
	case "php":
		return agent.UninstallPHP(ctx, version)
	case "nodejs", "node":
		return agent.UninstallNodeJS(ctx)
	case "python":
		return agent.UninstallPython(ctx, version)
	case "ruby":
		return agent.UninstallRuby(ctx)
	case "go", "golang":
		return agent.UninstallGo(ctx)
	default:
		return fmt.Errorf("unsupported runtime: %s", runtime)
	}
}

// ──────────────────────────────────────────────────────
// PHP Extensions
// ──────────────────────────────────────────────────────

// ListPHPExtensions returns installed and available extensions for a PHP version.
func (s *SoftwareService) ListPHPExtensions(ctx context.Context, phpVersion string) ([]map[string]interface{}, error) {
	return agent.ListPHPExtensions(ctx, phpVersion)
}

// InstallPHPExtension installs a PHP extension.
func (s *SoftwareService) InstallPHPExtension(ctx context.Context, phpVersion, extension string) error {
	return agent.InstallPHPExtension(ctx, phpVersion, extension)
}

// UninstallPHPExtension removes a PHP extension.
func (s *SoftwareService) UninstallPHPExtension(ctx context.Context, phpVersion, extension string) error {
	return agent.UninstallPHPExtension(ctx, phpVersion, extension)
}

// ──────────────────────────────────────────────────────
// PHP-FPM Management
// ──────────────────────────────────────────────────────

// ListPHPFPMPools returns FPM pools for a PHP version.
func (s *SoftwareService) ListPHPFPMPools(ctx context.Context, phpVersion string) ([]map[string]interface{}, error) {
	return agent.ListPHPFPMPools(ctx, phpVersion)
}

// GetPHPFPMStatus returns the status of PHP-FPM.
func (s *SoftwareService) GetPHPFPMStatus(ctx context.Context, phpVersion string) (map[string]interface{}, error) {
	return agent.GetPHPFPMStatus(ctx, phpVersion)
}

// RestartPHPFPM restarts PHP-FPM for a specific version.
func (s *SoftwareService) RestartPHPFPM(ctx context.Context, phpVersion string) error {
	return agent.ServiceAction(ctx, fmt.Sprintf("php%s-fpm", phpVersion), "restart")
}

// EnablePHPFPMPool enables a disabled FPM pool.
func (s *SoftwareService) EnablePHPFPMPool(ctx context.Context, phpVersion, poolName string) error {
	return agent.EnablePHPFPMPool(ctx, phpVersion, poolName)
}

// DisablePHPFPMPool disables an FPM pool.
func (s *SoftwareService) DisablePHPFPMPool(ctx context.Context, phpVersion, poolName string) error {
	return agent.DisablePHPFPMPool(ctx, phpVersion, poolName)
}
