package database

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

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
			{Keys: bson.D{{Key: "collected_at", Value: -1}}},
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
	}

	for col, idxs := range indexes {
		_, err := db.Collection(col).Indexes().CreateMany(ctx, idxs)
		if err != nil {
			return err
		}
	}

	return nil
}
