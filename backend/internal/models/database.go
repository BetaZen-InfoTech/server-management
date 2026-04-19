package models

import (
	"time"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

type Database struct {
	ID               primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	DBName           string             `bson:"db_name" json:"db_name"`
	Type             string             `bson:"type" json:"type"` // "mongodb" or "mysql"
	Username         string             `bson:"username" json:"username"`
	Password         string             `bson:"password" json:"-"`
	Domain           string             `bson:"domain" json:"domain"`
	Host             string             `bson:"host" json:"host"`
	Port             int                `bson:"port" json:"port"`
	ConnectionString string            `bson:"connection_string" json:"connection_string"`
	SizeMB           float64           `bson:"size_mb" json:"size_mb"`
	CreatedAt        time.Time         `bson:"created_at" json:"created_at"`
	UpdatedAt        time.Time         `bson:"updated_at" json:"updated_at"`
}

type CreateDatabaseRequest struct {
	DBName   string `json:"db_name" validate:"required"`
	Type     string `json:"type" validate:"required,oneof=mongodb mysql"`
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required,min=8"`
	// Vendor is the system user the db should be namespaced under —
	// every db_name + username gets prefixed with "<vendor>_". Either
	// Vendor or Domain may be supplied; Domain takes precedence and the
	// backend looks up the domain's owner. Domain is now optional (the
	// db isn't tied to one specific website — vendors often share a db
	// across several domains).
	Vendor string `json:"vendor"`
	Domain string `json:"domain"`
}

type DatabaseUser struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	DatabaseID primitive.ObjectID `bson:"database_id" json:"database_id"`
	Username   string             `bson:"username" json:"username"`
	Password   string             `bson:"password" json:"-"`
	Role       string             `bson:"role" json:"role"`
	CreatedAt  time.Time          `bson:"created_at" json:"created_at"`
}

type CreateDBUserRequest struct {
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required,min=8"`
	Role     string `json:"role" validate:"required,oneof=readWrite read dbAdmin dbOwner userAdmin"`
}

type RemoteAccessRequest struct {
	Username  string `json:"username" validate:"required"`
	AllowedIP string `json:"allowed_ip" validate:"required"`
}

// DBAccessHost is a persistent, per-database record of a remote IP / CIDR /
// hostname / MySQL wildcard pattern that's allowed to connect. Mirrors
// cPanel's "Remote Database Access" page: the operator adds hosts one-by-one
// and the backend both creates a MySQL user scoped to that host AND opens the
// firewall for it. Dropping the host removes both sides.
type DBAccessHost struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	DatabaseID primitive.ObjectID `bson:"database_id" json:"database_id"`
	Host       string             `bson:"host" json:"host"`
	Comment    string             `bson:"comment" json:"comment"`
	CreatedAt  time.Time          `bson:"created_at" json:"created_at"`
}

// AddAccessHostRequest is the body for POST /databases/:id/access-hosts.
// Host accepts `%`, an IPv4 / IPv6, a CIDR, a hostname, or a MySQL wildcard
// pattern like `192.168.1.%` — validation happens server-side.
type AddAccessHostRequest struct {
	Host    string `json:"host" validate:"required,min=1,max=253"`
	Comment string `json:"comment" validate:"max=200"`
}

type UpdatePasswordRequest struct {
	Password string `json:"password" validate:"required,min=8"`
}

type UpdateUserRoleRequest struct {
	Role string `json:"role" validate:"required,oneof=readWrite read dbAdmin dbOwner userAdmin"`
}

type ConnectionInfoResponse struct {
	Type             string `json:"type"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	Database         string `json:"database"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	ConnectionString string `json:"connection_string"`
	CLICommand       string `json:"cli_command"`
}

type PhpMyAdminResponse struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
	Database string `json:"database"`
	Server   string `json:"server"`
}
