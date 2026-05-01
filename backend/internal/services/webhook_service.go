// Package services — WebhookService manages caller-owned outbound webhooks.
//
// Each WebhookEndpoint has its own HMAC-SHA256 signing secret stored AES-GCM
// encrypted at rest under APP_ENCRYPTION_KEY. When the panel emits an event
// the dispatcher serialises the payload, signs `<timestamp>.<body>` with the
// endpoint's secret, and POSTs to the URL with these headers:
//
//	X-Betazen-Event:     <event key>
//	X-Betazen-Delivery:  <uuid>
//	X-Betazen-Timestamp: <unix-seconds>
//	X-Betazen-Signature: sha256=<hex hmac>
//
// Receivers verify by recomputing the HMAC and rejecting timestamps older
// than 5 minutes. Failed deliveries get up to three retries with exponential
// backoff (1m / 5m / 30m); each attempt updates the WebhookDelivery row.
package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/crypto"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// WebhookService is the persistence + dispatch layer for outbound webhooks.
type WebhookService struct {
	db     *mongo.Database
	encKey []byte
	client *http.Client

	mu      sync.Mutex
	stopCh  chan struct{}
	started bool
}

// NewWebhookService wires the persistence layer to the encryption key. The HTTP
// client is given a 10-second timeout so a slow webhook receiver can't stall
// the dispatcher.
func NewWebhookService(db *mongo.Database, encKey []byte) *WebhookService {
	s := &WebhookService{
		db:     db,
		encKey: encKey,
		client: &http.Client{Timeout: 10 * time.Second},
	}
	s.ensureIndexes(context.Background())
	return s
}

func (s *WebhookService) ensureIndexes(ctx context.Context) {
	_, _ = s.db.Collection(database.ColWebhookEndpoints).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "tenant_id", Value: 1}}},
		{Keys: bson.D{{Key: "owner_user_id", Value: 1}}},
		{Keys: bson.D{{Key: "events", Value: 1}}},
		{Keys: bson.D{{Key: "active", Value: 1}}},
	})
	_, _ = s.db.Collection(database.ColWebhookDeliveries).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "endpoint_id", Value: 1}, {Key: "created_at", Value: -1}}},
		{Keys: bson.D{{Key: "next_retry_at", Value: 1}}},
		// 30-day TTL on the attempt log — keeps the collection bounded
		// even on busy panels without operator intervention.
		{
			Keys:    bson.D{{Key: "created_at", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(60 * 60 * 24 * 30),
		},
	})
}

// Subscribe creates a new endpoint owned by the caller and returns the
// plaintext signing secret exactly once. Mirrors the API-token "shown once"
// model — the operator must copy the secret before dismissing the dialog.
func (s *WebhookService) Subscribe(ctx context.Context, scope *CallerScope, req models.CreateWebhookEndpointRequest) (*models.IssuedWebhook, error) {
	if scope == nil {
		return nil, errors.New("webhook: caller scope required")
	}
	url := strings.TrimSpace(req.URL)
	if url == "" {
		return nil, errors.New("webhook: url is required")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, errors.New("webhook: url must start with http:// or https://")
	}
	events := []string{}
	seen := map[string]bool{}
	for _, e := range req.Events {
		key := strings.TrimSpace(e)
		if key == "" || seen[key] {
			continue
		}
		if !models.IsKnownWebhookEvent(key) {
			return nil, fmt.Errorf("webhook: unknown event %q", key)
		}
		events = append(events, key)
		seen[key] = true
	}
	if len(events) == 0 {
		return nil, errors.New("webhook: at least one event is required")
	}

	secret, err := apiTokenRandomHex(32)
	if err != nil {
		return nil, fmt.Errorf("webhook: secret gen: %w", err)
	}
	enc, err := crypto.EncryptGCM([]byte(secret), s.encKey)
	if err != nil {
		return nil, fmt.Errorf("webhook: encrypt secret: %w", err)
	}

	now := time.Now()
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	wh := &models.WebhookEndpoint{
		URL:         url,
		Description: strings.TrimSpace(req.Description),
		Events:      events,
		SecretEnc:   enc,
		Prefix:      "whsec_" + secret[:6],
		OwnerKind:   ownerKindFromScope(scope),
		Active:      active,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if oid, err := primitive.ObjectIDFromHex(scope.UserHex); err == nil {
		wh.OwnerUserID = oid
	}
	if oid, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
		wh.TenantID = oid
	}
	res, err := s.db.Collection(database.ColWebhookEndpoints).InsertOne(ctx, wh)
	if err != nil {
		return nil, err
	}
	wh.ID = res.InsertedID.(primitive.ObjectID)
	return &models.IssuedWebhook{Webhook: wh, PlaintextSecret: secret}, nil
}

// List returns the caller's endpoints, newest first.
func (s *WebhookService) List(ctx context.Context, scope *CallerScope) ([]*models.WebhookEndpoint, error) {
	filter := bson.M{}
	if scope != nil && scope.Role != "vendor_owner" {
		if oid, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
			filter["tenant_id"] = oid
		}
	}
	cur, err := s.db.Collection(database.ColWebhookEndpoints).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []*models.WebhookEndpoint{}
	for cur.Next(ctx) {
		var wh models.WebhookEndpoint
		if err := cur.Decode(&wh); err == nil {
			out = append(out, &wh)
		}
	}
	return out, nil
}

// Update flips active or rewrites events on an existing endpoint. URL is
// immutable to prevent a hijacked vendor account from quietly redirecting
// every webhook to an attacker-controlled URL.
func (s *WebhookService) Update(ctx context.Context, scope *CallerScope, idHex string, active *bool, events []string, description *string) error {
	oid, err := primitive.ObjectIDFromHex(idHex)
	if err != nil {
		return errors.New("invalid webhook id")
	}
	filter := bson.M{"_id": oid}
	if scope != nil && scope.Role != "vendor_owner" {
		if t, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
			filter["tenant_id"] = t
		}
	}
	set := bson.M{"updated_at": time.Now()}
	if active != nil {
		set["active"] = *active
	}
	if description != nil {
		set["description"] = strings.TrimSpace(*description)
	}
	if len(events) > 0 {
		clean := []string{}
		seen := map[string]bool{}
		for _, e := range events {
			k := strings.TrimSpace(e)
			if k == "" || seen[k] {
				continue
			}
			if !models.IsKnownWebhookEvent(k) {
				return fmt.Errorf("unknown event %q", k)
			}
			clean = append(clean, k)
			seen[k] = true
		}
		set["events"] = clean
	}
	res, err := s.db.Collection(database.ColWebhookEndpoints).UpdateOne(ctx, filter, bson.M{"$set": set})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return errors.New("webhook not found")
	}
	return nil
}

// Delete permanently removes the endpoint. Existing delivery rows survive
// (kept for the ~30-day TTL window) so the operator can still inspect
// history; new dispatches stop immediately.
func (s *WebhookService) Delete(ctx context.Context, scope *CallerScope, idHex string) error {
	oid, err := primitive.ObjectIDFromHex(idHex)
	if err != nil {
		return errors.New("invalid webhook id")
	}
	filter := bson.M{"_id": oid}
	if scope != nil && scope.Role != "vendor_owner" {
		if t, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
			filter["tenant_id"] = t
		}
	}
	res, err := s.db.Collection(database.ColWebhookEndpoints).DeleteOne(ctx, filter)
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return errors.New("webhook not found")
	}
	return nil
}

// Rotate mints a fresh signing secret. The old secret stops working
// immediately; the operator must copy the new plaintext from the response.
func (s *WebhookService) Rotate(ctx context.Context, scope *CallerScope, idHex string) (*models.IssuedWebhook, error) {
	oid, err := primitive.ObjectIDFromHex(idHex)
	if err != nil {
		return nil, errors.New("invalid webhook id")
	}
	filter := bson.M{"_id": oid}
	if scope != nil && scope.Role != "vendor_owner" {
		if t, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
			filter["tenant_id"] = t
		}
	}
	var wh models.WebhookEndpoint
	if err := s.db.Collection(database.ColWebhookEndpoints).FindOne(ctx, filter).Decode(&wh); err != nil {
		return nil, errors.New("webhook not found")
	}
	secret, err := apiTokenRandomHex(32)
	if err != nil {
		return nil, err
	}
	enc, err := crypto.EncryptGCM([]byte(secret), s.encKey)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if _, err := s.db.Collection(database.ColWebhookEndpoints).UpdateOne(ctx, bson.M{"_id": oid}, bson.M{
		"$set": bson.M{"secret_enc": enc, "prefix": "whsec_" + secret[:6], "updated_at": now},
	}); err != nil {
		return nil, err
	}
	wh.SecretEnc = enc
	wh.Prefix = "whsec_" + secret[:6]
	wh.UpdatedAt = now
	return &models.IssuedWebhook{Webhook: &wh, PlaintextSecret: secret}, nil
}

// TestFire sends a synthetic event so the operator can verify their receiver
// before depending on real events. Body is a minimal {ping: true, ts: ...}.
func (s *WebhookService) TestFire(ctx context.Context, scope *CallerScope, idHex string) error {
	oid, err := primitive.ObjectIDFromHex(idHex)
	if err != nil {
		return errors.New("invalid webhook id")
	}
	filter := bson.M{"_id": oid}
	if scope != nil && scope.Role != "vendor_owner" {
		if t, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
			filter["tenant_id"] = t
		}
	}
	var wh models.WebhookEndpoint
	if err := s.db.Collection(database.ColWebhookEndpoints).FindOne(ctx, filter).Decode(&wh); err != nil {
		return errors.New("webhook not found")
	}
	payload := map[string]any{"ping": true, "fired_at": time.Now().UTC().Format(time.RFC3339)}
	go s.dispatchOne(context.Background(), &wh, "webhook.test", payload)
	return nil
}

// ListDeliveries returns the most recent attempts for endpoints visible to
// the caller. Useful for the Delivery Log page.
func (s *WebhookService) ListDeliveries(ctx context.Context, scope *CallerScope, limit int) ([]*models.WebhookDelivery, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	endpointFilter := bson.M{}
	if scope != nil && scope.Role != "vendor_owner" {
		if t, err := primitive.ObjectIDFromHex(scope.TenantHex); err == nil {
			endpointFilter["tenant_id"] = t
		}
	}
	cur, err := s.db.Collection(database.ColWebhookEndpoints).Find(ctx, endpointFilter, options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	ids := []primitive.ObjectID{}
	for cur.Next(ctx) {
		var row struct {
			ID primitive.ObjectID `bson:"_id"`
		}
		if err := cur.Decode(&row); err == nil {
			ids = append(ids, row.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	dCur, err := s.db.Collection(database.ColWebhookDeliveries).Find(ctx,
		bson.M{"endpoint_id": bson.M{"$in": ids}},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, err
	}
	defer dCur.Close(ctx)
	out := []*models.WebhookDelivery{}
	for dCur.Next(ctx) {
		var d models.WebhookDelivery
		if err := dCur.Decode(&d); err == nil {
			out = append(out, &d)
		}
	}
	return out, nil
}

// Emit is the public entry point for event-emitting code paths. Looks up
// every endpoint subscribed to `event` and dispatches in the background so
// the caller's request returns immediately. tenantUserID lets us scope
// emissions to the resource's owning tenant — endpoints belonging to other
// vendors never see the event.
func (s *WebhookService) Emit(ctx context.Context, event string, tenantUserID primitive.ObjectID, payload map[string]any) {
	if event == "" {
		return
	}
	if !models.IsKnownWebhookEvent(event) && event != "webhook.test" {
		return
	}
	filter := bson.M{
		"events": event,
		"active": true,
	}
	// Owner-issued endpoints (tenant_id = owner's user_id) and the
	// resource-owner's own endpoints both see the event. Other vendors
	// are filtered out. tenantUserID can be zero for global events
	// (e.g. server-level), in which case only owner-issued endpoints
	// receive them.
	or := []bson.M{{"owner_kind": models.APITokenOwnerKindOwner}}
	if !tenantUserID.IsZero() {
		or = append(or, bson.M{"tenant_id": tenantUserID})
	}
	filter["$or"] = or

	cur, err := s.db.Collection(database.ColWebhookEndpoints).Find(ctx, filter)
	if err != nil {
		return
	}
	defer cur.Close(ctx)
	endpoints := []*models.WebhookEndpoint{}
	for cur.Next(ctx) {
		var wh models.WebhookEndpoint
		if err := cur.Decode(&wh); err == nil {
			cp := wh
			endpoints = append(endpoints, &cp)
		}
	}
	for _, wh := range endpoints {
		go s.dispatchOne(context.Background(), wh, event, payload)
	}
}

// dispatchOne is the inner POST + retry loop. Each attempt persists a
// WebhookDelivery row; success ends the loop, failure schedules the next
// retry until we hit the max attempt count.
func (s *WebhookService) dispatchOne(ctx context.Context, wh *models.WebhookEndpoint, event string, payload map[string]any) {
	secret, err := crypto.DecryptGCM(wh.SecretEnc, s.encKey)
	if err != nil {
		return
	}
	body, err := json.Marshal(map[string]any{
		"event":     event,
		"fired_at":  time.Now().UTC().Format(time.RFC3339),
		"data":      payload,
	})
	if err != nil {
		return
	}
	delivery := &models.WebhookDelivery{
		EndpointID: wh.ID,
		Event:      event,
		Payload:    string(body),
		Timestamp:  time.Now().Unix(),
		CreatedAt:  time.Now(),
	}
	signed := fmt.Sprintf("%d.%s", delivery.Timestamp, body)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signed))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	delivery.Signature = sig

	res, err := s.db.Collection(database.ColWebhookDeliveries).InsertOne(ctx, delivery)
	if err != nil {
		return
	}
	delivery.ID = res.InsertedID.(primitive.ObjectID)

	maxAttempts := 3
	backoff := []time.Duration{0, 1 * time.Minute, 5 * time.Minute, 30 * time.Minute}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 && attempt < len(backoff) {
			time.Sleep(backoff[attempt-1])
		}
		statusCode, err := s.send(ctx, wh.URL, body, sig, event, delivery.ID.Hex(), delivery.Timestamp)
		now := time.Now()
		update := bson.M{
			"$set": bson.M{
				"status_code": statusCode,
				"attempts":    attempt,
			},
		}
		success := err == nil && statusCode >= 200 && statusCode < 300
		if success {
			update["$set"].(bson.M)["success"] = true
			update["$set"].(bson.M)["delivered_at"] = now
			update["$set"].(bson.M)["error"] = ""
			_, _ = s.db.Collection(database.ColWebhookDeliveries).UpdateOne(ctx, bson.M{"_id": delivery.ID}, update)
			_, _ = s.db.Collection(database.ColWebhookEndpoints).UpdateOne(ctx, bson.M{"_id": wh.ID}, bson.M{
				"$set": bson.M{"last_success_at": now, "last_error": "", "updated_at": now},
			})
			return
		}
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		} else {
			errMsg = fmt.Sprintf("HTTP %d", statusCode)
		}
		update["$set"].(bson.M)["error"] = errMsg
		if attempt == maxAttempts {
			update["$set"].(bson.M)["success"] = false
		} else if attempt < len(backoff) {
			next := now.Add(backoff[attempt])
			update["$set"].(bson.M)["next_retry_at"] = next
		}
		_, _ = s.db.Collection(database.ColWebhookDeliveries).UpdateOne(ctx, bson.M{"_id": delivery.ID}, update)
		_, _ = s.db.Collection(database.ColWebhookEndpoints).UpdateOne(ctx, bson.M{"_id": wh.ID}, bson.M{
			"$set": bson.M{"last_failure_at": now, "last_error": errMsg, "updated_at": now},
		})
	}
}

// ReencryptForTransfer takes a webhook signing-secret cipher that was sealed
// under the SOURCE's APP_ENCRYPTION_KEY and returns a fresh cipher sealed
// under THIS panel's key. Used by the server-transfer pipeline so migrated
// webhook endpoints come up active on the destination instead of silently
// dropping every event because the local key can't decrypt the migrated
// blob. Mirrors PanelMailService.ReencryptForTransfer's contract:
//
//   - (nil, nil)            on empty input — caller treats as "no cipher"
//   - (nil, error)          when source key is malformed or doesn't match
//                           the cipher — caller drops the secret and
//                           lands the row inactive so the operator
//                           rotates manually rather than running with a
//                           silently-broken HMAC seed
//   - (newCipher, nil)      on a successful decrypt+re-encrypt round-trip
func (s *WebhookService) ReencryptForTransfer(srcCipher []byte, srcEncKeyRaw string) ([]byte, error) {
	if len(srcCipher) == 0 || strings.TrimSpace(srcEncKeyRaw) == "" {
		return nil, nil
	}
	if len(s.encKey) != 32 {
		return nil, fmt.Errorf("destination encryption key unavailable")
	}
	srcKey, err := crypto.LoadKey(srcEncKeyRaw)
	if err != nil {
		return nil, fmt.Errorf("load source key: %w", err)
	}
	plain, err := crypto.DecryptGCM(srcCipher, srcKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt with source key: %w", err)
	}
	return crypto.EncryptGCM(plain, s.encKey)
}

func (s *WebhookService) send(ctx context.Context, url string, body []byte, sig, event, deliveryID string, ts int64) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "BetazenWebhook/1.0")
	req.Header.Set("X-Betazen-Event", event)
	req.Header.Set("X-Betazen-Delivery", deliveryID)
	req.Header.Set("X-Betazen-Timestamp", fmt.Sprintf("%d", ts))
	req.Header.Set("X-Betazen-Signature", sig)
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}
