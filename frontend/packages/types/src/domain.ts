// Deployment-tier values a domain can be tagged with. "prod" is the
// default; anything unrecognised normalises to "prod" server-side.
export type DomainEnvironment = "prod" | "dev" | "test" | "local";

export interface Domain {
  id: string;
  domain: string;
  user: string;
  php_version: string;
  // Operator-chosen deployment tier. Optional/empty on legacy rows;
  // treated as "prod" by the UI. Distinct from the structural
  // domain_type (primary/subdomain/addon) computed by preflight.
  environment?: DomainEnvironment | string;
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
  // Deployment tier for the new domain/subdomain; defaults to "prod".
  environment?: DomainEnvironment | string;
  // DNS backend for the new domain: "cloudflare" auto-connects to Cloudflare;
  // "powerdns" (default) keeps it on Betazen DNS. Empty follows the panel's
  // global "Default DNS Provider" setting.
  dns_provider?: "cloudflare" | "powerdns" | string;
  // Cloudflare orange-cloud choice for web A records, used only when
  // dns_provider is "cloudflare" and the domain is a primary: "on" = proxied,
  // "off" = DNS-only, "" = follow the system default.
  cf_proxy?: "on" | "off" | string;
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
