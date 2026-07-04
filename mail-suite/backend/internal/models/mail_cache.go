package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// FolderHeaderCache is a read-through cache of a mailbox folder's message
// headers (page 1), stored per (account, folder). It lets the inbox render
// instantly from MongoDB instead of re-fetching from a slow external IMAP server
// (Gmail etc.) on every open; a background refresh keeps it current.
type FolderHeaderCache struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	AccountID primitive.ObjectID `bson:"account_id"`
	Folder    string             `bson:"folder"`
	Headers   []MessageHeader    `bson:"headers"`
	Total     int                `bson:"total"`
	SyncedAt  time.Time          `bson:"synced_at"`
}
