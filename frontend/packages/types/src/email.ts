export interface Mailbox {
  id: string;
  email: string;
  domain: string;
  quota_mb: number;
  used_mb: number;
  send_limit_per_hour: number;
  created_at: string;
}

export interface CreateMailboxRequest {
  email: string;
  password: string;
  domain: string;
  quota_mb?: number;
  send_limit_per_hour?: number;
}
