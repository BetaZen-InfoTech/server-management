export interface App {
  id: string;
  name: string;
  domain: string;
  app_type: string;
  deploy_method: string;
  user: string;
  port: number;
  status: "running" | "stopped" | "failed" | "deploying";
  pid: number;
  memory_mb: number;
  cpu_percent: number;
  uptime: string;
  git_url: string;
  git_branch: string;
  last_deployed: string | null;
  deployments_count: number;
  created_at: string;
}

export interface DeployAppRequest {
  name: string;
  domain: string;
  app_type: string;
  deploy_method: string;
  user: string;
  port: number;
  git_url?: string;
  git_branch?: string;
  build_cmd?: string;
  start_cmd?: string;
  health_check_path?: string;
  env_vars?: Record<string, string>;
}
