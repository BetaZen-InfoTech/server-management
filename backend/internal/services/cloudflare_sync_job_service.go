package services

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/cloudflare"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// syncJobTimeout caps a single sync run so a hung Cloudflare API can't pin a
// goroutine forever. Generous for a large sync_all.
const syncJobTimeout = 45 * time.Minute

// maxSyncEvents bounds the durable event stream so the doc can't grow without
// limit on a big sync_all — the UI shows the most recent activity.
const maxSyncEvents = 300

// CloudflareSyncJobService runs Cloudflare DNS sync jobs following the exact
// shape of ssl_bulk_job_service: a Mongo doc is the single source of truth, the
// work runs in a detached goroutine that writes progress after each domain, and
// a boot sweep marks abandoned "running" rows failed. It reuses CloudflareService
// for the provider + local-record access (same package → unexported helpers).
type CloudflareSyncJobService struct {
	db        *mongo.Database
	cf        *CloudflareService
	recoverer sync.Once
}

func NewCloudflareSyncJobService(db *mongo.Database, cf *CloudflareService) *CloudflareSyncJobService {
	return &CloudflareSyncJobService{db: db, cf: cf}
}

func (s *CloudflareSyncJobService) col() *mongo.Collection {
	return s.db.Collection(database.ColCloudflareSyncJobs)
}

// StartSyncDomain queues + launches a single-domain sync (local → Cloudflare).
func (s *CloudflareSyncJobService) StartSyncDomain(ctx context.Context, domain string, applyDeletes bool, owner, tenant primitive.ObjectID) (*models.CloudflareSyncJob, error) {
	domain = normalizeDomain(domain)
	job := s.newJob("sync_domain", applyDeletes, owner, tenant, []string{domain})
	return s.insertAndRun(ctx, job)
}

// StartSyncAll queues + launches a sync over every domain connected to
// Cloudflare (dns_zones.provider == "cloudflare" with a zone id).
func (s *CloudflareSyncJobService) StartSyncAll(ctx context.Context, applyDeletes bool, owner, tenant primitive.ObjectID) (*models.CloudflareSyncJob, error) {
	domains, err := s.connectedDomains(ctx)
	if err != nil {
		return nil, err
	}
	job := s.newJob("sync_all", applyDeletes, owner, tenant, domains)
	return s.insertAndRun(ctx, job)
}

func (s *CloudflareSyncJobService) connectedDomains(ctx context.Context) ([]string, error) {
	cur, err := s.db.Collection(database.ColDNSZones).Find(ctx, bson.M{
		"provider":   "cloudflare",
		"cf_zone_id": bson.M{"$nin": bson.A{nil, ""}},
	})
	if err != nil {
		return nil, err
	}
	var zones []models.DNSZone
	if err := cur.All(ctx, &zones); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(zones))
	seen := map[string]struct{}{}
	for _, z := range zones {
		d := normalizeDomain(z.Domain)
		if d == "" {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	sort.Strings(out)
	return out, nil
}

func (s *CloudflareSyncJobService) newJob(kind string, applyDeletes bool, owner, tenant primitive.ObjectID, domains []string) *models.CloudflareSyncJob {
	now := time.Now()
	items := make([]models.CloudflareSyncItem, 0, len(domains))
	for _, d := range domains {
		items = append(items, models.CloudflareSyncItem{Domain: d, Status: "pending"})
	}
	return &models.CloudflareSyncJob{
		OwnerUserID:  owner,
		TenantID:     tenant,
		Kind:         kind,
		Direction:    "local_to_cf",
		Status:       "queued",
		ApplyDeletes: applyDeletes,
		Total:        len(domains),
		Items:        items,
		Events:       []models.CloudflareSyncEvent{},
		StartedAt:    now,
		UpdatedAt:    now,
	}
}

func (s *CloudflareSyncJobService) insertAndRun(ctx context.Context, job *models.CloudflareSyncJob) (*models.CloudflareSyncJob, error) {
	res, err := s.col().InsertOne(ctx, job)
	if err != nil {
		return nil, err
	}
	job.ID = res.InsertedID.(primitive.ObjectID)
	// Detached goroutine so the work survives the HTTP client disconnecting.
	go s.run(job.ID)
	return job, nil
}

// run executes the job in the background with its own timeout context.
func (s *CloudflareSyncJobService) run(jobID primitive.ObjectID) {
	ctx, cancel := context.WithTimeout(context.Background(), syncJobTimeout)
	defer cancel()

	var job models.CloudflareSyncJob
	if err := s.col().FindOne(ctx, bson.M{"_id": jobID}).Decode(&job); err != nil {
		return
	}
	job.Status = "running"
	job.UpdatedAt = time.Now()
	s.appendEvent(&job, "info", "", "sync started")
	s.persist(ctx, &job)

	// Resolve the provider once — token/enabled is constant for the run.
	provider, _, err := s.cf.providerFor(ctx)
	if err != nil {
		s.finish(ctx, &job, "failed", err.Error())
		return
	}

	for i := range job.Items {
		if s.isCancelled(ctx, jobID) {
			job.CancelRequested = true
			s.appendEvent(&job, "warn", "", "cancelled by operator — stopping after current domain")
			s.finish(ctx, &job, "cancelled", "")
			return
		}
		it := &job.Items[i]
		it.Status = "running"
		job.CurrentDomain = it.Domain
		job.UpdatedAt = time.Now()
		s.persist(ctx, &job)

		s.reconcileDomain(ctx, provider, &job, it)

		if it.Error != "" {
			it.Status = "failed"
			job.Failed++
		} else {
			it.Status = "done"
		}
		job.Created += it.Created
		job.Updated += it.Updated
		job.Deleted += it.Deleted
		job.Skipped += it.Skipped
		job.Progress = int(float64(i+1) / float64(max1(len(job.Items))) * 100)
		job.UpdatedAt = time.Now()
		s.persist(ctx, &job)
	}

	job.CurrentDomain = ""
	status := "completed"
	if job.Failed > 0 && job.Failed == job.Total {
		status = "failed"
	}
	s.appendEvent(&job, "info", "", "sync finished: "+summary(&job))
	s.finish(ctx, &job, status, "")
}

// reconcileDomain pushes a single domain's local DNS records into its
// Cloudflare zone: create missing, update changed (pairing within an rrset to
// preserve record ids), and — only when ApplyDeletes is set — remove non-mail
// records that exist in Cloudflare but not locally. Mail records (MX / SPF /
// DKIM / DMARC / mail-A) are never proxied and never deleted. NS / SOA are
// skipped (Cloudflare owns them).
func (s *CloudflareSyncJobService) reconcileDomain(ctx context.Context, provider DNSProvider, job *models.CloudflareSyncJob, it *models.CloudflareSyncItem) {
	domain := it.Domain
	zoneID, err := s.cf.resolveZoneID(ctx, domain)
	if err != nil {
		it.Error = err.Error()
		s.appendEvent(job, "error", domain, "resolve zone failed: "+err.Error())
		return
	}
	if zoneID == "" {
		it.Status = "skipped"
		it.Skipped++
		s.appendEvent(job, "warn", domain, "not connected to Cloudflare — skipped")
		return
	}

	cfRecs, err := provider.ListRecords(ctx, zoneID)
	if err != nil {
		it.Error = err.Error()
		s.appendEvent(job, "error", domain, "list Cloudflare records failed: "+err.Error())
		return
	}
	localRecs, err := s.cf.localRecords(ctx, domain)
	if err != nil {
		it.Error = err.Error()
		s.appendEvent(job, "error", domain, "load local records failed: "+err.Error())
		return
	}

	type key struct{ typ, name string }
	localGroups := map[key]map[string]models.DNSRecord{}
	for _, r := range localRecs {
		k := key{strings.ToUpper(r.Type), normalizeName(r.Name, domain)}
		v := normalizeValue(r.Type, r.Value, r.Priority)
		if localGroups[k] == nil {
			localGroups[k] = map[string]models.DNSRecord{}
		}
		localGroups[k][v] = r
	}
	cfGroups := map[key]map[string]cloudflare.DNSRecord{}
	for _, r := range cfRecs {
		k := key{strings.ToUpper(r.Type), normalizeName(r.Name, domain)}
		v := normalizeValue(r.Type, r.Content, r.Priority)
		if cfGroups[k] == nil {
			cfGroups[k] = map[string]cloudflare.DNSRecord{}
		}
		cfGroups[k][v] = r
	}

	keySet := map[key]struct{}{}
	for k := range localGroups {
		keySet[k] = struct{}{}
	}
	for k := range cfGroups {
		keySet[k] = struct{}{}
	}
	keys := make([]key, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].typ != keys[j].typ {
			return keys[i].typ < keys[j].typ
		}
		return keys[i].name < keys[j].name
	})

	for _, k := range keys {
		// Cloudflare owns NS + SOA — never push or delete them.
		if k.typ == "NS" || k.typ == "SOA" {
			it.Skipped += len(localGroups[k])
			continue
		}
		lv := localGroups[k]
		cv := cfGroups[k]

		// Matched values — already correct; stamp the local row with the CF id
		// + managed_by marker so future syncs and IP-change protection have it.
		for val, rec := range lv {
			if cfr, ok := cv[val]; ok {
				s.stampLocalRecord(ctx, rec, cfr.ID)
				it.Skipped++
			}
		}

		toCreate := extrasSorted(lv, cv)
		cfExtra := cfExtrasSorted(cv, lv)

		// Pair create-vs-extra as in-place UPDATEs (preserves record ids).
		pair := len(toCreate)
		if len(cfExtra) < pair {
			pair = len(cfExtra)
		}
		for i := 0; i < pair; i++ {
			localRec := lv[toCreate[i]]
			cfr := cv[cfExtra[i]]
			p := localToParams(localRec, domain)
			// Preserve the record's existing Cloudflare proxied state on update.
			// Cloudflare's PUT is a full replace and local (PowerDNS) has no
			// proxy concept, so without this a sync would silently turn OFF an
			// operator's orange-cloud (CDN/WAF) on every web record. Mail
			// records were already forced DNS-only by localToParams.
			if !isMailRecord(p.Type, p.Name, p.Content, "") {
				proxied := cfr.Proxied
				p.Proxied = &proxied
			}
			if _, err := provider.UpdateRecord(ctx, zoneID, cfr.ID, p); err != nil {
				it.Error = err.Error()
				s.appendEvent(job, "error", domain, "update "+k.typ+" "+k.name+" failed: "+err.Error())
				return
			}
			s.stampLocalRecord(ctx, localRec, cfr.ID)
			it.Updated++
			s.appendEvent(job, "info", domain, "updated "+k.typ+" "+k.name)
		}
		for i := pair; i < len(toCreate); i++ {
			localRec := lv[toCreate[i]]
			p := localToParams(localRec, domain)
			created, err := provider.CreateRecord(ctx, zoneID, p)
			if err != nil {
				it.Error = err.Error()
				s.appendEvent(job, "error", domain, "create "+k.typ+" "+k.name+" failed: "+err.Error())
				return
			}
			if created != nil {
				s.stampLocalRecord(ctx, localRec, created.ID)
			}
			it.Created++
			s.appendEvent(job, "info", domain, "created "+k.typ+" "+k.name)
		}
		// Leftover Cloudflare-only records in this rrset.
		for i := pair; i < len(cfExtra); i++ {
			cfr := cv[cfExtra[i]]
			if isMailRecord(cfr.Type, cfr.Name, cfr.Content, "") {
				it.Skipped++
				s.appendEvent(job, "warn", domain, "kept mail record "+cfr.Type+" "+cfr.Name+" (protected)")
				continue
			}
			if !job.ApplyDeletes {
				it.Skipped++
				continue
			}
			if err := provider.DeleteRecord(ctx, zoneID, cfr.ID); err != nil {
				it.Error = err.Error()
				s.appendEvent(job, "error", domain, "delete "+cfr.Type+" "+cfr.Name+" failed: "+err.Error())
				return
			}
			it.Deleted++
			s.appendEvent(job, "warn", domain, "deleted Cloudflare-only "+cfr.Type+" "+cfr.Name)
		}
	}

	// Mark the local zone synced.
	_, _ = s.db.Collection(database.ColDNSZones).UpdateOne(ctx,
		bson.M{"domain": domain},
		bson.M{"$set": bson.M{"sync_state": "synced", "last_sync_at": time.Now(), "last_error": ""}},
	)
}

// stampLocalRecord records the Cloudflare record id + managed_by classification
// on the local dns_records row so subsequent syncs address records by id and
// the IP-change flow can protect mail records by the explicit marker.
func (s *CloudflareSyncJobService) stampLocalRecord(ctx context.Context, r models.DNSRecord, cfRecordID string) {
	if r.ID.IsZero() {
		return
	}
	managed := classifyRecord(r.Type, r.Name, r.Value, r.ManagedBy)
	_, _ = s.db.Collection(database.ColDNSRecords).UpdateByID(ctx, r.ID, bson.M{"$set": bson.M{
		"cf_record_id": cfRecordID,
		"managed_by":   managed,
	}})
}

// GetSyncJob returns a job by id for polling.
func (s *CloudflareSyncJobService) GetSyncJob(ctx context.Context, id primitive.ObjectID) (*models.CloudflareSyncJob, error) {
	var job models.CloudflareSyncJob
	if err := s.col().FindOne(ctx, bson.M{"_id": id}).Decode(&job); err != nil {
		return nil, err
	}
	return &job, nil
}

// ListSyncJobs returns the most recent jobs (for the panel's history list).
func (s *CloudflareSyncJobService) ListSyncJobs(ctx context.Context, limit int64) ([]models.CloudflareSyncJob, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	cur, err := s.col().Find(ctx, bson.M{},
		options.Find().SetSort(bson.M{"started_at": -1}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	var jobs []models.CloudflareSyncJob
	if err := cur.All(ctx, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

// CancelSyncJob flags a running job for cooperative cancellation.
func (s *CloudflareSyncJobService) CancelSyncJob(ctx context.Context, id primitive.ObjectID) error {
	_, err := s.col().UpdateOne(ctx, bson.M{"_id": id, "status": bson.M{"$in": bson.A{"queued", "running"}}},
		bson.M{"$set": bson.M{"cancel_requested": true, "updated_at": time.Now()}})
	return err
}

// RecoverStaleOnBoot marks any job left "running"/"queued" when the process
// died as failed, so the UI shows a terminal state instead of a stuck bar. Sync
// jobs are idempotent, so the operator simply re-runs. Guarded by a Once.
func (s *CloudflareSyncJobService) RecoverStaleOnBoot(ctx context.Context) {
	s.recoverer.Do(func() {
		now := time.Now()
		_, _ = s.col().UpdateMany(ctx,
			bson.M{"status": bson.M{"$in": bson.A{"queued", "running"}}},
			bson.M{"$set": bson.M{
				"status":       "failed",
				"error":        "interrupted by a backend restart — re-run the sync (it is idempotent)",
				"updated_at":   now,
				"completed_at": now,
			}},
		)
	})
}

// --- internals --------------------------------------------------------------

func (s *CloudflareSyncJobService) isCancelled(ctx context.Context, id primitive.ObjectID) bool {
	var doc struct {
		Cancel bool `bson:"cancel_requested"`
	}
	if err := s.col().FindOne(ctx, bson.M{"_id": id},
		options.FindOne().SetProjection(bson.M{"cancel_requested": 1})).Decode(&doc); err != nil {
		return false
	}
	return doc.Cancel
}

func (s *CloudflareSyncJobService) appendEvent(job *models.CloudflareSyncJob, level, domain, msg string) {
	job.Events = append(job.Events, models.CloudflareSyncEvent{
		Time: time.Now(), Level: level, Domain: domain, Message: msg,
	})
	if len(job.Events) > maxSyncEvents {
		job.Events = job.Events[len(job.Events)-maxSyncEvents:]
	}
}

func (s *CloudflareSyncJobService) persist(ctx context.Context, job *models.CloudflareSyncJob) {
	// $set only the fields the run() goroutine owns. cancel_requested is
	// DELIBERATELY excluded: it is owned by CancelSyncJob (a separate
	// UpdateOne). A whole-doc ReplaceOne here would write the in-memory
	// cancel_requested (always false) and clobber a cancel that arrived while
	// a domain was mid-sync — silently losing the cancellation. Excluding it
	// keeps the DB flag authoritative so the next isCancelled() read sees it.
	_, _ = s.col().UpdateByID(ctx, job.ID, bson.M{"$set": bson.M{
		"status":         job.Status,
		"total":          job.Total,
		"created":        job.Created,
		"updated":        job.Updated,
		"deleted":        job.Deleted,
		"skipped":        job.Skipped,
		"failed":         job.Failed,
		"progress":       job.Progress,
		"current_domain": job.CurrentDomain,
		"items":          job.Items,
		"events":         job.Events,
		"error":          job.Error,
		"updated_at":     job.UpdatedAt,
		"completed_at":   job.CompletedAt,
	}})
}

func (s *CloudflareSyncJobService) finish(ctx context.Context, job *models.CloudflareSyncJob, status, errMsg string) {
	now := time.Now()
	job.Status = status
	if errMsg != "" {
		job.Error = errMsg
	}
	if status == "completed" || status == "cancelled" {
		job.Progress = 100
	}
	job.CompletedAt = &now
	job.UpdatedAt = now
	s.persist(ctx, job)
}

// --- helpers ----------------------------------------------------------------

func summary(job *models.CloudflareSyncJob) string {
	return strconv.Itoa(job.Created) + " created, " +
		strconv.Itoa(job.Updated) + " updated, " +
		strconv.Itoa(job.Deleted) + " deleted, " +
		strconv.Itoa(job.Skipped) + " unchanged, " +
		strconv.Itoa(job.Failed) + " failed"
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// extrasSorted returns keys of local map not present in cf map, sorted.
func extrasSorted(a map[string]models.DNSRecord, b map[string]cloudflare.DNSRecord) []string {
	out := make([]string, 0)
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// cfExtrasSorted returns keys of cf map not present in local map, sorted.
func cfExtrasSorted(a map[string]cloudflare.DNSRecord, b map[string]models.DNSRecord) []string {
	out := make([]string, 0)
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// localToParams converts a local DNS record to Cloudflare RecordParams. It
// splits an MX priority out of the value, clamps sub-minimum TTLs to Cloudflare's
// floor, and forces mail records DNS-only.
func localToParams(r models.DNSRecord, domain string) cloudflare.RecordParams {
	p := cloudflare.RecordParams{
		Type:    strings.ToUpper(r.Type),
		Name:    normalizeName(r.Name, domain),
		Content: strings.TrimSuffix(strings.TrimSpace(r.Value), "."),
		TTL:     r.TTL,
	}
	if r.Priority != nil {
		pr := *r.Priority
		p.Priority = &pr
	}
	if p.Type == "MX" {
		fields := strings.Fields(p.Content)
		if len(fields) > 1 {
			if n, err := strconv.Atoi(fields[0]); err == nil {
				pr := n
				p.Priority = &pr
				p.Content = strings.Join(fields[1:], " ")
			}
		}
		if p.Priority == nil {
			def := 10
			p.Priority = &def
		}
	}
	if p.Type == "TXT" {
		// Send the bare TXT string — Cloudflare adds its own quoting. Sending
		// PowerDNS's surrounding quotes would round-trip as an escaped,
		// mismatching value and create a duplicate on the next sync. Case is
		// preserved (DKIM public keys are case-sensitive base64).
		p.Content = stripTXTQuoting(p.Content)
	}
	// Cloudflare rejects explicit TTLs below 60 (1 == "auto"). Local bootstrap
	// TTLs of 30s would 400, so clamp up.
	if p.TTL != 1 && p.TTL < 60 {
		p.TTL = 60
	}
	forceMailDNSOnly(&p)
	return p
}
