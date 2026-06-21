// DeveloperPanel — shared UI for the Developer page (API tokens, outbound
// webhooks, delivery log). The same component is rendered in both the WHM
// platform-owner SPA and the User Panel vendor SPA; the only difference is
// the `scope` prop, which selects between /api/v1/whm/developer/* and
// /api/v1/cpanel/developer/*. Tenant scoping in the backend keeps the
// vendor view restricted to their own rows automatically.
//
// The plaintext secret returned on token / webhook creation is shown ONCE
// inside a modal with copy + warning UX; we never persist it client-side.
import { useEffect, useMemo, useState } from "react";
import { developerAPI } from "@serverpanel/api-client";
import type {
  ApiToken,
  ApiTokenScope,
  CreateApiTokenRequest,
  IssuedApiToken,
  WebhookEndpoint,
  WebhookEvent,
  WebhookDelivery,
  IssuedWebhook,
} from "@serverpanel/types";
import { Card } from "./Card";
import { Button } from "./Button";
import { Modal } from "./Modal";
import { StatusBadge } from "./StatusBadge";
import { confirmAction } from "./ConfirmDialog";
import { copyToClipboard } from "./clipboard";

type Scope = "whm" | "cpanel";

export interface DeveloperPanelProps {
  scope: Scope;
}

const inputCls =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelCls = "block text-sm font-medium text-panel-text mb-1";

function formatRel(iso?: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

export function DeveloperPanel({ scope }: DeveloperPanelProps) {
  const [tab, setTab] = useState<"tokens" | "webhooks" | "deliveries">("tokens");

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-panel-text">Developer</h1>
        <p className="text-sm text-panel-muted mt-1">
          API tokens for programmatic access and outbound webhooks for event
          notifications. Tokens authorise calls to <code>/api/v1/external/*</code>;
          webhooks fire on resource events you subscribe to.
        </p>
      </div>

      <div className="border-b border-panel-border">
        {(["tokens", "webhooks", "deliveries"] as const).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`px-4 py-2 text-sm font-medium border-b-2 -mb-px transition-colors ${
              tab === t
                ? "border-blue-500 text-blue-400"
                : "border-transparent text-panel-muted hover:text-panel-text"
            }`}
          >
            {t === "tokens" ? "API Tokens" : t === "webhooks" ? "Webhooks" : "Delivery Log"}
          </button>
        ))}
      </div>

      {tab === "tokens" && <TokensTab scope={scope} />}
      {tab === "webhooks" && <WebhooksTab scope={scope} />}
      {tab === "deliveries" && <DeliveriesTab scope={scope} />}
    </div>
  );
}

// ---------- Tokens tab ----------

function TokensTab({ scope }: { scope: Scope }) {
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [scopes, setScopes] = useState<ApiTokenScope[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreate, setShowCreate] = useState(false);
  const [issued, setIssued] = useState<IssuedApiToken | null>(null);
  // 3.1.94 — Edit-scopes flow. `editing` holds the token whose
  // scopes the operator is editing; null means the modal is closed.
  const [editing, setEditing] = useState<ApiToken | null>(null);

  const load = async () => {
    setLoading(true);
    try {
      const [t, s] = await Promise.all([
        developerAPI.listTokens(scope),
        developerAPI.listScopes(scope),
      ]);
      setTokens(t.data?.data || []);
      setScopes(s.data?.data || []);
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    load();
  }, [scope]);

  const onRevoke = async (id: string) => {
    if (!(await confirmAction({
      title: "Revoke token?",
      body: "This stops the token from working immediately. Integrations using it will need a fresh one.",
      confirmLabel: "Revoke",
      danger: true,
    }))) return;
    await developerAPI.revokeToken(id, scope);
    await load();
  };

  const onRotate = async (id: string) => {
    if (!(await confirmAction({
      title: "Rotate token?",
      body: "This mints a new secret for the same token id. The old secret stops working immediately.",
      confirmLabel: "Rotate",
    }))) return;
    const r = await developerAPI.rotateToken(id, scope);
    setIssued(r.data?.data || null);
    await load();
  };

  return (
    <div className="space-y-4">
      <div className="flex justify-between items-center">
        <p className="text-sm text-panel-muted">
          Tokens look like <code>btz_prod_xxxx_…</code>. The secret half is shown ONCE on
          creation — store it before dismissing the dialog.
        </p>
        <Button onClick={() => setShowCreate(true)}>+ Create Token</Button>
      </div>
      <Card>
        <table className="w-full text-sm">
          <thead className="text-left text-panel-muted">
            <tr>
              <th className="py-2 px-3">Name</th>
              <th className="px-3">Prefix</th>
              <th className="px-3">Owner</th>
              <th className="px-3">Scopes</th>
              <th className="px-3">Status</th>
              <th className="px-3">Expires</th>
              <th className="px-3">Last used</th>
              <th className="px-3">Actions</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr><td colSpan={8} className="py-8 text-center text-panel-muted">Loading…</td></tr>
            ) : tokens.length === 0 ? (
              <tr><td colSpan={8} className="py-8 text-center text-panel-muted">No tokens yet.</td></tr>
            ) : tokens.map((t) => (
              <tr key={t.id} className="border-t border-panel-border">
                <td className="py-2 px-3 font-medium">{t.name}</td>
                <td className="px-3 font-mono text-xs">{t.prefix}…</td>
                <td className="px-3">
                  {/* 3.1.105 — Owner column. Platform-admin tokens
                      render as a muted "Platform admin" label so
                      vendors looking at WHM rows don't see admin's
                      account name. Vendor-owned tokens show the
                      vendor's display name + username (subtitle).
                      Tokens whose owner was deleted render "—". */}
                  {t.owner_name === "Platform admin" ? (
                    <span className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded bg-blue-500/10 text-blue-300 border border-blue-500/20">
                      Platform admin
                    </span>
                  ) : t.owner_name ? (
                    <div className="flex flex-col">
                      <span className="text-panel-text">{t.owner_name}</span>
                      {t.owner_username && <span className="text-[10px] text-panel-muted/70 font-mono">@{t.owner_username}</span>}
                    </div>
                  ) : (
                    <span className="text-panel-muted/50">—</span>
                  )}
                </td>
                <td className="px-3">
                  <div className="flex flex-wrap gap-1">
                    {t.scopes.map((s) => (
                      <span key={s} className="text-xs px-2 py-0.5 rounded bg-panel-bg border border-panel-border">{s}</span>
                    ))}
                  </div>
                </td>
                <td className="px-3"><StatusBadge status={t.status} /></td>
                <td className="px-3 text-panel-muted">{t.expires_at ? formatRel(t.expires_at) : "Never"}</td>
                <td className="px-3 text-panel-muted">{formatRel(t.last_used_at)}</td>
                <td className="px-3">
                  <div className="flex gap-2">
                    {t.status === "active" && (
                      <button onClick={() => setEditing(t)} className="text-xs text-amber-400 hover:underline" title="Change which scopes this token can use — bearer string stays valid">Edit</button>
                    )}
                    <button onClick={() => onRotate(t.id)} className="text-xs text-blue-400 hover:underline">Rotate</button>
                    {t.status === "active" && (
                      <button onClick={() => onRevoke(t.id)} className="text-xs text-red-400 hover:underline">Revoke</button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>

      <CreateTokenModal
        open={showCreate}
        onClose={() => setShowCreate(false)}
        scope={scope}
        scopes={scopes}
        onIssued={(i) => {
          setIssued(i);
          setShowCreate(false);
          load();
        }}
      />
      <EditScopesModal
        token={editing}
        scope={scope}
        scopes={scopes}
        onClose={() => setEditing(null)}
        onSaved={() => { setEditing(null); load(); }}
      />
      <RevealSecretModal issued={issued} onClose={() => setIssued(null)} />
    </div>
  );
}

function CreateTokenModal({
  open,
  onClose,
  scope,
  scopes,
  onIssued,
}: {
  open: boolean;
  onClose: () => void;
  scope: Scope;
  scopes: ApiTokenScope[];
  onIssued: (i: IssuedApiToken) => void;
}) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [picked, setPicked] = useState<string[]>([]);
  const [expiresInDays, setExpiresInDays] = useState<number>(0);
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState("");

  const grouped = useMemo(() => {
    const groups: Record<string, ApiTokenScope[]> = {};
    scopes.forEach((s) => {
      groups[s.group] = groups[s.group] || [];
      groups[s.group].push(s);
    });
    return groups;
  }, [scopes]);

  const reset = () => {
    setName("");
    setDescription("");
    setPicked([]);
    setExpiresInDays(0);
    setErr("");
  };

  const submit = async () => {
    setErr("");
    if (!name.trim()) {
      setErr("Name is required.");
      return;
    }
    if (picked.length === 0) {
      setErr("Pick at least one scope.");
      return;
    }
    setSubmitting(true);
    try {
      const body: CreateApiTokenRequest = {
        name: name.trim(),
        description: description.trim() || undefined,
        scopes: picked,
        expires_in_days: expiresInDays || 0,
      };
      const r = await developerAPI.createToken(body, scope);
      const issued = r.data?.data as IssuedApiToken | undefined;
      if (issued) {
        onIssued(issued);
        reset();
      }
    } catch (e: any) {
      setErr(e?.response?.data?.error?.message || "Failed to create token");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal isOpen={open} onClose={onClose} title="Create API Token">
      <div className="space-y-4">
        <div>
          <label className={labelCls}>Name</label>
          <input className={inputCls} value={name} onChange={(e) => setName(e.target.value)} placeholder="Deploy automation" />
        </div>
        <div>
          <label className={labelCls}>Description (optional)</label>
          <input className={inputCls} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="What is this token used for?" />
        </div>
        <div>
          <label className={labelCls}>Expiration</label>
          <select className={inputCls} value={expiresInDays} onChange={(e) => setExpiresInDays(parseInt(e.target.value, 10))}>
            <option value={0}>Never (until disabled)</option>
            <option value={7}>7 days</option>
            <option value={30}>30 days</option>
            <option value={90}>90 days</option>
            <option value={365}>1 year</option>
          </select>
        </div>
        <div>
          <label className={labelCls}>Scopes</label>
          <div className="space-y-3 max-h-72 overflow-y-auto pr-2">
            {Object.entries(grouped).map(([group, items]) => (
              <div key={group}>
                <div className="text-xs uppercase tracking-wider text-panel-muted mb-1">{group}</div>
                <div className="space-y-1">
                  {items.map((s) => (
                    <label key={s.key} className="flex items-start gap-2 text-sm cursor-pointer">
                      <input
                        type="checkbox"
                        checked={picked.includes(s.key)}
                        onChange={(e) => {
                          if (e.target.checked) setPicked([...picked, s.key]);
                          else setPicked(picked.filter((p) => p !== s.key));
                        }}
                      />
                      <div>
                        <div className="font-medium">{s.label} <span className="text-xs text-panel-muted">({s.key})</span></div>
                        <div className="text-xs text-panel-muted">{s.description}</div>
                      </div>
                    </label>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>
        {err && <div className="text-sm text-red-400">{err}</div>}
        <div className="flex justify-end gap-2 pt-2">
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={submit} disabled={submitting}>{submitting ? "Creating…" : "Create token"}</Button>
        </div>
      </div>
    </Modal>
  );
}

// 3.1.94 — EditScopesModal. Same scope-picker UX as CreateTokenModal
// but seeded with the token's current scopes and posting to the
// PATCH endpoint instead of POST. Bearer string stays valid; only
// the authorization grant changes. Survives server-to-server
// migration automatically — the transfer pipeline's syncAPITokens
// copies the whole api_tokens doc through normaliseDoc so the
// edited scope list rides along to the destination.
function EditScopesModal({
  token,
  scope,
  scopes,
  onClose,
  onSaved,
}: {
  token: ApiToken | null;
  scope: Scope;
  scopes: ApiTokenScope[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [picked, setPicked] = useState<string[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState("");

  // Re-seed the picker every time a new token opens — the operator
  // expects to see the current scope set, not whatever was last
  // edited.
  useEffect(() => {
    if (token) {
      setPicked([...token.scopes]);
      setErr("");
    }
  }, [token]);

  const grouped = useMemo(() => {
    const groups: Record<string, ApiTokenScope[]> = {};
    scopes.forEach((s) => {
      groups[s.group] = groups[s.group] || [];
      groups[s.group].push(s);
    });
    return groups;
  }, [scopes]);

  // Detect whether the picker matches the token's stored scopes —
  // Save is disabled until the operator changes something so a
  // misclick doesn't trigger a pointless round-trip + audit row.
  const dirty = useMemo(() => {
    if (!token) return false;
    if (picked.length !== token.scopes.length) return true;
    const a = [...picked].sort();
    const b = [...token.scopes].sort();
    return a.some((v, i) => v !== b[i]);
  }, [picked, token]);

  const submit = async () => {
    if (!token) return;
    setErr("");
    if (picked.length === 0) {
      setErr("Pick at least one scope.");
      return;
    }
    setSubmitting(true);
    try {
      await developerAPI.updateTokenScopes(token.id, picked, scope);
      onSaved();
    } catch (e: any) {
      setErr(e?.response?.data?.error?.message || "Failed to update scopes");
    } finally {
      setSubmitting(false);
    }
  };

  if (!token) return null;
  return (
    <Modal isOpen={true} onClose={onClose} title={`Edit scopes — ${token.name}`}>
      <div className="space-y-4">
        <div className="text-xs text-panel-muted">
          The token's bearer string stays valid — only its authorization
          changes. Take effect immediately for new requests; in-flight
          requests on the old grant complete normally.
        </div>
        <div>
          <label className={labelCls}>Scopes</label>
          <div className="space-y-3 max-h-72 overflow-y-auto pr-2">
            {Object.entries(grouped).map(([group, items]) => (
              <div key={group}>
                <div className="text-xs uppercase tracking-wider text-panel-muted mb-1">{group}</div>
                <div className="space-y-1">
                  {items.map((s) => (
                    <label key={s.key} className="flex items-start gap-2 text-sm cursor-pointer">
                      <input
                        type="checkbox"
                        checked={picked.includes(s.key)}
                        onChange={(e) => {
                          if (e.target.checked) setPicked([...picked, s.key]);
                          else setPicked(picked.filter((p) => p !== s.key));
                        }}
                      />
                      <div>
                        <div className="font-medium">{s.label} <span className="text-xs text-panel-muted">({s.key})</span></div>
                        <div className="text-xs text-panel-muted">{s.description}</div>
                      </div>
                    </label>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>
        {err && <div className="text-sm text-red-400">{err}</div>}
        <div className="flex justify-end gap-2 pt-2">
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={submit} disabled={submitting || !dirty}>
            {submitting ? "Saving…" : dirty ? "Save scopes" : "No changes"}
          </Button>
        </div>
      </div>
    </Modal>
  );
}

function RevealSecretModal({ issued, onClose }: { issued: IssuedApiToken | null; onClose: () => void }) {
  if (!issued) return null;
  return (
    <Modal isOpen onClose={onClose} title="Save your token now">
      <div className="space-y-4">
        <div className="text-sm text-amber-400 bg-amber-500/10 border border-amber-500/30 rounded-lg px-3 py-2">
          This is the only time the full token will be shown. Copy it and store it
          somewhere safe. We do not keep a copy and cannot recover it.
        </div>
        <div className="bg-panel-bg border border-panel-border rounded-lg p-3 font-mono text-xs break-all select-all">
          {issued.plaintext_token}
        </div>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => copyToClipboard(issued.plaintext_token)}>Copy</Button>
          <Button onClick={onClose}>I have saved it</Button>
        </div>
      </div>
    </Modal>
  );
}

// ---------- Webhooks tab ----------

interface MailboxLite {
  id: string;
  email: string;
  domain: string;
}

function WebhooksTab({ scope }: { scope: Scope }) {
  const [items, setItems] = useState<WebhookEndpoint[]>([]);
  const [events, setEvents] = useState<WebhookEvent[]>([]);
  const [mailboxes, setMailboxes] = useState<MailboxLite[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreate, setShowCreate] = useState(false);
  const [issued, setIssued] = useState<IssuedWebhook | null>(null);

  const load = async () => {
    setLoading(true);
    try {
      const [w, e, m] = await Promise.all([
        developerAPI.listWebhooks(scope),
        developerAPI.listWebhookEvents(scope),
        developerAPI.listMailboxesForScope(scope).catch(() => null),
      ]);
      setItems(w.data?.data || []);
      setEvents(e.data?.data || []);
      const mboxes = m?.data?.data || [];
      setMailboxes(
        Array.isArray(mboxes)
          ? mboxes.map((mb: any) => ({ id: mb.id, email: mb.email, domain: mb.domain }))
          : []
      );
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    load();
  }, [scope]);

  const onDelete = async (id: string) => {
    if (!(await confirmAction({
      title: "Delete webhook?",
      body: "Future events will not be delivered to this URL.",
      confirmLabel: "Delete",
      danger: true,
    }))) return;
    await developerAPI.deleteWebhook(id, scope);
    await load();
  };

  const onRotate = async (id: string) => {
    if (!(await confirmAction({
      title: "Rotate signing secret?",
      body: "Your receiver will need the new secret to verify signatures. Old signatures stop working immediately.",
      confirmLabel: "Rotate",
    }))) return;
    const r = await developerAPI.rotateWebhook(id, scope);
    setIssued(r.data?.data || null);
    await load();
  };

  const onTest = async (id: string) => {
    await developerAPI.testWebhook(id, scope);
  };

  const onToggle = async (id: string, active: boolean) => {
    await developerAPI.updateWebhook(id, { active }, scope);
    await load();
  };

  return (
    <div className="space-y-4">
      <div className="flex justify-between items-center">
        <p className="text-sm text-panel-muted">
          Outbound webhooks fire to a URL you control on subscribed events. Each has its own
          HMAC-SHA256 signing secret — verify <code>X-Betazen-Signature</code> on receive.
          Bind a webhook to a single mailbox to receive <code>email.message.received</code>
          notifications for inbound mail (metadata only — fetch the body via IMAP/POP3).
        </p>
        <Button onClick={() => setShowCreate(true)}>+ Create Webhook</Button>
      </div>
      <Card>
        <table className="w-full text-sm">
          <thead className="text-left text-panel-muted">
            <tr>
              <th className="py-2 px-3">URL</th>
              <th className="px-3">Scope</th>
              <th className="px-3">Events</th>
              <th className="px-3">Status</th>
              <th className="px-3">Last result</th>
              <th className="px-3">Actions</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr><td colSpan={6} className="py-8 text-center text-panel-muted">Loading…</td></tr>
            ) : items.length === 0 ? (
              <tr><td colSpan={6} className="py-8 text-center text-panel-muted">No webhooks yet.</td></tr>
            ) : items.map((w) => (
              <tr key={w.id} className="border-t border-panel-border">
                <td className="py-2 px-3 font-mono text-xs break-all">{w.url}</td>
                <td className="px-3 text-xs">
                  {w.mailbox_email ? (
                    <span className="px-2 py-0.5 rounded bg-blue-500/10 border border-blue-500/30 text-blue-300 font-mono">
                      {w.mailbox_email}
                    </span>
                  ) : (
                    <span className="text-panel-muted">All mailboxes</span>
                  )}
                </td>
                <td className="px-3">
                  <div className="flex flex-wrap gap-1">
                    {w.events.slice(0, 3).map((e) => (
                      <span key={e} className="text-xs px-2 py-0.5 rounded bg-panel-bg border border-panel-border">{e}</span>
                    ))}
                    {w.events.length > 3 && <span className="text-xs text-panel-muted">+{w.events.length - 3}</span>}
                  </div>
                </td>
                <td className="px-3">
                  <button onClick={() => onToggle(w.id, !w.active)} className="text-xs">
                    <StatusBadge status={w.active ? "active" : "paused"} />
                  </button>
                </td>
                <td className="px-3 text-panel-muted text-xs">
                  {w.last_success_at ? `Success ${formatRel(w.last_success_at)}` :
                    w.last_failure_at ? `Failed ${formatRel(w.last_failure_at)}` : "—"}
                  {w.last_error && <div className="text-red-400 truncate max-w-xs">{w.last_error}</div>}
                </td>
                <td className="px-3">
                  <div className="flex gap-2">
                    <button onClick={() => onTest(w.id)} className="text-xs text-blue-400 hover:underline">Test</button>
                    <button onClick={() => onRotate(w.id)} className="text-xs text-blue-400 hover:underline">Rotate</button>
                    <button onClick={() => onDelete(w.id)} className="text-xs text-red-400 hover:underline">Delete</button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>

      <CreateWebhookModal
        open={showCreate}
        onClose={() => setShowCreate(false)}
        scope={scope}
        events={events}
        mailboxes={mailboxes}
        onIssued={(i) => {
          setIssued(i);
          setShowCreate(false);
          load();
        }}
      />
      <RevealWebhookSecretModal issued={issued} onClose={() => setIssued(null)} />
    </div>
  );
}

function CreateWebhookModal({
  open,
  onClose,
  scope,
  events,
  mailboxes,
  onIssued,
}: {
  open: boolean;
  onClose: () => void;
  scope: Scope;
  events: WebhookEvent[];
  mailboxes: MailboxLite[];
  onIssued: (i: IssuedWebhook) => void;
}) {
  const [url, setUrl] = useState("");
  const [description, setDescription] = useState("");
  const [picked, setPicked] = useState<string[]>([]);
  const [mailboxEmail, setMailboxEmail] = useState<string>("");
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState("");

  const grouped = useMemo(() => {
    const groups: Record<string, WebhookEvent[]> = {};
    events.forEach((e) => {
      groups[e.group] = groups[e.group] || [];
      groups[e.group].push(e);
    });
    return groups;
  }, [events]);

  const submit = async () => {
    setErr("");
    if (!url.trim()) {
      setErr("URL is required.");
      return;
    }
    if (picked.length === 0) {
      setErr("Pick at least one event.");
      return;
    }
    setSubmitting(true);
    try {
      const r = await developerAPI.createWebhook(
        {
          url: url.trim(),
          description: description.trim() || undefined,
          events: picked,
          mailbox_email: mailboxEmail || undefined,
        },
        scope
      );
      const issued = r.data?.data as IssuedWebhook | undefined;
      if (issued) onIssued(issued);
    } catch (e: any) {
      setErr(e?.response?.data?.error?.message || "Failed to create webhook");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal isOpen={open} onClose={onClose} title="Create Webhook">
      <div className="space-y-4">
        <div>
          <label className={labelCls}>URL</label>
          <input className={inputCls} value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://example.com/hooks/betazen" />
        </div>
        <div>
          <label className={labelCls}>Description (optional)</label>
          <input className={inputCls} value={description} onChange={(e) => setDescription(e.target.value)} />
        </div>
        <div>
          <label className={labelCls}>Bind to mailbox (optional)</label>
          <select className={inputCls} value={mailboxEmail} onChange={(e) => setMailboxEmail(e.target.value)}>
            <option value="">All mailboxes (tenant-wide)</option>
            {mailboxes.map((m) => (
              <option key={m.id} value={m.email}>{m.email}</option>
            ))}
          </select>
          <p className="text-xs text-panel-muted mt-1">
            Pin this endpoint to a single mailbox so it only fires for that address.
            Leave on "All mailboxes" for tenant-wide delivery. Required if you only
            want <code>email.message.received</code> for one inbox.
          </p>
        </div>
        <div>
          <label className={labelCls}>Events</label>
          <div className="space-y-3 max-h-72 overflow-y-auto pr-2">
            {Object.entries(grouped).map(([group, items]) => (
              <div key={group}>
                <div className="text-xs uppercase tracking-wider text-panel-muted mb-1">{group}</div>
                <div className="space-y-1">
                  {items.map((e) => (
                    <label key={e.key} className="flex items-start gap-2 text-sm cursor-pointer">
                      <input
                        type="checkbox"
                        checked={picked.includes(e.key)}
                        onChange={(ev) => {
                          if (ev.target.checked) setPicked([...picked, e.key]);
                          else setPicked(picked.filter((p) => p !== e.key));
                        }}
                      />
                      <div>
                        <div className="font-medium">{e.label} <span className="text-xs text-panel-muted">({e.key})</span></div>
                        <div className="text-xs text-panel-muted">{e.description}</div>
                      </div>
                    </label>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>
        {err && <div className="text-sm text-red-400">{err}</div>}
        <div className="flex justify-end gap-2 pt-2">
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={submit} disabled={submitting}>{submitting ? "Creating…" : "Create webhook"}</Button>
        </div>
      </div>
    </Modal>
  );
}

function RevealWebhookSecretModal({ issued, onClose }: { issued: IssuedWebhook | null; onClose: () => void }) {
  if (!issued) return null;
  return (
    <Modal isOpen onClose={onClose} title="Save your webhook signing secret">
      <div className="space-y-4">
        <div className="text-sm text-amber-400 bg-amber-500/10 border border-amber-500/30 rounded-lg px-3 py-2">
          This is the only time the secret will be shown. Use it on your receiver to verify
          the <code>X-Betazen-Signature</code> header on each delivery.
        </div>
        <div className="bg-panel-bg border border-panel-border rounded-lg p-3 font-mono text-xs break-all select-all">
          {issued.plaintext_secret}
        </div>
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => copyToClipboard(issued.plaintext_secret)}>Copy</Button>
          <Button onClick={onClose}>I have saved it</Button>
        </div>
      </div>
    </Modal>
  );
}

// ---------- Deliveries tab ----------

function DeliveriesTab({ scope }: { scope: Scope }) {
  const [rows, setRows] = useState<WebhookDelivery[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let mounted = true;
    (async () => {
      setLoading(true);
      try {
        const r = await developerAPI.listDeliveries({ limit: 200 }, scope);
        if (mounted) setRows(r.data?.data || []);
      } finally {
        if (mounted) setLoading(false);
      }
    })();
    return () => {
      mounted = false;
    };
  }, [scope]);

  return (
    <Card>
      <table className="w-full text-sm">
        <thead className="text-left text-panel-muted">
          <tr>
            <th className="py-2 px-3">When</th>
            <th className="px-3">Event</th>
            <th className="px-3">Status</th>
            <th className="px-3">Attempts</th>
            <th className="px-3">Error</th>
          </tr>
        </thead>
        <tbody>
          {loading ? (
            <tr><td colSpan={5} className="py-8 text-center text-panel-muted">Loading…</td></tr>
          ) : rows.length === 0 ? (
            <tr><td colSpan={5} className="py-8 text-center text-panel-muted">No deliveries yet.</td></tr>
          ) : rows.map((d) => (
            <tr key={d.id} className="border-t border-panel-border">
              <td className="py-2 px-3 text-panel-muted">{formatRel(d.created_at)}</td>
              <td className="px-3 font-mono text-xs">{d.event}</td>
              <td className="px-3">
                {d.success ? <StatusBadge status="success" /> : d.attempts > 0 ? <StatusBadge status="failed" /> : <StatusBadge status="pending" />}
                {d.status_code ? <span className="ml-2 text-xs text-panel-muted">HTTP {d.status_code}</span> : null}
              </td>
              <td className="px-3 text-panel-muted">{d.attempts}</td>
              <td className="px-3 text-red-400 text-xs truncate max-w-md">{d.error || "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Card>
  );
}
