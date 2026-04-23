package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/mailer"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// NotifierService is the single funnel that turns "something happened in
// the panel" (a domain was added, SSL issued, SSL failed, a registrar
// expiry is approaching) into an outgoing email to the right vendor.
//
// It exists so the per-event email logic — who to send to, what panel
// name and URL to use, how to swallow transient mailer failures
// without bubbling them into product flows — lives in exactly one
// place. Services that need to notify get a pointer to this (via
// Set* wiring from main.go) and call the appropriate method; they
// never touch mailer.Mailer directly.
//
// All sends run with a short internal timeout so a slow or dead SMTP
// relay can't stall the HTTP handler that triggered the event. When
// the mailer isn't configured yet (fresh install), methods log and
// return nil instead of erroring — nothing about the product flow
// (adding a domain, issuing SSL) should break just because the
// operator hasn't filled in SMTP yet.
type NotifierService struct {
	db           *mongo.Database
	m            *mailer.Mailer
	panelDomain  string // e.g. "panel.betazeninfotech.com", used to build PanelURL
	panelName    string // product name; sourced from pkg/version but we keep it here to avoid an import
}

// NewNotifierService wires the mailer + panel hostname used in every
// email link. Both can be empty on a fresh install — the notifier
// no-ops gracefully until they're populated.
func NewNotifierService(db *mongo.Database, m *mailer.Mailer, panelDomain string) *NotifierService {
	return &NotifierService{
		db:          db,
		m:           m,
		panelDomain: panelDomain,
		panelName:   "Betazen Server Panel",
	}
}

// whmURL builds the link we want WHM-side recipients (the platform
// owner on SMTP-setup confirmation) to land on. Falls back to the
// public https://<panelDomain> when the panel is reachable over TLS,
// and to http://<panelDomain>/whm otherwise — we don't *know* whether
// SSL is on here, so https is the safer default on production domains.
func (n *NotifierService) whmURL() string {
	d := strings.TrimSpace(n.panelDomain)
	if d == "" {
		return ""
	}
	return "https://" + d + "/whm"
}

// userURL builds the link vendors / customers should land on. Same
// shape as whmURL but points at the /user-panel surface — domain adds
// and SSL notices go to hosting customers, not the platform owner.
func (n *NotifierService) userURL() string {
	d := strings.TrimSpace(n.panelDomain)
	if d == "" {
		return ""
	}
	return "https://" + d + "/user-panel"
}

// send performs the actual SMTP dispatch with a hard 30-second ceiling
// so a hung relay can't block the caller's request. Every notifier
// method uses this so retry / logging behaviour stays consistent.
// Returns nil when the mailer is disabled or the recipient is empty
// so fire-and-forget callers (domain/SSL events) don't need to care;
// callers that DO care (SMTP save verification) can inspect the
// returned error.
func (n *NotifierService) send(ctx context.Context, to, subject, text, html string, ev string) error {
	if n == nil || n.m == nil || !n.m.Enabled() {
		log.Info().Str("event", ev).Str("to", to).Msg("notifier: mailer disabled — skipped")
		return nil
	}
	to = strings.TrimSpace(to)
	if to == "" {
		log.Warn().Str("event", ev).Msg("notifier: empty recipient — skipped")
		return nil
	}
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := n.m.Send(sendCtx, mailer.Message{
		To:      to,
		Subject: subject,
		Text:    text,
		HTML:    html,
	})
	if err != nil {
		if errors.Is(err, mailer.ErrDisabled) {
			return nil
		}
		log.Warn().Err(err).Str("event", ev).Str("to", to).Msg("notifier: send failed")
		return err
	}
	log.Info().Str("event", ev).Str("to", to).Msg("notifier: sent")
	return nil
}

// lookupVendorFor returns the tenant root's registered email for the
// given hosting-account username. The user requirement is that domain
// and SSL notifications go to the VENDOR's reg mailid, not to the
// hosting account holder — so when the direct owner is a customer /
// staff / support / developer (role below vendor_admin), we walk up
// via TenantID to the vendor_admin / vendor_owner who owns the
// tenant and return that user's email instead.
//
// For accounts that ARE the tenant root (vendor_admin creating a
// domain directly on themselves, or the platform owner on their own
// account), TenantID == their own _id so the walk is a no-op and we
// return their email unchanged.
//
// Falls back to the direct owner's email when TenantID is missing
// (pre-multi-tenant legacy rows) or the tenant root lookup fails so
// notifications still go somewhere instead of being dropped silently.
func (n *NotifierService) lookupVendorFor(ctx context.Context, username string) (email, name string) {
	// Delegate to the shared helper in tenant_scope.go so this rule
	// lives in exactly one place. Logging that used to happen inside
	// this method is intentionally dropped — the helper is silent so
	// other callers (domain list enrichment, SSL autofill) don't
	// pollute logs on every request. If a notifier-specific trace is
	// ever needed it can be added back here at the call site.
	return LookupVendorEmailForUsername(ctx, n.db, username)
}

// contactEmail reads the server-wide admin contact address (written by
// the "Contact Email" field on Server Settings). Falls back to the
// configured SMTP From address so the SMTP-setup confirmation still
// lands somewhere useful on a fresh install where the contact field
// hasn't been touched yet.
func (n *NotifierService) contactEmail(ctx context.Context) string {
	if n == nil || n.db == nil {
		return ""
	}
	var doc bson.M
	_ = n.db.Collection(database.ColServerConfig).
		FindOne(ctx, bson.M{"key": "contact_email"}).
		Decode(&doc)
	if v, ok := doc["value"].(string); ok {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	if n.m != nil {
		if s := strings.TrimSpace(n.m.Config().FromAddr); s != "" {
			return s
		}
	}
	return ""
}

// -----------------------------------------------------------------------
// Public API — one method per panel event.
// -----------------------------------------------------------------------

// NotifySMTPConfigured is called from PanelMailService.Save once the
// relay flips into the "configured" state. The recipient is the panel
// contact email; the email doubles as a self-test — if it doesn't
// land, the operator knows something's wrong with the relay before
// password resets start failing silently. Returns the send error so
// Save can surface the real reason (Gmail app-password required,
// port blocked, etc.) back to the UI instead of letting it rot in
// the server log.
func (n *NotifierService) NotifySMTPConfigured(ctx context.Context) error {
	if n == nil || n.m == nil || !n.m.Enabled() {
		return nil
	}
	to := n.contactEmail(ctx)
	if to == "" {
		log.Info().Msg("notifier: SMTP setup — no contact email to send confirmation to")
		return nil
	}
	cfg := n.m.Config()
	subject, text, html, err := mailer.BuildSMTPSetup(mailer.SMTPSetupData{
		Email:     to,
		Host:      cfg.Host,
		Port:      cfg.Port,
		TLSMode:   cfg.TLSMode,
		FromAddr:  cfg.FromAddr,
		PanelName: n.panelName,
		PanelURL:  n.whmURL(),
	})
	if err != nil {
		log.Warn().Err(err).Msg("notifier: SMTP setup template render failed")
		return err
	}
	return n.send(ctx, to, subject, text, html, "smtp.configured")
}

// NotifyNewDomain fires right after the domain row is persisted in
// Mongo (before auto-SSL runs), so the vendor sees the chronology
// "domain added → SSL issued" and not the other way around.
func (n *NotifierService) NotifyNewDomain(ctx context.Context, domain *models.Domain, serverIP string) error {
	if n == nil || domain == nil {
		return nil
	}
	email, name := n.lookupVendorFor(ctx, domain.User)
	if email == "" {
		log.Info().Str("event", "domain.added").Str("user", domain.User).Str("domain", domain.Domain).Msg("notifier: vendor has no email — skipped")
		return nil
	}
	subject, text, html, err := mailer.BuildNewDomain(mailer.NewDomainData{
		Name:       name,
		Email:      email,
		Domain:     domain.Domain,
		PHPVersion: domain.PHPVersion,
		ServerIP:   serverIP,
		PanelName:  n.panelName,
		PanelURL:   n.userURL(),
	})
	if err != nil {
		log.Warn().Err(err).Msg("notifier: new-domain template render failed")
		return err
	}
	return n.send(ctx, email, subject, text, html, "domain.added")
}

// NotifySSLActive fires on both first-issue success AND renewal success.
// Automated=true distinguishes the renewal case so the subject reads
// "renewed" instead of "issued".
func (n *NotifierService) NotifySSLActive(ctx context.Context, domainName, issuer string, expiresAt *time.Time, autoRenew, automated bool) error {
	if n == nil {
		return nil
	}
	vendorUsername, err := n.lookupDomainOwner(ctx, domainName)
	if err != nil || vendorUsername == "" {
		log.Info().Str("event", "ssl.active").Str("domain", domainName).Msg("notifier: no owner — skipped")
		return nil
	}
	email, name := n.lookupVendorFor(ctx, vendorUsername)
	if email == "" {
		return nil
	}
	expires := ""
	if expiresAt != nil {
		expires = expiresAt.Format("02 Jan 2006")
	}
	subject, text, html, berr := mailer.BuildSSLActive(mailer.SSLActiveData{
		Name:      name,
		Email:     email,
		Domain:    domainName,
		Issuer:    issuer,
		ExpiresOn: expires,
		AutoRenew: autoRenew,
		Automated: automated,
		PanelName: n.panelName,
		PanelURL:  n.userURL(),
	})
	if berr != nil {
		log.Warn().Err(berr).Msg("notifier: ssl-active template render failed")
		return berr
	}
	return n.send(ctx, email, subject, text, html, "ssl.active")
}

// NotifySSLFailed fires after the SSL issuer has exhausted retries.
// The reason string should already be the friendly (non-stack-trace)
// form produced by friendlyCertbotError so the vendor sees something
// actionable like "DNS lookup failed" rather than a 20-line certbot
// dump.
func (n *NotifierService) NotifySSLFailed(ctx context.Context, domainName, reason string, attempts int) error {
	if n == nil {
		return nil
	}
	vendorUsername, err := n.lookupDomainOwner(ctx, domainName)
	if err != nil || vendorUsername == "" {
		return nil
	}
	email, name := n.lookupVendorFor(ctx, vendorUsername)
	if email == "" {
		return nil
	}
	subject, text, html, berr := mailer.BuildSSLFailed(mailer.SSLFailedData{
		Name:      name,
		Email:     email,
		Domain:    domainName,
		Reason:    reason,
		Attempts:  attempts,
		PanelName: n.panelName,
		PanelURL:  n.userURL(),
	})
	if berr != nil {
		log.Warn().Err(berr).Msg("notifier: ssl-failed template render failed")
		return berr
	}
	return n.send(ctx, email, subject, text, html, "ssl.failed")
}

// NotifyDomainExpiry is called by the daily cron for every (domain,
// stage) pair where the stage hasn't been sent yet. The caller is
// responsible for stage tracking so this method stays stateless and
// easy to unit-test.
func (n *NotifierService) NotifyDomainExpiry(ctx context.Context, domain *models.Domain, daysLeft int) error {
	if n == nil || domain == nil {
		return nil
	}
	email, name := n.lookupVendorFor(ctx, domain.User)
	if email == "" {
		return nil
	}
	expires := ""
	if domain.ExpiresOn != nil {
		expires = domain.ExpiresOn.Format("02 Jan 2006")
	}
	subject, text, html, err := mailer.BuildDomainExpiry(mailer.DomainExpiryData{
		Name:      name,
		Email:     email,
		Domain:    domain.Domain,
		Registrar: domain.Registrar,
		ExpiresOn: expires,
		DaysLeft:  daysLeft,
		AutoRenew: domain.AutoRenew,
		PanelName: n.panelName,
		PanelURL:  n.userURL(),
	})
	if err != nil {
		log.Warn().Err(err).Msg("notifier: domain-expiry template render failed")
		return err
	}
	return n.send(ctx, email, subject, text, html, "domain.expiry")
}

// lookupDomainOwner returns the `user` field (Linux account username)
// on the domain doc. SSL events carry only the FQDN; we need the
// owning account to find the vendor's email.
func (n *NotifierService) lookupDomainOwner(ctx context.Context, domainName string) (string, error) {
	if n == nil || n.db == nil {
		return "", errors.New("notifier: db unavailable")
	}
	col := n.db.Collection(database.ColDomains)
	var d models.Domain
	if err := col.FindOne(ctx, bson.M{"domain": domainName}).Decode(&d); err != nil {
		return "", err
	}
	return d.User, nil
}

// expiryBuckets is the descending ladder of days-left stages the cron
// will email the vendor about. Descending order matters: we match the
// FIRST bucket the domain is at or below that hasn't been sent yet,
// so a domain noticed for the first time at 6 days triggers the 7-
// day warning (not the 5 / 3 / 2 / 1 ones — those fire on subsequent
// days as the ticker rolls forward).
var expiryBuckets = []int{30, 21, 14, 7, 5, 3, 2, 1}

// RunDomainExpirySweep scans every domain that has an ExpiresOn set
// and, for each one, sends the smallest unsent expiry warning the
// domain is due for. Idempotent per-day: ExpiryNoticeStage on the
// domain doc tracks how far down the ladder we've already emailed.
//
// Called once at boot (best-effort) and once every 24h from the
// ticker in main.go. Returns the number of emails sent, so the
// caller can log a short summary.
func (n *NotifierService) RunDomainExpirySweep(ctx context.Context) (int, error) {
	if n == nil || n.db == nil {
		return 0, nil
	}

	col := n.db.Collection(database.ColDomains)
	// Pull every domain that has ExpiresOn set AND hasn't already
	// crossed the smallest (1-day) bucket. A nil/zero ExpiresOn is a
	// domain the operator never filled in the registration fields for
	// — nothing to warn about.
	filter := bson.M{
		"expires_on": bson.M{"$ne": nil},
	}
	cur, err := col.Find(ctx, filter)
	if err != nil {
		return 0, err
	}
	defer cur.Close(ctx)

	sent := 0
	now := time.Now()
	for cur.Next(ctx) {
		var d models.Domain
		if err := cur.Decode(&d); err != nil {
			continue
		}
		if d.ExpiresOn == nil {
			continue
		}

		// Whole-day resolution: ceil to the next day so a domain that
		// expires in 23h15m reports as 1 day out (not 0). Past-expiry
		// domains skip — we don't spam after the fact; they'll surface
		// in the panel's expiries widget.
		hours := d.ExpiresOn.Sub(now).Hours()
		if hours <= 0 {
			continue
		}
		daysLeft := int((hours + 23) / 24) // ceil

		// Pick the stage we SHOULD notify at: the largest bucket that
		// daysLeft is <=. If daysLeft is 13, that's the 14 bucket; if
		// it's 6, that's the 7 bucket; if it's 35, no bucket yet so we
		// skip. (Buckets are in descending order — first match wins.)
		stage := 0
		for _, b := range expiryBuckets {
			if daysLeft <= b {
				stage = b
				// keep iterating — we want the SMALLEST matching
				// bucket so 13d hits 7 (the most urgent applicable
				// one), not 30.
			}
		}
		if stage == 0 {
			continue
		}

		// Renewal detection: stage going UP (stage > saved) means the
		// registrar expiry was extended and the domain is now LESS
		// urgent than last time we emailed. Reset the tracker so the
		// next cycle starts fresh, and skip sending this round — the
		// vendor doesn't need a "30 days left" ping immediately after
		// renewing; the cron will catch the real warnings later if
		// renewal doesn't go through next year.
		if d.ExpiryNoticeStage != 0 && stage > d.ExpiryNoticeStage {
			_, rErr := col.UpdateOne(ctx,
				bson.M{"_id": d.ID},
				bson.M{"$set": bson.M{"expiry_notice_stage": 0, "updated_at": time.Now()}},
			)
			if rErr != nil {
				log.Warn().Err(rErr).Str("domain", d.Domain).Msg("notifier: failed to reset expiry_notice_stage after renewal")
			}
			continue
		}

		// Dedup: we never re-send a bucket we've already emailed for.
		// ExpiryNoticeStage stores the smallest bucket the cron has
		// stamped; current stage <= saved means this bucket (or an
		// earlier, larger one) is already covered.
		if d.ExpiryNoticeStage != 0 && stage >= d.ExpiryNoticeStage {
			continue
		}

		if err := n.NotifyDomainExpiry(ctx, &d, daysLeft); err != nil {
			// Already logged inside send() — don't stamp the bucket on
			// send failure so the next sweep retries the same bucket.
			continue
		}
		sent++

		// Stamp the domain so the next sweep doesn't repeat this
		// bucket. Best-effort — on write failure we log and move on;
		// at worst the vendor gets a duplicate notice at the next
		// tick, which is strictly better than silently dropping a
		// warning because Mongo had a hiccup.
		_, upErr := col.UpdateOne(ctx,
			bson.M{"_id": d.ID},
			bson.M{"$set": bson.M{"expiry_notice_stage": stage, "updated_at": time.Now()}},
		)
		if upErr != nil {
			log.Warn().Err(upErr).Str("domain", d.Domain).Msg("notifier: failed to stamp expiry_notice_stage")
		}
	}
	return sent, cur.Err()
}
