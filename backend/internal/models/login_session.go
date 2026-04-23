package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// LoginSession is an audit row written on every successful login
// (password OR OTP). It carries what we can glean about the device and
// origin so a user can review "was that me?" from the Account page.
//
// Geolocation is best-effort — ip-api.com is free and rate-limited, so
// we record whatever it returns but never block login on it. Blank
// Country/City means the lookup failed or was skipped (private IPs).
type LoginSession struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	UserID    primitive.ObjectID `bson:"user_id" json:"user_id"`
	Email     string             `bson:"email" json:"email"`
	Role      string             `bson:"role" json:"role"`
	Method    string             `bson:"method" json:"method"` // "password" | "otp"
	IP        string             `bson:"ip" json:"ip"`
	Country   string             `bson:"country,omitempty" json:"country,omitempty"`
	Region    string             `bson:"region,omitempty" json:"region,omitempty"`
	City      string             `bson:"city,omitempty" json:"city,omitempty"`
	UserAgent string             `bson:"user_agent" json:"user_agent"`
	Browser   string             `bson:"browser,omitempty" json:"browser,omitempty"`
	OS        string             `bson:"os,omitempty" json:"os,omitempty"`
	Device    string             `bson:"device,omitempty" json:"device,omitempty"` // desktop | mobile | tablet | bot
	LoginAt   time.Time          `bson:"login_at" json:"login_at"`
}
