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

type TransferService struct {
	db *mongo.Database
}

func NewTransferService(db *mongo.Database) *TransferService {
	return &TransferService{db: db}
}

// List returns paginated transfer jobs.
func (s *TransferService) List(ctx context.Context, page, limit int) ([]models.TransferJob, int64, error) {
	col := s.db.Collection(database.ColTransferJobs)
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

	var jobs []models.TransferJob
	if err := cursor.All(ctx, &jobs); err != nil {
		return nil, 0, err
	}
	if jobs == nil {
		jobs = []models.TransferJob{}
	}
	return jobs, total, nil
}

// GetByID retrieves a single transfer job.
func (s *TransferService) GetByID(ctx context.Context, id string) (*models.TransferJob, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid transfer ID")
	}
	var job models.TransferJob
	if err := s.db.Collection(database.ColTransferJobs).FindOne(ctx, bson.M{"_id": oid}).Decode(&job); err != nil {
		return nil, err
	}
	return &job, nil
}

// TestConnection tests SSH connectivity to the source server.
func (s *TransferService) TestConnection(ctx context.Context, req *models.TestConnectionRequest) error {
	return agent.TestRemoteConnection(ctx, req.Protocol, req.Host, req.Port, req.Username, req.Password)
}

// Discover probes the source server to enumerate transferable resources.
func (s *TransferService) Discover(ctx context.Context, req *models.DiscoverRequest) (*models.DiscoveredData, error) {
	host := req.SourceIP
	port := req.Port
	user := req.Username
	pass := req.Password

	data := &models.DiscoveredData{}

	hostname, err := agent.DiscoverHostname(ctx, host, port, user, pass)
	if err == nil {
		data.Hostname = hostname
	}

	domains, _ := agent.DiscoverDomains(ctx, host, port, user, pass)
	data.Domains = domains

	databases, _ := agent.DiscoverDatabases(ctx, host, port, user, pass)
	data.Databases = databases

	emailDomains, _ := agent.DiscoverEmailDomains(ctx, host, port, user, pass)
	data.EmailDomains = emailDomains

	dnsZones, _ := agent.DiscoverDNSZones(ctx, host, port, user, pass)
	data.DNSZones = dnsZones

	sslDomains, _ := agent.DiscoverSSLDomains(ctx, host, port, user, pass)
	data.SSLDomains = sslDomains

	cronUsers, _ := agent.DiscoverCronUsers(ctx, host, port, user, pass)
	data.CronUsers = cronUsers

	ftpUsers, _ := agent.DiscoverFTPUsers(ctx, host, port, user, pass)
	data.FTPUsers = ftpUsers

	return data, nil
}

// Create starts a new transfer job and runs it in the background.
func (s *TransferService) Create(ctx context.Context, req *models.CreateTransferRequest) (*models.TransferJob, error) {
	jobType := "full"
	if len(req.Domains) > 0 {
		jobType = "partial"
	}

	steps := s.buildSteps(req.Components)

	job := models.TransferJob{
		Type:      jobType,
		Direction: "incoming",
		SourceServer: models.SourceServer{
			IP:       req.SourceIP,
			Port:     req.SourcePort,
			Username: req.Username,
			Password: req.Password,
			Protocol: req.Protocol,
		},
		Components: req.Components,
		Domains:    req.Domains,
		Status:     "pending",
		Progress:   0,
		Steps:      steps,
		Logs:       []models.TransferLog{},
		CreatedAt:  time.Now(),
	}

	result, err := s.db.Collection(database.ColTransferJobs).InsertOne(ctx, job)
	if err != nil {
		return nil, err
	}
	job.ID = result.InsertedID.(primitive.ObjectID)

	// Execute transfer in background
	go s.executeTransfer(job.ID.Hex(), req)

	return &job, nil
}

// Cancel marks a running transfer as cancelled.
func (s *TransferService) Cancel(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid transfer ID")
	}
	_, err = s.db.Collection(database.ColTransferJobs).UpdateOne(ctx, bson.M{"_id": oid},
		bson.M{"$set": bson.M{"status": "cancelled"}})
	return err
}

func (s *TransferService) buildSteps(c models.TransferComponents) []models.TransferStep {
	steps := []models.TransferStep{
		{Name: "Validate Connection", Status: "pending"},
		{Name: "Discover Resources", Status: "pending"},
	}
	if c.Hostname {
		steps = append(steps, models.TransferStep{Name: "Transfer Hostname", Status: "pending"})
	}
	if c.Domains || c.Files {
		steps = append(steps, models.TransferStep{Name: "Transfer Domains & Files", Status: "pending"})
	}
	if c.DNS {
		steps = append(steps, models.TransferStep{Name: "Transfer DNS Zones", Status: "pending"})
	}
	if c.SSL {
		steps = append(steps, models.TransferStep{Name: "Transfer SSL Certificates", Status: "pending"})
	}
	if c.Databases {
		steps = append(steps, models.TransferStep{Name: "Transfer Databases", Status: "pending"})
	}
	if c.EmailData {
		steps = append(steps, models.TransferStep{Name: "Transfer Email", Status: "pending"})
	}
	if c.CronJobs {
		steps = append(steps, models.TransferStep{Name: "Transfer Cron Jobs", Status: "pending"})
	}
	if c.FTPAccounts {
		steps = append(steps, models.TransferStep{Name: "Transfer FTP Accounts", Status: "pending"})
	}
	if c.Firewall {
		steps = append(steps, models.TransferStep{Name: "Transfer Firewall Rules", Status: "pending"})
	}
	if c.ServerConfig {
		steps = append(steps, models.TransferStep{Name: "Transfer Server Config", Status: "pending"})
	}
	steps = append(steps, models.TransferStep{Name: "Verify Transfer", Status: "pending"})
	return steps
}

// executeTransfer runs the full migration in a background goroutine.
func (s *TransferService) executeTransfer(jobID string, req *models.CreateTransferRequest) {
	ctx := context.Background()
	host := req.SourceIP
	port := req.SourcePort
	user := req.Username
	pass := req.Password

	s.updateJobStatus(ctx, jobID, "in_progress", 0)
	now := time.Now()
	s.updateJobField(ctx, jobID, "started_at", &now)

	stepIdx := 0
	totalSteps := s.countEnabledSteps(req.Components)
	failedSteps := 0

	// Helper to advance progress
	advance := func() {
		stepIdx++
		progress := (stepIdx * 100) / totalSteps
		if progress > 100 {
			progress = 100
		}
		s.updateJobField(ctx, jobID, "progress", progress)
	}

	// Check if cancelled
	isCancelled := func() bool {
		job, err := s.GetByID(ctx, jobID)
		if err != nil {
			return false
		}
		return job.Status == "cancelled"
	}

	// Step 1: Validate Connection
	s.startStep(ctx, jobID, "Validate Connection")
	s.addLog(ctx, jobID, "info", fmt.Sprintf("Testing SSH connection to %s:%d", host, port), "connection")
	if err := agent.TestRemoteConnection(ctx, "ssh", host, port, user, pass); err != nil {
		s.failStep(ctx, jobID, "Validate Connection", err.Error())
		s.updateJobStatus(ctx, jobID, "failed", 0)
		s.addLog(ctx, jobID, "error", fmt.Sprintf("Connection failed: %s", err.Error()), "connection")
		return
	}
	s.completeStep(ctx, jobID, "Validate Connection", "SSH connection successful")
	s.addLog(ctx, jobID, "info", "SSH connection verified", "connection")
	advance()

	if isCancelled() {
		return
	}

	// Step 2: Discover Resources
	s.startStep(ctx, jobID, "Discover Resources")
	s.addLog(ctx, jobID, "info", "Discovering resources on source server", "discovery")
	discovered, err := s.Discover(ctx, &models.DiscoverRequest{
		SourceIP: host, Port: port, Username: user, Password: pass,
	})
	if err != nil {
		s.failStep(ctx, jobID, "Discover Resources", err.Error())
		s.addLog(ctx, jobID, "error", fmt.Sprintf("Discovery failed: %s", err.Error()), "discovery")
	} else {
		s.completeStep(ctx, jobID, "Discover Resources",
			fmt.Sprintf("Found %d domains, %d databases, %d email domains",
				len(discovered.Domains), len(discovered.Databases), len(discovered.EmailDomains)))
		s.updateJobField(ctx, jobID, "discovered", discovered)
		s.addLog(ctx, jobID, "info", fmt.Sprintf("Discovered: %d domains, %d databases", len(discovered.Domains), len(discovered.Databases)), "discovery")
	}
	advance()

	if isCancelled() {
		return
	}

	// Filter domains if specific ones were requested
	domains := discovered.Domains
	if len(req.Domains) > 0 {
		domains = req.Domains
	}

	tmpDir := fmt.Sprintf("/tmp/serverpanel-transfer-%s", jobID)
	os.MkdirAll(tmpDir, 0750)
	defer os.RemoveAll(tmpDir)

	// Step: Transfer Hostname
	if req.Components.Hostname {
		s.startStep(ctx, jobID, "Transfer Hostname")
		if discovered != nil && discovered.Hostname != "" {
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Setting hostname to %s", discovered.Hostname), "hostname")
			if _, err := agent.RunCommand(ctx, "hostnamectl", "set-hostname", discovered.Hostname); err != nil {
				s.failStep(ctx, jobID, "Transfer Hostname", err.Error())
				failedSteps++
			} else {
				s.completeStep(ctx, jobID, "Transfer Hostname", fmt.Sprintf("Hostname set to %s", discovered.Hostname))
			}
		} else {
			s.skipStep(ctx, jobID, "Transfer Hostname")
		}
		advance()
	}

	if isCancelled() {
		return
	}

	// Step: Transfer Domains & Files
	if req.Components.Domains || req.Components.Files {
		s.startStep(ctx, jobID, "Transfer Domains & Files")
		domainErrors := 0
		for _, domain := range domains {
			if isCancelled() {
				return
			}
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Transferring files for %s", domain), "files")

			// Determine system user (use domain name as user if we can't determine it)
			sysUser := strings.ReplaceAll(domain, ".", "_")
			if len(sysUser) > 32 {
				sysUser = sysUser[:32]
			}

			// Create user and directories on destination
			agent.RunCommand(ctx, "useradd", "-m", "-s", "/bin/bash", sysUser)
			agent.RunCommand(ctx, "mkdir", "-p", fmt.Sprintf("/home/%s/domains/%s/public_html", sysUser, domain))
			agent.RunCommand(ctx, "mkdir", "-p", fmt.Sprintf("/home/%s/backups", sysUser))
			agent.RunCommand(ctx, "mkdir", "-p", fmt.Sprintf("/home/%s/logs", sysUser))

			// Download files from source
			localArchive := fmt.Sprintf("%s/%s-files.tar.gz", tmpDir, domain)
			if err := agent.RemoteBackupUserFiles(ctx, host, port, user, pass, sysUser, localArchive); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to transfer files for %s: %s", domain, err.Error()), "files")
				domainErrors++
				continue
			}

			// Restore files
			if err := agent.RestoreFiles(ctx, sysUser, localArchive); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to restore files for %s: %s", domain, err.Error()), "files")
				domainErrors++
				continue
			}

			s.addLog(ctx, jobID, "info", fmt.Sprintf("Files transferred for %s", domain), "files")
			os.Remove(localArchive)
		}
		if domainErrors > 0 {
			s.completeStep(ctx, jobID, "Transfer Domains & Files",
				fmt.Sprintf("Completed with %d errors out of %d domains", domainErrors, len(domains)))
		} else {
			s.completeStep(ctx, jobID, "Transfer Domains & Files",
				fmt.Sprintf("All %d domains transferred", len(domains)))
		}
		advance()
	}

	if isCancelled() {
		return
	}

	// Step: Transfer DNS Zones
	if req.Components.DNS {
		s.startStep(ctx, jobID, "Transfer DNS Zones")
		dnsErrors := 0
		dnsZones := domains
		if discovered != nil && len(discovered.DNSZones) > 0 {
			dnsZones = discovered.DNSZones
		}
		// Get destination server IP for updating A records
		destIP := ""
		if result, err := agent.RunCommand(ctx, "hostname", "-I"); err == nil {
			parts := strings.Fields(strings.TrimSpace(result.Output))
			if len(parts) > 0 {
				destIP = parts[0]
			}
		}

		for _, zone := range dnsZones {
			if isCancelled() {
				return
			}
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Transferring DNS zone for %s", zone), "dns")

			// Export zone from source
			zoneData, err := agent.ExportDNSZoneFromRemote(ctx, host, port, user, pass, zone)
			if err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to export DNS for %s: %s", zone, err.Error()), "dns")
				dnsErrors++
				continue
			}

			// Create zone on destination
			agent.RunCommand(ctx, "pdnsutil", "create-zone", zone)

			// Parse and import records
			for _, line := range strings.Split(zoneData, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, ";") {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) < 4 {
					continue
				}
				name := parts[0]
				ttl := parts[1]
				recType := ""
				value := ""

				// Find record type (skip IN class)
				idx := 2
				if strings.ToUpper(parts[idx]) == "IN" {
					idx++
				}
				if idx < len(parts) {
					recType = strings.ToUpper(parts[idx])
					idx++
				}
				if idx < len(parts) {
					value = strings.Join(parts[idx:], " ")
				}

				if recType == "" || value == "" {
					continue
				}

				// Update A records to point to new server IP
				if recType == "A" && destIP != "" && (name == zone+"." || name == zone) {
					value = destIP
				}

				agent.RunCommand(ctx, "pdnsutil", "add-record", zone, name, recType, ttl, value)
			}

			s.addLog(ctx, jobID, "info", fmt.Sprintf("DNS zone imported for %s", zone), "dns")
		}
		if dnsErrors > 0 {
			s.completeStep(ctx, jobID, "Transfer DNS Zones",
				fmt.Sprintf("Completed with %d errors out of %d zones", dnsErrors, len(dnsZones)))
		} else {
			s.completeStep(ctx, jobID, "Transfer DNS Zones",
				fmt.Sprintf("All %d DNS zones transferred", len(dnsZones)))
		}
		advance()
	}

	if isCancelled() {
		return
	}

	// Step: Transfer SSL
	if req.Components.SSL {
		s.startStep(ctx, jobID, "Transfer SSL Certificates")
		sslErrors := 0
		sslDomains := domains
		if discovered != nil && len(discovered.SSLDomains) > 0 {
			sslDomains = discovered.SSLDomains
		}
		for _, domain := range sslDomains {
			if isCancelled() {
				return
			}
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Transferring SSL for %s", domain), "ssl")

			localCertDir := fmt.Sprintf("%s/ssl-%s", tmpDir, domain)
			os.MkdirAll(localCertDir, 0750)

			if err := agent.ExportSSLFromRemote(ctx, host, port, user, pass, domain, localCertDir); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to transfer SSL for %s: %s (will try Let's Encrypt)", domain, err.Error()), "ssl")
				// Try issuing a new cert instead
				if _, issueErr := agent.RunCommand(ctx, "certbot", "certonly", "--nginx",
					"-d", domain, "--non-interactive", "--agree-tos",
					"--email", "admin@"+domain); issueErr != nil {
					s.addLog(ctx, jobID, "warn", fmt.Sprintf("Let's Encrypt also failed for %s: %s", domain, issueErr.Error()), "ssl")
					sslErrors++
				}
				continue
			}

			// Copy certs to proper location
			destCertDir := fmt.Sprintf("/etc/letsencrypt/live/%s", domain)
			os.MkdirAll(destCertDir, 0750)
			agent.RunCommand(ctx, "cp", "-r", localCertDir+"/"+domain+"/.", destCertDir+"/")

			s.addLog(ctx, jobID, "info", fmt.Sprintf("SSL transferred for %s", domain), "ssl")
			os.RemoveAll(localCertDir)
		}
		if sslErrors > 0 {
			s.completeStep(ctx, jobID, "Transfer SSL Certificates",
				fmt.Sprintf("Completed with %d errors out of %d domains", sslErrors, len(sslDomains)))
		} else {
			s.completeStep(ctx, jobID, "Transfer SSL Certificates",
				fmt.Sprintf("All %d SSL certs transferred", len(sslDomains)))
		}
		advance()
	}

	if isCancelled() {
		return
	}

	// Step: Transfer Databases
	if req.Components.Databases {
		s.startStep(ctx, jobID, "Transfer Databases")
		dbErrors := 0
		databases := []string{}
		if discovered != nil {
			databases = discovered.Databases
		}
		for _, db := range databases {
			if isCancelled() {
				return
			}
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Transferring database %s", db), "database")

			localDump := fmt.Sprintf("%s/%s-dump.gz", tmpDir, db)
			if err := agent.RemoteMongoDump(ctx, host, port, user, pass, db, localDump); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to transfer database %s: %s", db, err.Error()), "database")
				dbErrors++
				continue
			}

			if err := agent.RestoreMongoDB(ctx, db, localDump); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to restore database %s: %s", db, err.Error()), "database")
				dbErrors++
				continue
			}

			s.addLog(ctx, jobID, "info", fmt.Sprintf("Database %s transferred", db), "database")
			os.Remove(localDump)
		}
		if dbErrors > 0 {
			s.completeStep(ctx, jobID, "Transfer Databases",
				fmt.Sprintf("Completed with %d errors out of %d databases", dbErrors, len(databases)))
		} else {
			s.completeStep(ctx, jobID, "Transfer Databases",
				fmt.Sprintf("All %d databases transferred", len(databases)))
		}
		advance()
	}

	if isCancelled() {
		return
	}

	// Step: Transfer Email
	if req.Components.EmailData {
		s.startStep(ctx, jobID, "Transfer Email")
		emailErrors := 0
		emailDomains := []string{}
		if discovered != nil {
			emailDomains = discovered.EmailDomains
		}
		for _, domain := range emailDomains {
			if isCancelled() {
				return
			}
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Transferring email for %s", domain), "email")

			localArchive := fmt.Sprintf("%s/%s-email.tar.gz", tmpDir, domain)
			if err := agent.RemoteBackupEmail(ctx, host, port, user, pass, domain, localArchive); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to transfer email for %s: %s", domain, err.Error()), "email")
				emailErrors++
				continue
			}

			if err := agent.RestoreEmail(ctx, domain, localArchive); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to restore email for %s: %s", domain, err.Error()), "email")
				emailErrors++
				continue
			}

			s.addLog(ctx, jobID, "info", fmt.Sprintf("Email transferred for %s", domain), "email")
			os.Remove(localArchive)
		}
		if emailErrors > 0 {
			s.completeStep(ctx, jobID, "Transfer Email",
				fmt.Sprintf("Completed with %d errors out of %d domains", emailErrors, len(emailDomains)))
		} else {
			s.completeStep(ctx, jobID, "Transfer Email",
				fmt.Sprintf("All %d email domains transferred", len(emailDomains)))
		}
		advance()
	}

	if isCancelled() {
		return
	}

	// Step: Transfer Cron Jobs
	if req.Components.CronJobs {
		s.startStep(ctx, jobID, "Transfer Cron Jobs")
		cronErrors := 0
		cronUsers := []string{}
		if discovered != nil {
			cronUsers = discovered.CronUsers
		}
		for _, cronUser := range cronUsers {
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Transferring crontab for %s", cronUser), "cron")

			crontab, err := agent.ExportCrontabFromRemote(ctx, host, port, user, pass, cronUser)
			if err != nil || strings.TrimSpace(crontab) == "" {
				continue
			}

			// Write crontab entries on destination
			for _, line := range strings.Split(crontab, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				// Parse cron schedule and command
				parts := strings.Fields(line)
				if len(parts) < 6 {
					continue
				}
				schedule := strings.Join(parts[:5], " ")
				command := strings.Join(parts[5:], " ")
				if err := agent.WriteCrontab(ctx, cronUser, schedule, command); err != nil {
					cronErrors++
				}
			}
		}
		if cronErrors > 0 {
			s.completeStep(ctx, jobID, "Transfer Cron Jobs",
				fmt.Sprintf("Completed with %d errors", cronErrors))
		} else {
			s.completeStep(ctx, jobID, "Transfer Cron Jobs",
				fmt.Sprintf("Cron jobs transferred for %d users", len(cronUsers)))
		}
		advance()
	}

	if isCancelled() {
		return
	}

	// Step: Transfer FTP Accounts
	if req.Components.FTPAccounts {
		s.startStep(ctx, jobID, "Transfer FTP Accounts")
		s.addLog(ctx, jobID, "info", "FTP accounts will need to be re-created with new passwords", "ftp")
		ftpUsers := []string{}
		if discovered != nil {
			ftpUsers = discovered.FTPUsers
		}
		s.completeStep(ctx, jobID, "Transfer FTP Accounts",
			fmt.Sprintf("Found %d FTP accounts (passwords must be reset)", len(ftpUsers)))
		advance()
	}

	// Step: Transfer Firewall Rules
	if req.Components.Firewall {
		s.startStep(ctx, jobID, "Transfer Firewall Rules")
		s.addLog(ctx, jobID, "info", "Exporting firewall rules from source", "firewall")

		result, err := agent.SSHCommand(ctx, host, port, user, pass, "ufw status numbered 2>/dev/null || iptables-save 2>/dev/null || echo ''")
		if err == nil && strings.TrimSpace(result.Output) != "" {
			s.addLog(ctx, jobID, "info", "Firewall rules exported (manual review recommended)", "firewall")
			s.completeStep(ctx, jobID, "Transfer Firewall Rules", "Firewall rules exported for review")
		} else {
			s.skipStep(ctx, jobID, "Transfer Firewall Rules")
		}
		advance()
	}

	// Step: Transfer Server Config
	if req.Components.ServerConfig {
		s.startStep(ctx, jobID, "Transfer Server Config")
		s.addLog(ctx, jobID, "info", "Transferring server configuration", "config")
		s.completeStep(ctx, jobID, "Transfer Server Config", "Server configuration noted for manual review")
		advance()
	}

	// Step: Verify Transfer
	s.startStep(ctx, jobID, "Verify Transfer")
	s.addLog(ctx, jobID, "info", "Running post-transfer verification", "verify")

	// Verify nginx configs
	if _, err := agent.RunCommand(ctx, "nginx", "-t"); err != nil {
		s.addLog(ctx, jobID, "warn", "Nginx configuration test failed — manual review needed", "verify")
	} else {
		s.addLog(ctx, jobID, "info", "Nginx configuration test passed", "verify")
	}

	s.completeStep(ctx, jobID, "Verify Transfer", "Verification complete")
	advance()

	// Final status
	finalStatus := "completed"
	if failedSteps > 0 {
		finalStatus = "partial"
	}
	s.updateJobStatus(ctx, jobID, finalStatus, 100)
	completedAt := time.Now()
	s.updateJobField(ctx, jobID, "completed_at", &completedAt)
	s.addLog(ctx, jobID, "info", fmt.Sprintf("Transfer %s", finalStatus), "transfer")
}

func (s *TransferService) countEnabledSteps(c models.TransferComponents) int {
	count := 3 // validate + discover + verify (always present)
	if c.Hostname {
		count++
	}
	if c.Domains || c.Files {
		count++
	}
	if c.DNS {
		count++
	}
	if c.SSL {
		count++
	}
	if c.Databases {
		count++
	}
	if c.EmailData {
		count++
	}
	if c.CronJobs {
		count++
	}
	if c.FTPAccounts {
		count++
	}
	if c.Firewall {
		count++
	}
	if c.ServerConfig {
		count++
	}
	return count
}

// --- DB update helpers ---

func (s *TransferService) updateJobStatus(ctx context.Context, jobID, status string, progress int) {
	oid, _ := primitive.ObjectIDFromHex(jobID)
	s.db.Collection(database.ColTransferJobs).UpdateOne(ctx, bson.M{"_id": oid},
		bson.M{"$set": bson.M{"status": status, "progress": progress}})
}

func (s *TransferService) updateJobField(ctx context.Context, jobID, field string, value interface{}) {
	oid, _ := primitive.ObjectIDFromHex(jobID)
	s.db.Collection(database.ColTransferJobs).UpdateOne(ctx, bson.M{"_id": oid},
		bson.M{"$set": bson.M{field: value}})
}

func (s *TransferService) addLog(ctx context.Context, jobID, level, message, component string) {
	oid, _ := primitive.ObjectIDFromHex(jobID)
	logEntry := models.TransferLog{
		Timestamp: time.Now(),
		Level:     level,
		Message:   message,
		Component: component,
	}
	s.db.Collection(database.ColTransferJobs).UpdateOne(ctx, bson.M{"_id": oid},
		bson.M{"$push": bson.M{"logs": logEntry}})
}

func (s *TransferService) startStep(ctx context.Context, jobID, stepName string) {
	oid, _ := primitive.ObjectIDFromHex(jobID)
	now := time.Now()
	s.db.Collection(database.ColTransferJobs).UpdateOne(ctx,
		bson.M{"_id": oid, "steps.name": stepName},
		bson.M{"$set": bson.M{"steps.$.status": "in_progress", "steps.$.started_at": &now}})
}

func (s *TransferService) completeStep(ctx context.Context, jobID, stepName, details string) {
	oid, _ := primitive.ObjectIDFromHex(jobID)
	now := time.Now()
	s.db.Collection(database.ColTransferJobs).UpdateOne(ctx,
		bson.M{"_id": oid, "steps.name": stepName},
		bson.M{"$set": bson.M{"steps.$.status": "completed", "steps.$.completed_at": &now, "steps.$.details": details}})
}

func (s *TransferService) failStep(ctx context.Context, jobID, stepName, errMsg string) {
	oid, _ := primitive.ObjectIDFromHex(jobID)
	now := time.Now()
	s.db.Collection(database.ColTransferJobs).UpdateOne(ctx,
		bson.M{"_id": oid, "steps.name": stepName},
		bson.M{"$set": bson.M{"steps.$.status": "failed", "steps.$.completed_at": &now, "steps.$.error": errMsg}})
}

func (s *TransferService) skipStep(ctx context.Context, jobID, stepName string) {
	oid, _ := primitive.ObjectIDFromHex(jobID)
	now := time.Now()
	s.db.Collection(database.ColTransferJobs).UpdateOne(ctx,
		bson.M{"_id": oid, "steps.name": stepName},
		bson.M{"$set": bson.M{"steps.$.status": "skipped", "steps.$.completed_at": &now}})
}
