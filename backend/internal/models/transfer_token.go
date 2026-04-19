package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// TransferToken is a one-shot, expiring credential that a Betazen Server
// Panel mints on the SOURCE side so the destination panel can pull data
// without ever seeing the source's root password.
//
// Lifecycle:
//
//  1. WHM admin on the source clicks "Generate Transfer Token". The source
//     mints a fresh ed25519 SSH keypair, writes the public half into
//     /root/.ssh/authorized_keys with a `# bzn-transfer-<id>` comment so
//     the janitor can find and remove it, and stores the private half in
//     this record. The opaque token string handed to the operator is
//     PlainToken (not stored — only its bcrypt hash sits in TokenHash).
//  2. Operator pastes the token into the destination wizard. The
//     destination calls `POST /api/v1/transfer/redeem` on the source. The
//     source matches the token to a row, returns `{ssh_user, ssh_port,
//     private_key_pem, expires_at}`, and stamps RedeemedAt/RedeemedFromIP.
//  3. After the transfer completes (or fails, or the operator clicks
//     Revoke), Status flips to "revoked" and the public key is removed
//     from authorized_keys. Expired tokens are cleaned the same way by
//     an hourly background janitor.
//
// PrivateKey and TokenHash are tagged `json:"-"` so they never leak back
// through the listing endpoint. PlainToken is a transient field returned
// only on the issue response — it's deliberately NOT persisted.
type TransferToken struct {
	ID              primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Label           string             `bson:"label" json:"label"`
	TokenHash       string             `bson:"token_hash" json:"-"`
	TokenPrefix     string             `bson:"token_prefix" json:"token_prefix"` // first 12 chars, for UI display
	SSHUser         string             `bson:"ssh_user" json:"ssh_user"`
	SSHPort         int                `bson:"ssh_port" json:"ssh_port"`
	PrivateKey      string             `bson:"private_key" json:"-"`
	PublicKey       string             `bson:"public_key" json:"public_key"`
	AuthKeyMarker   string             `bson:"auth_key_marker" json:"auth_key_marker"` // unique comment used to find the line in authorized_keys
	Status          string             `bson:"status" json:"status"`                   // active, revoked, expired
	CreatedAt       time.Time          `bson:"created_at" json:"created_at"`
	ExpiresAt       time.Time          `bson:"expires_at" json:"expires_at"`
	RedeemedAt      *time.Time         `bson:"redeemed_at,omitempty" json:"redeemed_at,omitempty"`
	RedeemedFromIP  string             `bson:"redeemed_from_ip,omitempty" json:"redeemed_from_ip,omitempty"`
	RevokedAt       *time.Time         `bson:"revoked_at,omitempty" json:"revoked_at,omitempty"`
	CreatedByUserID primitive.ObjectID `bson:"created_by_user_id,omitempty" json:"created_by_user_id,omitempty"`
	CreatedByEmail  string             `bson:"created_by_email,omitempty" json:"created_by_email,omitempty"`

	// PlainToken is set only on the response from Issue() and is never
	// persisted. The operator must copy it immediately because it cannot
	// be recovered later — only the bcrypt hash is stored.
	PlainToken string `bson:"-" json:"plain_token,omitempty"`
}

// IssueTransferTokenRequest is the body for POST /whm/transfer/tokens.
//
// Label is shown back to the operator in the listing so a single panel
// holding several outstanding tokens can still tell them apart ("VPS A
// → DC2 cutover", "test migration", etc). TTLHours is clamped server-side
// to [1, 168] (one hour to one week) — the source server's authorized_keys
// is the blast-radius surface here, so we don't allow indefinite tokens.
type IssueTransferTokenRequest struct {
	Label    string `json:"label"`
	TTLHours int    `json:"ttl_hours"`
	SSHPort  int    `json:"ssh_port"`
}

// RedeemTransferTokenRequest is the body for the public
// POST /api/v1/transfer/redeem endpoint. Token is the opaque string the
// operator pasted on the destination side.
type RedeemTransferTokenRequest struct {
	Token string `json:"token" validate:"required"`
}

// RedeemTransferTokenResponse is what the destination panel receives back
// from the source's redeem endpoint. The destination uses PrivateKeyPEM
// for SSH key auth instead of a password.
type RedeemTransferTokenResponse struct {
	SSHUser       string    `json:"ssh_user"`
	SSHPort       int       `json:"ssh_port"`
	PrivateKeyPEM string    `json:"private_key_pem"`
	TokenID       string    `json:"token_id"`
	ExpiresAt     time.Time `json:"expires_at"`
}
