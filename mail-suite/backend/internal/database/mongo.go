package database

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type DB struct {
	Client *mongo.Client
	DB     *mongo.Database
}

func Connect(ctx context.Context, uri, name string) (*DB, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(cctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	if err := client.Ping(cctx, nil); err != nil {
		return nil, err
	}
	return &DB{Client: client, DB: client.Database(name)}, nil
}

func (d *DB) Close(ctx context.Context) error {
	return d.Client.Disconnect(ctx)
}

func (d *DB) Col(name string) *mongo.Collection {
	return d.DB.Collection(name)
}
