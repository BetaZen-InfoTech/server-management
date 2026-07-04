package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// PushSubscription is a browser/PWA Web Push endpoint (RFC 8030 + VAPID). One
// row per subscribed browser; a user with the webmail open on phone + laptop
// has two. Endpoint is globally unique (the push service issues it), so a
// browser re-subscribing upserts the same row.
type PushSubscription struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID    primitive.ObjectID `bson:"user_id" json:"user_id"`
	Endpoint  string             `bson:"endpoint" json:"endpoint"`
	P256dh    string             `bson:"p256dh" json:"p256dh"`
	Auth      string             `bson:"auth" json:"auth"`
	UA        string             `bson:"ua,omitempty" json:"ua,omitempty"`
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
}

// PushSubscribeRequest mirrors the shape a browser's PushSubscription.toJSON()
// produces: { endpoint, keys: { p256dh, auth } }.
type PushSubscribeRequest struct {
	Endpoint string `json:"endpoint" validate:"required"`
	Keys     struct {
		P256dh string `json:"p256dh" validate:"required"`
		Auth   string `json:"auth" validate:"required"`
	} `json:"keys" validate:"required"`
}

type PushUnsubscribeRequest struct {
	Endpoint string `json:"endpoint" validate:"required"`
}

// NotifyState remembers the last INBOX UIDNEXT we saw for an account so the
// new-mail poller can tell "a new message arrived" from "nothing changed"
// without re-scanning the whole mailbox. One row per account.
type NotifyState struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	AccountID   primitive.ObjectID `bson:"account_id" json:"account_id"`
	UserID      primitive.ObjectID `bson:"user_id" json:"user_id"`
	LastUIDNext uint32             `bson:"last_uidnext" json:"last_uidnext"`
	UpdatedAt   time.Time          `bson:"updated_at" json:"updated_at"`
}
