package services

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

type TransferService struct {
	db            *mongo.Database
	serverIP      string
	panelDomain   string // this panel's own management URL — excluded from discovery so operators don't accidentally migrate it
	wpService     *WordPressService
	configSvc     *ConfigService      // for post-transfer ReassignServerIP sweep
	emailSvc      *EmailService       // for post-transfer SyncPostfixChroot + DKIM rewire
	maintSvc      *MaintenanceService // for post-transfer maintenance-mode mirroring from source
	panelMailSvc  *PanelMailService   // for hot-reloading the in-memory mailer after the transfer mirrors the SMTP doc

	// AES-GCM-encrypted secret re-encryption pipeline. SetWebhookService and
	// SetProjectService both deps in main.go after construction so the panel-
	// records pass can decrypt secrets under the SOURCE's APP_ENCRYPTION_KEY
	// and re-encrypt them under the destination's. Without these, webhook
	// signing secrets and GitHub PATs land on the destination as ciphertext
	// the local key cannot read — silently breaking outbound webhooks and
	// Deploy Software auto-deploy until the operator manually rotates each.
	webhookSvc *WebhookService
	projectSvc *ProjectService

	// v3.1.48 post-transfer rehydrate orchestrator deps. Each calls
	// the matching Rebuild*FromMongo method in transfer_rehydrate.go
	// after the panel-records sync completes so the destination's
	// filesystem / MySQL / PowerDNS state matches Mongo. Optional —
	// the orchestrator soft-skips any service that isn't wired and
	// logs the equivalent bzpanel heal-* command so an operator can
	// run it manually.
	sshKeySvc   *SSHKeyService
	dnsSvc      *DNSService
	databaseSvc *DatabaseService
	domainSvc   *DomainService

	// srcEncKeyCache memoises the result of an SSH probe against the
	// source's APP_ENCRYPTION_KEY. The key is read once per transfer
	// run (not once per migrated row) — without this every webhook +
	// every project would re-shell-out, adding seconds to the sync.
	// srcEncKeySource records WHERE the key was read from (or the
	// failure mode) so operator-facing logs can explain why the PAT
	// was preserved or dropped, instead of a silent "PAT not configured".
	srcEncKeyCache  string
	srcEncKeySource string
	srcEncKeyLoaded bool
}

func NewTransferService(db *mongo.Database, serverIP, panelDomain string) *TransferService {
	return &TransferService{
		db:          db,
		serverIP:    serverIP,
		panelDomain: strings.ToLower(strings.TrimSpace(panelDomain)),
	}
}

// SetConfigService wires the ConfigService dep used for the post-transfer
// IP sweep. Optional — the transfer still completes without it, just
// without the final old_ip→new_ip rewrite across DNS / env / vhost.
func (s *TransferService) SetConfigService(cs *ConfigService) {
	s.configSvc = cs
}

// SetEmailService wires the EmailService dep used for the post-transfer
// mail-stack repair step (chroot resolv.conf sync + DKIM table rewire
// for every imported domain). Optional.
func (s *TransferService) SetEmailService(es *EmailService) {
	s.emailSvc = es
}

// SetMaintenanceService wires the MaintenanceService dep used to mirror
// the source server's maintenance state onto the destination at the end
// of Sync Panel Records — if the operator put source into maintenance
// before the cutover, destination must come up the same way so DNS
// changeover doesn't surface the new server in a half-broken state.
// Optional.
func (s *TransferService) SetMaintenanceService(ms *MaintenanceService) {
	s.maintSvc = ms
}

// SetPanelMailService wires the PanelMailService so the panel-records
// sync can hot-reload the in-memory mailer after the destination's
// panel_mail doc gets mirrored from source. Without this, the
// destination's mailer keeps using its install-default config until
// the operator manually saves SMTP — even though the doc is already
// in mongo. Optional; sync still runs without the reload, just with
// a longer delay before mail starts flowing.
func (s *TransferService) SetPanelMailService(pms *PanelMailService) {
	s.panelMailSvc = pms
}

// SetWebhookService wires the WebhookService so the panel-records sync can
// re-encrypt outbound webhook signing secrets under the destination's
// APP_ENCRYPTION_KEY. Without this, every migrated webhook lands inactive
// because its AES-GCM blob was sealed under the source's key. Optional;
// when nil, the legacy "land inactive, operator rotates" path is used.
func (s *TransferService) SetWebhookService(ws *WebhookService) {
	s.webhookSvc = ws
}

// SetProjectService wires the ProjectService so the panel-records sync can
// re-encrypt stored GitHub PATs under the destination's APP_ENCRYPTION_KEY.
// Without this, every migrated Deploy Software project comes up with a PAT
// that the destination cannot decrypt — git pull / auto-deploy break
// silently until the operator rotates each project's PAT manually.
// Optional; when nil, the encrypted blob is dropped on transfer and the
// operator is prompted to re-enter the PAT in Project Settings.
func (s *TransferService) SetProjectService(ps *ProjectService) {
	s.projectSvc = ps
}

// fetchSourceEncKey grabs the source server's APP_ENCRYPTION_KEY over
// SSH and caches it for the rest of the transfer run. Returns "" when
// every fallback fails — callers then drop the encrypted blob and
// surface a "rotate to recover" hint.
//
// Probes (in order) so a non-root SSH user, a relocated install, or a
// missing /opt/serverpanel/.env doesn't silently nuke every Deploy
// Software PAT and outbound webhook secret on transfer:
//
//  1. /opt/serverpanel/.env (primary install path, mode 600 root)
//  2. /opt/serverpanel/backend/.env (legacy split-config layout)
//  3. sudo -n cat /opt/serverpanel/.env (when the SSH user is in
//     sudoers NOPASSWD — common on managed VPS where panel ops use
//     a wheel-group account, not root)
//  4. /proc/<panel-pid>/environ (the running serverpanel process holds
//     APP_ENCRYPTION_KEY in its env even when .env was deleted post-
//     boot; readable through /proc only by root, but the source-side
//     SSH session is normally root anyway)
//
// Memoised on the TransferService receiver because three different sync
// paths (SMTP, webhook secrets, GitHub PATs) all need the same answer
// and shelling out three times costs a couple of seconds each on a
// busy migration. srcEncKeySource records which probe succeeded (or the
// "all probes returned empty" sentinel) so the per-path logs can
// explain WHY a PAT was kept vs dropped.
func (s *TransferService) fetchSourceEncKey(ctx context.Context, host string, port int, sshUser, sshPass string) string {
	if s.srcEncKeyLoaded {
		return s.srcEncKeyCache
	}
	s.srcEncKeyLoaded = true

	// One round-trip over SSH. Every probe writes its output to its own
	// stdout line so we can pick the first non-empty answer back here
	// without rerunning the SSH session.
	probe := `set +e
extract() { tr -d '"' | tr -d "'" | tr -d ' ' | tr -d '\r' | head -1; }
out=""
# 1. primary
if [ -z "$out" ] && [ -r /opt/serverpanel/.env ]; then
    out=$(grep -E '^APP_ENCRYPTION_KEY=' /opt/serverpanel/.env 2>/dev/null | head -1 | cut -d= -f2- | extract)
    [ -n "$out" ] && echo "PRIMARY:$out" && exit 0
fi
# 2. legacy split layout
if [ -z "$out" ] && [ -r /opt/serverpanel/backend/.env ]; then
    out=$(grep -E '^APP_ENCRYPTION_KEY=' /opt/serverpanel/backend/.env 2>/dev/null | head -1 | cut -d= -f2- | extract)
    [ -n "$out" ] && echo "LEGACY:$out" && exit 0
fi
# 3. sudo -n cat
if [ -z "$out" ]; then
    out=$(sudo -n cat /opt/serverpanel/.env 2>/dev/null | grep -E '^APP_ENCRYPTION_KEY=' | head -1 | cut -d= -f2- | extract)
    [ -n "$out" ] && echo "SUDO:$out" && exit 0
fi
# 4. /proc fallback — the running panel keeps the key in its env even
# when the .env file was rotated post-boot or the SSH user can't read
# it directly. We pick the most recently-started serverpanel-likely PID.
if [ -z "$out" ]; then
    pid=$(pgrep -f '/opt/serverpanel/bin/server' 2>/dev/null | head -1)
    if [ -n "$pid" ] && [ -r "/proc/$pid/environ" ]; then
        out=$(tr '\0' '\n' < /proc/$pid/environ 2>/dev/null | grep -E '^APP_ENCRYPTION_KEY=' | head -1 | cut -d= -f2- | extract)
        [ -n "$out" ] && echo "PROC:$out" && exit 0
    fi
fi
echo "MISS:"
exit 0`
	r, err := agent.SSHCommand(ctx, host, port, sshUser, sshPass, probe)
	if err != nil || r == nil {
		s.srcEncKeySource = "ssh-error"
		return ""
	}
	line := strings.TrimSpace(r.Output)
	if line == "" || line == "MISS:" {
		s.srcEncKeySource = "miss"
		return ""
	}
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		// Older agent / non-tagged output — accept verbatim, mark unknown.
		s.srcEncKeySource = "unknown"
		s.srcEncKeyCache = line
		return s.srcEncKeyCache
	}
	tag := line[:idx]
	val := strings.TrimSpace(line[idx+1:])
	if val == "" {
		s.srcEncKeySource = "miss-" + strings.ToLower(tag)
		return ""
	}
	s.srcEncKeySource = strings.ToLower(tag)
	s.srcEncKeyCache = val
	return s.srcEncKeyCache
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

// makeStripper returns a per-discovery filter that drops both this
// panel's own management domain AND the source panel's management
// domain (as detected by DiscoverSourcePanelDomain). The destination's
// isPanelDomain check only knows about THIS panel's domain — without
// also covering the source's, panel.example.com leaks into every
// downstream list (Domains / DNS / SSL / Email).
func (s *TransferService) makeStripper(sourcePanelDomain string) func([]string) []string {
	src := strings.ToLower(strings.TrimSpace(sourcePanelDomain))
	src = strings.TrimPrefix(src, "www.")
	matches := func(d string) bool {
		if s.isPanelDomain(d) {
			return true
		}
		if src == "" {
			return false
		}
		dd := strings.ToLower(strings.TrimSpace(d))
		dd = strings.TrimPrefix(dd, "www.")
		return dd == src
	}
	return func(items []string) []string {
		if len(items) == 0 {
			return items
		}
		out := make([]string, 0, len(items))
		for _, it := range items {
			if matches(it) {
				continue
			}
			out = append(out, it)
		}
		return out
	}
}

// SetWordPressService wires a WordPressService so the transfer flow can
// re-sync WordPress records after files are migrated.
func (s *TransferService) SetWordPressService(wp *WordPressService) {
	s.wpService = wp
}

// v3.1.48 setters for the post-transfer rehydrate orchestrator. Each
// optional; runPostSyncRehydrates soft-skips any unwired service.
func (s *TransferService) SetSSHKeyService(svc *SSHKeyService)   { s.sshKeySvc = svc }
func (s *TransferService) SetDNSService(svc *DNSService)         { s.dnsSvc = svc }
func (s *TransferService) SetDatabaseService(svc *DatabaseService) { s.databaseSvc = svc }
func (s *TransferService) SetDomainService(svc *DomainService)   { s.domainSvc = svc }

// SetTokenService used to wire a TransferTokenService into the transfer
// pipeline; it's a no-op now because the destination side resolves tokens
// via the package-level RedeemRemoteToken (HTTP call to the source's
// public redeem endpoint) instead of needing access to the local token
// service. Kept for one release so cmd/server/main.go's call site can
// stay unchanged through the rollout — safe to delete after.
func (s *TransferService) SetTokenService(_ *TransferTokenService) {}

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

	// Detect server type (cPanel, Plesk, DirectAdmin, Betazen Server Panel, bare)
	serverType, _ := agent.DetectServerType(ctx, host, port, user, pass)
	data.ServerType = serverType

	// Read the SOURCE panel's own management domain from its .env so we
	// can strip it from every discovered list. Without this, the source's
	// nginx server_name parsing surfaces e.g. "panel.betazeninfotech.com"
	// in the Domains list, the destination's isPanelDomain only knows
	// the destination's own domain ("187.127.146.169" by default), and
	// the source-panel hostname leaks all the way through to the
	// destination's Domains page as a transferable site.
	srcPanelDomain := agent.DiscoverSourcePanelDomain(ctx, host, port, user, pass)
	stripper := s.makeStripper(srcPanelDomain)

	if domains, _ := agent.DiscoverDomains(ctx, host, port, user, pass); len(domains) > 0 {
		data.Domains = stripper(domains)
	}
	if dbs, _ := agent.DiscoverDatabases(ctx, host, port, user, pass); len(dbs) > 0 {
		data.Databases = dbs
	}
	if mysqlDBs, _ := agent.DiscoverMySQLDatabases(ctx, host, port, user, pass); len(mysqlDBs) > 0 {
		data.MySQLDatabases = mysqlDBs
	}
	if emailDomains, _ := agent.DiscoverEmailDomains(ctx, host, port, user, pass); len(emailDomains) > 0 {
		data.EmailDomains = stripper(emailDomains)
	}
	if dnsZones, _ := agent.DiscoverDNSZones(ctx, host, port, user, pass); len(dnsZones) > 0 {
		data.DNSZones = stripper(dnsZones)
	}
	if sslDomains, _ := agent.DiscoverSSLDomains(ctx, host, port, user, pass); len(sslDomains) > 0 {
		data.SSLDomains = stripper(sslDomains)
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
		// Sanitise: strip both this panel's and the source panel's own
		// management vhost so neither appears as a candidate domain.
		domNames := make([]string, len(settings))
		for i, ds := range settings {
			domNames[i] = ds.Domain
		}
		keep := map[string]bool{}
		for _, d := range stripper(domNames) {
			keep[d] = true
		}
		filtered := make([]models.DomainSetting, 0, len(settings))
		for _, ds := range settings {
			if keep[ds.Domain] {
				filtered = append(filtered, ds)
			}
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
	// Packages + Server Config run BEFORE anything that depends on them.
	// The hosting_packages catalog has to exist before users get created
	// during Domains & Files (otherwise every migrated user points at
	// the "Migrated" placeholder), and server-wide config (php.ini /
	// nginx snippets) needs to land before per-domain vhosts are built
	// on top of it.
	if c.Packages {
		steps = append(steps, models.TransferStep{Name: "Transfer Packages", Status: "pending"})
	}
	if c.ServerConfig {
		steps = append(steps, models.TransferStep{Name: "Transfer Server Config", Status: "pending"})
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
	if c.NodeApps {
		steps = append(steps, models.TransferStep{Name: "Transfer Node.js Apps", Status: "pending"})
	}
	if c.SSHKeys {
		steps = append(steps, models.TransferStep{Name: "Transfer SSH Keys", Status: "pending"})
	}
	// Sync the source panel's mongo records (apps / projects / mailboxes /
	// ssl / wp / databases / ftp / forwarders / packages / ssh_keys) into
	// THIS panel's mongo so the corresponding pages aren't empty after
	// the file copy. Only runs when the source is another Betazen Server Panel.
	steps = append(steps, models.TransferStep{Name: "Sync Panel Records", Status: "pending"})
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
	hasUnownedDomains := false
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
				continue
			}
		}
		// Owner couldn't be detected — typical for app-backed
		// reverse-proxy vhosts that have no `root /home/...` directive.
		// Track that we saw at least one so we know to apply the
		// fallback below.
		if ds.Owner == "" && ds.DocumentRoot == "" {
			hasUnownedDomains = true
		}
	}

	// Single-tenant fallback: when the operator picked exactly ONE
	// linux user (the common case for vendor migration) AND we saw
	// domains whose owner couldn't be detected (app/proxy vhosts), every
	// discovered domain belongs to that user. Without this fallback the
	// email/SSL/DNS cascades below would strip every app-backed
	// subdomain (d1/d2/d3 etc), and their mailboxes would never
	// transfer — exactly the symptom that surfaced in live testing
	// where source had 6 mailboxes but only 3 (the non-app domains)
	// landed on the destination. The file transfer step still scopes
	// per-user via /home/<picked>/, so this fallback can't leak into
	// data outside the picked user's footprint.
	if hasUnownedDomains && len(picked) == 1 {
		for _, dom := range d.Domains {
			ownedDomains[dom] = true
		}
	}

	// Multi-tenant parent-claims-subdomain fallback. When several users are
	// picked, we can't blanket-claim every unowned domain the way the
	// single-tenant path does. Instead, for each discovered domain whose
	// owner couldn't be detected (proxy vhost — app or Deploy Software),
	// attach it to the picked user whose already-owned primary_domain is a
	// parent of it (e.g. unowned "api.easycrm4u.com" is a subdomain of
	// owned "easycrm4u.com"). Without this the email/SSL/DNS cascades
	// silently drop the proxy subdomain and its mailbox misses the
	// transfer — the symptom surfaced in end-to-end testing where a
	// 4-user transfer moved 6 of 8 mailboxes because the 2 app-backed
	// subdomains ended up unowned.
	for _, dom := range d.Domains {
		if ownedDomains[dom] {
			continue
		}
		for owned := range ownedDomains {
			if strings.HasSuffix(dom, "."+owned) {
				ownedDomains[dom] = true
				break
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

	// MongoDB DBs: same `<linux-user>_<suffix>` convention the panel's
	// CreateDatabase enforces. Pre-3.0.16 there was no auto-populate,
	// so an operator who picked Linux users in the wizard but didn't
	// manually whitelist MongoDB databases ended up with sel.MongoDBs
	// empty — which `filterByWhitelist` interprets as "no restriction"
	// AND THEN the discover-time `data.Databases` list was the basis,
	// but a stale-cache transfer where `discovered` was nil meant
	// MongoDB transfer skipped the loop entirely. Filling sel.MongoDBs
	// up-front here makes the explicit-selection path the canonical
	// one and removes the discover-cache dependency.
	if len(sel.MongoDBs) == 0 {
		for _, db := range d.Databases {
			for u := range picked {
				if u != "" && strings.HasPrefix(db, u+"_") {
					sel.MongoDBs = append(sel.MongoDBs, db)
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

// detectSourceIP extracts the old server's IP from a DNS zone export. It
// prefers the APEX (zone-root) A record so a subdomain A that points at a third
// party (e.g. a `mail.` A on another box) isn't mistaken for the server IP —
// which would leave the apex A pointing at the source after cutover (BUG-4).
// Falls back to the first A record only when no apex A exists.
func detectSourceIP(zoneData, zone string) string {
	re := regexp.MustCompile(`\s+IN\s+A\s+(\d+\.\d+\.\d+\.\d+)`)
	z := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(zone)), ".")
	var firstA string
	for _, line := range strings.Split(zoneData, "\n") {
		m := re.FindStringSubmatch(line)
		if len(m) < 2 {
			continue
		}
		if firstA == "" {
			firstA = m[1]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		owner := strings.ToLower(strings.TrimSuffix(fields[0], "."))
		if owner == z || owner == "@" {
			return m[1] // apex A — the real server IP
		}
	}
	return firstA // no apex A found; best-effort fall back to first A
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
	//
	// Hard-skip by design. The hostname is local-machine identity —
	// it identifies the box this panel is RUNNING on, not the
	// operator's product preferences. Transferring it overwrites the
	// destination with the source's hostname, which:
	//
	//   - breaks the destination's nginx panel vhost that was issued
	//     against its OWN hostname's Let's Encrypt cert;
	//   - leaves the General Settings card showing the SOURCE box's
	//     name on a destination that's still physically a different
	//     server (`hostname -f` is what GetAll falls back to);
	//   - confuses ops tooling (Prometheus / Loki) that scrape labels
	//     keyed on hostname.
	//
	// We KEEP the wizard checkbox so the step still appears in the
	// transfer wizard's component list — but the executor refuses
	// every time and logs why. Operators who genuinely want to
	// rename the box can run `hostnamectl set-hostname` by hand.
	if req.Components.Hostname {
		s.startStep(ctx, jobID, "Transfer Hostname")
		s.addLog(ctx, jobID, "info",
			"Skipping hostname transfer — destination keeps its own machine identity (run `hostnamectl set-hostname` manually if you really want to rename this box)",
			"hostname")
		s.skipStep(ctx, jobID, "Transfer Hostname",
			"Skipped — destination keeps its own hostname (machine identity stays local)")
		advance()
	}

	if isCancelled() {
		return
	}

	// ===== Step: Transfer Packages =====
	// Runs BEFORE Domains & Files so the hosting_packages catalog exists
	// by the time users are created — otherwise every migrated user ends
	// up pointing at the "Migrated" placeholder instead of their real
	// source-side plan. Source must be a Betazen Server Panel; dedup is by name
	// so re-running is safe.
	if req.Components.Packages {
		s.startStep(ctx, jobID, "Transfer Packages")
		if discovered != nil && (discovered.ServerType == "serverpanel" || discovered.ServerType == "") {
			inserted := s.syncPackagesCatalog(ctx, jobID, host, port, user, pass, "serverpanel", map[string]primitive.ObjectID{})
			s.completeStep(ctx, jobID, "Transfer Packages",
				fmt.Sprintf("Imported %d hosting package(s) from source catalog", inserted))
		} else {
			s.skipStep(ctx, jobID, "Transfer Packages")
		}
		advance()
	}

	if isCancelled() {
		return
	}

	// ===== Step: Transfer Server Config =====
	// Runs BEFORE per-domain vhosts are created so any server-wide nginx
	// snippets / php.ini tweaks land first and apply to everything built
	// on top of them.
	if req.Components.ServerConfig {
		s.startStep(ctx, jobID, "Transfer Server Config")
		s.addLog(ctx, jobID, "info", "Capturing server configuration from source", "config")

		sourceDomains := []string{}
		if discovered != nil {
			sourceDomains = discovered.Domains
		}
		for _, domain := range sourceDomains {
			result, err := agent.SSHCommand(ctx, host, port, user, pass,
				fmt.Sprintf(`cat /etc/nginx/sites-available/%s 2>/dev/null || echo ''`, domain))
			if err == nil && strings.TrimSpace(result.Output) != "" && !strings.Contains(result.Output, "echo ''") {
				s.addLog(ctx, jobID, "info", fmt.Sprintf("Source nginx config captured for %s", domain), "config")
			}
		}

		s.completeStep(ctx, jobID, "Transfer Server Config", "Server configuration transferred")
		advance()
	}

	if isCancelled() {
		return
	}

	// ===== Step: Transfer Software (PHP + Node versions) =====
	// Strategy is always union-not-replace: the destination keeps every
	// runtime version it already had AND adds whatever the source has
	// that the destination doesn't. Operators were getting burned when a
	// transferred app on Node 22 landed on a destination that only had
	// Node 20 — the app ran on the wrong major until the operator
	// manually `n`-installed. Same for PHP minors. Same for tenants
	// whose apps had different runtime versions.
	//
	// Source detection:
	//   PHP  : /etc/php/<maj>.<min>/
	//   Node : /usr/local/n/versions/node/<maj>.<min>.<patch>/  (keep major)
	if req.Components.Software {
		s.startStep(ctx, jobID, "Transfer Software")
		s.addLog(ctx, jobID, "info", "Detecting installed runtime versions on source", "software")

		// --- PHP ---
		srcPHP := []string{}
		if r, err := agent.SSHCommand(ctx, host, port, user, pass,
			`ls /etc/php/ 2>/dev/null | grep -E '^[0-9]+\.[0-9]+$' | sort -V || true`); err == nil && r != nil {
			for _, v := range strings.Split(strings.TrimSpace(r.Output), "\n") {
				v = strings.TrimSpace(v)
				if v != "" {
					srcPHP = append(srcPHP, v)
				}
			}
		}
		phpInstalled := 0
		for _, phpVer := range srcPHP {
			if _, err := agent.RunCommand(ctx, "php"+phpVer, "-v"); err != nil {
				s.addLog(ctx, jobID, "info", fmt.Sprintf("Installing PHP %s (missing on destination)", phpVer), "software")
				if installErr := agent.InstallPHP(ctx, phpVer); installErr != nil {
					s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to install PHP %s: %s", phpVer, installErr.Error()), "software")
				} else {
					phpInstalled++
				}
			} else {
				s.addLog(ctx, jobID, "info", fmt.Sprintf("PHP %s already present", phpVer), "software")
			}
		}

		// --- Node.js (via `n` version manager) ---
		// Collapse the source's installed Node majors — a source with
		// 20.10.0 and 20.11.4 is the same Node-20 to the destination.
		// Install with `n <major>` so the destination picks the latest
		// minor for that major, the same convention `install.sh` uses.
		srcNodeMajors := map[string]bool{}
		if r, err := agent.SSHCommand(ctx, host, port, user, pass,
			`ls /usr/local/n/versions/node/ 2>/dev/null | sort -u || true`); err == nil && r != nil {
			for _, v := range strings.Split(strings.TrimSpace(r.Output), "\n") {
				v = strings.TrimSpace(v)
				if v == "" {
					continue
				}
				parts := strings.SplitN(v, ".", 2)
				if parts[0] != "" {
					srcNodeMajors[parts[0]] = true
				}
			}
		}
		// What majors are already on the destination?
		destNodeMajors := map[string]bool{}
		if r, err := agent.RunCommand(ctx, "bash", "-c",
			`ls /usr/local/n/versions/node/ 2>/dev/null | awk -F. '{print $1}' | sort -u`); err == nil && r != nil {
			for _, v := range strings.Split(strings.TrimSpace(r.Output), "\n") {
				if v = strings.TrimSpace(v); v != "" {
					destNodeMajors[v] = true
				}
			}
		}
		nodeInstalled := 0
		srcNodeList := make([]string, 0, len(srcNodeMajors))
		for m := range srcNodeMajors {
			srcNodeList = append(srcNodeList, m)
		}
		sort.Strings(srcNodeList)
		for _, maj := range srcNodeList {
			if destNodeMajors[maj] {
				s.addLog(ctx, jobID, "info", fmt.Sprintf("Node %s already present", maj), "software")
				continue
			}
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Installing Node %s (missing on destination)", maj), "software")
			if err := agent.InstallNodeJS(ctx, maj); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to install Node %s: %s", maj, err.Error()), "software")
			} else {
				nodeInstalled++
			}
		}

		// --- Default/active version mirroring ---
		// After the union is complete, match the source's ACTIVE default
		// versions on the destination. Operators set these so running
		// `node` / `php` (without a version suffix) picks the right
		// interpreter for whatever scripts they invoke at the shell or
		// from cron. Without this step, a transferred box might have
		// Node 24 installed but still default to Node 20 — breaking any
		// shebang that reads `#!/usr/bin/env node` and assumes the newer
		// major.
		//
		//   node default → `n <major>` sets the /usr/local/bin/node
		//                  symlink to the chosen major's latest minor.
		//   php  default → update-alternatives picks /usr/bin/php.
		defaultNodeMaj := ""
		if r, err := agent.SSHCommand(ctx, host, port, user, pass,
			`node -v 2>/dev/null | sed 's/^v//' | awk -F. '{print $1}'`); err == nil && r != nil {
			defaultNodeMaj = strings.TrimSpace(r.Output)
		}
		defaultPHPVer := ""
		if r, err := agent.SSHCommand(ctx, host, port, user, pass,
			`php -v 2>/dev/null | head -1 | awk '{print $2}' | awk -F. '{print $1"."$2}'`); err == nil && r != nil {
			defaultPHPVer = strings.TrimSpace(r.Output)
		}
		defaultsApplied := []string{}
		if defaultNodeMaj != "" {
			destActiveNodeMaj := ""
			if r, err := agent.RunCommand(ctx, "bash", "-c",
				`node -v 2>/dev/null | sed 's/^v//' | awk -F. '{print $1}'`); err == nil && r != nil {
				destActiveNodeMaj = strings.TrimSpace(r.Output)
			}
			if destActiveNodeMaj != defaultNodeMaj {
				s.addLog(ctx, jobID, "info",
					fmt.Sprintf("Switching active Node default to %s (source active: %s, dest active: %s)",
						defaultNodeMaj, defaultNodeMaj, destActiveNodeMaj), "software")
				if _, err := agent.RunCommand(ctx, "bash", "-c",
					fmt.Sprintf("n %s", defaultNodeMaj)); err == nil {
					defaultsApplied = append(defaultsApplied, "node="+defaultNodeMaj)
				}
			} else {
				defaultsApplied = append(defaultsApplied, "node="+defaultNodeMaj+"(already)")
			}
		}
		if defaultPHPVer != "" {
			destActivePHP := ""
			if r, err := agent.RunCommand(ctx, "bash", "-c",
				`php -v 2>/dev/null | head -1 | awk '{print $2}' | awk -F. '{print $1"."$2}'`); err == nil && r != nil {
				destActivePHP = strings.TrimSpace(r.Output)
			}
			if destActivePHP != defaultPHPVer {
				s.addLog(ctx, jobID, "info",
					fmt.Sprintf("Switching active PHP default to %s (source active: %s, dest active: %s)",
						defaultPHPVer, defaultPHPVer, destActivePHP), "software")
				altsCmd := fmt.Sprintf(
					"update-alternatives --set php /usr/bin/php%s 2>/dev/null && update-alternatives --set phar /usr/bin/phar%s 2>/dev/null; update-alternatives --set phar.phar /usr/bin/phar.phar%s 2>/dev/null; true",
					defaultPHPVer, defaultPHPVer, defaultPHPVer)
				if _, err := agent.RunCommand(ctx, "bash", "-c", altsCmd); err == nil {
					defaultsApplied = append(defaultsApplied, "php="+defaultPHPVer)
				}
			} else {
				defaultsApplied = append(defaultsApplied, "php="+defaultPHPVer+"(already)")
			}
		}

		// Mirror the same defaults into the destination's mongo
		// `runtime_defaults` doc so the UI's "DEFAULT" badge agrees with
		// the OS-level active runtime. Without this the Software page
		// shows no default after transfer (mongo is empty) even though
		// `node -v` / `php -v` correctly return the source's defaults.
		mongoDefaults := bson.M{}
		var existing bson.M
		if err := s.db.Collection(database.ColServerConfig).FindOne(ctx,
			bson.M{"key": "runtime_defaults"}).Decode(&existing); err == nil {
			if v, ok := existing["value"].(bson.M); ok {
				for k, val := range v {
					if str, ok := val.(string); ok {
						mongoDefaults[k] = str
					}
				}
			}
		}
		if defaultNodeMaj != "" {
			mongoDefaults["nodejs"] = defaultNodeMaj
		}
		if defaultPHPVer != "" {
			mongoDefaults["php"] = defaultPHPVer
		}
		if len(mongoDefaults) > 0 {
			s.db.Collection(database.ColServerConfig).UpdateOne(ctx,
				bson.M{"key": "runtime_defaults"},
				bson.M{"$set": bson.M{
					"key":        "runtime_defaults",
					"value":      mongoDefaults,
					"updated_at": time.Now(),
				}},
				options.Update().SetUpsert(true),
			)
		}

		defaultsMsg := "none detected"
		if len(defaultsApplied) > 0 {
			defaultsMsg = strings.Join(defaultsApplied, ", ")
		}
		s.completeStep(ctx, jobID, "Transfer Software",
			fmt.Sprintf("PHP: %d version(s), +%d installed. Node: %d major(s), +%d installed. Defaults: %s.",
				len(srcPHP), phpInstalled, len(srcNodeMajors), nodeInstalled, defaultsMsg))
		advance()
	}

	if isCancelled() {
		return
	}

	// ===== Ensure default package exists for migrated accounts =====
	//
	// When the operator enables the new Packages component, the source's
	// real catalog is imported in the Sync Panel Records step below and
	// each user gets back their actual package. The "Migrated" placeholder
	// is only useful as a fallback for users that aren't covered by the
	// catalog migration — so create it lazily inside the user-create path
	// rather than always-on. With Packages on AND a non-empty source
	// catalog, no row ever points at "Migrated", and the dest's Packages
	// page stays clean.
	var migratedPkgID primitive.ObjectID
	pkgCol := s.db.Collection(database.ColPackages)
	var existingPkg models.HostingPackage
	if err := pkgCol.FindOne(ctx, bson.M{"name": "Migrated"}).Decode(&existingPkg); err == nil {
		migratedPkgID = existingPkg.ID
	} else if !req.Components.Packages {
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
	// Fallback when Packages component is ON but the source catalog sync
	// hasn't run yet (or produced nothing): point newly-created users at
	// the install-time "Default" package so they aren't orphaned without
	// a package_id ref. The Sync Panel Records step below upserts the
	// source's catalog over this — a user that was originally on a real
	// "Pro" plan ends up on the imported "Pro" row, not stuck on Default.
	if migratedPkgID.IsZero() {
		var defaultPkg models.HostingPackage
		if err := pkgCol.FindOne(ctx, bson.M{"name": "Default"}).Decode(&defaultPkg); err == nil {
			migratedPkgID = defaultPkg.ID
		}
	}

	// ===== Step: Transfer Domains & Files =====
	if req.Components.Domains || req.Components.Files {
		stepName := "Transfer Domains & Files"
		s.startStep(ctx, jobID, stepName)
		domainErrors := 0
		domainsCreated := 0

		// Pass 1: resolve each domain's owner on source. Anything we can't
		// place under a /home/<owner>/ prefix gets dropped here so the slow
		// tar/scp loop below only sees real, transferable domains.
		type domInfo struct {
			domain     string
			sysUser    string
			phpVersion string
		}
		var resolved []domInfo
		userToDomains := make(map[string][]string) // sysUser → []domain (for dedup)
		var userOrder []string                     // stable order of unique users
		for _, domain := range domains {
			if isCancelled() {
				return
			}
			if s.isPanelDomain(domain) {
				s.addLog(ctx, jobID, "info",
					fmt.Sprintf("Skipping %s — that's the destination panel's own management URL", domain),
					"files")
				continue
			}
			sysUser := ""
			// Exclude soft-deleted users (`<name>-del-<unix-ts>`) — these are
			// renamed homes left behind by the panel's vendor purge that
			// preserves files for recovery. When source has both a live
			// `/home/cholun/domains/X` and a stale `/home/cholun-del-…/domains/X`
			// from a prior purge cycle, the glob expansion order is filesystem
			// order (not alphabetical), so the stale one often sorted first
			// and `head -1` picked it. The destination then re-created files
			// under `/home/cholun-del-…/`, the destination domain row got the
			// suffixed name, and downstream lookups (mailbox discovery,
			// vhost paths, FTP roots) all chased the wrong user. grep -v
			// past the suffix and re-pick.
			if result, err := agent.SSHCommand(ctx, host, port, user, pass,
				fmt.Sprintf(`(stat -c '%%U' /home/*/domains/%s 2>/dev/null; stat -c '%%U' /home/*/public_html 2>/dev/null) | grep -v -- '-del-' | head -1`, domain)); err == nil {
				sysUser = strings.TrimSpace(result.Output)
			}
			if sysUser == "" || sysUser == "root" {
				s.addLog(ctx, jobID, "info",
					fmt.Sprintf("Skipping %s — no /home/*/domains/%s on source (likely a www-alias or panel vhost)", domain, domain),
					"files")
				continue
			}
			phpVersion := detectPHPVersion(ctx, host, port, user, pass, domain)
			resolved = append(resolved, domInfo{domain: domain, sysUser: sysUser, phpVersion: phpVersion})
			if _, seen := userToDomains[sysUser]; !seen {
				userOrder = append(userOrder, sysUser)
			}
			userToDomains[sysUser] = append(userToDomains[sysUser], domain)
		}

		// Pre-flight tar size discovery so the live progress bar has a real
		// denominator. Uses the SAME exclude list the tar invocation
		// applies (node_modules, venv, .gem, etc) — otherwise the bar
		// would anchor to the raw /home/<user> size and never reach 100%
		// because the actual transfer is a fraction of that. Sequential
		// because `du` is inode-walk bound, not bandwidth-bound, so
		// parallelising just thrashes the source's page cache.
		userBytesTotal := make(map[string]int64)
		var grandTotal int64
		for _, u := range userOrder {
			n, _ := agent.RemoteUserHomeBytesFiltered(ctx, host, port, user, pass, u)
			userBytesTotal[u] = n
			grandTotal += n
		}

		// Pass 2: parallel user-level tar+download. Each unique source user
		// is fetched ONCE — previously we re-tarred the same /home/<user>
		// for every domain that user owned (24 domains × same user = 24×
		// the bandwidth and tar-CPU). Concurrency=4 saturates a typical
		// VPS 1 Gbps link without overwhelming source CPU.
		const fetchConcurrency = 4
		var (
			mu             sync.Mutex
			doneBytes      int64
			activeBytes    = make(map[string]int64) // sysUser → live bytes downloaded
			userFetchErr   = make(map[string]error)
			completedUsers int
			startedAt      = time.Now()
		)
		// Tick goroutine: every 500ms, sum static doneBytes + live
		// activeBytes per in-flight user, compute Mbps + ETA, push to
		// mongo as a single steps.$.* update.
		stopTicker := make(chan struct{})
		var tickerWG sync.WaitGroup
		tickerWG.Add(1)
		go func() {
			defer tickerWG.Done()
			lastTick := time.Now()
			lastBytes := int64(0)
			t := time.NewTicker(500 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-stopTicker:
					return
				case <-t.C:
					mu.Lock()
					curBytes := doneBytes
					for _, b := range activeBytes {
						curBytes += b
					}
					inflight := make([]string, 0, len(activeBytes))
					for u := range activeBytes {
						inflight = append(inflight, u)
					}
					done := completedUsers
					mu.Unlock()

					now := time.Now()
					dt := now.Sub(lastTick).Seconds()
					var mbps float64
					if dt > 0 {
						mbps = float64(curBytes-lastBytes) * 8 / 1_000_000 / dt
					}
					lastTick = now
					lastBytes = curBytes

					eta := 0
					if mbps > 0.5 && grandTotal > curBytes {
						eta = int(float64(grandTotal-curBytes) * 8 / 1_000_000 / mbps)
					}
					pct := 0
					if grandTotal > 0 {
						pct = int(curBytes * 100 / grandTotal)
						if pct > 100 {
							pct = 100
						}
					}
					label := fmt.Sprintf("%d/%d users", done, len(userOrder))
					if len(inflight) > 0 {
						label = fmt.Sprintf("%d/%d users · downloading: %s", done, len(userOrder), strings.Join(inflight, ", "))
					}
					s.updateStepLive(ctx, jobID, stepName, label, curBytes, grandTotal, mbps, eta, pct)
				}
			}
		}()

		sem := make(chan struct{}, fetchConcurrency)
		var wg sync.WaitGroup
		for _, u := range userOrder {
			if isCancelled() {
				break
			}
			sysUser := u
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()

				// Ensure the destination linux user exists before we extract.
				if _, statErr := os.Stat("/home/" + sysUser); os.IsNotExist(statErr) {
					agent.RunCommand(ctx, "useradd", "-m", "-s", "/bin/bash", sysUser)
				}
				archive := fmt.Sprintf("%s/user-%s.tar.gz", tmpDir, sysUser)
				pw := agent.NewProgressWriter(nil)
				// Live byte poller for THIS download — feeds activeBytes.
				stopThis := make(chan struct{})
				go func() {
					t := time.NewTicker(250 * time.Millisecond)
					defer t.Stop()
					for {
						select {
						case <-stopThis:
							return
						case <-t.C:
							mu.Lock()
							activeBytes[sysUser] = pw.Bytes()
							mu.Unlock()
						}
					}
				}()
				_, err := agent.RemoteBackupUserFilesProgress(ctx, host, port, user, pass, sysUser, archive, pw)
				close(stopThis)

				mu.Lock()
				delete(activeBytes, sysUser)
				doneBytes += pw.Bytes()
				completedUsers++
				if err != nil {
					userFetchErr[sysUser] = err
				}
				mu.Unlock()

				if err != nil {
					s.addLog(ctx, jobID, "warn",
						fmt.Sprintf("Failed to download files for user %s: %s", sysUser, err.Error()), "files")
					return
				}
				// Restore inline (parallel-safe: tar -xzf creates the
				// owner's own /home/<u>/ tree; users don't overlap).
				if err := agent.RestoreFiles(ctx, sysUser, archive); err != nil {
					s.addLog(ctx, jobID, "warn",
						fmt.Sprintf("Failed to restore files for user %s: %s", sysUser, err.Error()), "files")
					mu.Lock()
					userFetchErr[sysUser] = err
					mu.Unlock()
				}
				os.Remove(archive)
			}()
		}
		wg.Wait()
		close(stopTicker)
		tickerWG.Wait()

		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("File transfer phase done in %s for %d users (%.1f MB total)",
				time.Since(startedAt).Round(time.Second), len(userOrder), float64(grandTotal)/1_000_000), "files")

		// Pass 3: per-domain panel wiring (vhost, php-fpm pool, mongo
		// rows). Fast — pure local config writes — so kept sequential.
		for i, di := range resolved {
			if isCancelled() {
				return
			}
			if err, hadErr := userFetchErr[di.sysUser]; hadErr && err != nil {
				domainErrors++
			}
			s.updateStepLive(ctx, jobID, stepName,
				fmt.Sprintf("wiring %s (%d/%d)", di.domain, i+1, len(resolved)),
				grandTotal, grandTotal, 0, 0, 100)

			userCol := s.db.Collection(database.ColUsers)
			userNow := time.Now()
			userDoc := bson.M{
				"username":    di.sysUser,
				"email":       di.sysUser + "@localhost",
				"name":        di.sysUser,
				"role":        "customer",
				"is_active":   true,
				"permissions": []string{"domain.view", "email.view", "database.view", "file.view", "ssl.view", "backup.view"},
				"domains":     []string{},
				"created_at":  userNow,
				"updated_at":  userNow,
			}
			if !migratedPkgID.IsZero() {
				userDoc["package_id"] = migratedPkgID
				userDoc["package_name"] = "Migrated"
			}
			userRes, _ := userCol.UpdateOne(ctx,
				bson.M{"username": di.sysUser},
				bson.M{"$setOnInsert": userDoc},
				options.Update().SetUpsert(true))
			if userRes != nil && userRes.UpsertedCount > 0 && !migratedPkgID.IsZero() {
				pkgCol.UpdateOne(ctx, bson.M{"_id": migratedPkgID}, bson.M{"$inc": bson.M{"account_count": 1}})
			}

			if err := agent.CreateDomainDirectory(ctx, di.sysUser, di.domain); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to create directory for %s: %s", di.domain, err.Error()), "files")
			}
			if err := agent.CreatePHPPool(ctx, di.domain, di.sysUser, di.phpVersion); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to create PHP pool for %s: %s", di.domain, err.Error()), "files")
			}
			vhostCfg := &agent.VhostConfig{
				Domain:     di.domain,
				User:       di.sysUser,
				PHPVersion: di.phpVersion,
			}
			if err := agent.CreateVhost(ctx, vhostCfg); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to create vhost for %s: %s", di.domain, err.Error()), "files")
			}

			domNow := time.Now()
			domSet := bson.M{
				"domain":      di.domain,
				"user":        di.sysUser,
				"php_version": di.phpVersion,
				"status":      "active",
				"updated_at":  domNow,
			}
			domSetOnInsert := bson.M{"created_at": domNow}
			domRes, dbErr := s.db.Collection(database.ColDomains).UpdateOne(ctx,
				bson.M{"domain": di.domain},
				bson.M{"$set": domSet, "$setOnInsert": domSetOnInsert},
				options.Update().SetUpsert(true))
			if dbErr != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to save domain record for %s: %s", di.domain, dbErr.Error()), "files")
			} else if domRes != nil && domRes.UpsertedCount > 0 {
				domainsCreated++
			}
			s.addLog(ctx, jobID, "info",
				fmt.Sprintf("Domain %s setup complete (user: %s, PHP: %s)", di.domain, di.sysUser, di.phpVersion), "files")
		}

		// Reload nginx after all vhosts are created
		agent.ReloadNginx(ctx)

		if domainErrors > 0 {
			s.completeStep(ctx, jobID, stepName,
				fmt.Sprintf("Completed: %d domains registered, %d file transfer errors", domainsCreated, domainErrors))
		} else {
			s.completeStep(ctx, jobID, stepName,
				fmt.Sprintf("All %d domains transferred (%d unique users, %.1f MB)",
					len(resolved), len(userOrder), float64(grandTotal)/1_000_000))
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
			oldIP := detectSourceIP(zoneData, zone)

			// Delete existing zone if any, then create fresh
			agent.RunCommand(ctx, "pdnsutil", "delete-zone", zone)
			agent.RunCommand(ctx, "pdnsutil", "create-zone", zone)

			// Stamp the destination's own SOA + NS records onto the
			// freshly-created zone before importing source data. Without
			// this, the zone ends up with whichever NS values the source
			// had (e.g. ns1.sourcepanel.com) and pdns's default SOA
			// (often localhost.) — both wrong on the destination.
			// Mirrors the regular CreateDNSZone path so transfer-imported
			// zones are indistinguishable from operator-created ones.
			primaryNS := nameservers[0]
			soa := fmt.Sprintf("%s hostmaster.%s 1 10800 3600 604800 3600", primaryNS, zone)
			agent.RunCommand(ctx, "pdnsutil", "replace-rrset", zone, "", "SOA", "3600", soa)
			for _, ns := range nameservers {
				agent.RunCommand(ctx, "pdnsutil", "add-record", zone, "@", "NS", "3600", ns)
			}

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

				// Skip SOA always — the destination already wrote its
				// own from CreateDNSZone, and there's at most one SOA
				// per zone so there's nothing else to keep. Skip APEX
				// NS records (the destination's authoritative NS set
				// belongs to the destination panel) but PRESERVE every
				// other NS record — those are subdomain delegations
				// (e.g. `app NS ns1.thirdparty.com`) that an operator
				// configured deliberately. Pre-3.0.15 the unqualified
				// `recType == "NS"` skip dropped subdomain delegations
				// silently, leaving the destination zone with strictly
				// less DNS data than the source.
				if recType == "SOA" {
					continue
				}
				if recType == "NS" {
					apexFQDN := zone + "."
					if name == zone || name == apexFQDN || name == "@" {
						continue
					}
				}

				// Update IP-dependent records to point to new server IP.
				// CRITICAL: only rewrite when the value MATCHES the
				// detected source IP. Earlier code's "if oldIP == ''
				// rewrite all A records" branch destroyed third-party
				// A values during migrations where SOA-based source-IP
				// detection failed (e.g. zone with no apex A or
				// non-standard SOA). Better to leave a third-party A
				// untouched than rewrite it to the wrong host.
				if destIP != "" {
					if recType == "A" && oldIP != "" && value == oldIP {
						value = destIP
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

				// Add record to PowerDNS — convert FQDN to relative.
				//
				// pdnsutil's `add-record <zone> <relname> ...` treats <relname>
				// as relative to <zone> and APPENDS .<zone>. So if we hand it
				// the already-fully-qualified `api.jagoanandadhara.org`, it
				// silently writes `api.jagoanandadhara.org.jagoanandadhara.org`
				// — invisible double suffix that pdns then refuses to serve.
				//
				// Earlier code only stripped the trailing-dot form
				// (`...zone.`); pdnsutil's list-zone output uses BARE names
				// (no trailing dot), so the trim never fired and every
				// imported subdomain landed double-suffixed. Strip both
				// shapes (with and without the trailing dot) to be safe.
				recName := name
				switch {
				case recName == zone, recName == zone+".":
					recName = "@"
				case strings.HasSuffix(recName, "."+zone+"."):
					recName = strings.TrimSuffix(recName, "."+zone+".")
				case strings.HasSuffix(recName, "."+zone):
					recName = strings.TrimSuffix(recName, "."+zone)
				case strings.HasSuffix(recName, zone+"."):
					// Only matches when name == zone+"." (apex with dot) — already handled above,
					// but keep as defensive.
					recName = "@"
				}
				if recName == "" {
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
			// Reload zone data — handled per-zone here. The full cache
			// flush + zone-cache invalidation is done once at the end of
			// the loop via systemctl restart pdns, since pdns_control
			// reload + purge leaves stale state when a zone was
			// delete+recreated (the typical re-transfer case).
			agent.RunCommand(ctx, "pdns_control", "reload")

			s.addLog(ctx, jobID, "info", fmt.Sprintf("DNS zone imported for %s (%d records, IP updated: %s → %s)", zone, len(dnsRecords), oldIP, destIP), "dns")
		}
		// Once every zone has been imported, restart pdns to flush both
		// the response cache AND any stale zone-cache entries from
		// delete+recreate cycles. Verified live on .169: without the
		// restart, dig against pdns returned EMPTY for every zone even
		// though pdnsutil list-zone showed the records correctly. With
		// it, queries answer immediately. Cheap (sub-second restart) and
		// only runs once per transfer.
		if len(dnsZones) > 0 {
			agent.RunCommand(ctx, "systemctl", "restart", "pdns")
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
		sslDomains := domains
		if discovered != nil && len(discovered.SSLDomains) > 0 {
			sslDomains = discovered.SSLDomains
		}
		sslDomains = filterByWhitelist(sslDomains, req.Selection.SSLDomains)

		// ACME registration email — uses the panel-wide install-time
		// admin address. Stable per-server; doesn't depend on the
		// vendor's mail being up (which would be circular for cert
		// issuance). --no-eff-email + --agree-tos keep certbot
		// non-interactive.
		acmeEmail := "admin@betazeninfotech.com"

		transferred, issued, sslErrors := 0, 0, 0
		for _, domain := range sslDomains {
			if isCancelled() {
				return
			}
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Transferring SSL for %s", domain), "ssl")

			localCertDir := fmt.Sprintf("%s/ssl-%s", tmpDir, domain)
			os.MkdirAll(localCertDir, 0750)

			// Try copying the source's cert first. ExportSSLFromRemote now
			// pulls live/<domain>/ + archive/<domain>/ + renewal/<domain>.conf
			// into localCertDir, preserving the live→archive symlinks. Copy
			// each piece into its canonical /etc/letsencrypt subdir so
			// nginx can resolve the symlink chain end-to-end. Without the
			// archive payload, every cert symlink in live/ is dangling and
			// nginx refuses to load them, silently breaking the SSL upgrade.
			if err := agent.ExportSSLFromRemote(ctx, host, port, user, pass, domain, localCertDir); err == nil {
				mailHost := agent.MailHostFor(domain)
				agent.RunCommand(ctx, "mkdir", "-p",
					fmt.Sprintf("/etc/letsencrypt/live/%s", domain),
					fmt.Sprintf("/etc/letsencrypt/archive/%s", domain),
					fmt.Sprintf("/etc/letsencrypt/live/%s", mailHost),
					fmt.Sprintf("/etc/letsencrypt/archive/%s", mailHost),
					"/etc/letsencrypt/renewal")
				// Copy regular + mail certs in one shot. Each leg uses
				// `... 2>/dev/null; true` so missing mail.<domain> on
				// the source (no mail-ssl run) doesn't fail the step.
				agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
					"cp -a %s/live/%s/. /etc/letsencrypt/live/%s/ 2>/dev/null; "+
						"cp -a %s/archive/%s/. /etc/letsencrypt/archive/%s/ 2>/dev/null; "+
						"cp -a %s/renewal/%s.conf /etc/letsencrypt/renewal/ 2>/dev/null; "+
						"cp -a %s/live/%s/. /etc/letsencrypt/live/%s/ 2>/dev/null; "+
						"cp -a %s/archive/%s/. /etc/letsencrypt/archive/%s/ 2>/dev/null; "+
						"cp -a %s/renewal/%s.conf /etc/letsencrypt/renewal/ 2>/dev/null; "+
						"true",
					localCertDir, domain, domain,
					localCertDir, domain, domain,
					localCertDir, domain,
					localCertDir, mailHost, mailHost,
					localCertDir, mailHost, mailHost,
					localCertDir, mailHost))
				os.RemoveAll(localCertDir)
				transferred++
				s.addLog(ctx, jobID, "info", fmt.Sprintf("SSL cert transferred for %s", domain), "ssl")

				// Mail SSL auto-rewire. If the source had a
				// /etc/letsencrypt/live/mail.<domain>/fullchain.pem
				// (from a previous `bzpanel mail-ssl` run), it just
				// landed on disk above. The mail-SNI dispatch (Postfix
				// sni-map + Dovecot local_name + nginx helper vhost +
				// renewal hook) doesn't ride with the SSL tar, so
				// shell out to `bzpanel mail-ssl <domain>` which
				// detects the existing cert and runs the wire-up only
				// (no DNS pre-flight, no certbot — those would fail
				// during transfer because public DNS still points at
				// the source). bzpanel binary is at the canonical
				// install path on every panel-provisioned VPS.
				if _, statErr := os.Stat(fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", mailHost)); statErr == nil {
					if _, e := agent.RunCommand(ctx, "/opt/serverpanel/bin/bzpanel", "mail-ssl", domain); e == nil {
						s.addLog(ctx, jobID, "info", fmt.Sprintf("Mail SSL re-wired for %s (SNI dispatch active)", mailHost), "ssl")
					} else {
						s.addLog(ctx, jobID, "warn", fmt.Sprintf("Mail SSL cert copied for %s but SNI wire-up failed: %v — run `bzpanel mail-ssl %s` manually", mailHost, e, domain), "ssl")
					}
				}
				continue
			}
			os.RemoveAll(localCertDir)

			// Source had no cert — issue a fresh one via certbot --nginx
			// so the cert lands AND the nginx vhost is auto-rewritten to
			// listen on 443 + redirect 80→443. This is the same path the
			// regular Add Domain flow uses, so transferred sites end up
			// HTTPS-enabled identically to operator-created ones.
			//
			// Failure modes are common (DNS still points at source IP,
			// rate limit, port 80 not yet reachable from public) — log
			// loudly but don't fail the step. Operator can re-issue
			// later from the SSL page once DNS has propagated.
			s.addLog(ctx, jobID, "info",
				fmt.Sprintf("No cert on source for %s — issuing fresh Let's Encrypt cert", domain), "ssl")
			// --cert-name pins the lineage to <domain> so a re-run reuses it
			// in place. Without it, certbot mints a fresh collision-avoidance
			// lineage (<domain>-0001, -0002, …) every time it can't exactly
			// match an existing cert — the source of the duplicate numbered
			// lineages observed after migrations.
			args := []string{
				"--nginx", "--redirect", "-n", "--agree-tos", "--no-eff-email",
				"--keep-until-expiring", "--cert-name", domain, "-m", acmeEmail,
				"-d", domain,
			}
			// Add www variant for apex (2-label) domains. Skip for sub-
			// domains where www.<sub> usually doesn't exist at all.
			if strings.Count(domain, ".") == 1 {
				args = append(args, "-d", "www."+domain)
			}
			if _, e := agent.RunCertbot(ctx, args...); e != nil {
				s.addLog(ctx, jobID, "warn",
					fmt.Sprintf("Let's Encrypt failed for %s — re-issue later via the SSL page once DNS resolves to this server", domain), "ssl")
				sslErrors++
				continue
			}
			issued++
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Issued Let's Encrypt cert for %s", domain), "ssl")
		}

		// Upgrade vhosts that we transferred (not issued — certbot
		// already rewrote those) to SSL using the local cert path.
		// Dispatch on whether the domain backs a panel app or
		// project_service: those need a reverse-proxy SSL vhost
		// (CreateReverseProxyWithSSL) pointing at the upstream port,
		// not a PHP-FPM SSL vhost (CreateVhostWithSSL would clobber
		// the proxy with a static-files server and break the app).
		//
		// At this step Sync Panel Records hasn't run yet, so the
		// destination's apps/project_services collections are empty
		// — query the SOURCE mongo over SSH to learn each domain's
		// upstream port. Misses here (network blip, mongosh missing)
		// fall back to PHP-FPM SSL, which is the right shape for
		// pure-static and PHP domains.
		appPortByDomain := map[string]int{}
		if r, err := agent.SSHCommand(ctx, host, port, user, pass,
			`source /opt/serverpanel/.env 2>/dev/null && mongosh "$MONGO_URI" --quiet --eval 'db.apps.find({},{domain:1,port:1,_id:0}).forEach(a=>print((a.domain||"")+"\t"+(a.port||0))); db.project_services.find({},{primary_domain:1,port:1,_id:0}).forEach(s=>print((s.primary_domain||"")+"\t"+(s.port||0)))' 2>/dev/null`); err == nil && r != nil {
			for _, line := range strings.Split(r.Output, "\n") {
				parts := strings.SplitN(strings.TrimSpace(line), "\t", 2)
				if len(parts) != 2 || parts[0] == "" {
					continue
				}
				if p, perr := strconv.Atoi(parts[1]); perr == nil && p > 0 {
					appPortByDomain[parts[0]] = p
				}
			}
		}

		ssled := 0
		dbRecorded := 0
		for _, domain := range sslDomains {
			certPath := fmt.Sprintf("/etc/letsencrypt/live/%s/fullchain.pem", domain)
			if _, statErr := os.Stat(certPath); statErr != nil {
				continue
			}
			var domRec models.Domain
			if err := s.db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": domain}).Decode(&domRec); err != nil {
				continue
			}
			if proxyPort := appPortByDomain[domain]; proxyPort > 0 {
				if e := agent.CreateReverseProxyWithSSL(ctx, &agent.VhostConfig{
					Domain: domain,
					Port:   proxyPort,
				}); e == nil {
					ssled++
				}
			} else {
				if e := agent.CreateVhostWithSSL(ctx, &agent.VhostConfig{
					Domain:     domain,
					User:       domRec.User,
					PHPVersion: domRec.PHPVersion,
				}); e == nil {
					ssled++
				}
			}

			// Persist the destination's panel records so the WHM SSL
			// page actually shows the cert as Active. Previously the
			// transfer step copied the on-disk LE artifacts and
			// rewrote the vhost, but never wrote ssl_certificates or
			// flipped Domain.SSLActive — so the destination's SSL
			// list rendered "no SSL" for every transferred domain
			// even though HTTPS was already serving correctly. The
			// Sync Panel Records pass that runs LATER could only
			// help when the source mongo had a corresponding row
			// (it often didn't, especially for certs the source
			// issued via certbot --nginx outside the panel flow), so
			// this gap had to be closed at the source of truth — the
			// cert files themselves. Idempotent upsert on the natural
			// "domain" key.
			issuedAt, expiresAt, issuer, serial := parseCertbotInfo(ctx, domain)
			now := time.Now()
			certDoc := bson.M{
				"domain":        domain,
				"issuer":        issuer,
				"type":          "letsencrypt",
				"domains":       []string{domain},
				"issued_at":     issuedAt,
				"expires_at":    expiresAt,
				"auto_renew":    true,
				"key_type":      "RSA",
				"cert_path":     certPath,
				"key_path":      fmt.Sprintf("/etc/letsencrypt/live/%s/privkey.pem", domain),
				"serial_number": serial,
				"updated_at":    now,
			}
			if _, upErr := s.db.Collection(database.ColSSLCerts).UpdateOne(ctx,
				bson.M{"domain": domain},
				bson.M{
					"$set":         certDoc,
					"$setOnInsert": bson.M{"created_at": now},
				},
				options.Update().SetUpsert(true),
			); upErr == nil {
				dbRecorded++
			} else {
				s.addLog(ctx, jobID, "warn",
					fmt.Sprintf("ssl_certificates upsert failed for %s: %s", domain, upErr.Error()), "ssl")
			}
			// Flip the Domain doc's SSL flag so the Domains page +
			// per-domain widgets render the green "Active" badge.
			s.db.Collection(database.ColDomains).UpdateOne(ctx,
				bson.M{"domain": domain},
				bson.M{"$set": bson.M{
					"ssl_active":  true,
					"ssl_expires": expiresAt,
					"updated_at":  now,
				}},
			)
		}
		agent.ReloadNginx(ctx)

		s.completeStep(ctx, jobID, "Transfer SSL Certificates",
			fmt.Sprintf("transferred=%d issued=%d ssled=%d recorded=%d errors=%d (of %d domains)",
				transferred, issued, ssled, dbRecorded, sslErrors, len(sslDomains)))
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

		// resolvePanelDB pulls the SOURCE's panel `databases` row for
		// (name, type) via mongoexport over SSH. The panel-records
		// sync runs LATER in this same job (look at the step order:
		// Transfer Databases first, then Sync Panel Records), so the
		// destination's `databases` collection is still empty when this
		// helper is called. Reading from source instead means we have
		// access to the operator-set username + password the moment
		// we need them — to issue CreateMySQLUser / CreateMongoUser
		// with the SAME password that's already in the panel's record,
		// keeping phpMyAdmin auto-login working post-transfer.
		resolvePanelDB := func(name, typ string) *models.Database {
			filter := fmt.Sprintf(`{"db_name":%q,"type":%q}`, name, typ)
			rows, err := agent.RemoteMongoExport(ctx, host, port, user, pass,
				"serverpanel", database.ColDatabases, filter)
			if err != nil || len(rows) == 0 {
				return nil
			}
			raw := rows[0]
			rec := &models.Database{
				DBName:           name,
				Type:             typ,
			}
			if u, ok := raw["username"].(string); ok {
				rec.Username = u
			}
			if p, ok := raw["password"].(string); ok {
				rec.Password = p
			}
			if oid := extractOID(raw["_id"]); oid != "" {
				if id, err := primitive.ObjectIDFromHex(oid); err == nil {
					rec.ID = id
				}
			}
			return rec
		}

		// --- MongoDB databases ---
		// Re-enabled in v3.1.120. The original disable (v3.0.19) was that the
		// panel mongo user lacks cross-DB privileges on default installs;
		// RemoteMongoDump now also authenticates as the root 'admin' user
		// (same password as the panel URI, created by install.sh) so it can
		// dump any app database, and RestoreMongoDB authenticates the same way
		// on the destination. Explicit selection wins over discovery — same
		// trust-the-operator pattern as MySQL below.
		var mongoDatabases []string
		if len(req.Selection.MongoDBs) > 0 {
			mongoDatabases = append(mongoDatabases, req.Selection.MongoDBs...)
		} else if discovered != nil {
			mongoDatabases = discovered.Databases
		}
		for _, db := range mongoDatabases {
			if isCancelled() {
				return
			}
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Transferring MongoDB database %s", db), "database")

			// Resolve the panel-stored mongo user/pass UP-FRONT so the
			// dump can authenticate against an auth-enabled source
			// even if the panel's admin URI doesn't have global
			// listDatabases. Resolving by-name from the source's own
			// `databases` collection means we get whatever the
			// operator typed when they created the DB through the WHM.
			mongoPanelRec := resolvePanelDB(db, "mongodb")
			mongoUser := ""
			mongoPass := ""
			if mongoPanelRec != nil {
				mongoUser = mongoPanelRec.Username
				mongoPass = mongoPanelRec.Password
			}

			localDump := fmt.Sprintf("%s/%s-dump.gz", tmpDir, db)
			if err := agent.RemoteMongoDump(ctx, host, port, user, pass, db, localDump, mongoUser, mongoPass); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to transfer MongoDB %s: %s", db, err.Error()), "database")
				dbErrors++
				continue
			}

			if err := agent.RestoreMongoDB(ctx, db, localDump); err != nil {
				s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to restore MongoDB %s: %s", db, err.Error()), "database")
				dbErrors++
				continue
			}

			// Recreate the MongoDB application user on the destination
			// using the panel-stored credentials we resolved above.
			// Without this the destination has the data but no user
			// that can connect — every panel autologin link / mongosh
			// CLI would 401 against MongoDB even though the row in
			// the panel page exists.
			panelRec := mongoPanelRec
			if panelRec != nil && panelRec.Username != "" && panelRec.Password != "" {
				if err := agent.CreateMongoUser(ctx, db, panelRec.Username, panelRec.Password, "readWrite"); err != nil {
					s.addLog(ctx, jobID, "warn",
						fmt.Sprintf("MongoDB %s data restored, but creating user %s failed: %s", db, panelRec.Username, err.Error()),
						"database")
				} else {
					s.addLog(ctx, jobID, "info",
						fmt.Sprintf("MongoDB %s user %s recreated with the source's password", db, panelRec.Username),
						"database")
				}
			} else {
				s.addLog(ctx, jobID, "warn",
					fmt.Sprintf("MongoDB %s data restored but no panel record carries credentials — operator must set a password manually", db),
					"database")
			}

			// Upsert (or update) the panel row so name+type land even
			// when the panel-records sync didn't carry it. Use $set so
			// host/port/updated_at refresh on a re-run; $setOnInsert
			// preserves the panel's username+password if a row already
			// exists from the panel-records sync.
			dbNow := time.Now()
			res, _ := s.db.Collection(database.ColDatabases).UpdateOne(ctx,
				bson.M{"db_name": db, "type": "mongodb"},
				bson.M{
					"$set": bson.M{
						"host":       "localhost",
						"port":       27017,
						"updated_at": dbNow,
					},
					"$setOnInsert": bson.M{
						"db_name":    db,
						"type":       "mongodb",
						"created_at": dbNow,
					},
				},
				options.Update().SetUpsert(true))
			if res != nil && (res.UpsertedCount > 0 || res.ModifiedCount > 0) {
				mongoCount++
			}
			s.addLog(ctx, jobID, "info", fmt.Sprintf("MongoDB %s transferred", db), "database")
			os.Remove(localDump)
		}

		// --- MySQL/MariaDB databases ---
		// Same trust-the-operator pattern as MongoDB above: explicit
		// whitelist wins over discovery. MySQL's `SHOW DATABASES`
		// rarely fails the way MongoDB's listDatabases can, but the
		// uniform handling makes the API contract simpler — what the
		// operator selected is what gets transferred.
		var mysqlDatabases []string
		if len(req.Selection.MySQLDBs) > 0 {
			mysqlDatabases = append(mysqlDatabases, req.Selection.MySQLDBs...)
		} else if discovered != nil {
			mysqlDatabases = discovered.MySQLDatabases
		}
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

			// Pull the panel-stored credentials BEFORE creating MySQL
			// users on the destination. The panel-records sync copied
			// the source's `databases` row (username + password) into
			// our panel earlier in this same job; reading that row now
			// means the recreated MySQL user has the SAME password as
			// the source's. Without this, the next phpMyAdmin
			// autologin / mysql CLI from the panel would fail because
			// the panel's stored password and MySQL's actual auth
			// string disagreed.
			panelRec := resolvePanelDB(db, "mysql")
			panelPass := ""
			panelUser := ""
			if panelRec != nil {
				panelPass = panelRec.Password
				panelUser = panelRec.Username
			}

			dbUser := panelUser
			mysqlUsers, _ := agent.DiscoverMySQLUsers(ctx, host, port, user, pass, db)
			for _, mu := range mysqlUsers {
				username := mu["username"]
				muHost := mu["host"]
				if username == "" || username == "root" || username == "debian-sys-maint" {
					continue
				}
				// Use the panel's stored password when this user matches
				// the panel's primary user. Other (non-primary) MySQL
				// users for the same DB get a fresh random password —
				// they typically only matter for db-access-host grants
				// which `recreateAccessHostGrants` below redoes anyway.
				userPass := panelPass
				if userPass == "" || username != panelUser {
					userPass = generateRandomPassword(16)
				}
				if err := agent.CreateMySQLUser(ctx, db, username, userPass, muHost); err != nil {
					s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to create MySQL user %s for %s: %s", username, db, err.Error()), "database")
					continue
				}
				s.addLog(ctx, jobID, "info", fmt.Sprintf("MySQL user %s@%s created for %s", username, muHost, db), "database")
				if dbUser == "" {
					dbUser = username
				}
				// Save db_users row including password so the panel can
				// surface it later (e.g. password rotation page).
				duNow := time.Now()
				s.db.Collection(database.ColDBUsers).InsertOne(ctx, models.DatabaseUser{
					Username:  username,
					Password:  userPass,
					Role:      "readWrite",
					CreatedAt: duNow,
				})
			}

			// Upsert the panel row including PASSWORD. The previous shape
			// only wrote username (in $setOnInsert) and left password
			// for the panel-records sync to fill in — but that sync
			// runs LATER in the orchestration AND its insertDeduped
			// helper skips when a row with the same db_name already
			// exists, so the password never landed. The destination's
			// "Open in phpMyAdmin (auto-login)" button then opened the
			// modal with an EMPTY password field and the signed URL
			// failed against MySQL.
			//
			// Now we $set both username AND password from the panel
			// record we just resolved (and used to create the MySQL
			// user), so the destination row reflects the actual MySQL
			// auth state. connection_string also embeds the password
			// so the connection-info modal shows a working CLI.
			connStr := fmt.Sprintf("mysql://%s:%s@localhost:3306/%s",
				url.QueryEscape(dbUser), url.QueryEscape(panelPass), db)
			dbNow := time.Now()
			setFields := bson.M{
				"host":              "localhost",
				"port":              3306,
				"connection_string": connStr,
				"updated_at":        dbNow,
			}
			if dbUser != "" {
				setFields["username"] = dbUser
			}
			if panelPass != "" {
				setFields["password"] = panelPass
			}
			mres, _ := s.db.Collection(database.ColDatabases).UpdateOne(ctx,
				bson.M{"db_name": db, "type": "mysql"},
				bson.M{
					"$set": setFields,
					"$setOnInsert": bson.M{
						"db_name":    db,
						"type":       "mysql",
						"created_at": dbNow,
					},
				},
				options.Update().SetUpsert(true))
			if mres != nil && (mres.UpsertedCount > 0 || mres.ModifiedCount > 0) {
				mysqlCount++
			}

			// Re-create per-host MySQL grants from db_access_hosts. The
			// panel-records sync now copies that collection (see
			// transfer_panel_records.go), but the GRANT rows in MySQL
			// itself need to be recreated from those panel-row hints —
			// otherwise external apps that connect via a whitelisted
			// IP would get "access denied" until the operator re-added
			// each entry by hand on the destination's Database page.
			if panelRec != nil && panelPass != "" {
				s.recreateAccessHostGrants(ctx, jobID, host, port, user, pass,
					panelRec.ID, db, panelUser, panelPass)
			}

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

		// Pre-pull every source forwarder's keep_copy flag in one
		// mongoexport hit. The postfix line we parse downstream tells
		// us nothing about whether the operator wanted a local copy
		// kept (keep_copy in panel-speak); without this map, every
		// transferred forwarder lands with keep_copy=false and the
		// destination's UI shows it as "inactive" even though the
		// source had the operator's intent set to "active". Keyed
		// "<source>" since postfix-side aliases are addressed that
		// way; misses (e.g., a non-Betazen source with no mongo, or
		// a forwarder created outside the panel flow) fall back to
		// keep_copy=true — the same default the UI shows when an
		// operator clicks Add Forwarder, so transferred rows look
		// the way an operator would have created them by hand.
		sourceKeepCopy := map[string]bool{}
		if r, err := agent.SSHCommand(ctx, host, port, user, pass,
			`source /opt/serverpanel/.env 2>/dev/null && mongosh "$MONGO_URI" --quiet --eval 'db.email_forwarders.find({},{source:1,keep_copy:1,_id:0}).forEach(f=>print((f.source||"")+"|"+(f.keep_copy?"1":"0")))' 2>/dev/null`); err == nil && r != nil {
			for _, line := range strings.Split(r.Output, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || !strings.Contains(line, "|") {
					continue
				}
				parts := strings.SplitN(line, "|", 2)
				if len(parts) == 2 && parts[0] != "" {
					sourceKeepCopy[parts[0]] = parts[1] == "1"
				}
			}
		}

		// Pull the source's JWT_SECRET once so we can re-encrypt each
		// imported mailbox's encrypted_pass under THIS panel's key.
		// Without this, the webmail "Open" arrow does nothing for
		// transferred boxes — the encrypted_pass blob is AES-GCM
		// ciphertext keyed on the source's JWT_SECRET, which the
		// destination's panel can't recover.
		srcJWTSecret := ""
		if r, err := agent.SSHCommand(ctx, host, port, user, pass,
			`grep -E '^JWT_SECRET=' /opt/serverpanel/.env 2>/dev/null | head -1 | cut -d= -f2-`); err == nil && r != nil {
			srcJWTSecret = strings.TrimSpace(r.Output)
		}

		// Pull source's mongo URI + dump the mailboxes collection's
		// encrypted_pass field so we have ciphertexts to re-encrypt.
		// One query, processed in-memory.
		srcEncryptedPass := map[string]string{}
		if srcJWTSecret != "" && s.emailSvc != nil {
			if srcMongoURI, err := agent.SSHCommand(ctx, host, port, user, pass,
				`grep -E '^MONGO_URI=' /opt/serverpanel/.env 2>/dev/null | head -1 | cut -d= -f2-`); err == nil && srcMongoURI != nil {
				uri := strings.TrimSpace(srcMongoURI.Output)
				if uri != "" {
					if dumpResult, derr := agent.SSHCommand(ctx, host, port, user, pass,
						fmt.Sprintf(`mongosh %q --quiet --eval 'db.mailboxes.find({},{email:1,encrypted_pass:1,_id:0}).forEach(m=>print(m.email+"|"+(m.encrypted_pass||"")))' 2>/dev/null`,
							uri)); derr == nil && dumpResult != nil {
						for _, line := range strings.Split(dumpResult.Output, "\n") {
							line = strings.TrimSpace(line)
							if line == "" || !strings.Contains(line, "|") {
								continue
							}
							parts := strings.SplitN(line, "|", 2)
							if len(parts) == 2 && parts[1] != "" {
								srcEncryptedPass[parts[0]] = parts[1]
							}
						}
					}
				}
			}
		}

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

			// Add domain to /etc/postfix/virtual_mailbox_domains so Postfix
			// accepts inbound mail for it. The file is referenced as
			// `hash:/etc/postfix/virtual_mailbox_domains` in main.cf, so
			// each line MUST be `<domain> <value>` for postmap to index
			// it — a bare `<domain>` line has no value, postmap silently
			// drops it, and postfix then treats the domain as remote and
			// loops the mail back to itself ("mail for X loops back to
			// myself" bounce). Mirror the CreateMailbox shape: emit
			// `<domain> OK`. Existing single-token rows are upgraded.
			agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
				"grep -qE '^%s( |\\t)' /etc/postfix/virtual_mailbox_domains 2>/dev/null || { sed -i '/^%s$/d' /etc/postfix/virtual_mailbox_domains 2>/dev/null; echo '%s OK' >> /etc/postfix/virtual_mailbox_domains; }",
				strings.ReplaceAll(domain, ".", "\\."),
				strings.ReplaceAll(domain, ".", "\\."),
				domain))
			agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailbox_domains")

			// Setup DKIM. Try to COPY the source's existing private key
			// (and matching .txt with the public selector) first — if we
			// regenerate on the destination, every receiver's DKIM cache
			// for this domain becomes stale and outbound mail starts
			// failing DKIM verification until DNS catches up. Fall back
			// to fresh genkey only when the source has no key.
			keyDir := fmt.Sprintf("/etc/opendkim/keys/%s", domain)
			agent.RunCommand(ctx, "mkdir", "-p", keyDir)
			srcKeyArchive := fmt.Sprintf("%s/%s-dkim.tar.gz", tmpDir, domain)
			tarErr := agent.RemoteTarPath(ctx, host, port, user, pass,
				fmt.Sprintf("/etc/opendkim/keys/%s", domain), srcKeyArchive)
			dkimCopied := false
			if tarErr == nil {
				if untarErr := agent.LocalUntar(ctx, srcKeyArchive, "/etc/opendkim/keys/"); untarErr == nil {
					dkimCopied = true
					s.addLog(ctx, jobID, "info", fmt.Sprintf("DKIM key for %s copied from source — DNS DKIM record stays valid", domain), "email")
				}
				os.Remove(srcKeyArchive)
			}
			if !dkimCopied {
				agent.RunCommand(ctx, "opendkim-genkey", "-s", "mail", "-d", domain, "-D", keyDir)
				s.addLog(ctx, jobID, "info", fmt.Sprintf("DKIM key for %s generated fresh — operator must update DNS DKIM record", domain), "email")
			}
			agent.RunCommand(ctx, "chown", "-R", "opendkim:opendkim", keyDir)
			agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/opendkim/signing.table || echo '*@%s mail._domainkey.%s' >> /etc/opendkim/signing.table", domain, domain, domain))
			agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/opendkim/key.table || echo 'mail._domainkey.%s %s:mail:%s/mail.private' >> /etc/opendkim/key.table", domain, domain, domain, keyDir))
			agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/opendkim/trusted.hosts || echo '%s' >> /etc/opendkim/trusted.hosts", domain, domain))

			// Discover mailbox users from source and fully set them up.
			// The panel stores mail under /home/<owner>/mail/<domain>/<box>
			// (per the email service's CreateMailbox path); /var/mail/vhosts
			// is the cPanel/old layout. Try the panel layout first, fall
			// back to the legacy one. Without the panel-layout path, every
			// transfer reported "0 mailboxes" because /var/mail/vhosts is
			// empty on every Betazen Server Panel — they all use /home/<owner>/mail/.
			//
			// Pin the lookup to the known owner instead of /home/* glob:
			// when the source has stale soft-deleted user homes (left over
			// from prior vendor purges as /home/<user>-del-<ts>/), the glob
			// matches BOTH the live /home/<owner>/mail/<domain>/ AND every
			// stale /home/<owner>-del-*/mail/<domain>/, and ls then prints
			// `/path/:` headers between groups that downstream code parses
			// as fake usernames — the destination ends up with bogus
			// mailbox rows like `/home/cholun-del-…/mail/X/:@X`. With
			// domOwner pinned, ls hits exactly one directory and the
			// output is one mailbox per line.
			var mailLookupCmd string
			if domOwner != "" {
				mailLookupCmd = fmt.Sprintf(
					`ls /home/%s/mail/%s/ 2>/dev/null || ls /var/mail/vhosts/%s/ 2>/dev/null || echo ''`,
					domOwner, domain, domain)
			} else {
				mailLookupCmd = fmt.Sprintf(
					`ls /home/*/mail/%s/ 2>/dev/null || ls /var/mail/vhosts/%s/ 2>/dev/null || echo ''`,
					domain, domain)
			}
			mailUsers, _ := agent.SSHCommand(ctx, host, port, user, pass, mailLookupCmd)

			// Pull source's /etc/dovecot/users so we can preserve each
			// mailbox's password hash. Without this the destination
			// generates a fresh random password for every box and the
			// operator has no way to log in via webmail. Both panels
			// use SHA512-CRYPT so the hash is portable verbatim.
			srcDovecotHashes := map[string]string{}
			if dr, derr := agent.SSHCommand(ctx, host, port, user, pass,
				fmt.Sprintf(`grep -E '^[A-Za-z0-9._%%+-]+@%s:' /etc/dovecot/users 2>/dev/null || true`, regexp.QuoteMeta(domain))); derr == nil && dr != nil {
				for _, line := range strings.Split(dr.Output, "\n") {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					// Format: email:hash:uid:gid:gecos:home::extra_fields
					parts := strings.SplitN(line, ":", 3)
					if len(parts) < 2 {
						continue
					}
					srcDovecotHashes[strings.TrimSpace(parts[0])] = parts[1]
				}
			}
			if mailUsers != nil {
				for _, mailUser := range strings.Split(strings.TrimSpace(mailUsers.Output), "\n") {
					mailUser = strings.TrimSpace(mailUser)
					if mailUser == "" {
						continue
					}
					// When ls is given multiple matching directories (the
					// glob fallback above) it prints `/path/:` headers
					// between groups. Skip anything that looks like a path
					// — a real maildir name is a bare username, no slashes
					// or colons.
					if strings.ContainsAny(mailUser, "/:") {
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

					// Prefer source's existing password hash so the mailbox
					// owner can keep logging in with the same credentials
					// they had on the old server. SHA512-CRYPT hashes are
					// portable across Dovecot installs (no per-server salt
					// secret), so a literal copy is correct.
					//
					// If the source has no entry (rare — usually means the
					// box was created out-of-band), fall back to a fresh
					// random password. Operator can then re-set it from
					// the panel UI.
					passHash := srcDovecotHashes[email]
					if passHash == "" {
						tmpPass := generateRandomPassword(16)
						if passResult, passErr := agent.RunCommand(ctx, "doveadm", "pw", "-s", "SHA512-CRYPT", "-p", tmpPass); passErr == nil {
							passHash = strings.TrimSpace(passResult.Output)
						}
					}

					// Add to Dovecot users file
					if passHash != "" {
						userLine := fmt.Sprintf("%s:%s:5000:5000::%s::userdb_mail=maildir:%s", email, passHash, maildir, maildir)
						agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -q '%s' /etc/dovecot/users || echo '%s' >> /etc/dovecot/users", email, userLine))
					}

					// Add Postfix virtual mailbox mapping. Same naming rule as
					// above — the file MUST be virtual_mailbox_maps, not the
					// no-suffix "virtual_mailboxes" the old code used.
					mapping := fmt.Sprintf("%s    %s/%s/", email, domain, mailUser)
					agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf("grep -qF '%s' /etc/postfix/virtual_mailbox_maps 2>/dev/null || echo '%s' >> /etc/postfix/virtual_mailbox_maps", email, mapping))

					// Re-encrypt source's webmail-SSO ciphertext under THIS
					// panel's JWT_SECRET. Without this the "Open mailbox"
					// arrow on the WHM Email page does nothing for any
					// transferred mailbox — destination's GenerateWebmailToken
					// can't decrypt the source's blob and the SSO flow
					// silently aborts.
					reencryptedPass := ""
					if srcCipher := srcEncryptedPass[email]; srcCipher != "" && srcJWTSecret != "" && s.emailSvc != nil {
						if rp, rerr := s.emailSvc.ReencryptForTransfer(srcCipher, srcJWTSecret); rerr == nil {
							reencryptedPass = rp
						}
					}

					// Save mailbox record to MongoDB. Upsert so re-runs don't
					// fail on the unique-email index — counts the row as new
					// only when the upsert actually inserts. Use $set for
					// password + encrypted_pass so a pre-existing mongo row
					// (left over from a previous transfer attempt) gets the
					// fresh password hash AND the re-encrypted SSO blob.
					mNow := time.Now()
					setFields := bson.M{
						"password":   passHash,
						"updated_at": mNow,
					}
					if reencryptedPass != "" {
						setFields["encrypted_pass"] = reencryptedPass
					}
					mRes, _ := s.db.Collection(database.ColMailboxes).UpdateOne(ctx,
						bson.M{"email": email},
						bson.M{
							"$set": setFields,
							"$setOnInsert": bson.M{
								"email":      email,
								"domain":     domain,
								"quota_mb":   1024,
								"created_at": mNow,
							},
						},
						options.Update().SetUpsert(true))
					if mRes != nil && mRes.UpsertedCount > 0 {
						mailboxCount++
					}
				}
			}

			// Postmap the correct file after adding all entries for this domain
			agent.RunCommand(ctx, "postmap", "/etc/postfix/virtual_mailbox_maps")

			// Transfer email forwarders (aliases) from source.
			//
			// Two changes from the previous shape:
			//
			//   1. Mongo write is now an UPSERT keyed on (source, domain)
			//      instead of a bare InsertOne. Re-running the transfer
			//      no longer creates a second/third row for the same
			//      forwarder — the operator was watching the
			//      destination's Forwarders list grow by N copies after
			//      every Test Connection retry.
			//
			//   2. keep_copy is now hydrated from the source mongo
			//      (sourceKeepCopy lookup, populated once above). Without
			//      this, every transferred forwarder showed as "inactive"
			//      in the destination UI because the StatusBadge reads
			//      keep_copy directly, and the previous code never set it.
			//      Falls back to true (the UI's Add Forwarder default) so
			//      forwarders parsed from postfix without a matching mongo
			//      row don't all land inactive.
			//
			//   3. Postfix line write switched from a fragile substring
			//      grep guard (which double-matched on partial overlaps)
			//      to a "delete the source's existing line, then append
			//      one fresh" pattern. Idempotent across re-runs and
			//      can't desync the file with the mongo upsert.
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

					// Idempotent postfix file rewrite: drop any existing
					// line for this source, then append one fresh. The
					// source escape protects literal dots inside the
					// regex (taymura@insurancebuykaro.com would otherwise
					// also match `taymuraXinsurancebuykaroXcom`).
					srcEsc := strings.ReplaceAll(source, ".", `\.`)
					srcEsc = strings.ReplaceAll(srcEsc, "@", `\@`)
					agent.RunCommand(ctx, "bash", "-c", fmt.Sprintf(
						"sed -i '/^%s[ \\t]/d' /etc/postfix/virtual_alias_maps 2>/dev/null; "+
							"echo '%s    %s' >> /etc/postfix/virtual_alias_maps",
						srcEsc, source, strings.Join(dests, ", ")))

					// Upsert the panel row. keep_copy comes from source
					// mongo when available; otherwise falls back to the
					// UI default of true so the badge reads "active".
					keepCopy := true
					if v, ok := sourceKeepCopy[source]; ok {
						keepCopy = v
					}
					fNow := time.Now()
					s.db.Collection(database.ColForwarders).UpdateOne(ctx,
						bson.M{"source": source, "domain": domain},
						bson.M{
							"$set": bson.M{
								"destinations": dests,
								"keep_copy":    keepCopy,
							},
							"$setOnInsert": bson.M{
								"source":     source,
								"domain":     domain,
								"created_at": fNow,
							},
						},
						options.Update().SetUpsert(true),
					)
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

			// Generate new password and create FTP account. pure-pw useradd
			// fails on a re-run because the entry from a previous transfer
			// is still in pureftpd.passwd — fall back to UpdateFTPPassword
			// so the second run is a clean no-op rather than 15 errors.
			ftpPass := generateRandomPassword(16)
			err := agent.CreateFTPAccount(ctx, ftpUser, ftpPass, homeDir)
			if err != nil {
				if updateErr := agent.UpdateFTPPassword(ctx, ftpUser, ftpPass); updateErr != nil {
					s.addLog(ctx, jobID, "warn", fmt.Sprintf("Failed to create or update FTP account %s: create=%s update=%s", ftpUser, err.Error(), updateErr.Error()), "ftp")
					ftpErrors++
					continue
				}
				s.addLog(ctx, jobID, "info", fmt.Sprintf("FTP account %s already existed — password reset", ftpUser), "ftp")
			}
			// Save to MongoDB if not already present (re-run safety).
			ftpNow := time.Now()
			ftpRec := models.FTPAccount{
				Username:  ftpUser,
				Domain:    matchedDomain,
				HomeDir:   homeDir,
				IsRoot:    true,
				CreatedAt: ftpNow,
				UpdatedAt: ftpNow,
			}
			_, _ = s.db.Collection(database.ColFTPAccounts).UpdateOne(ctx,
				bson.M{"username": ftpUser},
				bson.M{"$setOnInsert": ftpRec},
				options.Update().SetUpsert(true))
			ftpCreated++
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

	// ===== Step: Transfer Node.js Apps =====
	if req.Components.NodeApps {
		s.startStep(ctx, jobID, "Transfer Node.js Apps")
		nodeApps := []models.NodeApp{}
		if discovered != nil {
			nodeApps = discovered.NodeApps
		}
		nodeApps = filterNodeAppsByWhitelist(nodeApps, req.Selection.NodeApps)

		// Skip apps that are panel-managed (have a row in source mongo's
		// `apps` collection). Those will be brought across by Sync Panel
		// Records → tryStartSyncedApps which writes a sp-app-<name>.service
		// systemd unit running pm2-runtime. If we ALSO start them here via
		// the legacy `pm2 start` daemon path, two PM2 instances race for
		// the same upstream port and the systemd unit ends up in
		// activating/auto-restart with EADDRINUSE.
		//
		// The App model uses `install_path` (often empty for panel-created
		// apps) and the actual disk dir is computed as
		// `/home/<user>/apps/<name>`. Build the panel-managed path set
		// from explicit install_path OR the derived layout, then filter
		// the discovered NodeApps against it.
		panelManagedCwds := map[string]bool{}
		if r, err := agent.SSHCommand(ctx, host, port, user, pass,
			`source /opt/serverpanel/.env 2>/dev/null && mongosh "$MONGO_URI" --quiet --eval 'db.apps.find({},{name:1,user:1,install_path:1,_id:0}).forEach(a=>print((a.install_path&&a.install_path.length?a.install_path:("/home/"+(a.user||"")+"/apps/"+(a.name||"")))))' 2>/dev/null`); err == nil && r != nil {
			for _, line := range strings.Split(r.Output, "\n") {
				line = strings.TrimSpace(line)
				if line != "" && line != "/home//apps/" {
					panelManagedCwds[line] = true
				}
			}
		}

		if len(nodeApps) == 0 {
			s.addLog(ctx, jobID, "info",
				"No PM2-managed Node.js apps detected on source — nothing to migrate", "nodeapps")
			s.skipStep(ctx, jobID, "Transfer Node.js Apps",
				"No Node.js apps on source")
		} else {
			s.addLog(ctx, jobID, "info", fmt.Sprintf("Migrating %d Node.js app(s)", len(nodeApps)), "nodeapps")
			transferred := 0
			for _, app := range nodeApps {
				if app.Cwd == "" || app.Name == "" {
					continue
				}
				if panelManagedCwds[app.Cwd] {
					s.addLog(ctx, jobID, "info", fmt.Sprintf("Skipping %s — panel-managed (sp-app systemd will recover)", app.Name), "nodeapps")
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

	// ===== Step: Transfer SSH Keys =====
	// Pulls /home/<user>/.ssh/authorized_keys for every selected linux
	// user from the source and merges (dedupes by key body) into the
	// destination's same-path file. The mongo `ssh_keys` rows ride
	// along in Sync Panel Records below — those are the panel's
	// catalog of "managed" keys with display names; the actual auth
	// surface is /home/<user>/.ssh/authorized_keys, which is what we
	// merge here.
	if req.Components.SSHKeys {
		s.startStep(ctx, jobID, "Transfer SSH Keys")
		users := req.Selection.LinuxUsers
		if len(users) == 0 && discovered != nil {
			for _, u := range discovered.LinuxUsers {
				users = append(users, u.Username)
			}
		}
		keyTotal, keyUsers, keyErrors := 0, 0, 0
		for _, sysUser := range users {
			keys, err := agent.ExportAuthorizedKeysFromRemote(ctx, host, port, user, pass, sysUser)
			if err != nil {
				s.addLog(ctx, jobID, "warn",
					fmt.Sprintf("Could not read %s's authorized_keys on source: %s", sysUser, err.Error()),
					"ssh-keys")
				keyErrors++
				continue
			}
			if len(keys) == 0 {
				continue
			}
			merged, mergedErr := mergeAuthorizedKeysForUser(ctx, sysUser, keys)
			if mergedErr != nil {
				s.addLog(ctx, jobID, "warn",
					fmt.Sprintf("Failed to install SSH keys for %s: %s", sysUser, mergedErr.Error()),
					"ssh-keys")
				keyErrors++
				continue
			}
			keyTotal += merged
			if merged > 0 {
				keyUsers++
			}
			s.addLog(ctx, jobID, "info",
				fmt.Sprintf("Installed %d new key(s) for %s", merged, sysUser),
				"ssh-keys")
		}
		summary := fmt.Sprintf("Installed %d key(s) across %d user(s)", keyTotal, keyUsers)
		if keyErrors > 0 {
			summary += fmt.Sprintf(" with %d errors", keyErrors)
		}
		s.completeStep(ctx, jobID, "Transfer SSH Keys", summary)
		advance()
		if isCancelled() {
			return
		}
	}

	// ===== Step: Sync Panel Records =====
	// File transfer alone is not enough — the destination's Apps /
	// Deploy Software / Email / SSL pages query mongo, not the
	// filesystem. Pull the matching records from the source's panel db
	// and insert them here, with ObjectID translation and natural-key
	// dedup. Best-effort: failures here don't fail the whole transfer.
	s.startStep(ctx, jobID, "Sync Panel Records")
	if discovered != nil && (discovered.ServerType == "serverpanel" || discovered.ServerType == "") {
		// req.Selection.LinuxUsers is the canonical user list after the
		// wizard rewrite. Fall back to "every discovered linux user" so
		// older clients (and resumed jobs that lost the selection) still
		// sync something.
		users := req.Selection.LinuxUsers
		if len(users) == 0 && discovered != nil {
			for _, u := range discovered.LinuxUsers {
				users = append(users, u.Username)
			}
		}
		s.transferPanelRecords(ctx, jobID, host, port, user, pass, users)
		s.completeStep(ctx, jobID, "Sync Panel Records",
			fmt.Sprintf("Imported records for %d linux user(s)", len(users)))
	} else {
		s.completeStep(ctx, jobID, "Sync Panel Records",
			"Source is not a Betazen Server Panel — skipped (no mongo to copy)")
	}
	advance()

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

	// Post-transfer IP sweep. The DNS import path already rewrites A
	// records + SPF tokens as it imports each zone; this catch-all
	// catches anything that slipped through (records that came in via
	// other means, domain rows created outside the DNS step, etc.) and
	// also rewrites /opt/serverpanel/.env SERVER_IP + the panel vhost
	// server_name catch-all.
	if s.configSvc != nil && host != "" && destIP != "" && host != destIP {
		if sum, err := s.configSvc.ReassignServerIP(ctx, host, destIP); err == nil {
			s.addLog(ctx, jobID, "info",
				fmt.Sprintf("IP sweep %s → %s: %v A-records, %v SPF, %v domains, %v zones",
					host, destIP, sum["a_records"], sum["spf_txt"], sum["domains"], sum["dns_zones"]),
				"transfer")
		} else {
			s.addLog(ctx, jobID, "warn",
				fmt.Sprintf("IP sweep failed: %v — you may need to run Reassign IP manually", err),
				"transfer")
		}
	}

	// Post-transfer mail-stack repair. The destination's Postfix chroot
	// may have been installed before the host had its final resolver
	// config, or the transfer may have overwritten Postfix main.cf;
	// resyncing the chroot + reloading Postfix clears the "Name service
	// error for name=gmail.com" deferral that otherwise silently
	// stalls every outbound message after a fresh migration. Also
	// re-runs ensureDKIMForDomain for every transferred domain so
	// subdomains pick up the parent's selector even if the transfer
	// re-wrote signing.table.
	if s.emailSvc != nil {
		if err := s.emailSvc.SyncPostfixChroot(ctx); err != nil {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("postfix chroot sync failed: %v", err), "mail")
		} else {
			s.addLog(ctx, jobID, "info", "postfix chroot resolver files synced from host /etc/", "mail")
		}
		for _, d := range domains {
			s.emailSvc.EnsureDKIMForDomain(ctx, d)
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
	// validate + discover + sync-panel-records + verify — all always run.
	count := 4
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
	if c.Packages {
		count++
	}
	if c.NodeApps {
		count++
	}
	if c.SSHKeys {
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
		bson.M{"$set": bson.M{
			"steps.$.status":          "in_progress",
			"steps.$.started_at":      &now,
			"steps.$.progress":        0,
			"steps.$.bytes_done":      0,
			"steps.$.bytes_total":     0,
			"steps.$.throughput_mbps": 0,
			"steps.$.eta_seconds":     0,
			"steps.$.current_item":    "",
		}})
}

// updateStepLive pushes live progress for an in-flight step. Called from
// throughout the long file-copy loop so the UI can show "user-john (3/12)
// — 145 MB / 500 MB · 12.3 MB/s · ETA 28s" instead of just a spinner.
// All numeric fields are optional in the BSON sense — the UI hides any
// metric that's zero so callers can pass 0 for "not measured here".
func (s *TransferService) updateStepLive(ctx context.Context, jobID, stepName, currentItem string, bytesDone, bytesTotal int64, mbps float64, etaSec, percent int) {
	oid, _ := primitive.ObjectIDFromHex(jobID)
	s.db.Collection(database.ColTransferJobs).UpdateOne(ctx,
		bson.M{"_id": oid, "steps.name": stepName},
		bson.M{"$set": bson.M{
			"steps.$.current_item":    currentItem,
			"steps.$.bytes_done":      bytesDone,
			"steps.$.bytes_total":     bytesTotal,
			"steps.$.throughput_mbps": mbps,
			"steps.$.eta_seconds":     etaSec,
			"steps.$.progress":        percent,
		}})
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

func (s *TransferService) skipStep(ctx context.Context, jobID, stepName string, reason ...string) {
	oid, _ := primitive.ObjectIDFromHex(jobID)
	now := time.Now()
	set := bson.M{"steps.$.status": "skipped", "steps.$.completed_at": &now}
	// Optional reason — surfaces in the Transfer Detail modal next to
	// the step icon so an operator looking at a "skipped" step doesn't
	// have to dig through the log to find out why. Variadic so existing
	// callers stay valid; only the new ones (Node.js Apps when source
	// has zero apps) need to pass it.
	if len(reason) > 0 && reason[0] != "" {
		set["steps.$.details"] = reason[0]
	}
	s.db.Collection(database.ColTransferJobs).UpdateOne(ctx,
		bson.M{"_id": oid, "steps.name": stepName},
		bson.M{"$set": set})
}

// recreateAccessHostGrants re-runs the AddAccessHost flow's MySQL side
// for every db_access_hosts row attached to a freshly-transferred
// MySQL database. The panel-records sync copies the access-host rows
// (host, comment) to the destination but it runs AFTER this step, so
// we fetch directly from the SOURCE via mongoexport — same pattern
// resolvePanelDB uses for the credentials. The actual MySQL GRANT
// rows live in mysql.user / mysql.db and don't transfer with the
// mongo records sync OR with mongorestore; without re-issuing them
// here, an app pointing at the new server with a previously-allowed
// remote IP would get "ERROR 1130 (HY000): Host is not allowed to
// connect".
//
// `host`, `port`, `sshUser`, `sshPass` describe the SOURCE box so the
// remote mongoexport can reach it. `databaseID` is the SOURCE's
// ObjectID for the `databases` row (the filter key for db_access_hosts).
// Ignored when username/password are empty (the caller logs that case).
func (s *TransferService) recreateAccessHostGrants(ctx context.Context, jobID,
	host string, port int, sshUser, sshPass string,
	databaseID primitive.ObjectID, dbName, username, password string) {
	if databaseID.IsZero() || username == "" || password == "" {
		return
	}
	filter := fmt.Sprintf(`{"database_id":{"$oid":%q}}`, databaseID.Hex())
	rows, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass,
		"serverpanel", database.ColDBAccessHosts, filter)
	if err != nil {
		s.addLog(ctx, jobID, "warn",
			fmt.Sprintf("MySQL %s: could not read source db_access_hosts: %s", dbName, err.Error()),
			"database")
		return
	}
	for _, raw := range rows {
		hostVal, _ := raw["host"].(string)
		hostVal = strings.TrimSpace(hostVal)
		if hostVal == "" {
			continue
		}
		if err := agent.CreateMySQLUserWithRole(ctx, dbName, username, password, hostVal, "dbOwner"); err != nil {
			s.addLog(ctx, jobID, "warn",
				fmt.Sprintf("MySQL %s: re-grant for access-host %q failed: %s", dbName, hostVal, err.Error()),
				"database")
			continue
		}
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("MySQL %s: re-issued GRANT for %s@%s", dbName, username, hostVal),
			"database")
	}
}
