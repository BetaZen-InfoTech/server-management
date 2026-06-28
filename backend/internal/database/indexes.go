package database

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// DedupEmailForwarders consolidates duplicate (source, domain) rows in
// the email_forwarders collection so the unique index below can be
// created cleanly. Duplicates land when a server-to-server transfer
// runs more than once: the Transfer Email step previously did a bare
// InsertOne for every postfix-parsed alias line, with no natural-key
// guard, so a 3rd retry left 3 copies of every forwarder in the list.
//
// Strategy: for each (source, domain) group with count > 1, keep ONE
// row — preferring the one with keep_copy=true (operator's intent
// outranks the transfer's default-false ghosts), and breaking ties on
// the oldest created_at. Deletes the rest. Idempotent — a second run
// finds zero groups with count > 1 and short-circuits.
//
// Returns the number of rows deleted; logged-only failures don't
// block boot. Called once before EnsureIndexes.
func DedupEmailForwarders(ctx context.Context, db *mongo.Database) (int, error) {
	col := db.Collection(ColForwarders)
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$group", Value: bson.M{
			"_id":   bson.M{"source": "$source", "domain": "$domain"},
			"ids":   bson.M{"$push": "$$ROOT"},
			"count": bson.M{"$sum": 1},
		}}},
		bson.D{{Key: "$match", Value: bson.M{"count": bson.M{"$gt": 1}}}},
	}
	cur, err := col.Aggregate(ctx, pipeline)
	if err != nil {
		return 0, err
	}
	defer cur.Close(ctx)

	deleted := 0
	for cur.Next(ctx) {
		var grp struct {
			IDs []bson.M `bson:"ids"`
		}
		if err := cur.Decode(&grp); err != nil {
			continue
		}
		if len(grp.IDs) <= 1 {
			continue
		}
		// Pick the keeper: highest priority is keep_copy=true,
		// secondary is oldest created_at. The rest get deleted.
		keepIdx := 0
		for i, doc := range grp.IDs {
			ki := keepCopyTrue(grp.IDs[keepIdx])
			cur := keepCopyTrue(doc)
			if !ki && cur {
				keepIdx = i
				continue
			}
			if ki == cur {
				if olderThan(doc, grp.IDs[keepIdx]) {
					keepIdx = i
				}
			}
		}
		for i, doc := range grp.IDs {
			if i == keepIdx {
				continue
			}
			if id, ok := doc["_id"]; ok {
				if _, dErr := col.DeleteOne(ctx, bson.M{"_id": id}); dErr == nil {
					deleted++
				}
			}
		}
	}
	return deleted, cur.Err()
}

func keepCopyTrue(doc bson.M) bool {
	if v, ok := doc["keep_copy"]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func olderThan(a, b bson.M) bool {
	// Best-effort comparison — both rows usually carry created_at as
	// a BSON DateTime which decodes into either time.Time or
	// primitive.DateTime depending on the driver path. Handle both;
	// missing timestamps sort LAST so a row with a real created_at
	// wins the keep slot when the other side is bare metadata.
	at, atok := timeOf(a, "created_at")
	bt, btok := timeOf(b, "created_at")
	if !atok && !btok {
		return false
	}
	if !atok {
		return false
	}
	if !btok {
		return true
	}
	return at.Before(bt)
}

func timeOf(doc bson.M, key string) (time.Time, bool) {
	v, exists := doc[key]
	if !exists {
		return time.Time{}, false
	}
	switch t := v.(type) {
	case time.Time:
		return t, true
	case primitive.DateTime:
		return t.Time(), true
	}
	return time.Time{}, false
}

func EnsureIndexes(ctx context.Context, db *mongo.Database) error {
	indexes := map[string][]mongo.IndexModel{
		ColUsers: {
			{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "role", Value: 1}}},
		},
		ColDomains: {
			{Keys: bson.D{{Key: "domain", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "user", Value: 1}}},
			{Keys: bson.D{{Key: "status", Value: 1}}},
		},
		ColApps: {
			{Keys: bson.D{{Key: "name", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "domain", Value: 1}}},
		},
		ColDatabases: {
			{Keys: bson.D{{Key: "db_name", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "domain", Value: 1}}},
		},
		ColMailboxes: {
			{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "domain", Value: 1}}},
		},
		ColForwarders: {
			// (source, domain) is unique — duplicate alias rows only
			// land via repeated transfer runs that pre-dated the upsert
			// fix. DedupEmailForwarders runs before this index is
			// created so existing duplicates get consolidated first.
			{Keys: bson.D{{Key: "source", Value: 1}, {Key: "domain", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "domain", Value: 1}}},
		},
		ColDNSZones: {
			{Keys: bson.D{{Key: "domain", Value: 1}}, Options: options.Index().SetUnique(true)},
		},
		ColDNSRecords: {
			{Keys: bson.D{{Key: "zone_id", Value: 1}}},
			{Keys: bson.D{{Key: "type", Value: 1}, {Key: "name", Value: 1}}},
		},
		ColSSLCerts: {
			{Keys: bson.D{{Key: "domain", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "expires_at", Value: 1}}},
		},
		ColBackups: {
			{Keys: bson.D{{Key: "domain", Value: 1}}},
			{Keys: bson.D{{Key: "created_at", Value: -1}}},
		},
		ColCronJobs: {
			{Keys: bson.D{{Key: "domain", Value: 1}}},
			{Keys: bson.D{{Key: "user", Value: 1}}},
		},
		ColAuditLogs: {
			{Keys: bson.D{{Key: "timestamp", Value: -1}}},
			{Keys: bson.D{{Key: "user.id", Value: 1}}},
			{Keys: bson.D{{Key: "action", Value: 1}}},
			{Keys: bson.D{{Key: "resource_type", Value: 1}}},
		},
		ColGitHubDeploys: {
			{Keys: bson.D{{Key: "domain", Value: 1}}},
			{Keys: bson.D{{Key: "repo", Value: 1}}},
		},
		ColMetrics: {
			// MonitoringService stores samples with a "timestamp" field (see
			// monitoring_service.go) and both the range query and the
			// retention DeleteMany filter on "timestamp" — the prior index on
			// the non-existent "collected_at" field meant every metrics read +
			// the per-tick cleanup ran a COLLSCAN. Index the real field.
			{Keys: bson.D{{Key: "timestamp", Value: -1}}},
		},
		ColEmailServerConfigs: {
			{Keys: bson.D{{Key: "domain", Value: 1}}},
			{Keys: bson.D{{Key: "status", Value: 1}}},
		},
		ColEmailInstallations: {
			{Keys: bson.D{{Key: "config_id", Value: 1}}},
			{Keys: bson.D{{Key: "status", Value: 1}}},
			{Keys: bson.D{{Key: "created_at", Value: -1}}},
		},
		ColProjects: {
			{Keys: bson.D{{Key: "slug", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "tenant_id", Value: 1}, {Key: "name", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"tenant_id": bson.M{"$exists": true}})},
		},
		ColProjectServices: {
			{Keys: bson.D{{Key: "project_id", Value: 1}, {Key: "name", Value: 1}}, Options: options.Index().SetUnique(true)},
			// `$gt: ""` picks up any non-empty string without tripping Mongo's
			// "partial filter can't use $not" rule that $ne: "" would hit.
			{Keys: bson.D{{Key: "primary_domain", Value: 1}}, Options: options.Index().SetPartialFilterExpression(bson.M{"primary_domain": bson.M{"$gt": ""}})},
		},
		ColProjectDeployments: {
			{Keys: bson.D{{Key: "project_id", Value: 1}, {Key: "started_at", Value: -1}}},
			{Keys: bson.D{{Key: "service_id", Value: 1}, {Key: "started_at", Value: -1}}},
		},
		ColDBAccessHosts: {
			{Keys: bson.D{{Key: "database_id", Value: 1}}},
			// (database_id, host) is unique — adding the same host twice is
			// nonsensical and we want the second add to fail fast with a
			// clear "already exists" error.
			{Keys: bson.D{{Key: "database_id", Value: 1}, {Key: "host", Value: 1}}, Options: options.Index().SetUnique(true)},
		},
		ColMailLogs: {
			// log_key = "<queue_id>:<first_seen_unix>" is the natural key the
			// ingestor upserts on (queue IDs are recycled by Postfix over
			// time, so pairing with first-seen keeps two unrelated messages
			// from merging into one row).
			{Keys: bson.D{{Key: "log_key", Value: 1}}, Options: options.Index().SetUnique(true)},
			// Primary list ordering — newest first.
			{Keys: bson.D{{Key: "first_seen", Value: -1}}},
			// Filter facets surfaced in the UI.
			{Keys: bson.D{{Key: "status", Value: 1}}},
			{Keys: bson.D{{Key: "direction", Value: 1}}},
			{Keys: bson.D{{Key: "source", Value: 1}}},
			// Tenant scoping: rows are matched on the local domains they
			// touch (sender + recipient domains) intersected with the
			// caller's tenant domains.
			{Keys: bson.D{{Key: "domains", Value: 1}}},
			// 90-day retention — TTL sweeps old rows so the collection can't
			// grow unbounded on a busy mail server. Operators who need
			// longer retention can drop this index and add their own.
			{Keys: bson.D{{Key: "created_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(90 * 24 * 60 * 60)},
		},
	}

	for col, idxs := range indexes {
		_, err := db.Collection(col).Indexes().CreateMany(ctx, idxs)
		if err != nil {
			return err
		}
	}

	return nil
}
