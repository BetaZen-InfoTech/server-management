// Package services — events.go is the shared fire-and-forget bus for outbound
// webhook emissions. Resource services call EmitEvent(...) at write
// boundaries; the dispatcher (WebhookService) picks the call up if it has
// been wired via SetWebhookEmitter at boot. Keeping this as a package-level
// hook avoids threading the webhook service through every constructor and
// avoids an import cycle if a service ever needs to import another service.
package services

import (
	"context"
	"sync"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// WebhookEmitter is the minimal contract the bus needs from a webhook
// dispatcher. WebhookService satisfies it.
type WebhookEmitter interface {
	Emit(ctx context.Context, event string, tenantUserID primitive.ObjectID, payload map[string]any)
}

var (
	emitterMu sync.RWMutex
	emitter   WebhookEmitter
)

// SetWebhookEmitter wires the dispatcher. Called once at boot in main.go
// after the webhook service is constructed; subsequent calls overwrite the
// hook (useful in tests).
func SetWebhookEmitter(e WebhookEmitter) {
	emitterMu.Lock()
	emitter = e
	emitterMu.Unlock()
}

// EmitEvent is the convenience wrapper used by resource services. It is a
// no-op when no dispatcher is wired (e.g. unit tests, CLI tools), so the
// call site never has to nil-check. tenantUserID may be zero for
// platform-level events; only owner-issued endpoints will receive those.
func EmitEvent(ctx context.Context, event string, tenantUserID primitive.ObjectID, payload map[string]any) {
	emitterMu.RLock()
	e := emitter
	emitterMu.RUnlock()
	if e == nil {
		return
	}
	e.Emit(ctx, event, tenantUserID, payload)
}

// LookupTenantIDForUsername resolves the tenant root's user_id for a given
// linux username. Used by services that key resources on `user` (domains,
// email, ssl) so webhook emissions land on the right tenant. Returns zero
// ObjectID if the user is unknown — emit calls treat that as a global event
// and only owner-issued endpoints will see it.
func LookupTenantIDForUsername(ctx context.Context, db *mongo.Database, username string) primitive.ObjectID {
	if username == "" || db == nil {
		return primitive.NilObjectID
	}
	var u struct {
		ID       primitive.ObjectID `bson:"_id"`
		TenantID primitive.ObjectID `bson:"tenant_id"`
	}
	if err := db.Collection(database.ColUsers).FindOne(ctx, bson.M{"username": username}).Decode(&u); err != nil {
		return primitive.NilObjectID
	}
	if !u.TenantID.IsZero() {
		return u.TenantID
	}
	return u.ID
}

// LookupTenantIDForDomain resolves the tenant id for a domain by its name.
// Convenience wrapper for services that have only a domain string in scope.
func LookupTenantIDForDomain(ctx context.Context, db *mongo.Database, domain string) primitive.ObjectID {
	if domain == "" || db == nil {
		return primitive.NilObjectID
	}
	var d struct {
		User string `bson:"user"`
	}
	if err := db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": domain}).Decode(&d); err != nil {
		return primitive.NilObjectID
	}
	return LookupTenantIDForUsername(ctx, db, d.User)
}
