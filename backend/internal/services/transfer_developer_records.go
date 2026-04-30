// Package services — transfer_developer_records.go syncs the Developer-page
// assets (API tokens + outbound webhook subscriptions) when a panel migrates
// servers. Both collections key on tenant_id, which normaliseDoc translates
// through the source→destination user_id map automatically.
//
// Webhook signing secrets are AES-GCM encrypted under the source's
// APP_ENCRYPTION_KEY. Rather than ferry that key over SSH (the SMTP path
// does this; we deliberately don't here), we land each webhook with its
// secret blob preserved but `active=false`. The destination's UI surfaces a
// "Rotate to activate" CTA that mints a fresh secret under the destination's
// own key, so the operator never has to re-create the URL / event list /
// description to recover from the migration.
//
// Delivery logs are intentionally skipped — they're short-lived attempt
// records, not config worth migrating.
package services

import (
	"context"
	"fmt"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// syncAPITokens copies api_tokens rows from source to destination. Dedup is
// by token_id (the public half of the bearer string) — tokens never get a
// fresh id on transfer because integrations on the operator's side already
// know that string. Bcrypt'd secret_hash carries over verbatim and keeps
// working on the destination since bcrypt validates without external state.
func (s *TransferService) syncAPITokens(ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB string, idMap map[string]primitive.ObjectID) int {
	docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, database.ColAPITokens, "{}")
	if err != nil {
		s.addLog(ctx, jobID, "warn", fmt.Sprintf("Could not read source api_tokens: %s", err), "panel-records")
		return 0
	}
	col := s.db.Collection(database.ColAPITokens)
	inserted := 0
	for _, raw := range docs {
		doc := s.normaliseDoc(raw, idMap)
		tokenID, _ := doc["token_id"].(string)
		if tokenID == "" {
			continue
		}
		// Drop tokens whose tenant_id didn't translate through idMap. They
		// belong to a vendor we didn't migrate, so leaving them would
		// orphan a bearer string against a missing tenant.
		if tid, ok := doc["tenant_id"]; !ok || tid == nil {
			continue
		}
		var existing bson.M
		if err := col.FindOne(ctx, bson.M{"token_id": tokenID}).Decode(&existing); err == nil {
			continue
		} else if err != mongo.ErrNoDocuments {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("api_tokens lookup token_id=%q: %s", tokenID, err), "panel-records")
			continue
		}
		doc["_id"] = primitive.NewObjectID()
		if _, err := col.InsertOne(ctx, doc); err != nil {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("insert api_token %q failed: %s", tokenID, err), "panel-records")
			continue
		}
		inserted++
	}
	if inserted > 0 {
		s.addLog(ctx, jobID, "info", fmt.Sprintf("Migrated %d API token(s) — bearer strings keep working without rotation.", inserted), "panel-records")
	}
	return inserted
}

// syncWebhookEndpoints copies webhook_endpoints rows from source to dest.
// Dedup is by (tenant_id, url) — the same operator hooking the same URL
// twice on a re-run would otherwise duplicate the row.
//
// Imported endpoints land with `active=false`. The signing secret survives
// (encrypted under the source's APP_ENCRYPTION_KEY, which we don't have on
// the destination); the operator must click Rotate to mint a fresh secret
// under the destination's key, at which point the row flips back to active.
// Without this guard, the dispatcher would try to decrypt the migrated
// blob, fail at AEAD verification, and silently drop every event.
func (s *TransferService) syncWebhookEndpoints(ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB string, idMap map[string]primitive.ObjectID) int {
	docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, database.ColWebhookEndpoints, "{}")
	if err != nil {
		s.addLog(ctx, jobID, "warn", fmt.Sprintf("Could not read source webhook_endpoints: %s", err), "panel-records")
		return 0
	}
	col := s.db.Collection(database.ColWebhookEndpoints)
	inserted := 0
	for _, raw := range docs {
		doc := s.normaliseDoc(raw, idMap)
		url, _ := doc["url"].(string)
		if url == "" {
			continue
		}
		tid, ok := doc["tenant_id"].(primitive.ObjectID)
		if !ok || tid.IsZero() {
			continue
		}
		var existing bson.M
		if err := col.FindOne(ctx, bson.M{"tenant_id": tid, "url": url}).Decode(&existing); err == nil {
			continue
		} else if err != mongo.ErrNoDocuments {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("webhook_endpoints lookup url=%q: %s", url, err), "panel-records")
			continue
		}
		doc["_id"] = primitive.NewObjectID()
		// Disabled until the operator rotates the secret. The dispatcher
		// keys on `active=true`, so freezing here means zero spurious
		// "AEAD: message authentication failed" attempts in the meantime.
		doc["active"] = false
		doc["last_error"] = "Migrated from source server — rotate signing secret to reactivate."
		if _, err := col.InsertOne(ctx, doc); err != nil {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("insert webhook %q failed: %s", url, err), "panel-records")
			continue
		}
		inserted++
	}
	if inserted > 0 {
		s.addLog(ctx, jobID, "info", fmt.Sprintf("Migrated %d webhook endpoint(s) — rotate each signing secret in Developer → Webhooks to reactivate.", inserted), "panel-records")
	}
	return inserted
}
