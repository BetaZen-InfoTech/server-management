export interface Domain {
  id: string;
  domain: string;
  user: string;
  php_version: string;
  disk_quota_mb: number;
  bandwidth_limit_gb: number;
  max_databases: number;
  max_email_accounts: number;
  max_subdomains: number;
  max_apps: number;
  ssl_active: boolean;
  ssl_expires: string | null;
  status: "active" | "suspended";
  created_at: string;
  updated_at: string;
}

export interface CreateDomainRequest {
  domain: string;
  user: string;
  password: string;
  php_version: string;
  disk_quota_mb?: number;
  bandwidth_limit_gb?: number;
  max_databases?: number;
  max_email_accounts?: number;
  max_subdomains?: number;
  max_apps?: number;
}

export interface DomainStats {
  domain: string;
  disk_used_mb: number;
  disk_quota_mb: number;
  bandwidth_used_gb: number;
  bandwidth_limit_gb: number;
  email_accounts: number;
  max_email_accounts: number;
  databases: number;
  max_databases: number;
  subdomains: number;
  max_subdomains: number;
  apps: number;
  max_apps: number;
  php_version: string;
  ssl_active: boolean;
  ssl_expires: string | null;
  status: string;
  created_at: string;
}
