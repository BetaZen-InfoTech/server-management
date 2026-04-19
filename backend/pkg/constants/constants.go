package constants

// Roles
const (
	RoleVendorOwner = "vendor_owner"
	RoleVendorAdmin = "vendor_admin"
	RoleVendorStaff = "vendor_staff"
	RoleDeveloper   = "developer"
	RoleSupport     = "support"
	RoleCustomer    = "customer"
)

// IsTenantRoot reports whether a role is allowed to own a tenant (its tenant_id
// equals its own _id). vendor_owner is the platform owner; vendor_admin is a
// reseller / "vendor" account.
func IsTenantRoot(role string) bool {
	return role == RoleVendorOwner || role == RoleVendorAdmin
}

// IsTenantScoped reports whether resource queries for this role must be
// filtered to the requester's tenant. The platform owner sees everything.
func IsTenantScoped(role string) bool {
	return role != RoleVendorOwner
}

// Permissions
const (
	PermServerManage      = "server.manage"
	PermServerView        = "server.view"
	PermDomainCreate      = "domain.create"
	PermDomainView        = "domain.view"
	PermDomainDelete      = "domain.delete"
	PermDomainManage      = "domain.manage"
	PermEmailCreate       = "email.create"
	PermEmailView         = "email.view"
	PermEmailManage       = "email.manage"
	PermDatabaseCreate    = "database.create"
	PermDatabaseView      = "database.view"
	PermDatabaseManage    = "database.manage"
	PermAppDeploy         = "app.deploy"
	PermAppManage         = "app.manage"
	PermAppView           = "app.view"
	PermWordPressInstall  = "wordpress.install"
	PermWordPressManage   = "wordpress.manage"
	PermBackupCreate      = "backup.create"
	PermBackupRestore     = "backup.restore"
	PermBackupView        = "backup.view"
	PermSSLManage         = "ssl.manage"
	PermDNSManage         = "dns.manage"
	PermDNSView           = "dns.view"
	PermCronManage        = "cron.manage"
	PermFirewallManage    = "firewall.manage"
	PermUserCreate        = "user.create"
	PermUserView          = "user.view"
	PermUserManage        = "user.manage"
	PermMonitorView       = "monitor.view"
	PermLogView           = "log.view"
	PermFileManage        = "file.manage"
	PermSSHManage         = "ssh.manage"
	PermProcessView       = "process.view"
	PermProcessManage     = "process.manage"
	PermNotificationManage = "notification.manage"
	PermAuditView         = "audit.view"
	PermConfigManage      = "config.manage"
	PermMaintenanceManage = "maintenance.manage"
	PermDeployManage      = "deploy.manage"
	PermDeployView        = "deploy.view"
	PermPackageView       = "package.view"
	PermPackageCreate     = "package.create"
	PermPackageManage     = "package.manage"
	PermPackageDelete     = "package.delete"
	PermTransferView      = "transfer.view"
	PermTransferCreate    = "transfer.create"
	PermTransferManage    = "transfer.manage"
	PermBackupDelete      = "backup.delete"
	PermBackupSchedule    = "backup.schedule"
)

// DefaultPermissions returns the default permission set for a role.
var DefaultPermissions = map[string][]string{
	RoleVendorOwner: {
		PermServerManage, PermServerView,
		PermDomainCreate, PermDomainView, PermDomainDelete, PermDomainManage,
		PermEmailCreate, PermEmailView, PermEmailManage,
		PermDatabaseCreate, PermDatabaseView, PermDatabaseManage,
		PermAppDeploy, PermAppManage, PermAppView,
		PermWordPressInstall, PermWordPressManage,
		PermBackupCreate, PermBackupRestore, PermBackupView,
		PermSSLManage, PermDNSManage, PermDNSView,
		PermCronManage, PermFirewallManage,
		PermUserCreate, PermUserView, PermUserManage,
		PermMonitorView, PermLogView, PermFileManage, PermSSHManage,
		PermProcessView, PermProcessManage,
		PermNotificationManage, PermAuditView, PermConfigManage, PermMaintenanceManage,
		PermDeployManage, PermDeployView,
		PermPackageView, PermPackageCreate, PermPackageManage, PermPackageDelete,
		PermTransferView, PermTransferCreate, PermTransferManage,
		PermBackupDelete, PermBackupSchedule,
	},
	RoleVendorAdmin: {
		PermServerView,
		PermDomainCreate, PermDomainView, PermDomainManage,
		PermEmailCreate, PermEmailView, PermEmailManage,
		PermDatabaseCreate, PermDatabaseView, PermDatabaseManage,
		PermAppDeploy, PermAppManage, PermAppView,
		PermWordPressInstall, PermWordPressManage,
		PermBackupCreate, PermBackupRestore, PermBackupView,
		PermSSLManage, PermDNSManage, PermDNSView,
		PermCronManage,
		PermUserCreate, PermUserView, PermUserManage,
		PermMonitorView, PermLogView, PermFileManage, PermSSHManage,
		PermProcessView,
		PermNotificationManage, PermAuditView, PermMaintenanceManage,
		PermDeployManage, PermDeployView,
		// Packages are a platform-level catalog owned by the platform
		// owner. Vendors can READ the catalog (to know what plans exist)
		// but CANNOT create / edit / delete packages — those are the
		// platform's pricing tiers, not per-tenant data. Switching their
		// own package is done via a payment-gated change request, not a
		// direct mutation on the catalog row.
		PermPackageView,
		PermTransferView, PermTransferCreate,
		PermBackupSchedule,
	},
	// vendor_staff = vendor_admin minus user management. Staff can use every
	// tool inside the tenant but cannot add/remove other staff or delete the
	// vendor account.
	RoleVendorStaff: {
		PermServerView,
		PermDomainCreate, PermDomainView, PermDomainManage,
		PermEmailCreate, PermEmailView, PermEmailManage,
		PermDatabaseCreate, PermDatabaseView, PermDatabaseManage,
		PermAppDeploy, PermAppManage, PermAppView,
		PermWordPressInstall, PermWordPressManage,
		PermBackupCreate, PermBackupRestore, PermBackupView,
		PermSSLManage, PermDNSManage, PermDNSView,
		PermCronManage,
		PermMonitorView, PermLogView, PermFileManage, PermSSHManage,
		PermProcessView,
		PermNotificationManage, PermMaintenanceManage,
		PermDeployManage, PermDeployView,
		PermPackageView,
		PermTransferView, PermTransferCreate,
		PermBackupSchedule,
	},
	RoleDeveloper: {
		PermDomainView,
		PermDatabaseView,
		PermAppDeploy, PermAppManage, PermAppView,
		PermMonitorView, PermLogView, PermFileManage,
		PermProcessView,
		PermDeployManage, PermDeployView,
	},
	RoleSupport: {
		PermServerView, PermDomainView,
		PermEmailView, PermDatabaseView, PermAppView,
		PermDNSView, PermBackupView,
		PermUserView, PermMonitorView, PermLogView,
		PermProcessView, PermAuditView, PermDeployView,
	},
	RoleCustomer: {
		PermDomainView,
		PermEmailCreate, PermEmailView, PermEmailManage,
		PermDatabaseCreate, PermDatabaseView, PermDatabaseManage,
		PermAppDeploy, PermAppView,
		PermWordPressInstall, PermWordPressManage,
		PermBackupCreate, PermBackupView,
		PermSSLManage, PermDNSView, PermCronManage,
		PermFileManage,
	},
}

// Domain statuses
const (
	DomainStatusActive    = "active"
	DomainStatusSuspended = "suspended"
)

// App statuses
const (
	AppStatusRunning  = "running"
	AppStatusStopped  = "stopped"
	AppStatusFailed   = "failed"
	AppStatusDeploying = "deploying"
)

// Backup types
const (
	BackupTypeFull     = "full"
	BackupTypeFiles    = "files"
	BackupTypeDatabase = "database"
	BackupTypeEmail    = "email"
	BackupTypeConfig   = "config"
)

// Deploy statuses
const (
	DeployStatusQueued      = "queued"
	DeployStatusCloning     = "cloning"
	DeployStatusBuilding    = "building"
	DeployStatusDeploying   = "deploying"
	DeployStatusLive        = "live"
	DeployStatusFailed      = "failed"
	DeployStatusCancelled   = "cancelled"
	DeployStatusRollingBack = "rolling_back"
)
