// Package services — domain_proxy.go is the DURABLE replacement for the
// fragile project-service `alias_domains` mechanism.
//
// Background: a domain attached to a Deploy-Software service used to be
// stored only inside the service row's `alias_domains` array, with the
// upstream port re-derived on the fly via lookupProjectServiceByDomain
// (which matched ONLY while the domain was still in that array). Three
// failures fell out of that design:
//
//   1. "added via API, works, doesn't show" — any UpdateService that
//      re-sent a stale alias list silently dropped the domain from
//      alias_domains. The orphaned SSL vhost kept serving it, so the
//      domain worked but vanished from the panel.
//   2. "conflicting server name … ignored" — the SSL-issue flow wrote a
//      SEPARATE per-domain vhost while the domain was ALSO merged into
//      the service primary's server_name → two server blocks claimed the
//      same name and nginx ignored one.
//   3. After migration / restore the port was gone, so the wrong vhost
//      (PHP-FPM placeholder) got rebuilt and the domain 404'd.
//
// The fix: persist the binding on the DOMAIN row (Domain.ProxyServiceID
// + Domain.ProxyPort). An attached domain now gets its OWN reverse-proxy
// vhost (server_name <d> www.<d> cname.<d> → 127.0.0.1:<port>) and its
// OWN cert — no merge, no conflict — and every vhost-building flow
// (issue/revoke SSL, restore, recheck, migration, backup-restore)
// rebuilds it durably from the stored port with zero service lookup.
//
// The helpers here are deliberately package-private free functions (not
// methods) so SSLService, the migration rehydrate path, and DomainService
// can all reuse the exact same rendering rule as ProjectService.AttachDomain.

package services

import (
	"context"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// applyAttachedProxyVhost (re)writes domainName's nginx vhost as a durable
// reverse-proxy to its attached project service, driven entirely by the
// persisted Domain.ProxyServiceID / Domain.ProxyPort. Returns true when it
// handled the domain — callers use that to SKIP their default PHP-FPM /
// placeholder / static template (which would otherwise clobber a live
// attached domain back to a "Welcome" page).
//
//   - ssl=true forces the 80→443 + 443 template using certPath/keyPath
//     (empty → canonical Let's Encrypt paths for this domain). When
//     ssl=false the vhost is auto-upgraded to SSL anyway IF a cert
//     already exists on disk, so a plain restore never downgrades HTTPS.
//   - A port-less frontend/static service falls back to serving the
//     service's BuildDir as a static site.
//
// Best-effort: a failed SSL render falls back to the HTTP reverse-proxy so
// the domain keeps routing rather than going dark.
func applyAttachedProxyVhost(ctx context.Context, db *mongo.Database, domainName string, ssl bool, certPath, keyPath string) bool {
	domainName = strings.TrimSpace(strings.ToLower(domainName))
	if domainName == "" {
		return false
	}
	var dom models.Domain
	if err := db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": domainName}).Decode(&dom); err != nil {
		return false
	}
	if dom.ProxyServiceID == nil || dom.ProxyServiceID.IsZero() {
		// Not an attached domain (proxy_port alone, without a service link,
		// is treated as advisory only — the link is the source of truth).
		if dom.ProxyPort <= 0 {
			return false
		}
	}

	port := dom.ProxyPort
	staticRoot := ""
	// Refresh the port from the live service row (the link is the source of
	// truth; the cached port can go stale if the service's port changed
	// before this domain's row was updated). Also resolves the static case.
	if dom.ProxyServiceID != nil && !dom.ProxyServiceID.IsZero() {
		var svc models.ProjectService
		if err := db.Collection(database.ColProjectServices).FindOne(ctx, bson.M{"_id": *dom.ProxyServiceID}).Decode(&svc); err == nil {
			if svc.Port > 0 {
				port = svc.Port
			} else if svc.Role == "frontend" || svc.Role == "static" {
				if svc.BuildDir != "" {
					staticRoot = svc.BuildDir
				} else if svc.InstallDir != "" {
					staticRoot = svc.InstallDir
				}
			}
		}
	}

	useSSL := ssl || agent.LetsEncryptCertExists(domainName)

	// Static (port-less) service → serve its build dir.
	if port <= 0 {
		if staticRoot == "" {
			return false
		}
		if useSSL {
			if err := agent.CreateStaticVhostWithSSL(ctx, domainName, staticRoot, certPath, keyPath); err != nil {
				_ = agent.CreateStaticVhost(ctx, domainName, staticRoot)
			}
		} else {
			_ = agent.CreateStaticVhost(ctx, domainName, staticRoot)
		}
		return true
	}

	// Backend / port-bound service → reverse proxy.
	if useSSL {
		if err := agent.CreateReverseProxyWithSSL(ctx, &agent.VhostConfig{Domain: domainName, Port: port, CertPath: certPath, KeyPath: keyPath}); err != nil {
			_ = agent.CreateReverseProxy(ctx, &agent.VhostConfig{Domain: domainName, Port: port})
		}
	} else {
		_ = agent.CreateReverseProxy(ctx, &agent.VhostConfig{Domain: domainName, Port: port})
	}
	return true
}

// stampDomainProxy persists the durable service link + cached port on a
// Domain row. Idempotent. The link makes the domain survive service edits
// (the binding no longer lives in the editable alias_domains array) and
// every vhost-rebuild flow reproduce the correct reverse proxy.
func stampDomainProxy(ctx context.Context, db *mongo.Database, domainName string, svcID primitive.ObjectID, port int) {
	db.Collection(database.ColDomains).UpdateOne(ctx,
		bson.M{"domain": strings.ToLower(strings.TrimSpace(domainName))},
		bson.M{"$set": bson.M{
			"proxy_service_id": svcID,
			"proxy_port":       port,
			"updated_at":       time.Now(),
		}})
}

// clearDomainProxy removes the service link from a Domain row (detach).
// Callers should rebuild the domain's base vhost afterwards
// (restoreDomainBaseVhost) so it stops proxying.
func clearDomainProxy(ctx context.Context, db *mongo.Database, domainName string) {
	db.Collection(database.ColDomains).UpdateOne(ctx,
		bson.M{"domain": strings.ToLower(strings.TrimSpace(domainName))},
		bson.M{
			"$unset": bson.M{"proxy_service_id": "", "proxy_port": ""},
			"$set":   bson.M{"updated_at": time.Now()},
		})
}

// attachedDomainNames returns every registered domain whose durable link
// points at svcID, sorted. Used to enrich the project-service API response
// so the panel can render the service's domain list from the source of
// truth (the Domain rows) rather than the legacy alias_domains array.
func attachedDomainNames(ctx context.Context, db *mongo.Database, svcID primitive.ObjectID) []string {
	cur, err := db.Collection(database.ColDomains).Find(ctx,
		bson.M{"proxy_service_id": svcID},
		options.Find().SetProjection(bson.M{"domain": 1}).SetSort(bson.M{"domain": 1}))
	if err != nil {
		return nil
	}
	defer cur.Close(ctx)
	var rows []struct {
		Domain string `bson:"domain"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if d := strings.TrimSpace(r.Domain); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// reapplyAttachedDomainsForService rebuilds the per-domain reverse-proxy
// vhost for every domain attached to svc, refreshing each row's cached
// proxy_port to the service's current port. Called when a service's port
// changes (UpdateService) or after a redeploy so the domains keep pointing
// at the live upstream. Best-effort per domain.
func reapplyAttachedDomainsForService(ctx context.Context, db *mongo.Database, svc *models.ProjectService) {
	if svc == nil {
		return
	}
	for _, d := range attachedDomainNames(ctx, db, svc.ID) {
		if svc.Port > 0 {
			db.Collection(database.ColDomains).UpdateOne(ctx,
				bson.M{"domain": d},
				bson.M{"$set": bson.M{"proxy_port": svc.Port, "updated_at": time.Now()}})
		}
		applyAttachedProxyVhost(ctx, db, d, false, "", "")
	}
}
