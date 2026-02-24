export interface GitHubDeploy {
  id: string;
  domain: string;
  repo: string;
  branch: string;
  app_type: string;
  auto_deploy: boolean;
  status: "queued" | "cloning" | "building" | "deploying" | "live" | "failed" | "cancelled";
  current_commit: string;
  commit_message: string;
  commit_author: string;
  deploy_url: string;
  paused: boolean;
  created_at: string;
}

export interface DeployRelease {
  id: string;
  commit: string;
  commit_message: string;
  author: string;
  branch: string;
  status: string;
  trigger: string;
  duration_seconds: number;
  deployed_at: string;
}
