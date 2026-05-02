// Developer API types — API tokens (programmatic auth) and outbound webhook
// subscriptions. Mirrors the backend models in
// backend/internal/models/api_token.go and webhook_endpoint.go; keep field
// names in sync if you change either side.

export type ApiTokenStatus = "active" | "expired" | "revoked";

export type ApiTokenOwnerKind = "owner" | "vendor";

export interface ApiToken {
  id: string;
  token_id: string;
  prefix: string;
  name: string;
  description?: string;
  owner_kind: ApiTokenOwnerKind;
  owner_user_id: string;
  tenant_id: string;
  pinned_vendor_id?: string;
  scopes: string[];
  ip_allowlist?: string[];
  expires_at?: string | null;
  status: ApiTokenStatus;
  last_used_at?: string | null;
  last_used_ip?: string;
  usage_count: number;
  created_at: string;
  updated_at: string;
  revoked_at?: string | null;
}

export interface ApiTokenScope {
  key: string;
  label: string;
  description: string;
  group: string;
}

export interface CreateApiTokenRequest {
  name: string;
  description?: string;
  scopes: string[];
  expires_in_days?: number;
  ip_allowlist?: string[];
  pinned_vendor_id?: string;
}

// IssuedApiToken is the response shape returned ONCE on token creation. The
// plaintext_token half is shown to the operator and never persisted; the
// token field carries the safe-to-display row.
export interface IssuedApiToken {
  token: ApiToken;
  plaintext_token: string;
}

export interface WebhookEndpoint {
  id: string;
  url: string;
  description?: string;
  owner_kind: ApiTokenOwnerKind;
  owner_user_id: string;
  tenant_id: string;
  /** Optional per-mailbox scope. When set, this endpoint only fires for
   * events whose payload concerns this address. Empty/undefined = tenant-
   * wide. Pair with the `email.message.received` event to get a webhook
   * fired once per delivered message for one mailbox. */
  mailbox_email?: string;
  events: string[];
  prefix: string;
  active: boolean;
  last_success_at?: string | null;
  last_failure_at?: string | null;
  last_error?: string;
  created_at: string;
  updated_at: string;
}

export interface WebhookEvent {
  key: string;
  label: string;
  description: string;
  group: string;
}

export interface CreateWebhookRequest {
  url: string;
  description?: string;
  events: string[];
  active?: boolean;
  /** Bind this endpoint to a single mailbox. Address must already exist on
   * the panel and be in the caller's scope. Empty/omitted = tenant-wide. */
  mailbox_email?: string;
}

export interface IssuedWebhook {
  webhook: WebhookEndpoint;
  plaintext_secret: string;
}

export interface WebhookDelivery {
  id: string;
  endpoint_id: string;
  event: string;
  payload: string;
  signature: string;
  timestamp: number;
  status_code?: number;
  attempts: number;
  success: boolean;
  error?: string;
  next_retry_at?: string | null;
  delivered_at?: string | null;
  created_at: string;
}
