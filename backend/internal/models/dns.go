package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type DNSZone struct {
	ID            primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Domain        string             `bson:"domain" json:"domain"`
	ServerIP      string             `bson:"server_ip" json:"server_ip"`
	AdminEmail    string             `bson:"admin_email" json:"admin_email"`
	Nameservers   []string           `bson:"nameservers" json:"nameservers"`
	DNSSECEnabled bool               `bson:"dnssec_enabled" json:"dnssec_enabled"`
	Status        string             `bson:"status,omitempty" json:"status"`
	Serial        int                `bson:"serial" json:"serial"`
	RecordsCount  int                `bson:"-" json:"records_count"`
	CreatedAt     time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt     time.Time          `bson:"updated_at" json:"updated_at"`
}

type DNSRecord struct {
	ID       primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	ZoneID   primitive.ObjectID `bson:"zone_id" json:"zone_id"`
	Type     string             `bson:"type" json:"type"`
	Name     string             `bson:"name" json:"name"`
	Value    string             `bson:"value" json:"value"`
	TTL      int                `bson:"ttl" json:"ttl"`
	Priority *int               `bson:"priority,omitempty" json:"priority,omitempty"`
	Weight   *int               `bson:"weight,omitempty" json:"weight,omitempty"`
	Port     *int               `bson:"port,omitempty" json:"port,omitempty"`
	CAAFlag  *int               `bson:"caa_flag,omitempty" json:"caa_flag,omitempty"`
	CAATag   string             `bson:"caa_tag,omitempty" json:"caa_tag,omitempty"`
	CreatedAt time.Time         `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time         `bson:"updated_at" json:"updated_at"`
}

type CreateZoneRequest struct {
	Domain      string      `json:"domain" validate:"required"`
	ServerIP    string      `json:"server_ip" validate:"required"`
	AdminEmail  string      `json:"admin_email"`
	Nameservers []string    `json:"nameservers"`
	Template    string      `json:"template" validate:"omitempty,oneof=standard email wordpress blank"`
	Records     []DNSRecord `json:"records"`
}

type CreateRecordRequest struct {
	Type     string `json:"type" validate:"required,oneof=A AAAA CNAME MX TXT NS SRV CAA SOA PTR ALIAS DNAME"`
	Name     string `json:"name" validate:"required"`
	Value    string `json:"value" validate:"required"`
	TTL      int    `json:"ttl"`
	Priority *int   `json:"priority"`
	Weight   *int   `json:"weight"`
	Port     *int   `json:"port"`
	CAAFlag  *int   `json:"caa_flag"`
	CAATag   string `json:"caa_tag"`
}
