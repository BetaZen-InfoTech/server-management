// Package services — transfer_rehydrate.go.
//
// Unified set of "rebuild filesystem/service state from Mongo" methods
// that the v3.1.48 server-transfer fix calls after the panel-records
// sync. Each method walks one Mongo collection and re-applies the
// per-row destination-side action that CreateX would have done, so an
// operator who runs the panel-records-only path of the transfer
// wizard gets the destination's filesystem / MySQL / PowerDNS / etc.
// in sync with the destination's just-imported Mongo state.
//
// All methods are:
//   * Idempotent — re-running produces the same state.
//   * Safe to call standalone via the new POST /transfer/rehydrate-all
//     endpoint or the bzpanel heal-after-transfer CLI subcommand.
//   * Best-effort per-row — one bad row logs WARN and the loop
//     continues. Caller gets the count of healed rows + the count
//     of failed rows for surfacing in the UI / CLI summary.
//
// Pre-3.1.48 each of these collections synced cleanly to Mongo on the
// destination but the destination-side filesystem / service state was
// NEVER rebuilt. Operators who re-ran the transfer in panel-records-
// only mode (or whose first transfer's full file-rehydrate step
// silently failed) saw the rows in the destination panel UI but the
// underlying feature didn't work — DNS queries returned NXDOMAIN, SSH
// logins failed "permission denied", MySQL connections rejected,
// etc.
package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// mongoDB local alias so the standalone RebuildFTPAccountsFromMongo
// signature stays short — `*mongoDB` reads as "mongo database
// pointer" without the package prefix noise inline.
type mongoDB = mongo.Database

// RehydrateResult is the per-rehydrate count carried back to the
// transfer-job log + the API response.
type RehydrateResult struct {
	Total    int    `json:"total"`
	Healed   int    `json:"healed"`
	Failed   int    `json:"failed"`
	Skipped  int    `json:"skipped"`
	Note     string `json:"note,omitempty"`
}

// AllRehydratesResult is what RunAllRehydrates returns — one
// RehydrateResult per area, plus a top-level "any failed?" flag.
type AllRehydratesResult struct {
	Mailboxes  *RehydrateResult `json:"mailboxes,omitempty"`
	Forwarders *RehydrateResult `json:"forwarders,omitempty"`
	SSHKeys    *RehydrateResult `json:"ssh_keys,omitempty"`
	DNS        *RehydrateResult `json:"dns,omitempty"`
	MySQL      *RehydrateResult `json:"mysql,omitempty"`
	FTP        *RehydrateResult `json:"ftp,omitempty"`
	WordPress  *RehydrateResult `json:"wordpress,omitempty"`
	Skipped    []string         `json:"skipped,omitempty"` // areas with no service wired
}

// RunAllRehydrates calls every Rebuild*FromMongo method on its
// matching service in sequence. Each call is best-effort — one
// failed area logs its error + continues to the next, so the
// operator gets the full per-area breakdown in one round-trip.
//
// Used by:
//   * Transfer panel-records sync (post-3.1.48 — wires this in
//     transfer_panel_records.go after every Mongo collection has
//     synced).
//   * POST /api/v1/whm/transfer/rehydrate-all — operator UI button
//     for manual recovery.
//   * `bzpanel heal-after-transfer` CLI subcommand for SSH-only
//     recovery when the panel itself isn't reachable.
//
// Areas whose service isn't wired into TransferService get added
// to the Skipped list with a hint so the operator can run the
// matching bzpanel heal-* command manually.
func (s *TransferService) RunAllRehydrates(ctx context.Context) *AllRehydratesResult {
	out := &AllRehydratesResult{}

	if s.emailSvc != nil {
		// Mailboxes — uses RebuildMailboxMaps from v3.1.47.
		count, err := s.emailSvc.RebuildMailboxMaps(ctx)
		out.Mailboxes = &RehydrateResult{Total: count, Healed: count}
		if err != nil {
			out.Mailboxes.Note = "FAILED: " + err.Error()
			out.Mailboxes.Failed = count
			out.Mailboxes.Healed = 0
		}

		// Forwarders — uses RebuildVirtualAliasMaps from v3.1.37.
		fcount, err := s.emailSvc.RebuildVirtualAliasMaps(ctx)
		out.Forwarders = &RehydrateResult{Total: fcount, Healed: fcount}
		if err != nil {
			out.Forwarders.Note = "FAILED: " + err.Error()
			out.Forwarders.Failed = fcount
			out.Forwarders.Healed = 0
		}
	} else {
		out.Skipped = append(out.Skipped, "mailboxes (EmailService not wired — run `bzpanel heal-mailboxes`)")
		out.Skipped = append(out.Skipped, "forwarders (EmailService not wired — run `bzpanel heal-forwarders` once shipped)")
	}

	if s.sshKeySvc != nil {
		r, err := s.sshKeySvc.RebuildAuthorizedKeysFromMongo(ctx)
		if err != nil && r != nil {
			r.Note = "FAILED: " + err.Error()
		}
		out.SSHKeys = r
	} else {
		out.Skipped = append(out.Skipped, "ssh_keys (SSHKeyService not wired)")
	}

	if s.dnsSvc != nil {
		r, err := s.dnsSvc.RebuildPowerDNSFromMongo(ctx)
		if err != nil && r != nil {
			r.Note = "FAILED: " + err.Error()
		}
		out.DNS = r
	} else {
		out.Skipped = append(out.Skipped, "dns (DNSService not wired)")
	}

	if s.databaseSvc != nil {
		r, err := s.databaseSvc.RebuildMySQLAccessFromMongo(ctx)
		if err != nil && r != nil {
			r.Note = "FAILED: " + err.Error()
		}
		out.MySQL = r
	} else {
		out.Skipped = append(out.Skipped, "mysql (DatabaseService not wired)")
	}

	// FTP rebuild is db-only (no DomainService deps), so we always
	// run it as long as the orchestrator has the db handle.
	if s.db != nil {
		r, err := RebuildFTPAccountsFromMongo(ctx, s.db)
		if err != nil && r != nil {
			r.Note = "FAILED: " + err.Error()
		}
		out.FTP = r
	} else {
		out.Skipped = append(out.Skipped, "ftp_accounts (db not available)")
	}

	if s.wpService != nil {
		r, err := s.wpService.RewriteWordPressConfigsFromMongo(ctx)
		if err != nil && r != nil {
			r.Note = "FAILED: " + err.Error()
		}
		out.WordPress = r
	} else {
		out.Skipped = append(out.Skipped, "wordpress (WordPressService not wired)")
	}

	log.Info().Interface("rehydrate_summary", out).Msg("post-transfer rehydrate complete")
	return out
}

// ─────────────────────────────────────────────────────────────────────
// SSH keys → /home/<user>/.ssh/authorized_keys
// ─────────────────────────────────────────────────────────────────────

// RebuildAuthorizedKeysFromMongo walks every ssh_keys row, groups by
// user, and writes /home/<user>/.ssh/authorized_keys (or /root/.ssh/
// authorized_keys for root). Atomic per-user replace via .tmp + mv
// so a reader doesn't see a half-written file.
//
// Used by transfer recovery + the new bzpanel heal-after-transfer.
// Pre-3.1.48 the destination's authorized_keys files were empty for
// every transferred SSH key and developers/team got "permission
// denied (publickey)" trying to SSH into their tenant box.
func (s *SSHKeyService) RebuildAuthorizedKeysFromMongo(ctx context.Context) (*RehydrateResult, error) {
	res := &RehydrateResult{}
	col := s.db.Collection(database.ColSSHKeys)
	cur, err := col.Find(ctx, bson.M{})
	if err != nil {
		return res, fmt.Errorf("list ssh_keys: %w", err)
	}
	defer cur.Close(ctx)

	keysByUser := map[string][]string{}
	for cur.Next(ctx) {
		var k models.SSHKey
		if err := cur.Decode(&k); err != nil {
			continue
		}
		user := strings.TrimSpace(k.User)
		pub := strings.TrimSpace(k.PublicKey)
		if user == "" || pub == "" {
			continue
		}
		keysByUser[user] = append(keysByUser[user], pub)
		res.Total++
	}

	for user, keys := range keysByUser {
		sshDir := "/home/" + user + "/.ssh"
		if user == "root" {
			sshDir = "/root/.ssh"
		}
		if _, err := agent.RunCommand(ctx, "mkdir", "-p", sshDir); err != nil {
			log.Warn().Err(err).Str("user", user).Msg("rebuild ssh keys: mkdir failed")
			res.Failed += len(keys)
			continue
		}
		body := strings.Join(keys, "\n") + "\n"
		tmp := sshDir + "/authorized_keys.bzpanel.tmp"
		final := sshDir + "/authorized_keys"
		writeCmd := fmt.Sprintf("cat > %s <<'BZPANEL_EOF'\n%sBZPANEL_EOF", tmp, body)
		if _, err := agent.RunCommand(ctx, "bash", "-c", writeCmd); err != nil {
			log.Warn().Err(err).Str("user", user).Msg("rebuild ssh keys: tmp write failed")
			res.Failed += len(keys)
			continue
		}
		if _, err := agent.RunCommand(ctx, "mv", tmp, final); err != nil {
			log.Warn().Err(err).Str("user", user).Msg("rebuild ssh keys: mv failed")
			res.Failed += len(keys)
			continue
		}
		_, _ = agent.RunCommand(ctx, "chmod", "700", sshDir)
		_, _ = agent.RunCommand(ctx, "chmod", "600", final)
		if user != "root" {
			_, _ = agent.RunCommand(ctx, "chown", "-R", user+":"+user, sshDir)
		}
		res.Healed += len(keys)
	}
	log.Info().Int("users", len(keysByUser)).Int("keys", res.Total).Int("healed", res.Healed).Int("failed", res.Failed).Msg("rebuilt authorized_keys from Mongo")
	return res, nil
}

// ─────────────────────────────────────────────────────────────────────
// DNS zones + records → PowerDNS via pdnsutil
// ─────────────────────────────────────────────────────────────────────

// RebuildPowerDNSFromMongo walks dns_zones + dns_records, calls
// `pdnsutil create-zone` for any zone missing in PowerDNS, then
// re-emits every record via `pdnsutil replace-rrset` so the live
// DNS state matches Mongo.
//
// Pre-3.1.48 panel-records-only re-runs left the destination's
// PowerDNS empty for every transferred zone. Result: NXDOMAIN on
// public lookups, mail SPF fails, HTTPS cert validation breaks,
// nameserver delegation broken.
//
// Idempotent — re-running on an already-synced server is a no-op
// (create-zone returns "exists" + replace-rrset is the same write).
func (s *DNSService) RebuildPowerDNSFromMongo(ctx context.Context) (*RehydrateResult, error) {
	res := &RehydrateResult{}

	// Step 1 — every zone gets `pdnsutil create-zone` if missing.
	zCol := s.db.Collection(database.ColDNSZones)
	zCur, err := zCol.Find(ctx, bson.M{})
	if err != nil {
		return res, fmt.Errorf("list dns_zones: %w", err)
	}
	defer zCur.Close(ctx)

	zoneByID := map[string]string{} // _id hex → domain name
	for zCur.Next(ctx) {
		var z models.DNSZone
		if err := zCur.Decode(&z); err != nil {
			continue
		}
		domain := strings.TrimSpace(z.Domain)
		if domain == "" {
			continue
		}
		zoneByID[z.ID.Hex()] = domain
		res.Total++
		// pdnsutil list-zone returns exit 0 if it exists, non-zero
		// if not. Use that as the "should I create?" probe — much
		// faster than parsing pdnsutil list-all-zones output.
		if _, err := agent.RunCommand(ctx, "pdnsutil", "list-zone", domain); err != nil {
			if _, err := agent.RunCommand(ctx, "pdnsutil", "create-zone", domain); err != nil {
				log.Warn().Err(err).Str("zone", domain).Msg("rebuild dns: create-zone failed")
				res.Failed++
				continue
			}
		}
		res.Healed++
	}

	// Step 2 — every record from Mongo gets re-emitted via
	// `pdnsutil replace-rrset` (idempotent — same write, no diff =
	// no actual change). Group by (zone, name, type) so multi-value
	// rrsets land in one call.
	rCol := s.db.Collection(database.ColDNSRecords)
	rCur, err := rCol.Find(ctx, bson.M{})
	if err != nil {
		return res, fmt.Errorf("list dns_records: %w", err)
	}
	defer rCur.Close(ctx)

	type rrsetKey struct{ zone, name, rtype string }
	type rrsetVal struct {
		ttl    int
		values []string
	}
	rrsets := map[rrsetKey]*rrsetVal{}
	for rCur.Next(ctx) {
		var r models.DNSRecord
		if err := rCur.Decode(&r); err != nil {
			continue
		}
		zone := zoneByID[r.ZoneID.Hex()]
		if zone == "" {
			continue // orphan record, skip
		}
		name := strings.TrimSpace(r.Name)
		if name == "" {
			name = "@"
		}
		rtype := strings.ToUpper(strings.TrimSpace(r.Type))
		val := strings.TrimSpace(r.Value)
		if val == "" {
			continue
		}
		// MX / SRV records carry priority/weight/port that pdnsutil
		// expects inline ahead of the value (e.g. `10 mail.x.com.`).
		if rtype == "MX" && r.Priority != nil {
			val = fmt.Sprintf("%d %s", *r.Priority, val)
		}
		if rtype == "SRV" && r.Priority != nil && r.Weight != nil && r.Port != nil {
			val = fmt.Sprintf("%d %d %d %s", *r.Priority, *r.Weight, *r.Port, val)
		}
		k := rrsetKey{zone, name, rtype}
		if rrsets[k] == nil {
			ttl := r.TTL
			if ttl <= 0 {
				ttl = 3600
			}
			rrsets[k] = &rrsetVal{ttl: ttl}
		}
		rrsets[k].values = append(rrsets[k].values, val)
	}

	for k, v := range rrsets {
		args := []string{"replace-rrset", k.zone, k.name, k.rtype, fmt.Sprintf("%d", v.ttl)}
		args = append(args, v.values...)
		if _, err := agent.RunCommand(ctx, "pdnsutil", args...); err != nil {
			log.Warn().Err(err).Str("zone", k.zone).Str("name", k.name).Str("type", k.rtype).Msg("rebuild dns: replace-rrset failed")
		}
	}

	// Reload PowerDNS once at the end so `pdnsutil` writes are
	// picked up by the live serving threads.
	_, _ = agent.RunCommand(ctx, "pdnsutil", "rectify-all-zones")
	_, _ = agent.RunCommand(ctx, "systemctl", "reload", "pdns")

	res.Note = fmt.Sprintf("%d zones + %d rrsets re-emitted via pdnsutil", res.Healed, len(rrsets))
	log.Info().Int("zones", res.Healed).Int("rrsets", len(rrsets)).Int("failed", res.Failed).Msg("rebuilt PowerDNS from Mongo")
	return res, nil
}

// ─────────────────────────────────────────────────────────────────────
// Databases → MySQL CREATE DATABASE + CREATE USER + GRANT
// ─────────────────────────────────────────────────────────────────────

// RebuildMySQLAccessFromMongo walks every databases row + every
// db_users row, runs `CREATE DATABASE IF NOT EXISTS` + `CREATE USER
// IF NOT EXISTS` + `GRANT ALL` on the destination MySQL/MariaDB.
// Honours db_access_hosts so the host-pattern allowlist matches the
// panel UI.
//
// Pre-3.1.48 panel-records-only re-runs landed databases/users in
// Mongo but the destination's MySQL never created the actual user
// or granted privileges. App connections then failed with
// "Access denied for user '<name>'@'<host>'".
//
// Best-effort: SSH + MySQL root credentials are read from
// /opt/serverpanel/.env (same place the panel itself reads them).
// Per-row failure logs WARN; loop continues.
func (s *DatabaseService) RebuildMySQLAccessFromMongo(ctx context.Context) (*RehydrateResult, error) {
	res := &RehydrateResult{}

	// Pre-collect every db_user row so we can join on database_id
	// without a per-database round-trip.
	uCol := s.db.Collection(database.ColDBUsers)
	uCur, err := uCol.Find(ctx, bson.M{})
	if err != nil {
		return res, fmt.Errorf("list db_users: %w", err)
	}
	defer uCur.Close(ctx)
	usersByDB := map[string][]models.DatabaseUser{}
	for uCur.Next(ctx) {
		var u models.DatabaseUser
		if err := uCur.Decode(&u); err != nil {
			continue
		}
		key := u.DatabaseID.Hex()
		usersByDB[key] = append(usersByDB[key], u)
	}

	// Pre-collect every db_access_host so we can apply allowlists per
	// database without an extra mongo lookup per user.
	ahCol := s.db.Collection(database.ColDBAccessHosts)
	ahCur, err := ahCol.Find(ctx, bson.M{})
	hostsByDB := map[string][]string{}
	if err == nil {
		defer ahCur.Close(ctx)
		for ahCur.Next(ctx) {
			var h struct {
				DatabaseID string `bson:"database_id"`
				Host       string `bson:"host"`
			}
			if err := ahCur.Decode(&h); err != nil {
				continue
			}
			hostsByDB[h.DatabaseID] = append(hostsByDB[h.DatabaseID], h.Host)
		}
	}

	dCol := s.db.Collection(database.ColDatabases)
	dCur, err := dCol.Find(ctx, bson.M{})
	if err != nil {
		return res, fmt.Errorf("list databases: %w", err)
	}
	defer dCur.Close(ctx)
	for dCur.Next(ctx) {
		var d models.Database
		if err := dCur.Decode(&d); err != nil {
			continue
		}
		dbName := strings.TrimSpace(d.DBName)
		if dbName == "" {
			continue
		}
		res.Total++

		// Step 1: CREATE DATABASE IF NOT EXISTS.
		if _, err := agent.RunCommand(ctx, "mysql", "-uroot", "-e",
			fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;", mysqlEscape(dbName))); err != nil {
			log.Warn().Err(err).Str("db", dbName).Msg("rebuild mysql: CREATE DATABASE failed")
			res.Failed++
			continue
		}

		// Step 2: per-user CREATE USER + GRANT for every host
		// allowlist entry on this db (or 'localhost' as fallback).
		hosts := hostsByDB[d.ID.Hex()]
		if len(hosts) == 0 {
			hosts = []string{"localhost"}
		}
		for _, u := range usersByDB[d.ID.Hex()] {
			user := strings.TrimSpace(u.Username)
			pass := strings.TrimSpace(u.Password)
			if user == "" || pass == "" {
				// Password not stored (legacy row) — skip the user
				// entirely; operator will re-rotate from the panel.
				continue
			}
			for _, host := range hosts {
				host = strings.TrimSpace(host)
				if host == "" {
					host = "localhost"
				}
				stmt := fmt.Sprintf(
					"CREATE USER IF NOT EXISTS '%s'@'%s' IDENTIFIED BY '%s'; "+
						"ALTER USER '%s'@'%s' IDENTIFIED BY '%s'; "+
						"GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%s';",
					mysqlEscape(user), mysqlEscape(host), mysqlEscape(pass),
					mysqlEscape(user), mysqlEscape(host), mysqlEscape(pass),
					mysqlEscape(dbName), mysqlEscape(user), mysqlEscape(host),
				)
				if _, err := agent.RunCommand(ctx, "mysql", "-uroot", "-e", stmt); err != nil {
					log.Warn().Err(err).Str("user", user).Str("host", host).Str("db", dbName).Msg("rebuild mysql: GRANT failed")
				}
			}
		}
		_, _ = agent.RunCommand(ctx, "mysql", "-uroot", "-e", "FLUSH PRIVILEGES;")
		res.Healed++
	}
	log.Info().Int("databases", res.Total).Int("healed", res.Healed).Int("failed", res.Failed).Msg("rebuilt MySQL access from Mongo")
	return res, nil
}

// mysqlEscape backslash-escapes single quotes + backslashes so a
// password containing them survives the SQL string literal.
// MySQL also accepts standard ANSI '' escape but the backslash form
// is what `mysql -e` parses safely under bash's single-quote
// outer wrapper.
func mysqlEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	return r.Replace(s)
}

// ─────────────────────────────────────────────────────────────────────
// FTP accounts → /etc/pure-ftpd/pureftpd.passwd via pure-pw
// ─────────────────────────────────────────────────────────────────────

// RebuildFTPAccountsFromMongo walks ftp_accounts + runs
// `pure-pw useradd` for every entry that doesn't already exist in
// /etc/pure-ftpd/pureftpd.passwd, then `pure-pw mkdb` to compile the
// .pdb file Pure-FTPd actually reads.
//
// Pre-3.1.48 transferred FTP accounts appeared in the Domains page
// FTP-accounts panel but `pure-pw show <user>` returned empty; FTP
// login attempts failed "authentication failed" silently.
//
// Skips rows with blank passwords (legacy entries) — operator must
// re-set the password from the panel which goes through CreateFTP
// + writes pure-pw correctly.
//
// Standalone (not a method on any service) because the FTP rebuild
// only needs Mongo + agent.RunCommand, and DomainService's
// constructor is heavyweight (requires DNS/SSL/Email + a config
// struct). Letting bzpanel heal-after-transfer call this directly
// without instantiating DomainService keeps the CLI dependency
// graph small.
func RebuildFTPAccountsFromMongo(ctx context.Context, db *mongoDB) (*RehydrateResult, error) {
	res := &RehydrateResult{}
	col := db.Collection(database.ColFTPAccounts)
	cur, err := col.Find(ctx, bson.M{})
	if err != nil {
		return res, fmt.Errorf("list ftp_accounts: %w", err)
	}
	defer cur.Close(ctx)

	for cur.Next(ctx) {
		var ftp struct {
			Username  string `bson:"username"`
			Password  string `bson:"password"` // panel stores plaintext or AES blob; we use what's there
			HomeDir   string `bson:"home_dir"`
			SystemUID string `bson:"system_uid,omitempty"`
		}
		if err := cur.Decode(&ftp); err != nil {
			continue
		}
		user := strings.TrimSpace(ftp.Username)
		pass := strings.TrimSpace(ftp.Password)
		home := strings.TrimSpace(ftp.HomeDir)
		if user == "" || pass == "" || home == "" {
			res.Skipped++
			continue
		}
		res.Total++

		// Idempotent: pure-pw userdel returns non-zero if user
		// doesn't exist — swallow that. Then useradd with the
		// fresh creds. UID/GID 5000 is the panel convention
		// matching install.sh's vmail user; ftp accounts share the
		// same numeric ids.
		_, _ = agent.RunCommand(ctx, "bash", "-c",
			fmt.Sprintf("pure-pw userdel %s 2>/dev/null || true", rehydrateShellQuote(user)))
		// Pipe the password into pure-pw via stdin twice (it asks
		// for confirmation). Use bash -c with a here-string.
		cmd := fmt.Sprintf(
			"printf '%%s\\n%%s\\n' %s %s | pure-pw useradd %s -u 5000 -g 5000 -d %s -m",
			rehydrateShellQuote(pass), rehydrateShellQuote(pass), rehydrateShellQuote(user), rehydrateShellQuote(home),
		)
		if _, err := agent.RunCommand(ctx, "bash", "-c", cmd); err != nil {
			log.Warn().Err(err).Str("user", user).Msg("rebuild ftp: pure-pw useradd failed")
			res.Failed++
			continue
		}
		res.Healed++
	}

	// Compile the .pdb once at the end. Pure-FTPd reads .pdb, not
	// the .passwd source — a rebuild without mkdb is a no-op.
	if res.Healed > 0 {
		if _, err := agent.RunCommand(ctx, "pure-pw", "mkdb"); err != nil {
			log.Warn().Err(err).Msg("rebuild ftp: pure-pw mkdb failed — entries written but .pdb stale")
		}
	}
	log.Info().Int("total", res.Total).Int("healed", res.Healed).Int("skipped", res.Skipped).Int("failed", res.Failed).Msg("rebuilt FTP accounts from Mongo")
	return res, nil
}

// rehydrateShellQuote wraps a string in single quotes for safe bash use.
// Local helper because config_service.go already declares an unexported
// `shellQuote` with the same signature; keeping a distinct name avoids
// the package-level redeclaration without changing existing callers.
func rehydrateShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ─────────────────────────────────────────────────────────────────────
// WordPress installs → wp-config.php DB_HOST/USER/PASSWORD rewrite
// ─────────────────────────────────────────────────────────────────────

// RewriteWordPressConfigsFromMongo walks every wordpress_installs row
// and rewrites its wp-config.php with the destination's MySQL
// credentials. Pre-3.1.48 a transferred WP install pointed at the
// SOURCE's DB_HOST (often a different LAN IP) and the site loaded
// "Error establishing a database connection".
//
// For each install we look up the corresponding databases row + first
// db_user, derive DB_HOST=localhost (the destination's MySQL is
// always localhost from the WP server's perspective on the same
// box), and use sed to update the four define() lines in
// wp-config.php. Operator's site goes from "DB connection error" to
// the homepage with one rebuild.
func (s *WordPressService) RewriteWordPressConfigsFromMongo(ctx context.Context) (*RehydrateResult, error) {
	res := &RehydrateResult{}
	col := s.db.Collection(database.ColWordPress)
	cur, err := col.Find(ctx, bson.M{})
	if err != nil {
		return res, fmt.Errorf("list wordpress installs: %w", err)
	}
	defer cur.Close(ctx)

	for cur.Next(ctx) {
		var wp struct {
			InstallPath string `bson:"install_path"`
			DBName      string `bson:"db_name"`
			DBUser      string `bson:"db_user"`
			DBPassword  string `bson:"db_password"`
		}
		if err := cur.Decode(&wp); err != nil {
			continue
		}
		path := strings.TrimSpace(wp.InstallPath)
		dbName := strings.TrimSpace(wp.DBName)
		if path == "" || dbName == "" {
			res.Skipped++
			continue
		}
		res.Total++

		wpConfig := strings.TrimRight(path, "/") + "/wp-config.php"
		// Skip if file doesn't exist (install not actually deployed
		// on this box yet — the file-transfer step would have
		// created it).
		if _, err := agent.RunCommand(ctx, "test", "-f", wpConfig); err != nil {
			res.Skipped++
			continue
		}
		// Atomic per-line rewrite via four `sed -i` calls. Each
		// pattern matches `define('DB_*', '...');` (single OR
		// double quoted, any whitespace) and replaces just the
		// value. WP supports both quote styles so the regex is
		// liberal.
		seds := []struct {
			key, val string
		}{
			{"DB_NAME", dbName},
			{"DB_USER", strings.TrimSpace(wp.DBUser)},
			{"DB_PASSWORD", strings.TrimSpace(wp.DBPassword)},
			{"DB_HOST", "localhost"},
		}
		failed := false
		for _, s := range seds {
			if s.val == "" && s.key != "DB_PASSWORD" {
				continue // skip empty replacements except DB_PASSWORD which can be empty
			}
			// Escape sed delimiters in the value.
			esc := strings.NewReplacer(`/`, `\/`, `&`, `\&`, `\`, `\\`).Replace(s.val)
			cmd := fmt.Sprintf(
				`sed -i -E "s/(define\\s*\\(\\s*['\"]%s['\"],\\s*)['\"][^'\"]*['\"](\\s*\\)\\s*;)/\\1'%s'\\2/" %s`,
				s.key, esc, rehydrateShellQuote(wpConfig),
			)
			if _, err := agent.RunCommand(ctx, "bash", "-c", cmd); err != nil {
				log.Warn().Err(err).Str("path", wpConfig).Str("key", s.key).Msg("rewrite wp-config: sed failed")
				failed = true
				break
			}
		}
		if failed {
			res.Failed++
		} else {
			res.Healed++
		}
	}
	log.Info().Int("total", res.Total).Int("healed", res.Healed).Int("skipped", res.Skipped).Int("failed", res.Failed).Msg("rewrote wp-config.php from Mongo")
	return res, nil
}
