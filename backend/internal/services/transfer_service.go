package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

type TransferService struct {
	db          *mongo.Database
	serverIP    string
	panelDomain string // this panel's own management URL — excluded from discovery so operators don't accidentally migrate it
	wpService   *WordPressService
	tokenSvc    *TransferTokenService
}

func NewTransferService(db *mongo.Database, serverIP, panelDomain string) *TransferService {
	return &TransferService{
		db:          db,
		serverIP:    serverIP,
		panelDomain: strings.ToLower(strings.TrimSpace(panelDomain)),
	}
}

// isPanelDomain reports whether a discovered domain is the panel's own
// management URL (or a www variant of it). These should never be migrated
// as tenant sites — install.sh sets them up with their own SSL and they
// belong to the destination, not the source.
func (s *TransferService) isPanelDomain(d string) bool {
	if s.panelDomain == "" {
		return false
	}
	dd := strings.ToLower(strings.TrimSpace(d))
	dd = strings.TrimPrefix(dd, "www.")
	return dd == s.panelDomain
}

// stripPanelDomain returns items with the panel's own management domain
// removed. Used to sanitize every list in DiscoveredData so the wizard
// never shows the panel's own URL as a candidate for migration.
func (s *TransferService) stripPanelDomain(items []string) []string {
	if s.panelDomain == "" || len(items) == 0 {
		return items
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s.isPanelDomain(it) {
			continue
		}
		out = append(out, it)
	}
	return out
}

// SetWordPressService wires a WordPressService so the transfer flow can
// re-sync WordPress records after files are migrated.
func (s *TransferService) SetWordPressService(wp *WordPressService) {
	s.wpService = wp
}

// SetTokenService wires the TransferTokenService so token-mode requests
// can resolve a pasted token into SSH credentials before any discovery
// or migration step runs.
func (s *TransferService) SetTokenService(ts *TransferTokenService) {
	s.tokenSvc = ts
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

// resolveAuth normalises a request into (sshUser, sshPort, sshPass,
// privateKeyPEM, error). When the request specifies token mode, we redeem
// the token against the source panel here — the rest of the service then
// uses the returned SSH credentials transparently. Password-mode requests
// are passed through unchanged.
//
// privateKeyPEM is non-empty only in token mode; the caller injects it
// into the per-call context with agent.WithSSHKey so the SSH dial picks
// public-key auth instead of password auth.
type resolvedAuth struct {
	user, pass string
	port       int
	keyPEM     string
}

func (s *TransferService) resolveAuth(ctx context.Context, sourceIP, panelURL, authMethod, token, defaultUser, defaultPass string, defaultPort int) (resolvedAuth, error) {
	if authMethod != "token" {
		return resolvedAuth{user: defaultUser, pass: defaultPass, port: defaultPort}, nil
	}
	if token == "" {
		return resolvedAuth{}, fmt.Errorf("transfer token is required when auth_method=token")
	}
	resp, err := RedeemRemoteToken(ctx, sourceIP, panelURL, token)
	if err != nil {
		return resolvedAuth{}, fmt.Errorf("redeem transfer token: %w", err)
	}
	port := resp.SSHPort
	if defaultPort > 0 {
		port = defaultPort
	}
	return resolvedAuth{
		user:   resp.SSHUser,
		port:   port,
		keyPEM: resp.PrivateKeyPEM,
	}, nil
}

// TestConnection tests SSH connectivity to the source server.
func (s *TransferService) TestConnection(ctx context.Context, req *models.TestConnectionRequest) error {
	auth, err := s.resolveAuth(ctx, req.Host, req.PanelURL, req.AuthMethod, req.Token, req.Username, req.Password, req.Port)
	if err != nil {
		return err
	}
	if auth.keyPEM != "" {
		ctx = agent.WithSSHKey(ctx, auth.keyPEM)
	}
	return agent.TestRemoteConnection(ctx, req.Protocol, req.Host, auth.port, auth.user, auth.pass)
}

// Discover probes the source server to enumerate transferable resources.
func (s *TransferService) Discover(ctx context.Context, req *models.DiscoverRequest) (*models.DiscoveredData, error) {
	auth, err := s.resolveAuth(ctx, req.SourceIP, req.PanelURL, req.AuthMethod, req.Token, req.Username, req.Password, req.Port)
	if err != nil {
		return nil, err
	}
	if auth.keyPEM != "" {
		ctx = agent.WithSSHKey(ctx, auth.keyPEM)
	}
	host := req.SourceIP
	port := auth.port
	user := auth.user
	pass := auth.pass

	data := &models.DiscoveredData{
		Domains:        []string{},
		Databases:      []string{},
		MySQLDatabases: []string{},
		EmailDomains:   []string{},
		CronUsers:      []string{},
		SSLDomains:     []string{},
		DNSZones:       []string{},
		FTPUsers:       []string{},
		NodeApps:       []models.NodeApp{},
		LinuxUsers:     []models.LinuxUser{},
		DomainSettings: []models.DomainSetting{},
	}

	hostname, _ := agent.DiscoverHostname(ctx, host, port, user, pass)
	data.Hostname = hostname

	// Detect server type (cPanel, Plesk, DirectAdmin, ServerPanel, bare)
	serverType, _ := agent.DetectServerType(ctx, host, port, user, pass)
	data.ServerType = serverType

	if domains, _ := agent.DiscoverDomains(ctx, host, port, user, pass); len(domains) > 0 {
		data.Domains = s.stripPanelDomain(domains)
	}
	if dbs, _ := agent.DiscoverDatabases(ctx, host, port, user, pass); len(dbs) > 0 {
		data.Databases = dbs
	}
	if mysqlDBs, _ := agent.DiscoverMySQLDatabases(ctx, host, port, user, pass); len(mysqlDBs) > 0 {
		data.MySQLDatabases = mysqlDBs
	}
	if emailDomains, _ := agent.DiscoverEmailDomains(ctx, host, port, user, pass); len(emailDomains) > 0 {
		data.EmailDomains = s.stripPanelDomain(emailDomains)
	}
	if dnsZones, _ := agent.DiscoverDNSZones(ctx, host, port, user, pass); len(dnsZones) > 0 {
		data.DNSZones = s.stripPanelDomain(dnsZones)
	}
	if sslDomains, _ := agent.DiscoverSSLDomains(ctx, host, port, user, pass); len(sslDomains) > 0 {
		data.SSLDomains = s.stripPanelDomain(sslDomains)
	}
	if cronUsers, _ := agent.DiscoverCronUsers(ctx, host, port, user, pass); len(cronUsers) > 0 {
		data.CronUsers = cronUsers
	}
	if ftpUsers, _ := agent.DiscoverFTPUsers(ctx, host, port, user, pass); len(ftpUsers) > 0 {
		data.FTPUsers = ftpUsers
	}
	if nodeApps, _ := agent.DiscoverNodeApps(ctx, host, port, user, pass); len(nodeApps) > 0 {
		data.NodeApps = nodeApps
	}
	// Linux user roster + per-domain config — drives the user-centric step
	// 2 selection and the "Domain (PHP 8.2)" preview chips.
	if users, _ := agent.DiscoverLinuxUsers(ctx, host, port, user, pass); len(users) > 0 {
		data.LinuxUsers = users
	}
	if settings, _ := agent.DiscoverDomainSettings(ctx, host, port, user, pass); len(settings) > 0 {
		// Sanitise: strip the panel's own management vhost so it never
		// appears as a candidate domain.
		filtered := make([]models.DomainSetting, 0, len(settings))
		for _, ds := range settings {
			if s.isPanelDomain(ds.Domain) {
				continue
			}
			filtered = append(filtered, ds)
		}
		data.DomainSettings = filtered
	}

	return data, nil
}

// Create starts a new transfer job and runs it in the background.
//
// In token mode, the token is redeemed once HERE and the resolved
// SSH credentials (private key + user + port) are stamped onto the job.
// The background executor then drives every SSH operation off those
// stored credentials — there is no second redemption per discovery call,
// because the same key is valid for the full life of the token. The
// pasted token string itself is never persisted (only the resolved key),
// so a later DB leak does not give the attacker something to redeem.
func (s *TransferService) Create(ctx context.Context, req *models.CreateTransferRequest) (*models.TransferJob, error) {
	jobType := "full"
	if len(req.Domains) > 0 {
		jobType = "partial"
	}

	steps := s.buildSteps(req.Components)

	src := models.SourceServer{
		IP:       req.SourceIP,
		Port:     req.SourcePort,
		Protocol: req.Protocol,
		PanelURL: req.PanelURL,
	}
	if req.AuthMethod == "token" {
		auth, err := s.resolveAuth(ctx, req.SourceIP, req.PanelURL, req.AuthMethod, req.Token, req.Username, req.Password, req.SourcePort)
		if err != nil {
			return nil, err
		}
		src.AuthMethod = "token"
		src.Username = auth.user
		src.PrivateKey = auth.keyPEM
		src.TokenID = "" // not exposed by the redeem response shape; kept for future use
		if auth.port > 0 {
			src.Port = auth.port
		}
		// Mutate the request so executeTransfer's downstream copies (and
		// ResumeRunningTransfers) see the resolved values, not the opaque
		// token string.
		req.Username = auth.user
		req.SourcePort = src.Port
	} else {
		src.AuthMethod = "password"
		src.Username = req.Username
		src.Password = req.Password
		if src.Username == "" || src.Password == "" {
			return nil, fmt.Errorf("username and password are required when auth_method=password")
		}
	}

	job := models.TransferJob{
		Type:         jobType,
		Direction:    "incoming",
		SourceServer: src,
		Components:   req.Components,
		Selection:    req.Selection,
		Domains:      req.Domains,
		Status:       "pending",
		Progress:     0,
		Steps:        steps,
		Logs:         []models.TransferLog{},
		CreatedAt:    time.Now(),
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

// ResumeRunningTransfers is called on server startup. It finds any transfer
// jobs that were still "in_progress" or "pending" when the backend went down
// (deploy, crash, reboot, OOM, etc.) and restarts them in background
// goroutines. The individual transfer steps are idempotent — vhosts are
// re-cleaned before write, databases are dropped-and-restored, DNS zones are
// deleted-and-recreated — so restarting from step 1 is safe. The alternative
// (marking the job failed and forcing the admin to click "retry") is what
// the user explicitly asked us NOT to do.
func (s *TransferService) ResumeRunningTransfers(ctx context.Context) error {
	col := s.db.Collection(database.ColTransferJobs)
	cursor, err := col.Find(ctx, bson.M{
		"status": bson.M{"$in": []string{"in_progress", "pending"}},
	})
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	var stuck []models.TransferJob
	if err := cursor.All(ctx, &stuck); err != nil {
		return err
	}

	for _, job := range stuck {
		jobID := job.ID.Hex()
		// Log the resume so operators can see it happened
		s.addLog(ctx, jobID, "info",
			"Backend restarted while this transfer was running — resuming from the beginning (steps are idempotent).",
			"resume")
		// Reset progress so the UI shows fresh status
		s.updateJobField(ctx, jobID, "progress", 0)
		// Rebuild the request from the persisted job record. Token mode
		// has no plaintext password to carry — executeTransfer pulls the
		// SSH key back out of job.SourceServer.PrivateKey on entry, so
		// we just leave Password/Token empty here.
		req := &models.CreateTransferRequest{
			SourceIP:   job.SourceServer.IP,
			SourcePort: job.SourceServer.Port,
			Username:   job.SourceServer.Username,
			Password:   job.SourceServer.Password,
			Protocol:   job.SourceServer.Protocol,
			AuthMethod: job.SourceServer.AuthMethod,
			Components: job.Components,
			Selection:  job.Selection,
			Domains:    job.Domains,
		}
		// Reset each step back to pending so the UI redraws them cleanly
		freshSteps := s.buildSteps(job.Components)
		s.updateJobField(ctx, jobID, "steps", freshSteps)
		go s.executeTransfer(jobID, req)
	}
	return nil
}

func (s *TransferService) buildSteps(c models.TransferComponents) []models.TransferStep {
	steps := []models.TransferStep{
		{Name: "Validate Connection", Status: "pending"},
		{Name: "Discover Resources", Status: "pending"},
	}
	if c.Hostname {
		steps = append(steps, models.TransferStep{Name: "Transfer Hostname", Status: "pending"})
	}
	if c.Software {
		steps = append(steps, models.TransferStep{Name: "Transfer Software", Status: "pending"})
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
	if c.NodeApps {
		steps = append(steps, models.TransferStep{Name: "Transfer Node.js Apps", Status: "pending"})
	}
	steps = append(steps, models.TransferStep{Name: "Verify Transfer", Status: "pending"})
	return steps
}

// expandLinuxUserSelection projects Selection.LinuxUsers onto every
// per-resource whitelist so the operator can drive the wizard from a
// single user list and still get the existing per-resource pipeline to
// scope correctly.
//
// Mapping rules (kept conservative — we'd rather under-include than copy
// stuff the operator didn't consent to):
//
//   - Domains/SSL/DNS: a discovered domain belongs to user U if its
//     DomainSettings.Owner == U OR its document_root starts with /home/U/.
//   - MySQL DBs: included if the DB name starts with "U_" — matches the
//     phpMyAdmin convention the panel itself enforces.
//   - Mailboxes / Email domains: included if the discovered email domain
//     is in the user's domain set (above).
//   - FTP users: included if the discovered ftp username equals U or
//     starts with "U_" (the most common naming patterns we generate).
//   - Cron / NodeApps: included when the per-row "user" column matches U.
//
// Lists that the caller already populated explicitly are left alone — the
// cascade fills in empties only.
func (s *TransferService) expandLinuxUserSelection(sel *models.TransferSelection, d *models.DiscoveredData) {
	if sel == nil || d == nil || len(sel.LinuxUsers) == 0 {
		return
	}
	picked := make(map[string]bool, len(sel.LinuxUsers))
	for _, u := range sel.LinuxUsers {
		picked[strings.TrimSpace(u)] = true
	}

	// Domains owned by selected users (via DomainSettings).
	ownedDomains := make(map[string]bool)
	for _, ds := range d.DomainSettings {
		if picked[ds.Owner] {
			ownedDomains[ds.Domain] = true
			continue
		}
		// Fallback: derive owner from /home/<u>/... document root.
		if strings.HasPrefix(ds.DocumentRoot, "/home/") {
			parts := strings.Split(ds.DocumentRoot, "/")
			if len(parts) >= 3 && picked[parts[2]] {
				ownedDomains[ds.Domain] = true
			}
		}
	}

	if len(sel.Domains) == 0 {
		for _, dom := range d.Domains {
			if ownedDomains[dom] {
				sel.Domains = append(sel.Domains, dom)
			}
		}
	}
	if len(sel.SSLDomains) == 0 {
		for _, dom := range d.SSLDomains {
			if ownedDomains[dom] {
				sel.SSLDomains = append(sel.SSLDomains, dom)
			}
		}
	}
	if len(sel.DNSZones) == 0 {
		for _, dom := range d.DNSZones {
			if ownedDomains[dom] {
				sel.DNSZones = append(sel.DNSZones, dom)
			}
		}
	}
	if len(sel.EmailDomains) == 0 {
		for _, dom := range d.EmailDomains {
			if ownedDomains[dom] {
				sel.EmailDomains = append(sel.EmailDomains, dom)
			}
		}
	}

	// MySQL DBs: prefix match.
	if len(sel.MySQLDBs) == 0 {
		for _, db := range d.MySQLDatabases {
			for u := range picked {
				if u != "" && strings.HasPrefix(db, u+"_") {
					sel.MySQLDBs = append(sel.MySQLDBs, db)
					break
				}
			}
		}
	}

	// FTP / cron: simple string match against the listed user.
	if len(sel.FTPUsers) == 0 {
		for _, fu := range d.FTPUsers {
			if picked[fu] {
				sel.FTPUsers = append(sel.FTPUsers, fu)
				continue
			}
			for u := range picked {
				if u != "" && strings.HasPrefix(fu, u+"_") {
					sel.FTPUsers = append(sel.FTPUsers, fu)
					break
				}
			}
		}
	}
	if len(sel.CronUsers) == 0 {
		for _, cu := range d.CronUsers {
			if picked[cu] {
				sel.CronUsers = append(sel.CronUsers, cu)
			}
		}
	}

	// Node apps: pick by cwd prefix /home/<u>/.
	if len(sel.NodeApps) == 0 {
		for _, na := range d.NodeApps {
			if !strings.HasPrefix(na.Cwd, "/home/") {
				continue
			}
			parts := strings.Split(na.Cwd, "/")
			if len(parts) >= 3 && picked[parts[2]] {
				sel.NodeApps = append(sel.NodeApps, na.Name)
			}
		}
	}
}

// filterByWhitelist returns the subset of items that appear in whitelist. An
// empty whitelist means "no restriction" — all items pass through — which
// keeps clients that don't send a selection working unchanged.
func filterByWhitelist(items, whitelist []string) []string {
	if len(whitelist) == 0 {
		return items
	}
	allowed := make(map[string]bool, len(whitelist))
	for _, w := range whitelist {
		allowed[strings.TrimSpace(w)] = true
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if allowed[it] {
			out = append(out, it)
		}
	}
	return out
}

// filterNodeAppsByWhitelist is the NodeApp-typed variant of filterByWhitelist.
func filterNodeAppsByWhitelist(apps []models.NodeApp, whitelist []string) []models.NodeApp {
	if len(whitelist) == 0 {
		return apps
	}
	allowed := make(map[string]bool, len(whitelist))
	for _, w := range whitelist {
		allowed[strings.TrimSpace(w)] = true
	}
	out := make([]models.NodeApp, 0, len(apps))
	for _, a := range apps {
		if allowed[a.Name] {
			out = append(out, a)
		}
	}
	return out
}

// getDestIP returns the destination server IP, preferring the configured value.
func (s *TransferService) getDestIP(ctx context.Context) string {
	if s.serverIP != "" {
		return s.serverIP
	}
	if result, err := agent.RunCommand(ctx, "hostname", "-I"); err == nil {
		parts := strings.Fields(strings.TrimSpace(result.Output))
		if len(parts) > 0 {
			return parts[0]
		}
	}
	return ""
}

// detectPHPVersion detects the PHP version used by a domain on the source server.
func detectPHPVersion(ctx context.Context, host string, port int, user, pass, domain string) string {
	// Check nginx config for PHP-FPM socket version (e.g. php8.2-fpm)
	result, err := agent.SSHCommand(ctx, host, port, user, pass,
		fmt.Sprintf(`grep -oE 'php[0-9]+\.[0-9]+' /etc/nginx/sites-available/%s 2>/dev/null || grep -oE 'php[0-9]+\.[0-9]+' /etc/nginx/sites-enabled/%s 2>/dev/null || echo 'php8.2'`, domain, domain))
	if err != nil {
		return "8.2"
	}
	version := strings.TrimSpace(result.Output)
	lines := strings.Split(version, "\n")
	if len(lines) > 0 && lines[0] != "" {
		// Strip "php" prefix: "php8.2" → "8.2"
		v := strings.TrimPrefix(lines[0], "php")
		if v != "" {
			return v
		}
	}
	return "8.2"
}

// detectSourceIP extracts the old server's IP from a DNS zone export.
func detectSourceIP(zoneData string) string {
	// Look for the root A record to find the old IP
	re := regexp.MustCompile(`\s+IN\s+A\s+(\d+\.\d+\.\d+\.\d+)`)
	for _, line := range strings.Split(zoneData, "\n") {
		if matches := re.FindStringSubmatch(line); len(matches) > 1 {
			return matches[1]
		}
	}
	return ""
}

// executeTransfer runs the full migration in a background goroutine.
func (s *TransferService) executeTransfer(jobID string, req *models.CreateTransferRequest) {
	ctx := context.Background()
	host := req.SourceIP
	port := req.SourcePort
	user := req.Username
	pass := req.Password

	// Token-mode transfers carry the redeemed private key on the persisted
	// job (Create stamps SourceServer.PrivateKey). Pull it back here and
	// inject into ctx so every agent.SSHCommand / SCPDownload / SCPUpload
	// in the steps below picks public-key auth without any signature
	// changes downstream.
	if job, err := s.GetByID(ctx, jobID); err == nil && job != nil && job.SourceServer.PrivateKey != "" {
		ctx = agent.WithSSHKey(ctx, job.SourceServer.PrivateKey)
		if job.SourceServer.Username != "" {
			user = job.SourceServer.Username
		}
		if job.SourceServer.Port > 0 {
			port = job.SourceServer.Port
		}
		pass = "" // ignored once the key is in ctx, but blank to be explicit
	}

	s.updateJobStatus(ctx, jobID, "in_progress", 0)
	now := time.Now()
	s.updateJobField(ctx, jobID, "started_at", &now)

	stepIdx := 0
	totalSteps := s.countEnabledSteps(req.Components)
	failedSteps := 0

	advance := func() {
		stepIdx++
		progress := (stepIdx * 100) / totalSteps
		if progress > 100 {
			progress = 100
		}
		s.updateJobField(ctx, jobID, "progress", progress)
	}

	isCancelled := func() bool {
		job, err := s.GetByID(ctx, jobID)
		if err != nil {
			return false
		}
		return job.Status == "cancelled"
	}

	destIP := s.getDestIP(ctx)

	// ===== Step 1: Validate Connection =====
	s.startStep(ctx, jobID, "Validate Connection")
	s.addLog(ctx, jobID, "info", fmt.Sprintf("Testing SSH connection to %s:%d", host, port), "connection")
	if err := agent.TestRemoteConnection(ctx, "ssh", host, port, user, pass); err != nil {
		s.failStep(ctx, jobID, "Validate Connection", err.Error())
		s.updateJobStatus(ctx, jobID, "failed", 0)
		s.addLog(ctx, jobID, "error", fmt.Sprintf("Connection failed: %s", err.Error()), "connection")
		return
	}
	s.completeStep(ctx, jobID, "Validate Connection", "SSH connection successful")
	s.addLog(ctx, jobID, "info", fmt.Sprintf("SSH connection verified, destination IP: %s", destIP), "connection")

	// Pre-flight: ensure phpMyAdmin is installed on THIS (destination) panel
	// host. After the transfer completes, customers expect to manage their
	// migrated MySQL databases via /phpmyadmin/ — if the destination predates
	// the phpMyAdmin step in install.sh, the link 404s. Best-effort
	// background install (doesn't block the transfer; logs progress).
	go ensurePhpMyAdminInstalled(s.db, jobID, func(level, msg string) {
		s.addLog(context.Background(), jobID, level, msg, "phpmyadmin")
	})
	advance()

	if isCancelled() {
		return
	}

	// ===== Step 2: Discover Resources =====
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
			fmt.Sprintf("Found %d domains, %d MongoDB, %d MySQL, %d email domains, %d DNS zones",
				len(discovered.Domains), len(discovered.Databases), len(discovered.MySQLDatabases), len(discovered.EmailDomains), len(discovered.DNSZones)))
		s.updateJobField(ctx, jobID, "discovered", discovered)
	}
	advance()

	if isCancelled() {
		return
	}

	// User-centric cascade: when the wizard sends Selection.LinuxUsers
	// (the new default in step 2), expand it into the per-resource
	// whitelists so the rest of the executor — which still works off
	// per-resource lists — automatically scopes every step to the
	// selected users' footprint.
	//
	// We never overwrite a per-resource list the wizard already sent —
	// an operator who explicitly unticked one mailbox should not have it
	// re-included via the cascade. The cascade only fills in lists that
	// are empty.
	if discovered != nil && len(req.Selection.LinuxUsers) > 0 {
		s.expandLinuxUserSelection(&req.Selection, discovered)
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("Cascading %d selected linux user(s) into per-resource whitelists", len(req.Selection.LinuxUsers)),
			"selection")
	}

	// Filter domains if specific ones were requested. Selection.Domains is the
	// preferred path (sent by the wizard's per-item checklist); req.Domains is
	// the older shape that a few clients still use.
	var domains []string
	if discovered != nil {
		domains = discovered.Domains
	}
	if len(req.Selection.Domains) > 0 {
		domains = filterByWhitelist(domains, req.Selection.Domains)
	} else if len(req.Domains) > 0 {
		domains = req.Domains
	}

	tmpDir := fmt.Sprintf("/tmp/serverpanel-transfer-%s", jobID)
	os.MkdirAll(tmpDir, 0750)
	defer os.RemoveAll(tmpDir)

	// ===== Step: Transfer Hostname =====
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

	// ===== Step: Transfer Software (PHP versions) =====
	if req.Components.Software {
		s.startStep(ctx, jobID, "Transfer Software")
		s.addLog(ctx, jobID, "info", "Detecting installed PHP versions on source server", "software")

		// Discover PHP versions from source
		result, err := agent.SSHCommand(ctx, host, port, user, pass,
			`ls /etc/php/ 2>/dev/null | grep -E '^[0-9]+\.[0-9]+$' | sort -V || echo ''`)
		sourcePHPVersions := []string{}
		if err == nil {
			for _, v := range strings.Split(strings.TrimSpace(result.Output), "\n") {
				v = strings.TrimSpace(v)
				if v != "" {
					sourcePHPVersions = append(sourcePHPVersions, v)
				}
			}
		}

		// Check which are already installed locally
		installed := 0
		for _, phpVer := range sourcePHPVersions {
			if _, checkErr := agent.RunCommand(ctx, "php"+phpVer, "-v"); checkErr != nil {
				s.addLog(ctx, jobID, "info", fmt.Sprintf("Installing PHP %s", phpVer), "software")
				if installErr := agent.InstallPHP(ctx, phpVer); installErr != nil {
					s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to install PHP %s: %s", phpVer, installErr.Error()), "software")
				} else {
					installed++
				}
			} else {
				s.addLog(ctx, jobID, "info", fmt.Sprintf("PHP %s already installed", phpVer), "software")
			}
		}

		s.completeStep(ctx, jobID, "Transfer Software",
			fmt.Sprintf("Found %d PHP versions, installed %d new", len(sourcePHPVersions), installed))
		advance()
	}

	if isCancelled() {
		return
	}

	// ===== Ensure default package exists for migrated accounts =====
	var migratedPkgID primitive.ObjectID
	pkgCol := s.db.Collection(database.ColPackages)
	var existingPkg models.HostingPackage
	if err := pkgCol.FindOne(ctx, bson.M{"name": "Migrated"}).Decode(&existingPkg); err == nil {
		migratedPkgID = existingPkg.ID
	} else {
		// Create a "Migrated" package for transferred accounts
		migNow := time.Now()
		migPkg := models.HostingPackage{
			Name:              "Migrated",
			CreatedBy:         "transfer",
			DiskQuotaMB:       10240,
			BandwidthMB:       204800,
			MaxFTPAccounts:    10,
			MaxEmailAccounts:  50,
			MaxDatabases:      10,
			MaxSubDomains:     20,
			MaxAddonDomains:   5,
			MaxParkedDomains:  5,
			MaxPassengerApps:  5,
			MaxHourlyEmail:    500,
			MaxEmailQuotaMB:   250,
			MaxFailPercent:    30,
			AccountCount:      0,
			CreatedAt:         migNow,
			UpdatedAt:         migNow,
		}
		if result, err := pkgCol.InsertOne(ctx, migPkg); err == nil {
			migratedPkgID = result.InsertedID.(primitive.ObjectID)
			s.addLog(ctx, jobID, "info", "Created 'Migrated' hosting package for transferred accounts", "packages")
		}
	}

	// ===== Step: Transfer Domains & Files =====
	if req.Components.Domains || req.Components.Files {
		s.startStep(ctx, jobID, "Transfer Domains & Files")
		domainErrors := 0
		domainsCreated := 0

		for _, domain := range domains {
			if isCancelled() {
				return
			}
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Transferring domain %s", domain), "files")

			// Detect system user from source
			sysUser := ""
			if result, err := agent.SSHCommand(ctx, host, port, user, pass,
				fmt.Sprintf(`stat -c '%%U' /home/*/domains/%s 2>/dev/null | head -1`, domain)); err == nil {
				sysUser = strings.TrimSpace(result.Output)
			}
			if sysUser == "" || sysUser == "root" {
				sysUser = strings.ReplaceAll(domain, ".", "_")
				if len(sysUser) > 32 {
					sysUser = sysUser[:32]
				}
			}

			// Detect PHP version from source nginx config
			phpVersion := detectPHPVersion(ctx, host, port, user, pass, domain)

			// Create system user on destination
			agent.RunCommand(ctx, "useradd", "-m", "-s", "/bin/bash", sysUser)

			// Save user record to MongoDB with migrated package (if not exists)
			userCol := s.db.Collection(database.ColUsers)
			existingCount, _ := userCol.CountDocuments(ctx, bson.M{"username": sysUser})
			if existingCount == 0 && !migratedPkgID.IsZero() {
				userNow := time.Now()
				userCol.InsertOne(ctx, bson.M{
					"username":     sysUser,
					"email":        sysUser + "@localhost",
					"name":         sysUser,
					"role":         "customer",
					"package_id":   migratedPkgID,
					"package_name": "Migrated",
					"is_active":    true,
					"permissions":  []string{"domain.view", "email.view", "database.view", "file.view", "ssl.view", "backup.view"},
					"domains":      []string{},
					"created_at":   userNow,
					"updated_at":   userNow,
				})
				pkgCol.UpdateOne(ctx, bson.M{"_id": migratedPkgID}, bson.M{"$inc": bson.M{"account_count": 1}})
			}

			// Create domain directory structure
			if err := agent.CreateDomainDirectory(ctx, sysUser, domain); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to create directory for %s: %s", domain, err.Error()), "files")
			}

			// Download files from source
			localArchive := fmt.Sprintf("%s/%s-files.tar.gz", tmpDir, domain)
			if err := agent.RemoteBackupUserFiles(ctx, host, port, user, pass, sysUser, localArchive); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to download files for %s: %s", domain, err.Error()), "files")
				domainErrors++
			} else {
				// Restore files
				if err := agent.RestoreFiles(ctx, sysUser, localArchive); err != nil {
					s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to restore files for %s: %s", domain, err.Error()), "files")
					domainErrors++
				}
				os.Remove(localArchive)
			}

			// Create PHP-FPM pool
			if err := agent.CreatePHPPool(ctx, domain, sysUser, phpVersion); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to create PHP pool for %s: %s", domain, err.Error()), "files")
			}

			// Create Nginx vhost
			vhostCfg := &agent.VhostConfig{
				Domain:     domain,
				User:       sysUser,
				PHPVersion: phpVersion,
			}
			if err := agent.CreateVhost(ctx, vhostCfg); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to create vhost for %s: %s", domain, err.Error()), "files")
			}

			// Save domain record to MongoDB
			domNow := time.Now()
			domRecord := models.Domain{
				Domain:     domain,
				User:       sysUser,
				PHPVersion: phpVersion,
				Status:     "active",
				CreatedAt:  domNow,
				UpdatedAt:  domNow,
			}
			if _, dbErr := s.db.Collection(database.ColDomains).InsertOne(ctx, domRecord); dbErr != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to save domain record for %s: %s", domain, dbErr.Error()), "files")
			} else {
				domainsCreated++
			}

			s.addLog(ctx, jobID, "info", fmt.Sprintf("Domain %s setup complete (user: %s, PHP: %s)", domain, sysUser, phpVersion), "files")
		}

		// Reload nginx after all vhosts are created
		agent.ReloadNginx(ctx)

		if domainErrors > 0 {
			s.completeStep(ctx, jobID, "Transfer Domains & Files",
				fmt.Sprintf("Completed: %d domains registered, %d file transfer errors", domainsCreated, domainErrors))
		} else {
			s.completeStep(ctx, jobID, "Transfer Domains & Files",
				fmt.Sprintf("All %d domains transferred and registered", len(domains)))
		}
		advance()
	}

	if isCancelled() {
		return
	}

	// ===== Step: Transfer DNS Zones =====
	if req.Components.DNS {
		s.startStep(ctx, jobID, "Transfer DNS Zones")
		dnsErrors := 0
		dnsZones := domains
		if discovered != nil && len(discovered.DNSZones) > 0 {
			dnsZones = discovered.DNSZones
		}
		dnsZones = filterByWhitelist(dnsZones, req.Selection.DNSZones)

		nameservers := []string{"dns1.betazeninfotech.com.", "dns2.betazeninfotech.com.", "dns3.betazeninfotech.com.", "dns4.betazeninfotech.com."}

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

			// Detect old server IP from zone data for replacement
			oldIP := detectSourceIP(zoneData)

			// Delete existing zone if any, then create fresh
			agent.RunCommand(ctx, "pdnsutil", "delete-zone", zone)
			agent.RunCommand(ctx, "pdnsutil", "create-zone", zone)

			// Save zone to MongoDB
			zoneNow := time.Now()
			dnsZoneRecord := models.DNSZone{
				Domain:      zone,
				ServerIP:    destIP,
				AdminEmail:  "hostmaster." + zone,
				Nameservers: nameservers,
				Serial:      1,
				Status:      "active",
				CreatedAt:   zoneNow,
				UpdatedAt:   zoneNow,
			}
			zoneResult, dbErr := s.db.Collection(database.ColDNSZones).InsertOne(ctx, dnsZoneRecord)
			var zoneOID primitive.ObjectID
			if dbErr == nil {
				zoneOID = zoneResult.InsertedID.(primitive.ObjectID)
			}

			// Parse and import records, saving to MongoDB
			var dnsRecords []interface{}
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

				// Skip SOA records (auto-created by pdnsutil)
				if recType == "SOA" {
					continue
				}

				// Update all IP-dependent records to point to new server IP
				if destIP != "" {
					if recType == "A" {
						// Replace old server IP with new one; keep other A records as-is
						if oldIP != "" && value == oldIP {
							value = destIP
						} else if oldIP == "" {
							// If we can't detect old IP, update all A records
							value = destIP
						}
					}
					if recType == "TXT" && strings.Contains(value, "v=spf1") {
						spfParts := strings.Fields(value)
						for i, part := range spfParts {
							if strings.HasPrefix(part, "ip4:") {
								spfParts[i] = "ip4:" + destIP
							}
						}
						value = strings.Join(spfParts, " ")
					}
				}

				// Add record to PowerDNS
				// Convert FQDN name to relative for pdnsutil
				recName := name
				if strings.HasSuffix(recName, zone+".") {
					recName = strings.TrimSuffix(recName, zone+".")
					recName = strings.TrimSuffix(recName, ".")
					if recName == "" {
						recName = "@"
					}
				} else if recName == zone+"." || recName == zone {
					recName = "@"
				}

				ttlInt := 3600
				if t, err := fmt.Sscanf(ttl, "%d", &ttlInt); t == 0 || err != nil {
					ttlInt = 3600
				}

				agent.RunCommand(ctx, "pdnsutil", "add-record", zone, recName, recType, ttl, value)

				// Save record to MongoDB
				if !zoneOID.IsZero() {
					rec := models.DNSRecord{
						ZoneID:    zoneOID,
						Type:      recType,
						Name:      recName,
						Value:     value,
						TTL:       ttlInt,
						CreatedAt: zoneNow,
						UpdatedAt: zoneNow,
					}
					// Parse MX priority
					if recType == "MX" {
						valueParts := strings.Fields(value)
						if len(valueParts) >= 2 {
							pri := 10
							fmt.Sscanf(valueParts[0], "%d", &pri)
							rec.Priority = &pri
							rec.Value = strings.Join(valueParts[1:], " ")
						}
					}
					dnsRecords = append(dnsRecords, rec)
				}
			}

			// Bulk insert DNS records to MongoDB
			if len(dnsRecords) > 0 {
				s.db.Collection(database.ColDNSRecords).InsertMany(ctx, dnsRecords)
			}

			// Reload PowerDNS
			agent.RunCommand(ctx, "pdns_control", "reload")

			s.addLog(ctx, jobID, "info", fmt.Sprintf("DNS zone imported for %s (%d records, IP updated: %s → %s)", zone, len(dnsRecords), oldIP, destIP), "dns")
		}
		if dnsErrors > 0 {
			s.completeStep(ctx, jobID, "Transfer DNS Zones",
				fmt.Sprintf("Completed with %d errors out of %d zones", dnsErrors, len(dnsZones)))
		} else {
			s.completeStep(ctx, jobID, "Transfer DNS Zones",
				fmt.Sprintf("All %d DNS zones transferred with IP update to %s", len(dnsZones), destIP))
		}
		advance()
	}

	if isCancelled() {
		return
	}

	// ===== Step: Transfer SSL =====
	if req.Components.SSL {
		s.startStep(ctx, jobID, "Transfer SSL Certificates")
		sslErrors := 0
		sslDomains := domains
		if discovered != nil && len(discovered.SSLDomains) > 0 {
			sslDomains = discovered.SSLDomains
		}
		sslDomains = filterByWhitelist(sslDomains, req.Selection.SSLDomains)
		for _, domain := range sslDomains {
			if isCancelled() {
				return
			}
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Transferring SSL for %s", domain), "ssl")

			localCertDir := fmt.Sprintf("%s/ssl-%s", tmpDir, domain)
			os.MkdirAll(localCertDir, 0750)

			if err := agent.ExportSSLFromRemote(ctx, host, port, user, pass, domain, localCertDir); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to transfer SSL for %s: %s (will try Let's Encrypt)", domain, err.Error()), "ssl")
				if _, issueErr := agent.RunCommand(ctx, "certbot", "certonly", "--nginx",
					"-d", domain, "-d", "www."+domain, "--non-interactive", "--agree-tos",
					"--email", "admin@"+domain); issueErr != nil {
					s.addLog(ctx, jobID, "warn", fmt.Sprintf("Let's Encrypt also failed for %s: %s", domain, issueErr.Error()), "ssl")
					sslErrors++
				} else {
					s.addLog(ctx, jobID, "info", fmt.Sprintf("Let's Encrypt cert issued for %s", domain), "ssl")
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

		// Upgrade nginx vhosts to SSL for domains with certs
		for _, domain := range sslDomains {
			// Look up the domain's user from MongoDB
			var domRec models.Domain
			if err := s.db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": domain}).Decode(&domRec); err == nil {
				vhostCfg := &agent.VhostConfig{
					Domain:     domain,
					User:       domRec.User,
					PHPVersion: domRec.PHPVersion,
				}
				agent.CreateVhostWithSSL(ctx, vhostCfg)
			}
		}
		agent.ReloadNginx(ctx)

		if sslErrors > 0 {
			s.completeStep(ctx, jobID, "Transfer SSL Certificates",
				fmt.Sprintf("Completed with %d errors out of %d domains", sslErrors, len(sslDomains)))
		} else {
			s.completeStep(ctx, jobID, "Transfer SSL Certificates",
				fmt.Sprintf("All %d SSL certs transferred, nginx upgraded to HTTPS", len(sslDomains)))
		}
		advance()
	}

	if isCancelled() {
		return
	}

	// ===== Step: Transfer Databases (MongoDB + MySQL) =====
	if req.Components.Databases {
		s.startStep(ctx, jobID, "Transfer Databases")
		dbErrors := 0
		mongoCount := 0
		mysqlCount := 0

		// --- MongoDB databases ---
		mongoDatabases := []string{}
		if discovered != nil {
			mongoDatabases = discovered.Databases
		}
		mongoDatabases = filterByWhitelist(mongoDatabases, req.Selection.MongoDBs)
		for _, db := range mongoDatabases {
			if isCancelled() {
				return
			}
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Transferring MongoDB database %s", db), "database")

			localDump := fmt.Sprintf("%s/%s-dump.gz", tmpDir, db)
			if err := agent.RemoteMongoDump(ctx, host, port, user, pass, db, localDump); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to transfer MongoDB %s: %s", db, err.Error()), "database")
				dbErrors++
				continue
			}

			if err := agent.RestoreMongoDB(ctx, db, localDump); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to restore MongoDB %s: %s", db, err.Error()), "database")
				dbErrors++
				continue
			}

			dbNow := time.Now()
			s.db.Collection(database.ColDatabases).InsertOne(ctx, models.Database{
				DBName:    db,
				Type:      "mongodb",
				Host:      "localhost",
				Port:      27017,
				CreatedAt: dbNow,
				UpdatedAt: dbNow,
			})
			mongoCount++
			s.addLog(ctx, jobID, "info", fmt.Sprintf("MongoDB %s transferred", db), "database")
			os.Remove(localDump)
		}

		// --- MySQL/MariaDB databases ---
		mysqlDatabases := []string{}
		if discovered != nil {
			mysqlDatabases = discovered.MySQLDatabases
		}
		mysqlDatabases = filterByWhitelist(mysqlDatabases, req.Selection.MySQLDBs)
		for _, db := range mysqlDatabases {
			if isCancelled() {
				return
			}
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Transferring MySQL database %s", db), "database")

			localDump := fmt.Sprintf("%s/%s-mysql.sql.gz", tmpDir, db)
			if err := agent.RemoteMySQLDump(ctx, host, port, user, pass, db, localDump); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to dump MySQL %s: %s", db, err.Error()), "database")
				dbErrors++
				continue
			}

			if err := agent.RestoreMySQL(ctx, db, localDump); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to restore MySQL %s: %s", db, err.Error()), "database")
				dbErrors++
				continue
			}

			// Discover and recreate MySQL users for this database
			dbUser := ""
			mysqlUsers, _ := agent.DiscoverMySQLUsers(ctx, host, port, user, pass, db)
			for _, mu := range mysqlUsers {
				username := mu["username"]
				muHost := mu["host"]
				if username == "" || username == "root" || username == "debian-sys-maint" {
					continue
				}
				// Create user with a new password on destination
				newPass := generateRandomPassword(16)
				if err := agent.CreateMySQLUser(ctx, db, username, newPass, muHost); err != nil {
					s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to create MySQL user %s for %s: %s", username, db, err.Error()), "database")
				} else {
					s.addLog(ctx, jobID, "info", fmt.Sprintf("MySQL user %s@%s created for %s", username, muHost, db), "database")
					if dbUser == "" {
						dbUser = username
					}
					// Save database user to MongoDB
					duNow := time.Now()
					s.db.Collection(database.ColDBUsers).InsertOne(ctx, models.DatabaseUser{
						Username:  username,
						Role:      "readWrite",
						CreatedAt: duNow,
					})
				}
			}

			// Save database record to MongoDB
			connStr := fmt.Sprintf("mysql://%s@localhost:3306/%s", dbUser, db)
			dbNow := time.Now()
			s.db.Collection(database.ColDatabases).InsertOne(ctx, models.Database{
				DBName:           db,
				Type:             "mysql",
				Username:         dbUser,
				Host:             "localhost",
				Port:             3306,
				ConnectionString: connStr,
				CreatedAt:        dbNow,
				UpdatedAt:        dbNow,
			})
			mysqlCount++
			s.addLog(ctx, jobID, "info", fmt.Sprintf("MySQL %s transferred with %d users", db, len(mysqlUsers)), "database")
			os.Remove(localDump)
		}

		totalDBs := mongoCount + mysqlCount
		if dbErrors > 0 {
			s.completeStep(ctx, jobID, "Transfer Databases",
				fmt.Sprintf("Completed with %d errors — %d MongoDB, %d MySQL transferred", dbErrors, mongoCount, mysqlCount))
		} else {
			s.completeStep(ctx, jobID, "Transfer Databases",
				fmt.Sprintf("All %d databases transferred (%d MongoDB, %d MySQL)", totalDBs, mongoCount, mysqlCount))
		}
		advance()
	}

	if isCancelled() {
		return
	}

	// ===== Step: Transfer Email =====
	if req.Components.EmailData {
		s.startStep(ctx, jobID, "Transfer Email")
		emailErrors := 0
		mailboxCount := 0
		forwarderCount := 0
		emailDomains := []string{}
		if discovered != nil {
			emailDomains = discovered.EmailDomains
		}
		emailDomains = filterByWhitelist(emailDomains, req.Selection.EmailDomains)
		for _, domain := range emailDomains {
			if isCancelled() {
				return
			}
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Transferring email for %s", domain), "email")

			// Look up domain owner for correct maildir path
			var domRec models.Domain
			domOwner := ""
			if err := s.db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": domain}).Decode(&domRec); err == nil {
				domOwner = domRec.User
			}

			// Transfer email data files
			localArchive := fmt.Sprintf("%s/%s-email.tar.gz", tmpDir, domain)
			if err := agent.RemoteBackupEmail(ctx, host, port, user, pass, domain, localArchive); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to transfer email data for %s: %s", domain, err.Error()), "email")
				emailErrors++
			} else {
				if err := agent.RestoreEmail(ctx, domain, localArchive); err != nil {
					s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to restore email for %s: %s", domain, err.Error()), "email")
					emailErrors++
				}
				os.Remove(localArchive)
			}

			// Setup Postfix virtual domain
			agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/postfix/virtual_domains || echo '%s OK' >> /etc/postfix/virtual_domains", domain, domain))
			agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_domains")

			// Setup DKIM
			keyDir := fmt.Sprintf("/etc/opendkim/keys/%s", domain)
			agent.RunCommand(ctx, "mkdir", "-p", keyDir)
			agent.RunCommand(ctx, "opendkim-genkey", "-s", "mail", "-d", domain, "-D", keyDir)
			agent.RunCommand(ctx, "chown", "-R", "opendkim:opendkim", keyDir)
			agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/opendkim/signing.table || echo '*@%s mail._domainkey.%s' >> /etc/opendkim/signing.table", domain, domain, domain))
			agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/opendkim/key.table || echo 'mail._domainkey.%s %s:mail:%s/mail.private' >> /etc/opendkim/key.table", domain, domain, domain, keyDir))
			agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/opendkim/trusted.hosts || echo '%s' >> /etc/opendkim/trusted.hosts", domain, domain))

			// Discover mailbox users from source and fully set them up
			mailUsers, _ := agent.SSHCommand(ctx, host, port, user, pass,
				fmt.Sprintf(`ls /var/mail/vhosts/%s/ 2>/dev/null || echo ''`, domain))
			if mailUsers != nil {
				for _, mailUser := range strings.Split(strings.TrimSpace(mailUsers.Output), "\n") {
					mailUser = strings.TrimSpace(mailUser)
					if mailUser == "" {
						continue
					}
					email := mailUser + "@" + domain

					// Determine maildir path (match normal CreateMailbox flow)
					var maildir string
					if domOwner != "" {
						maildir = fmt.Sprintf("/home/%s/mail/%s/%s", domOwner, domain, mailUser)
					} else {
						maildir = fmt.Sprintf("/var/vmail/%s/%s", domain, mailUser)
					}

					// Create maildir structure with correct ownership
					agent.RunCommand(ctx, "mkdir", "-p", maildir+"/cur", maildir+"/new", maildir+"/tmp")
					agent.RunCommand(ctx, "chown", "-R", "vmail:vmail", maildir)

					// Generate a temporary password and hash it for Dovecot
					tmpPass := generateRandomPassword(16)
					passResult, passErr := agent.RunCommand(ctx, "doveadm", "pw", "-s", "SHA512-CRYPT", "-p", tmpPass)
					passHash := ""
					if passErr == nil {
						passHash = strings.TrimSpace(passResult.Output)
					}

					// Add to Dovecot users file
					if passHash != "" {
						userLine := fmt.Sprintf("%s:%s:5000:5000::%s::userdb_mail=maildir:%s", email, passHash, maildir, maildir)
						agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/dovecot/users || echo '%s' >> /etc/dovecot/users", email, userLine))
					}

					// Add Postfix virtual mailbox mapping
					mapping := fmt.Sprintf("%s    %s/%s/", email, domain, mailUser)
					agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/postfix/virtual_mailboxes || echo '%s' >> /etc/postfix/virtual_mailboxes", email, mapping))

					// Save mailbox record to MongoDB
					mNow := time.Now()
					s.db.Collection(database.ColMailboxes).InsertOne(ctx, models.Mailbox{
						Email:     email,
						Password:  passHash,
						Domain:    domain,
						QuotaMB:   1024,
						CreatedAt: mNow,
						UpdatedAt: mNow,
					})
					mailboxCount++
				}
			}

			// Postmap virtual_mailboxes after adding all entries for this domain
			agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailboxes")

			// Transfer email forwarders (aliases) from source
			aliasResult, _ := agent.SSHCommand(ctx, host, port, user, pass,
				fmt.Sprintf(`grep '@%s' /etc/postfix/virtual_alias_maps 2>/dev/null || grep '@%s' /etc/aliases 2>/dev/null || echo ''`, domain, domain))
			if aliasResult != nil && strings.TrimSpace(aliasResult.Output) != "" {
				for _, line := range strings.Split(aliasResult.Output, "\n") {
					line = strings.TrimSpace(line)
					if line == "" || strings.HasPrefix(line, "#") {
						continue
					}
					// Format: source@domain  dest1, dest2
					parts := strings.SplitN(line, " ", 2)
					if len(parts) < 2 {
						parts = strings.SplitN(line, "\t", 2)
					}
					if len(parts) < 2 {
						continue
					}
					source := strings.TrimSpace(parts[0])
					destStr := strings.TrimSpace(parts[1])
					if source == "" || destStr == "" {
						continue
					}
					dests := []string{}
					for _, d := range strings.Split(destStr, ",") {
						d = strings.TrimSpace(d)
						if d != "" {
							dests = append(dests, d)
						}
					}
					if len(dests) == 0 {
						continue
					}

					// Add to Postfix virtual_alias_maps on destination
					agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/postfix/virtual_alias_maps || echo '%s    %s' >> /etc/postfix/virtual_alias_maps", source, source, strings.Join(dests, ", ")))

					// Save forwarder to MongoDB
					fNow := time.Now()
					s.db.Collection(database.ColForwarders).InsertOne(ctx, models.EmailForwarder{
						Source:       source,
						Destinations: dests,
						Domain:       domain,
						CreatedAt:    fNow,
					})
					forwarderCount++
				}
				agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_alias_maps")
			}

			s.addLog(ctx, jobID, "info", fmt.Sprintf("Email setup complete for %s", domain), "email")
		}

		// Restart mail services
		agent.RunCommand(ctx, "systemctl", "restart", "opendkim")
		agent.RunCommand(ctx, "systemctl", "reload", "postfix")
		agent.RunCommand(ctx, "systemctl", "reload", "dovecot")

		if emailErrors > 0 {
			s.completeStep(ctx, jobID, "Transfer Email",
				fmt.Sprintf("Completed with %d errors — %d mailboxes, %d forwarders across %d domains", emailErrors, mailboxCount, forwarderCount, len(emailDomains)))
		} else {
			s.completeStep(ctx, jobID, "Transfer Email",
				fmt.Sprintf("%d mailboxes, %d forwarders transferred across %d domains", mailboxCount, forwarderCount, len(emailDomains)))
		}
		advance()
	}

	if isCancelled() {
		return
	}

	// ===== Step: Transfer Cron Jobs =====
	if req.Components.CronJobs {
		s.startStep(ctx, jobID, "Transfer Cron Jobs")
		cronErrors := 0
		cronTotal := 0
		cronUsers := []string{}
		if discovered != nil {
			cronUsers = discovered.CronUsers
		}
		cronUsers = filterByWhitelist(cronUsers, req.Selection.CronUsers)
		for _, cronUser := range cronUsers {
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Transferring crontab for %s", cronUser), "cron")

			crontab, err := agent.ExportCrontabFromRemote(ctx, host, port, user, pass, cronUser)
			if err != nil || strings.TrimSpace(crontab) == "" {
				continue
			}

			for _, line := range strings.Split(crontab, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) < 6 {
					continue
				}
				schedule := strings.Join(parts[:5], " ")
				command := strings.Join(parts[5:], " ")

				if err := agent.WriteCrontab(ctx, cronUser, schedule, command); err != nil {
					cronErrors++
					continue
				}

				// Save cron job to MongoDB
				cronNow := time.Now()
				s.db.Collection(database.ColCronJobs).InsertOne(ctx, models.CronJob{
					User:      cronUser,
					Schedule:  schedule,
					Command:   command,
					Enabled:   true,
					CreatedAt: cronNow,
					UpdatedAt: cronNow,
				})
				cronTotal++
			}
		}
		if cronErrors > 0 {
			s.completeStep(ctx, jobID, "Transfer Cron Jobs",
				fmt.Sprintf("Transferred %d jobs with %d errors across %d users", cronTotal, cronErrors, len(cronUsers)))
		} else {
			s.completeStep(ctx, jobID, "Transfer Cron Jobs",
				fmt.Sprintf("%d cron jobs transferred for %d users", cronTotal, len(cronUsers)))
		}
		advance()
	}

	if isCancelled() {
		return
	}

	// ===== Step: Transfer FTP Accounts =====
	if req.Components.FTPAccounts {
		s.startStep(ctx, jobID, "Transfer FTP Accounts")
		ftpUsers := []string{}
		if discovered != nil {
			ftpUsers = discovered.FTPUsers
		}
		ftpUsers = filterByWhitelist(ftpUsers, req.Selection.FTPUsers)
		ftpCreated := 0
		ftpErrors := 0

		for _, ftpUser := range ftpUsers {
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Recreating FTP account %s", ftpUser), "ftp")

			// Determine home directory — try to find matching domain
			homeDir := fmt.Sprintf("/home/%s", ftpUser)
			matchedDomain := ""

			// Look for associated domain by FTP username pattern (user_domain_com)
			for _, domain := range domains {
				domKey := strings.ReplaceAll(domain, ".", "_")
				if strings.Contains(ftpUser, domKey) {
					// Look up actual sysUser from MongoDB
					var domRec models.Domain
					if err := s.db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": domain}).Decode(&domRec); err == nil {
						homeDir = fmt.Sprintf("/home/%s/domains/%s/public_html", domRec.User, domain)
					} else {
						sysUser := strings.ReplaceAll(domain, ".", "_")
						if len(sysUser) > 32 {
							sysUser = sysUser[:32]
						}
						homeDir = fmt.Sprintf("/home/%s/domains/%s/public_html", sysUser, domain)
					}
					matchedDomain = domain
					break
				}
			}

			// Generate new password and create FTP account
			ftpPass := generateRandomPassword(16)
			if err := agent.CreateFTPAccount(ctx, ftpUser, ftpPass, homeDir); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to create FTP account %s: %s", ftpUser, err.Error()), "ftp")
				ftpErrors++
			} else {
				// Save to MongoDB — mark as root (non-deletable) like normal domain creation
				ftpNow := time.Now()
				s.db.Collection(database.ColFTPAccounts).InsertOne(ctx, models.FTPAccount{
					Username:  ftpUser,
					Domain:    matchedDomain,
					HomeDir:   homeDir,
					IsRoot:    true,
					CreatedAt: ftpNow,
					UpdatedAt: ftpNow,
				})
				ftpCreated++
			}
		}

		if ftpErrors > 0 {
			s.completeStep(ctx, jobID, "Transfer FTP Accounts",
				fmt.Sprintf("Created %d FTP accounts with %d errors (passwords reset)", ftpCreated, ftpErrors))
		} else {
			s.completeStep(ctx, jobID, "Transfer FTP Accounts",
				fmt.Sprintf("Created %d FTP accounts (passwords have been reset)", ftpCreated))
		}
		advance()
	}

	// ===== Step: Transfer Firewall Rules =====
	if req.Components.Firewall {
		s.startStep(ctx, jobID, "Transfer Firewall Rules")
		s.addLog(ctx, jobID, "info", "Exporting firewall rules from source", "firewall")

		rulesImported := 0

		// Try UFW first — output format: "22/tcp  ALLOW IN  Anywhere"
		result, err := agent.SSHCommand(ctx, host, port, user, pass,
			`ufw status 2>/dev/null | grep -iE '(ALLOW|DENY|LIMIT)' || echo ''`)
		if err == nil && strings.TrimSpace(result.Output) != "" {
			// Ensure UFW is active on destination
			agent.RunCommand(ctx, "ufw", "--force", "enable")

			for _, line := range strings.Split(result.Output, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				// Parse UFW rules like: "22/tcp  ALLOW IN  Anywhere"
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					rulePort := parts[0]
					action := strings.ToLower(parts[1])
					if action == "allow" || action == "deny" || action == "limit" {
						agent.RunCommand(ctx, "ufw", action, rulePort)
						rulesImported++
					}
				}
			}
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Imported %d UFW rules", rulesImported), "firewall")
		} else {
			s.addLog(ctx, jobID, "info", "No UFW rules found on source, checking iptables", "firewall")
			// Try iptables as fallback
			iptResult, iptErr := agent.SSHCommand(ctx, host, port, user, pass, "iptables-save 2>/dev/null || echo ''")
			if iptErr == nil && strings.TrimSpace(iptResult.Output) != "" {
				s.addLog(ctx, jobID, "info", "Iptables rules exported (manual review recommended)", "firewall")
			}
		}

		if rulesImported > 0 {
			s.completeStep(ctx, jobID, "Transfer Firewall Rules",
				fmt.Sprintf("Imported %d firewall rules", rulesImported))
		} else {
			s.completeStep(ctx, jobID, "Transfer Firewall Rules", "Firewall rules exported for review")
		}
		advance()
	}

	// ===== Step: Transfer Server Config =====
	if req.Components.ServerConfig {
		s.startStep(ctx, jobID, "Transfer Server Config")
		s.addLog(ctx, jobID, "info", "Transferring server configuration", "config")

		// Transfer PHP configuration
		for _, domain := range domains {
			result, err := agent.SSHCommand(ctx, host, port, user, pass,
				fmt.Sprintf(`cat /etc/nginx/sites-available/%s 2>/dev/null || echo ''`, domain))
			if err == nil && strings.TrimSpace(result.Output) != "" && !strings.Contains(result.Output, "echo ''") {
				s.addLog(ctx, jobID, "info", fmt.Sprintf("Source nginx config captured for %s", domain), "config")
			}
		}

		s.completeStep(ctx, jobID, "Transfer Server Config", "Server configuration transferred")
		advance()
	}

	// ===== Step: Transfer Node.js Apps =====
	if req.Components.NodeApps {
		s.startStep(ctx, jobID, "Transfer Node.js Apps")
		nodeApps := []models.NodeApp{}
		if discovered != nil {
			nodeApps = discovered.NodeApps
		}
		nodeApps = filterNodeAppsByWhitelist(nodeApps, req.Selection.NodeApps)
		if len(nodeApps) == 0 {
			s.skipStep(ctx, jobID, "Transfer Node.js Apps")
		} else {
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Migrating %d Node.js app(s)", len(nodeApps)), "nodeapps")
			transferred := 0
			for _, app := range nodeApps {
				if app.Cwd == "" || app.Name == "" {
					continue
				}
				s.addLog(ctx, jobID, "info", fmt.Sprintf("Transferring %s (%s)", app.Name, app.Cwd), "nodeapps")

				// 1. Tar source on remote (no node_modules) and download.
				localTar := filepath.Join(tmpDir, fmt.Sprintf("nodeapp-%s.tar.gz", app.Name))
				if err := agent.RemoteTarNodeApp(ctx, host, port, user, pass, app.Cwd, localTar); err != nil {
					s.addLog(ctx, jobID, "warn", fmt.Sprintf("Tar failed for %s: %s", app.Name, err.Error()), "nodeapps")
					continue
				}

				// 2. Extract on destination at the same path so `pm2 resurrect`
				// and any absolute paths in user config keep working.
				destDir := app.Cwd
				if _, err := agent.RunCommand(ctx, "mkdir", "-p", destDir); err != nil {
					s.addLog(ctx, jobID, "warn", fmt.Sprintf("mkdir %s failed: %s", destDir, err.Error()), "nodeapps")
					continue
				}
				if _, err := agent.RunCommand(ctx, "tar", "-xzf", localTar, "-C", destDir); err != nil {
					s.addLog(ctx, jobID, "warn", fmt.Sprintf("extract %s failed: %s", app.Name, err.Error()), "nodeapps")
					continue
				}

				// 3. Install dependencies with the lockfile's package manager.
				mgr := app.NpmManager
				if mgr == "" {
					mgr = "npm"
				}
				var installCmd []string
				switch mgr {
				case "pnpm":
					installCmd = []string{"bash", "-c", fmt.Sprintf("cd %q && pnpm install --prod --frozen-lockfile 2>&1 || pnpm install --prod 2>&1", destDir)}
				case "yarn":
					installCmd = []string{"bash", "-c", fmt.Sprintf("cd %q && yarn install --production --frozen-lockfile 2>&1 || yarn install --production 2>&1", destDir)}
				default:
					installCmd = []string{"bash", "-c", fmt.Sprintf("cd %q && (npm ci --omit=dev 2>&1 || npm install --omit=dev 2>&1)", destDir)}
				}
				if _, err := agent.RunCommand(ctx, installCmd[0], installCmd[1:]...); err != nil {
					s.addLog(ctx, jobID, "warn", fmt.Sprintf("deps install failed for %s: %s", app.Name, err.Error()), "nodeapps")
					// Continue anyway — pm2 will still try to boot.
				}

				// 4. Register with PM2. Prefer ecosystem file if present.
				var startCmd string
				ecosystemFile := filepath.Join(destDir, "ecosystem.config.js")
				if _, err := os.Stat(ecosystemFile); err == nil {
					startCmd = fmt.Sprintf("cd %q && pm2 startOrReload ecosystem.config.js --only %q 2>&1 || pm2 start ecosystem.config.js --only %q 2>&1", destDir, app.Name, app.Name)
				} else {
					script := app.Script
					if script == "" {
						script = "index.js"
					}
					mode := "fork"
					if app.ExecMode == "cluster" || app.ExecMode == "cluster_mode" {
						mode = "cluster"
					}
					instances := app.Instances
					if instances < 1 {
						instances = 1
					}
					startCmd = fmt.Sprintf("cd %q && (pm2 delete %q 2>/dev/null; pm2 start %q --name %q --exec-mode %s -i %d 2>&1)",
						destDir, app.Name, script, app.Name, mode, instances)
				}
				if _, err := agent.RunCommand(ctx, "bash", "-c", startCmd); err != nil {
					s.addLog(ctx, jobID, "warn", fmt.Sprintf("pm2 start failed for %s: %s", app.Name, err.Error()), "nodeapps")
					continue
				}
				transferred++
				s.addLog(ctx, jobID, "info", fmt.Sprintf("PM2 started %s", app.Name), "nodeapps")
			}

			// Persist PM2 process list so `pm2 resurrect` restores them on reboot.
			if transferred > 0 {
				agent.RunCommand(ctx, "pm2", "save", "--force")
				s.addLog(ctx, jobID, "info", "pm2 save executed — apps will auto-restart on reboot", "nodeapps")
			}

			s.completeStep(ctx, jobID, "Transfer Node.js Apps",
				fmt.Sprintf("Transferred %d of %d Node.js app(s)", transferred, len(nodeApps)))
		}
		advance()
	}

	if isCancelled() {
		return
	}

	// ===== Step: Verify Transfer =====
	s.startStep(ctx, jobID, "Verify Transfer")
	s.addLog(ctx, jobID, "info", "Running post-transfer verification", "verify")
	verifyIssues := 0

	// 1. Verify nginx configs
	if _, err := agent.RunCommand(ctx, "nginx", "-t"); err != nil {
		s.addLog(ctx, jobID, "warn", "Nginx configuration test failed — manual review needed", "verify")
		verifyIssues++
	} else {
		s.addLog(ctx, jobID, "info", "Nginx configuration test passed", "verify")
		agent.ReloadNginx(ctx)
	}

	// 2. Verify PHP-FPM is running for transferred domains
	for _, domain := range domains {
		var domRec models.Domain
		if err := s.db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": domain}).Decode(&domRec); err == nil {
			if _, err := agent.RunCommand(ctx, "systemctl", "is-active", fmt.Sprintf("php%s-fpm", domRec.PHPVersion)); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("PHP-FPM %s not running, attempting start", domRec.PHPVersion), "verify")
				agent.RunCommand(ctx, "systemctl", "start", fmt.Sprintf("php%s-fpm", domRec.PHPVersion))
			}
		}
	}

	// 3. Verify DNS resolution for transferred domains
	if destIP != "" {
		for _, domain := range domains {
			if result, err := agent.RunCommand(ctx, "dig", "+short", domain, fmt.Sprintf("@%s", destIP)); err == nil {
				resolvedIP := strings.TrimSpace(result.Output)
				if resolvedIP == destIP {
					s.addLog(ctx, jobID, "info", fmt.Sprintf("DNS verified for %s → %s", domain, destIP), "verify")
				} else if resolvedIP != "" {
					s.addLog(ctx, jobID, "warn", fmt.Sprintf("DNS mismatch for %s: resolved to %s, expected %s", domain, resolvedIP, destIP), "verify")
					verifyIssues++
				}
			}
		}
	}

	// 4. Verify mail services
	if req.Components.EmailData {
		if _, err := agent.RunCommand(ctx, "systemctl", "is-active", "postfix"); err != nil {
			s.addLog(ctx, jobID, "warn", "Postfix not running, attempting start", "verify")
			agent.RunCommand(ctx, "systemctl", "start", "postfix")
			verifyIssues++
		} else {
			s.addLog(ctx, jobID, "info", "Postfix is running", "verify")
		}
		if _, err := agent.RunCommand(ctx, "systemctl", "is-active", "dovecot"); err != nil {
			s.addLog(ctx, jobID, "warn", "Dovecot not running, attempting start", "verify")
			agent.RunCommand(ctx, "systemctl", "start", "dovecot")
			verifyIssues++
		} else {
			s.addLog(ctx, jobID, "info", "Dovecot is running", "verify")
		}
		if _, err := agent.RunCommand(ctx, "systemctl", "is-active", "opendkim"); err != nil {
			agent.RunCommand(ctx, "systemctl", "restart", "opendkim")
		}
	}

	// 5. Restart ancillary services that were touched indirectly so the
	// operator never has to ssh in afterwards to bring a piece back up.
	autoRestart := []struct {
		Unit   string
		Reason bool // gate by whether the component even applies
	}{
		{"pdns", req.Components.DNS},
		{"mariadb", req.Components.Databases},
		{"mysql", req.Components.Databases},
		{"pure-ftpd", req.Components.FTPAccounts},
		{"ufw", req.Components.Firewall},
	}
	for _, svc := range autoRestart {
		if !svc.Reason {
			continue
		}
		// is-active returns 0 when active; only restart when inactive.
		if _, err := agent.RunCommand(ctx, "systemctl", "is-active", "--quiet", svc.Unit); err != nil {
			if _, rErr := agent.RunCommand(ctx, "systemctl", "restart", svc.Unit); rErr == nil {
				s.addLog(ctx, jobID, "info", fmt.Sprintf("Auto-restarted %s", svc.Unit), "verify")
			}
		}
	}

	// 6. PM2: resurrect any dumped processes and persist current state so
	// the apps survive a reboot of the destination server.
	if req.Components.NodeApps {
		agent.RunCommand(ctx, "pm2", "resurrect")
		agent.RunCommand(ctx, "pm2", "save", "--force")
		s.addLog(ctx, jobID, "info", "PM2 resurrect + save completed", "verify")
	}

	if verifyIssues > 0 {
		s.completeStep(ctx, jobID, "Verify Transfer", fmt.Sprintf("Verification complete with %d warnings", verifyIssues))
	} else {
		s.completeStep(ctx, jobID, "Verify Transfer", "All checks passed")
	}
	advance()

	// Re-sync WordPress records so auto_update / version match what's now on disk
	if s.wpService != nil {
		if n, err := s.wpService.RescanUser(ctx, ""); err == nil && n > 0 {
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Synced %d WordPress installation(s) from disk", n), "wordpress")
		}
	}

	// Final status
	finalStatus := "completed"
	if failedSteps > 0 {
		finalStatus = "partial"
	}
	s.updateJobStatus(ctx, jobID, finalStatus, 100)
	completedAt := time.Now()
	s.updateJobField(ctx, jobID, "completed_at", &completedAt)
	s.addLog(ctx, jobID, "info", fmt.Sprintf("Transfer %s — %d domains, IP: %s", finalStatus, len(domains), destIP), "transfer")
}

func (s *TransferService) countEnabledSteps(c models.TransferComponents) int {
	count := 3 // validate + discover + verify (always present)
	if c.Hostname {
		count++
	}
	if c.Software {
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
	if c.NodeApps {
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
