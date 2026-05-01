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
// Per-row signing-secret handling — the secret_enc field is AES-GCM-sealed
// under the SOURCE's APP_ENCRYPTION_KEY, which the destination doesn't
// have. We try (in order):
//
//  1. Re-encrypt under the destination key (the desired path) by SSH-grepping
//     /opt/serverpanel/.env on source and asking WebhookService to decrypt+
//     re-encrypt. On success the endpoint lands ACTIVE — no operator action
//     needed for the integration to keep firing post-cutover.
//  2. If the source key is unreadable or doesn't decrypt the blob, drop
//     secret_enc entirely and land the row INACTIVE with a "rotate to
//     reactivate" hint. The dispatcher's active-only filter keeps a
//     silently-broken HMAC seed from emitting noise events at the
//     receiver.
//
// WebhookService presence is enforced (rather than fall-through to inactive)
// because main.go always wires it now; a nil pointer here would silently
// regress the migration UX.
func (s *TransferService) syncWebhookEndpoints(ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB string, idMap map[string]primitive.ObjectID) int {
	docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, database.ColWebhookEndpoints, "{}")
	if err != nil {
		s.addLog(ctx, jobID, "warn", fmt.Sprintf("Could not read source webhook_endpoints: %s", err), "panel-records")
		return 0
	}
	col := s.db.Collection(database.ColWebhookEndpoints)

	// Source key is fetched once per run and reused across every webhook +
	// project PAT row. fetchSourceEncKey memoises the SSH grep result.
	srcEncKey := s.fetchSourceEncKey(ctx, host, port, sshUser, sshPass)
	if srcEncKey == "" {
		s.addLog(ctx, jobID, "warn",
			"webhooks: source APP_ENCRYPTION_KEY not readable from /opt/serverpanel/.env — migrated webhooks will land inactive (rotate to reactivate)",
			"panel-records")
	}

	inserted := 0
	reactivated := 0
	dormant := 0
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

		// Try the desired path: re-encrypt the secret under the
		// destination's key so the row comes up active.
		landActive := false
		if s.webhookSvc != nil && srcEncKey != "" {
			cipher := extractCipherBytes(raw["secret_enc"])
			if len(cipher) > 0 {
				newCipher, rErr := s.webhookSvc.ReencryptForTransfer(cipher, srcEncKey)
				if rErr == nil && len(newCipher) > 0 {
					doc["secret_enc"] = newCipher
					landActive = true
				} else {
					s.addLog(ctx, jobID, "warn",
						fmt.Sprintf("webhooks: re-encryption failed for %s (%v); landing inactive", url, rErr),
						"panel-records")
				}
			}
		}
		if landActive {
			doc["active"] = true
			doc["last_error"] = ""
			reactivated++
		} else {
			// Drop the unusable cipher rather than persist garbage that
			// would AEAD-fail on every dispatch attempt. Operator clicks
			// Rotate in Developer → Webhooks to mint a fresh secret.
			delete(doc, "secret_enc")
			doc["active"] = false
			doc["last_error"] = "Migrated from source server — rotate signing secret to reactivate."
			dormant++
		}

		if _, err := col.InsertOne(ctx, doc); err != nil {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("insert webhook %q failed: %s", url, err), "panel-records")
			continue
		}
		inserted++
	}
	if reactivated > 0 {
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("Migrated %d webhook endpoint(s) — secrets re-encrypted under destination key, endpoints ACTIVE.", reactivated),
			"panel-records")
	}
	if dormant > 0 {
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("%d webhook endpoint(s) landed INACTIVE — rotate each signing secret in Developer → Webhooks to reactivate.", dormant),
			"panel-records")
	}
	return inserted
}

// extractCipherBytes is shared with the SMTP path in transfer_panel_records.go.
// Both AES-GCM ciphers (panel_mail.password_cipher, webhook_endpoints.secret_enc,
// projects.github_pat_encrypted) come off mongoexport as either []byte,
// primitive.Binary, or a plain base64 string depending on the exporter
// version — that helper handles all three shapes.

// syncLegacyNotificationWebhooks copies the platform-owner notification
// webhooks (the admin/webhooks routes that pre-date the per-tenant
// webhook_endpoints surface). These are NOT tenant-keyed — a single owner
// rosters them for SMTP / on-call / Slack alerts. The HMAC secret is
// stored as plaintext, so it survives a transfer between two boxes with
// different APP_ENCRYPTION_KEY values without re-encryption.
//
// Dedup is by URL so a re-run doesn't double-insert. We don't translate
// any id refs — these rows have no foreign keys into the migrated tenant
// graph.
func (s *TransferService) syncLegacyNotificationWebhooks(ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB string) int {
	docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, database.ColWebhooks, "{}")
	if err != nil {
		s.addLog(ctx, jobID, "warn", fmt.Sprintf("Could not read source webhooks (legacy): %s", err), "panel-records")
		return 0
	}
	col := s.db.Collection(database.ColWebhooks)
	inserted := 0
	for _, raw := range docs {
		// idMap stays nil — these rows hold no tenant refs to translate.
		doc := s.normaliseDoc(raw, nil)
		url, _ := doc["url"].(string)
		if url == "" {
			continue
		}
		var existing bson.M
		if err := col.FindOne(ctx, bson.M{"url": url}).Decode(&existing); err == nil {
			continue
		} else if err != mongo.ErrNoDocuments {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("legacy webhook lookup url=%q: %s", url, err), "panel-records")
			continue
		}
		doc["_id"] = primitive.NewObjectID()
		if _, err := col.InsertOne(ctx, doc); err != nil {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("insert legacy webhook %q failed: %s", url, err), "panel-records")
			continue
		}
		inserted++
	}
	if inserted > 0 {
		s.addLog(ctx, jobID, "info",
			fmt.Sprintf("Migrated %d platform notification webhook(s) — secrets carried across, alerts keep firing.", inserted),
			"panel-records")
	}
	return inserted
}

// syncNotificationSettings copies the singleton notification_settings doc
// (email recipients + Slack webhook URL + per-channel event filters). One
// row per panel; dedup is "if a row exists on dest, leave it alone" so an
// operator who pre-configured Slack on the destination doesn't get
// overwritten by the source's stale config. Plaintext fields throughout —
// no encryption boundary to cross.
func (s *TransferService) syncNotificationSettings(ctx context.Context, jobID, host string, port int, sshUser, sshPass, srcDB string) int {
	docs, err := agent.RemoteMongoExport(ctx, host, port, sshUser, sshPass, srcDB, database.ColNotifications, "{}")
	if err != nil {
		// notifications collection may not exist yet on a fresh source
		// — silent ok.
		return 0
	}
	col := s.db.Collection(database.ColNotifications)
	inserted := 0
	for _, raw := range docs {
		doc := s.normaliseDoc(raw, nil)
		// Dest already has a settings row? Skip.
		if n, err := col.CountDocuments(ctx, bson.M{}); err == nil && n > 0 {
			break
		}
		doc["_id"] = primitive.NewObjectID()
		if _, err := col.InsertOne(ctx, doc); err != nil {
			s.addLog(ctx, jobID, "warn", fmt.Sprintf("insert notification_settings failed: %s", err), "panel-records")
			continue
		}
		inserted++
	}
	return inserted
}
