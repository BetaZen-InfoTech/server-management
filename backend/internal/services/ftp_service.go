package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/agent"
	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"github.com/betazeninfotech/whm-cpanel-management/pkg/constants"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// FTPService manages Pure-FTPd virtual users (one root account per domain is
// auto-created at Add Domain; this surface lets vendors/owners list them, add
// extra accounts, reset passwords, and delete non-root ones). It mirrors
// DatabaseService's tenant-scoping model: a resource is owned either via its
// Domain or via the stamped owner username ("user").
type FTPService struct {
	db       *mongo.Database
	serverIP string
}

func NewFTPService(db *mongo.Database) *FTPService {
	return &FTPService{db: db}
}

// SetServerIP wires the public IP advertised as the FTP host in the UI.
func (s *FTPService) SetServerIP(ip string) {
	s.serverIP = strings.TrimSpace(ip)
}

const ftpPort = 21

func (s *FTPService) host() string {
	if s.serverIP != "" {
		return s.serverIP
	}
	return "localhost"
}

func (s *FTPService) view(a models.FTPAccount, password string) models.FTPAccountView {
	return models.FTPAccountView{FTPAccount: a, Host: s.host(), Port: ftpPort, Password: password}
}

// sanitiseFTPSuffix keeps only [a-z0-9_] from a user-supplied username suffix
// (lower-cased), so the final pure-pw username is always a safe virtual-user
// identifier. Returns "" when nothing usable remains.
func sanitiseFTPSuffix(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "_")
}

// List returns FTP accounts visible to the caller. Tenant-scoped callers see
// accounts they own via EITHER the domain (legacy rows) OR the stamped owner
// username; non-tenant-scoped callers (owner/staff with perms) see all.
func (s *FTPService) List(ctx context.Context) ([]models.FTPAccountView, error) {
	col := s.db.Collection(database.ColFTPAccounts)
	filter := bson.M{}
	if scope := GetCallerScope(ctx); scope != nil && constants.IsTenantScoped(scope.Role) {
		domains, _ := scope.TenantDomains(ctx, s.db)
		owners, _ := TenantUsernames(ctx, s.db, scope.TenantHex)
		filter = bson.M{"$or": bson.A{
			bson.M{"domain": bson.M{"$in": domains}},
			bson.M{"user": bson.M{"$in": owners}},
		}}
	}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	cur, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var accounts []models.FTPAccount
	if err := cur.All(ctx, &accounts); err != nil {
		return nil, err
	}
	out := make([]models.FTPAccountView, 0, len(accounts))
	for _, a := range accounts {
		// Heal a missing owner username from the domain so later tenant
		// queries (and user-delete cleanup) match by "user" too.
		if a.User == "" && a.Domain != "" {
			if owner := s.domainOwner(ctx, a.Domain); owner != "" {
				a.User = owner
				s.db.Collection(database.ColFTPAccounts).UpdateByID(ctx, a.ID, bson.M{"$set": bson.M{"user": owner}})
			}
		}
		out = append(out, s.view(a, ""))
	}
	return out, nil
}

// getOwned loads an FTP account by id and enforces caller ownership.
func (s *FTPService) getOwned(ctx context.Context, id string) (*models.FTPAccount, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid FTP account id")
	}
	var a models.FTPAccount
	if err := s.db.Collection(database.ColFTPAccounts).FindOne(ctx, bson.M{"_id": oid}).Decode(&a); err != nil {
		return nil, fmt.Errorf("FTP account not found")
	}
	if scope := GetCallerScope(ctx); scope != nil && constants.IsTenantScoped(scope.Role) {
		okDomain := a.Domain != "" && scope.AssertOwnsDomain(ctx, s.db, a.Domain) == nil
		okOwner := a.User != "" && scope.AssertOwns(ctx, s.db, a.User) == nil
		if !okDomain && !okOwner {
			return nil, fmt.Errorf("FTP account not found")
		}
	}
	return &a, nil
}

func (s *FTPService) domainOwner(ctx context.Context, domain string) string {
	var dom models.Domain
	if err := s.db.Collection(database.ColDomains).FindOne(ctx, bson.M{"domain": domain}).Decode(&dom); err == nil {
		return dom.User
	}
	return ""
}

// ftpLimitForTenant returns the MaxFTPAccounts from the tenant root's package,
// or 0 meaning "unlimited / not enforced" (package unresolved, or max <= 0).
func (s *FTPService) ftpLimitForTenant(ctx context.Context, tenantHex string) int {
	if tenantHex == "" {
		return 0
	}
	tid, err := primitive.ObjectIDFromHex(tenantHex)
	if err != nil {
		return 0
	}
	var root models.User
	if err := s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": tid}).Decode(&root); err != nil {
		return 0
	}
	if root.PackageID == nil {
		return 0
	}
	var pkg models.HostingPackage
	if err := s.db.Collection(database.ColPackages).FindOne(ctx, bson.M{"_id": *root.PackageID}).Decode(&pkg); err != nil {
		return 0
	}
	if pkg.MaxFTPAccounts <= 0 {
		return 0
	}
	return pkg.MaxFTPAccounts
}

// Create adds an additional FTP account under a domain the caller owns.
func (s *FTPService) Create(ctx context.Context, req *models.CreateFTPRequest) (*models.FTPAccountView, error) {
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	// Resolve owner + verify the domain exists and the caller owns it.
	owner := s.domainOwner(ctx, domain)
	if owner == "" {
		return nil, fmt.Errorf("domain %q not found", domain)
	}
	scope := GetCallerScope(ctx)
	if scope != nil && constants.IsTenantScoped(scope.Role) {
		if err := scope.AssertOwnsDomain(ctx, s.db, domain); err != nil {
			return nil, err
		}
		// Enforce the tenant's package FTP-account cap (best-effort: skipped
		// when the package/limit can't be resolved).
		if limit := s.ftpLimitForTenant(ctx, scope.TenantHex); limit > 0 {
			owners, _ := TenantUsernames(ctx, s.db, scope.TenantHex)
			domains, _ := scope.TenantDomains(ctx, s.db)
			used, _ := s.db.Collection(database.ColFTPAccounts).CountDocuments(ctx, bson.M{"$or": bson.A{
				bson.M{"domain": bson.M{"$in": domains}},
				bson.M{"user": bson.M{"$in": owners}},
			}})
			if used >= int64(limit) {
				return nil, fmt.Errorf("FTP account limit reached (%d/%d for this plan) — delete an unused account or upgrade the package", used, limit)
			}
		}
	}

	suffix := sanitiseFTPSuffix(req.Username)
	if suffix == "" {
		suffix = sanitiseFTPSuffix(strings.ReplaceAll(domain, ".", "_"))
	}
	username := owner + "_" + suffix
	// Reject a duplicate username up-front for a clean error.
	if n, _ := s.db.Collection(database.ColFTPAccounts).CountDocuments(ctx, bson.M{"username": username}); n > 0 {
		return nil, fmt.Errorf("an FTP account named %q already exists — pick a different username", username)
	}

	password := strings.TrimSpace(req.Password)
	if password == "" {
		password = generateRandomPassword(16)
	}
	homeDir := fmt.Sprintf("/home/%s/domains/%s/public_html", owner, domain)

	if err := agent.CreateFTPAccount(ctx, username, password, homeDir); err != nil {
		return nil, fmt.Errorf("create FTP account: %w", err)
	}
	now := time.Now()
	acct := models.FTPAccount{
		Username:  username,
		Domain:    domain,
		HomeDir:   homeDir,
		User:      owner,
		IsRoot:    false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	res, err := s.db.Collection(database.ColFTPAccounts).InsertOne(ctx, acct)
	if err != nil {
		// Roll back the system user so Mongo and pure-pw don't drift.
		_ = agent.DeleteFTPAccount(ctx, username)
		return nil, fmt.Errorf("persist FTP account: %w", err)
	}
	if oid, ok := res.InsertedID.(primitive.ObjectID); ok {
		acct.ID = oid
	}
	v := s.view(acct, password)
	return &v, nil
}

// ResetPassword changes an FTP account's password and returns the new value.
func (s *FTPService) ResetPassword(ctx context.Context, id, newPassword string) (*models.FTPAccountView, error) {
	acct, err := s.getOwned(ctx, id)
	if err != nil {
		return nil, err
	}
	password := strings.TrimSpace(newPassword)
	if password == "" {
		password = generateRandomPassword(16)
	}
	if err := agent.UpdateFTPPassword(ctx, acct.Username, password); err != nil {
		return nil, fmt.Errorf("update FTP password: %w", err)
	}
	s.db.Collection(database.ColFTPAccounts).UpdateByID(ctx, acct.ID, bson.M{"$set": bson.M{"updated_at": time.Now()}})
	v := s.view(*acct, password)
	return &v, nil
}

// Delete removes a non-root FTP account (pure-pw + Mongo).
func (s *FTPService) Delete(ctx context.Context, id string) error {
	acct, err := s.getOwned(ctx, id)
	if err != nil {
		return err
	}
	if acct.IsRoot {
		return fmt.Errorf("the domain's primary FTP account cannot be deleted")
	}
	if err := agent.DeleteFTPAccount(ctx, acct.Username); err != nil {
		return fmt.Errorf("delete FTP account: %w", err)
	}
	if _, err := s.db.Collection(database.ColFTPAccounts).DeleteOne(ctx, bson.M{"_id": acct.ID}); err != nil {
		return err
	}
	return nil
}
