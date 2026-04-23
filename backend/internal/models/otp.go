package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// OTPRequest is a single email-login code issued by /auth/otp/request.
// The raw code is NEVER stored — only its SHA-256 hash, so a leaked DB
// dump can't be used to log anyone in. Code is consumed at most once
// (Used=true) and expires after ExpiresAt.
//
// BindingHash binds the OTP to the browser that requested it. The
// handler generates a random binding token, stores its sha256 here,
// and sets the raw token as an HTTP-only cookie on the requesting
// browser. /auth/otp/verify then requires that cookie back —
// otherwise the magic URL is useless if pasted into a different
// browser, which is exactly the "share the URL → instant takeover"
// hole the older code-only flow allowed. Empty string on legacy
// docs (created before binding shipped) so verification stays
// permissive for in-flight OTPs across the deploy window.
type OTPRequest struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	Email       string             `bson:"email" json:"email"`
	CodeHash    string             `bson:"code_hash" json:"-"`              // sha256(code) — raw code never stored
	BindingHash string             `bson:"binding_hash,omitempty" json:"-"` // sha256(browser cookie token)
	Surface     string             `bson:"surface" json:"surface"`          // "whm" | "user-panel"
	IP          string             `bson:"ip" json:"ip"`
	UserAgent   string             `bson:"user_agent" json:"user_agent"`
	Attempts    int                `bson:"attempts" json:"attempts"`
	Used        bool               `bson:"used" json:"used"`
	UsedAt      *time.Time         `bson:"used_at,omitempty" json:"used_at,omitempty"`
	ExpiresAt   time.Time          `bson:"expires_at" json:"expires_at"`
	CreatedAt   time.Time          `bson:"created_at" json:"created_at"`
}
