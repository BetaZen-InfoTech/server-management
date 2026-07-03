package database

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func EnsureIndexes(ctx context.Context, db *DB) error {
	specs := map[string][]mongo.IndexModel{
		ColUsers: {
			{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)},
		},
		ColAccounts: {
			{Keys: bson.D{{Key: "user_id", Value: 1}}},
			{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "address", Value: 1}}, Options: options.Index().SetUnique(true)},
		},
		ColDevices: {
			{Keys: bson.D{{Key: "user_id", Value: 1}}},
			{Keys: bson.D{{Key: "fcm_token", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
		},
		ColSignatures: {
			{Keys: bson.D{{Key: "user_id", Value: 1}}},
		},
		ColForwarders: {
			{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "source", Value: 1}}},
		},
		ColFilters: {
			{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "account_id", Value: 1}}},
		},
		ColThreadsIdx: {
			{Keys: bson.D{{Key: "account_id", Value: 1}, {Key: "folder", Value: 1}, {Key: "updated_at", Value: -1}}},
			{Keys: bson.D{{Key: "account_id", Value: 1}, {Key: "thread_key", Value: 1}}, Options: options.Index().SetUnique(true)},
		},
		ColMessagesIdx: {
			{Keys: bson.D{{Key: "account_id", Value: 1}, {Key: "uid", Value: 1}, {Key: "folder", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "thread_id", Value: 1}}},
		},
		ColPasskeys: {
			{Keys: bson.D{{Key: "user_id", Value: 1}}},
			{Keys: bson.D{{Key: "credential_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		},
		ColOAuthClients: {
			{Keys: bson.D{{Key: "client_id", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "owner_user_id", Value: 1}}},
		},
		ColOAuthGrants: {
			{Keys: bson.D{{Key: "code", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "expires_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
		},
		ColOAuthTokens: {
			{Keys: bson.D{{Key: "access_token", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "refresh_token", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
			{Keys: bson.D{{Key: "expires_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
		},
		ColRefreshToks: {
			{Keys: bson.D{{Key: "token", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "user_id", Value: 1}}},
			{Keys: bson.D{{Key: "expires_at", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
		},
		ColAudit: {
			{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "at", Value: -1}}},
		},
		ColDrafts: {
			{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "updated_at", Value: -1}}},
			{Keys: bson.D{{Key: "account_id", Value: 1}}},
		},
		ColSent: {
			{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "account_id", Value: 1}, {Key: "sent_at", Value: -1}}},
			{Keys: bson.D{{Key: "track_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		},
		ColTracking: {
			{Keys: bson.D{{Key: "track_id", Value: 1}, {Key: "at", Value: -1}}},
		},
		ColContacts: {
			{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "group_ids", Value: 1}}},
			{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "status", Value: 1}}},
			{Keys: bson.D{{Key: "unsub_token", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
		},
		ColContactGroups: {
			{Keys: bson.D{{Key: "user_id", Value: 1}, {Key: "created_at", Value: -1}}},
		},
	}

	for col, idx := range specs {
		if _, err := db.Col(col).Indexes().CreateMany(ctx, idx); err != nil {
			return err
		}
	}
	return nil
}
