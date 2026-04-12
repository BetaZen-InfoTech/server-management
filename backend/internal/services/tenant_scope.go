package services

import (
	"context"
	"fmt"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// tenant_scope.go is the single source of truth for multi-tenant filtering.
// Every service.List/Get/Create/Update/Delete that touches user-owned data
// should route through one of the helpers in this file. Adding a new helper
// here is preferable to inlining tenant logic at the call site — that way
// changing the scoping rule (e.g. supporting nested tenants) is one edit.

// resolveTenantID returns the tenant_id for a user record, falling back to
// the user's own _id when the field is not yet populated. This handles
// records created before the multi-tenant migration ran.
func resolveTenantID(user *models.User) string {
	if !user.TenantID.IsZero() {
		return user.TenantID.Hex()
	}
	return user.ID.Hex()
}

// TenantUsernames returns the linux usernames of every user belonging to the
// given tenant. Used by resource services to scope queries by `user IN (...)`.
// Returns an empty slice (not nil) when the tenant has no users.
func TenantUsernames(ctx context.Context, db *mongo.Database, tenantHex string) ([]string, error) {
	tid, err := primitive.ObjectIDFromHex(tenantHex)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant id")
	}
	col := db.Collection(database.ColUsers)
	// Match users where tenant_id == tid OR (tenant_id missing AND _id == tid).
	// The OR handles the tenant root itself before its tenant_id has been backfilled.
	cursor, err := col.Find(ctx, bson.M{
		"$or": bson.A{
			bson.M{"tenant_id": tid},
			bson.M{"_id": tid},
		},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var users []models.User
	if err := cursor.All(ctx, &users); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(users))
	for _, u := range users {
		if u.Username != "" {
			out = append(out, u.Username)
		}
	}
	return out, nil
}

// ApplyResourceScope wraps a resource filter with a tenant restriction. For
// vendor_owner the filter is returned unchanged (sees everything). For any
// other role we add `user IN (tenantUsernames)`, scoped to whichever linux
// usernames belong to the requester's tenant.
//
// `userField` is the BSON field name on the resource that holds the linux
// username — most resources use "user" but a few use "username" or "owner".
func ApplyResourceScope(ctx context.Context, db *mongo.Database, role, tenantHex, userField string, filter bson.M) (bson.M, error) {
	if !constants.IsTenantScoped(role) {
		return filter, nil
	}
	if tenantHex == "" {
		// Defensive: a tenant-scoped role with no tenant_id should see nothing.
		filter[userField] = bson.M{"$in": []string{}}
		return filter, nil
	}
	usernames, err := TenantUsernames(ctx, db, tenantHex)
	if err != nil {
		return nil, err
	}
	if userField == "" {
		userField = "user"
	}
	// Merge with any pre-existing constraint on the same field.
	if existing, ok := filter[userField]; ok {
		filter["$and"] = bson.A{
			bson.M{userField: existing},
			bson.M{userField: bson.M{"$in": usernames}},
		}
		delete(filter, userField)
	} else {
		filter[userField] = bson.M{"$in": usernames}
	}
	return filter, nil
}

// AssertUsernameInTenant returns an error if username is not part of the
// caller's tenant. vendor_owner bypasses the check. Used by handlers that
// take a username as a path/query param (terminal, ssh keys, file manager).
func AssertUsernameInTenant(ctx context.Context, db *mongo.Database, role, tenantHex, username string) error {
	if !constants.IsTenantScoped(role) {
		return nil
	}
	if username == "" {
		return fmt.Errorf("username required")
	}
	usernames, err := TenantUsernames(ctx, db, tenantHex)
	if err != nil {
		return err
	}
	for _, u := range usernames {
		if u == username {
			return nil
		}
	}
	return fmt.Errorf("user %q is not in your tenant", username)
}

// CallerScope captures who is making a request for the purposes of multi-tenant
// filtering. It is attached to context.Context by middleware.InjectScope so that
// services can pull it out without changing every method signature. Resource
// services apply CallerScope.ApplyTo() to their filter to scope queries by the
// linux usernames belonging to the caller's tenant.
type CallerScope struct {
	Role      string
	TenantHex string
	UserHex   string

	// Cached after first lookup so a single request only hits MongoDB once.
	usernames     []string
	loaded        bool
	domains       []string
	domainsLoaded bool
}

type ctxKey int

const scopeKey ctxKey = 1

// WithCallerScope returns a child context that carries the scope. Used by the
// auth middleware after token validation.
func WithCallerScope(ctx context.Context, scope *CallerScope) context.Context {
	return context.WithValue(ctx, scopeKey, scope)
}

// GetCallerScope pulls the scope off a context. Returns nil if no scope was
// attached — services treat that as "no filtering" (e.g. internal calls).
func GetCallerScope(ctx context.Context) *CallerScope {
	v, _ := ctx.Value(scopeKey).(*CallerScope)
	return v
}

// ApplyTo merges a tenant restriction into the given filter. vendor_owner is
// unrestricted; everyone else is limited to resources whose `userField` value
// is in the caller's tenant usernames. Pass "user" for the common case.
func (cs *CallerScope) ApplyTo(ctx context.Context, db *mongo.Database, userField string, filter bson.M) bson.M {
	if cs == nil || !constants.IsTenantScoped(cs.Role) {
		return filter
	}
	if userField == "" {
		userField = "user"
	}
	if !cs.loaded {
		cs.usernames, _ = TenantUsernames(ctx, db, cs.TenantHex)
		cs.loaded = true
	}
	// Defensive: a tenant-scoped caller with no resolved usernames sees nothing.
	if existing, ok := filter[userField]; ok {
		filter["$and"] = bson.A{
			bson.M{userField: existing},
			bson.M{userField: bson.M{"$in": cs.usernames}},
		}
		delete(filter, userField)
	} else {
		filter[userField] = bson.M{"$in": cs.usernames}
	}
	return filter
}

// TenantDomains returns the domain strings owned by users in the caller's
// tenant. Used to scope resources keyed on `domain` rather than `user`
// (databases, SSL certs, DNS zones, email mailboxes, ...). Cached on the
// scope so a request only hits MongoDB once.
func (cs *CallerScope) TenantDomains(ctx context.Context, db *mongo.Database) ([]string, error) {
	if cs == nil {
		return nil, nil
	}
	if !cs.loaded {
		cs.usernames, _ = TenantUsernames(ctx, db, cs.TenantHex)
		cs.loaded = true
	}
	if !cs.domainsLoaded {
		cursor, err := db.Collection(database.ColDomains).Find(ctx, bson.M{"user": bson.M{"$in": cs.usernames}})
		if err != nil {
			return nil, err
		}
		defer cursor.Close(ctx)
		var docs []struct {
			Domain string `bson:"domain"`
		}
		if err := cursor.All(ctx, &docs); err != nil {
			return nil, err
		}
		cs.domains = make([]string, 0, len(docs))
		for _, d := range docs {
			if d.Domain != "" {
				cs.domains = append(cs.domains, d.Domain)
			}
		}
		cs.domainsLoaded = true
	}
	return cs.domains, nil
}

// ApplyDomainScope is the domain-keyed sibling of ApplyTo. Use it on filters
// for collections whose owning user is identified by `domainField` rather than
// `userField`.
func (cs *CallerScope) ApplyDomainScope(ctx context.Context, db *mongo.Database, domainField string, filter bson.M) bson.M {
	if cs == nil || !constants.IsTenantScoped(cs.Role) {
		return filter
	}
	if domainField == "" {
		domainField = "domain"
	}
	domains, _ := cs.TenantDomains(ctx, db)
	if existing, ok := filter[domainField]; ok {
		filter["$and"] = bson.A{
			bson.M{domainField: existing},
			bson.M{domainField: bson.M{"$in": domains}},
		}
		delete(filter, domainField)
	} else {
		filter[domainField] = bson.M{"$in": domains}
	}
	return filter
}

// AssertOwnsDomain returns an error if the given domain string is not owned
// by anyone in the caller's tenant.
func (cs *CallerScope) AssertOwnsDomain(ctx context.Context, db *mongo.Database, domain string) error {
	if cs == nil || !constants.IsTenantScoped(cs.Role) {
		return nil
	}
	if domain == "" {
		return fmt.Errorf("domain required")
	}
	domains, err := cs.TenantDomains(ctx, db)
	if err != nil {
		return err
	}
	for _, d := range domains {
		if d == domain {
			return nil
		}
	}
	return fmt.Errorf("domain %q is not in your tenant", domain)
}

// AssertOwns checks that the given linux username is part of the caller's
// tenant. Used by handlers/services that take a username as input (terminal,
// ssh keys, file manager) before mutating anything.
func (cs *CallerScope) AssertOwns(ctx context.Context, db *mongo.Database, username string) error {
	if cs == nil || !constants.IsTenantScoped(cs.Role) {
		return nil
	}
	if username == "" {
		return fmt.Errorf("username required")
	}
	if !cs.loaded {
		cs.usernames, _ = TenantUsernames(ctx, db, cs.TenantHex)
		cs.loaded = true
	}
	for _, u := range cs.usernames {
		if u == username {
			return nil
		}
	}
	return fmt.Errorf("user %q is not in your tenant", username)
}

// LookupOwnUsername resolves the linux username for the user_id JWT claim.
// Used as the default `target user` when a vendor opens a panel terminal
// without specifying ?user=.
func LookupOwnUsername(ctx context.Context, db *mongo.Database, userIDHex string) (string, error) {
	oid, err := primitive.ObjectIDFromHex(userIDHex)
	if err != nil {
		return "", err
	}
	var user models.User
	if err := db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": oid}).Decode(&user); err != nil {
		return "", err
	}
	return user.Username, nil
}
