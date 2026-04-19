package services

import (
	"context"
	"fmt"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/database"
	"github.com/betazeninfotech/whm-cpanel-management/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type PackageService struct {
	db *mongo.Database
}

func NewPackageService(db *mongo.Database) *PackageService {
	return &PackageService{db: db}
}

func (s *PackageService) List(ctx context.Context, search string) ([]models.HostingPackage, error) {
	col := s.db.Collection(database.ColPackages)
	filter := bson.M{}
	if search != "" {
		filter["name"] = bson.M{"$regex": search, "$options": "i"}
	}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	cursor, err := col.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var packages []models.HostingPackage
	if err := cursor.All(ctx, &packages); err != nil {
		return nil, err
	}
	if packages == nil {
		packages = []models.HostingPackage{}
	}
	return packages, nil
}

func (s *PackageService) GetByID(ctx context.Context, id string) (*models.HostingPackage, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid package ID")
	}
	var pkg models.HostingPackage
	if err := s.db.Collection(database.ColPackages).FindOne(ctx, bson.M{"_id": oid}).Decode(&pkg); err != nil {
		return nil, err
	}
	return &pkg, nil
}

func (s *PackageService) Create(ctx context.Context, req *models.CreatePackageRequest, createdBy string) (*models.HostingPackage, error) {
	// Check for duplicate name
	col := s.db.Collection(database.ColPackages)
	count, _ := col.CountDocuments(ctx, bson.M{"name": req.Name})
	if count > 0 {
		return nil, fmt.Errorf("package name '%s' already exists", req.Name)
	}

	now := time.Now()
	pkg := models.HostingPackage{
		Name:      req.Name,
		CreatedBy: createdBy,

		DiskQuotaMB:        req.DiskQuotaMB,
		DiskQuotaUnlimited: req.DiskQuotaUnlimited,
		BandwidthMB:        req.BandwidthMB,
		BandwidthUnlimited: req.BandwidthUnlimited,
		MaxFTPAccounts:        req.MaxFTPAccounts,
		MaxFTPUnlimited:       req.MaxFTPUnlimited,
		MaxEmailAccounts:      req.MaxEmailAccounts,
		MaxEmailUnlimited:     req.MaxEmailUnlimited,
		MaxMailingLists:       req.MaxMailingLists,
		MaxMailingUnlimited:   req.MaxMailingUnlimited,
		MaxDatabases:          req.MaxDatabases,
		MaxDatabasesUnlimited: req.MaxDatabasesUnlimited,
		MaxSubDomains:         req.MaxSubDomains,
		MaxSubDomainsUnlimited: req.MaxSubDomainsUnlimited,
		MaxParkedDomains:      req.MaxParkedDomains,
		MaxParkedUnlimited:    req.MaxParkedUnlimited,
		MaxAddonDomains:       req.MaxAddonDomains,
		MaxAddonUnlimited:     req.MaxAddonUnlimited,
		MaxPassengerApps:      req.MaxPassengerApps,
		MaxPassengerUnlimited: req.MaxPassengerUnlimited,
		MaxHourlyEmail:        req.MaxHourlyEmail,
		MaxHourlyEmailUnlimited: req.MaxHourlyEmailUnlimited,
		MaxFailPercent:        req.MaxFailPercent,
		MaxEmailQuotaMB:       req.MaxEmailQuotaMB,
		MaxEmailQuotaUnlimited: req.MaxEmailQuotaUnlimited,

		DedicatedIP: req.DedicatedIP,
		ShellAccess: req.ShellAccess,
		CGIAccess:   req.CGIAccess,
		DigestAuth:  req.DigestAuth,
		Theme:       req.Theme,
		FeatureList: req.FeatureList,
		Locale:      req.Locale,

		WPToolkit:  req.WPToolkit,
		LVEEnabled: req.LVEEnabled,

		LVESpeed:      req.LVESpeed,
		LVESpeedMySQL: req.LVESpeedMySQL,
		LVEVMEM:       req.LVEVMEM,
		LVEPMEM:       req.LVEPMEM,
		LVEIO:         req.LVEIO,
		LVEMySQLIO:    req.LVEMySQLIO,
		LVEIOPS:       req.LVEIOPS,
		LVEEP:         req.LVEEP,
		LVENPROC:      req.LVENPROC,
		LVEINODESSoft: req.LVEINODESSoft,
		LVEINODESHard: req.LVEINODESHard,

		AccountCount: 0,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	result, err := col.InsertOne(ctx, pkg)
	if err != nil {
		return nil, err
	}
	pkg.ID = result.InsertedID.(primitive.ObjectID)
	return &pkg, nil
}

func (s *PackageService) Update(ctx context.Context, id string, updates map[string]interface{}) (*models.HostingPackage, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid package ID")
	}

	updates["updated_at"] = time.Now()
	col := s.db.Collection(database.ColPackages)
	_, err = col.UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": updates})
	if err != nil {
		return nil, err
	}

	var pkg models.HostingPackage
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&pkg); err != nil {
		return nil, err
	}
	return &pkg, nil
}

func (s *PackageService) Delete(ctx context.Context, id string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid package ID")
	}

	var pkg models.HostingPackage
	col := s.db.Collection(database.ColPackages)
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&pkg); err != nil {
		return fmt.Errorf("package not found")
	}

	if pkg.IsDefault {
		return fmt.Errorf("cannot delete the default package")
	}
	if pkg.AccountCount > 0 {
		return fmt.Errorf("cannot delete package with %d active accounts", pkg.AccountCount)
	}

	_, err = col.DeleteOne(ctx, bson.M{"_id": oid})
	return err
}

// ----------------------------------------------------------------------
// Package change requests — payment-gated vendor plan switches
// ----------------------------------------------------------------------

// RequestChange submits a vendor's "I want to switch plan" application.
// Rejects a second submission while an earlier one is still pending so
// the admin queue doesn't get flooded with duplicates.
func (s *PackageService) RequestChange(ctx context.Context, vendorID primitive.ObjectID, targetPackageID, note string) (*models.PackageChangeRequest, error) {
	targetOID, err := primitive.ObjectIDFromHex(targetPackageID)
	if err != nil {
		return nil, fmt.Errorf("invalid package id")
	}
	// Enforce one-pending-per-vendor.
	col := s.db.Collection(database.ColPackageRequests)
	if n, _ := col.CountDocuments(ctx, bson.M{
		"vendor_id": vendorID,
		"status":    "pending",
	}); n > 0 {
		return nil, fmt.Errorf("you already have a pending change request — wait for admin review before submitting another")
	}
	// Resolve vendor + from/to package names so the admin review UI
	// doesn't have to do extra lookups per row.
	var vendor models.User
	if err := s.db.Collection(database.ColUsers).FindOne(ctx, bson.M{"_id": vendorID}).Decode(&vendor); err != nil {
		return nil, fmt.Errorf("vendor not found")
	}
	var toPkg models.HostingPackage
	if err := s.db.Collection(database.ColPackages).FindOne(ctx, bson.M{"_id": targetOID}).Decode(&toPkg); err != nil {
		return nil, fmt.Errorf("target package not found")
	}
	// Block no-ops so the admin queue doesn't see "switch X → X".
	if vendor.PackageID != nil && *vendor.PackageID == targetOID {
		return nil, fmt.Errorf("you are already on the %q plan", toPkg.Name)
	}
	req := models.PackageChangeRequest{
		VendorID:        vendorID,
		VendorUsername:  vendor.Username,
		VendorName:      vendor.Name,
		FromPackageID:   vendor.PackageID,
		FromPackageName: vendor.PackageName,
		ToPackageID:     targetOID,
		ToPackageName:   toPkg.Name,
		Note:            note,
		Status:          "pending",
		CreatedAt:       time.Now(),
	}
	res, err := col.InsertOne(ctx, req)
	if err != nil {
		return nil, err
	}
	req.ID = res.InsertedID.(primitive.ObjectID)
	return &req, nil
}

// MyPendingRequest returns the vendor's own pending request (if any)
// so the vendor-side Packages page can show a "your upgrade to X is
// waiting on admin approval" banner.
func (s *PackageService) MyPendingRequest(ctx context.Context, vendorID primitive.ObjectID) (*models.PackageChangeRequest, error) {
	var req models.PackageChangeRequest
	err := s.db.Collection(database.ColPackageRequests).FindOne(ctx, bson.M{
		"vendor_id": vendorID,
		"status":    "pending",
	}).Decode(&req)
	if err != nil {
		return nil, nil // no pending → not an error for the caller
	}
	return &req, nil
}

// ListRequests returns requests optionally filtered by status. Empty
// status = every row. Sorted pending-first, newest-first inside status.
func (s *PackageService) ListRequests(ctx context.Context, status string) ([]models.PackageChangeRequest, error) {
	filter := bson.M{}
	if status != "" {
		filter["status"] = status
	}
	opts := options.Find().
		SetSort(bson.D{{Key: "status", Value: 1}, {Key: "created_at", Value: -1}}).
		SetLimit(200)
	cur, err := s.db.Collection(database.ColPackageRequests).Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var rows []models.PackageChangeRequest
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []models.PackageChangeRequest{}
	}
	return rows, nil
}

// ApproveRequest marks a pending request approved, applies the package
// swap on the vendor's user record (via the caller-provided applyFn so
// we don't have to import UserService directly here — avoids a
// cycle), and stamps the admin / payment ref. applyFn mirrors
// UserService.UpdatePackage's shape.
func (s *PackageService) ApproveRequest(ctx context.Context, id, adminUsername, paymentRef, note string, applyFn func(ctx context.Context, userID, packageID string) error) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid request id")
	}
	col := s.db.Collection(database.ColPackageRequests)
	var req models.PackageChangeRequest
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&req); err != nil {
		return fmt.Errorf("request not found")
	}
	if req.Status != "pending" {
		return fmt.Errorf("request is not pending (current status: %s)", req.Status)
	}
	if applyFn != nil {
		if err := applyFn(ctx, req.VendorID.Hex(), req.ToPackageID.Hex()); err != nil {
			return fmt.Errorf("package swap failed: %w", err)
		}
	}
	now := time.Now()
	_, err = col.UpdateByID(ctx, oid, bson.M{"$set": bson.M{
		"status":            "approved",
		"payment_reference": paymentRef,
		"admin_response":    note,
		"resolved_at":       now,
		"resolved_by":       adminUsername,
	}})
	return err
}

// RejectRequest marks a pending request as rejected with a reason.
// Leaves the vendor's existing package untouched.
func (s *PackageService) RejectRequest(ctx context.Context, id, adminUsername, reason string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid request id")
	}
	col := s.db.Collection(database.ColPackageRequests)
	var req models.PackageChangeRequest
	if err := col.FindOne(ctx, bson.M{"_id": oid}).Decode(&req); err != nil {
		return fmt.Errorf("request not found")
	}
	if req.Status != "pending" {
		return fmt.Errorf("request is not pending (current status: %s)", req.Status)
	}
	now := time.Now()
	_, err = col.UpdateByID(ctx, oid, bson.M{"$set": bson.M{
		"status":         "rejected",
		"admin_response": reason,
		"resolved_at":    now,
		"resolved_by":    adminUsername,
	}})
	return err
}
