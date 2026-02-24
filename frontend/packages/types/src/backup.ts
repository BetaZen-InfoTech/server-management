export interface Backup {
  id: string;
  type: "full" | "files" | "database" | "email" | "config";
  domain: string;
  user: string;
  storage: "local" | "s3";
  status: "pending" | "in_progress" | "completed" | "failed";
  size_mb: number;
  file_count: number;
  path: string;
  encrypted: boolean;
  created_at: string;
  completed_at: string | null;
}
