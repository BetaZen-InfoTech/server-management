package database

import (
	"context"
	"time"

	"github.com/betazeninfotech/whm-cpanel-management/internal/config"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

var DB *mongo.Database

func Connect(cfg *config.Config) (*mongo.Database, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	clientOpts := options.Client().
		ApplyURI(cfg.MongoURI).
		SetMaxPoolSize(50).
		SetMinPoolSize(5).
		SetMaxConnIdleTime(30 * time.Second)

	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, err
	}

	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return nil, err
	}

	DB = client.Database(cfg.MongoDBName)
	log.Info().Str("db", cfg.MongoDBName).Msg("Connected to MongoDB")

	// Dedup before indexing — earlier transfers could leave duplicate
	// (source, domain) rows in email_forwarders, and the unique index
	// in EnsureIndexes won't create over duplicates. Best-effort, log-
	// only on failure: a stuck dedup must not block boot.
	if n, err := DedupEmailForwarders(ctx, DB); err != nil {
		log.Warn().Err(err).Msg("email_forwarders dedup failed — unique index may not create")
	} else if n > 0 {
		log.Info().Int("deleted", n).Msg("email_forwarders: deduplicated stale duplicate rows from prior transfers")
	}

	if err := EnsureIndexes(ctx, DB); err != nil {
		log.Warn().Err(err).Msg("Failed to ensure some indexes")
	}

	return DB, nil
}

func Disconnect() {
	if DB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = DB.Client().Disconnect(ctx)
	}
}
