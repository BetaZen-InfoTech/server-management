package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type SSLCertificate struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Domain         string             `bson:"domain" json:"domain"`
	Issuer         string             `bson:"issuer" json:"issuer"`
	Type           string             `bson:"type" json:"type"`
	Domains        []string           `bson:"domains" json:"domains"`
	IssuedAt       *time.Time         `bson:"issued_at" json:"issued_at"`
	ExpiresAt      *time.Time         `bson:"expires_at" json:"expires_at"`
	DaysRemaining  int                `bson:"days_remaining" json:"days_remaining"`
	AutoRenew      bool               `bson:"auto_renew" json:"auto_renew"`
	ForceSSL       bool               `bson:"force_ssl" json:"force_ssl"`
	Wildcard       bool               `bson:"wildcard" json:"wildcard"`
	KeyType        string             `bson:"key_type" json:"key_type"`
	SerialNumber   string             `bson:"serial_number" json:"serial_number"`
	FingerprintSHA256 string          `bson:"fingerprint_sha256" json:"fingerprint_sha256"`
	CertPath       string             `bson:"cert_path" json:"-"`
	KeyPath        string             `bson:"key_path" json:"-"`
	CABundlePath   string             `bson:"ca_bundle_path" json:"-"`
	CreatedAt      time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt      time.Time          `bson:"updated_at" json:"updated_at"`
}

type IssueLetsEncryptRequest struct {
	Domain            string   `json:"domain" validate:"required"`
	Email             string   `json:"email" validate:"required,email"`
	Wildcard          bool     `json:"wildcard"`
	AdditionalDomains []string `json:"additional_domains"`
}

type UploadCustomCertRequest struct {
	Domain      string `json:"domain" validate:"required"`
	Certificate string `json:"certificate" validate:"required"`
	PrivateKey  string `json:"private_key" validate:"required"`
	CABundle    string `json:"ca_bundle"`
}
