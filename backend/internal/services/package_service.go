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

	if pkg.AccountCount > 0 {
		return fmt.Errorf("cannot delete package with %d active accounts", pkg.AccountCount)
	}

	_, err = col.DeleteOne(ctx, bson.M{"_id": oid})
	return err
}
