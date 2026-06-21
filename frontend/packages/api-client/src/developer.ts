// Developer-page API client — API tokens + outbound webhooks. The same
// module is imported from both the WHM and User Panel apps; the panel
// prefix is selected at call time so vendors hit /api/v1/cpanel/developer/*
// while the platform owner hits /api/v1/whm/developer/*.

import { apiClient } from "./client";

// scope is the panel namespace ("whm" or "cpanel"). Default is "whm" so
// existing callers don't have to change; the User Panel passes "cpanel".
type Scope = "whm" | "cpanel";

const root = (scope: Scope) => `/api/v1/${scope}/developer`;

// API tokens ---------------------------------------------------------------

export const listTokens = (scope: Scope = "whm") => apiClient.get(`${root(scope)}/tokens`);
export const listScopes = (scope: Scope = "whm") => apiClient.get(`${root(scope)}/tokens/scopes`);
export const createToken = (data: unknown, scope: Scope = "whm") =>
  apiClient.post(`${root(scope)}/tokens`, data);
export const rotateToken = (id: string, scope: Scope = "whm") =>
  apiClient.post(`${root(scope)}/tokens/${id}/rotate`);
// 3.1.94 — edit a token's scope set in place. Bearer string stays
// valid; only the authorization grant changes. Backend re-validates
// against the operator's own permissions so an edit can't grant a
// scope the operator doesn't have.
export const updateTokenScopes = (id: string, scopes: string[], scope: Scope = "whm") =>
  apiClient.patch(`${root(scope)}/tokens/${id}/scopes`, { scopes });
export const revokeToken = (id: string, scope: Scope = "whm") =>
  apiClient.delete(`${root(scope)}/tokens/${id}`);

// Webhook endpoints --------------------------------------------------------

export const listWebhooks = (scope: Scope = "whm") => apiClient.get(`${root(scope)}/webhooks`);
export const listWebhookEvents = (scope: Scope = "whm") =>
  apiClient.get(`${root(scope)}/webhooks/events`);
export const createWebhook = (data: unknown, scope: Scope = "whm") =>
  apiClient.post(`${root(scope)}/webhooks`, data);
export const updateWebhook = (id: string, data: unknown, scope: Scope = "whm") =>
  apiClient.patch(`${root(scope)}/webhooks/${id}`, data);
export const deleteWebhook = (id: string, scope: Scope = "whm") =>
  apiClient.delete(`${root(scope)}/webhooks/${id}`);
export const rotateWebhook = (id: string, scope: Scope = "whm") =>
  apiClient.post(`${root(scope)}/webhooks/${id}/rotate`);
export const testWebhook = (id: string, scope: Scope = "whm") =>
  apiClient.post(`${root(scope)}/webhooks/${id}/test`);
export const listDeliveries = (params?: Record<string, unknown>, scope: Scope = "whm") =>
  apiClient.get(`${root(scope)}/webhooks/deliveries`, { params });

// Mailbox list (scope-aware) for the per-mailbox webhook selector. The
// vendor panel hits /api/v1/cpanel/email so visibility stays bound to the
// caller's tenant; the WHM panel hits /api/v1/whm/email and sees every
// mailbox. Returned shape is the standard paginated wrapper.
export const listMailboxesForScope = (scope: Scope = "whm") =>
  apiClient.get(`/api/v1/${scope}/email`, { params: { limit: 500 } });
