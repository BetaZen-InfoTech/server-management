export interface WordPress {
  id: string;
  domain: string;
  path: string;
  version: string;
  db_name: string;
  site_url: string;
  admin_url: string;
  multisite: boolean;
  auto_update: boolean;
  debug_mode: boolean;
  maintenance_mode: boolean;
  disk_usage_mb: number;
  created_at: string;
}
