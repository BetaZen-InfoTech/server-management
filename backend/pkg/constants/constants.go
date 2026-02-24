package constants

// Roles
const (
	RoleVendorOwner = "vendor_owner"
	RoleVendorAdmin = "vendor_admin"
	RoleDeveloper   = "developer"
	RoleSupport     = "support"
	RoleCustomer    = "customer"
)

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
		PermUserCreate, PermUserView,
		PermMonitorView, PermLogView, PermFileManage, PermSSHManage,
		PermProcessView,
		PermNotificationManage, PermAuditView, PermMaintenanceManage,
		PermDeployManage, PermDeployView,
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
