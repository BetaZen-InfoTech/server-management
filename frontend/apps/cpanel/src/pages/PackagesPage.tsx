import { useEffect, useMemo, useState } from "react";
import { Button, Card, Modal } from "@serverpanel/ui";
import toast from "react-hot-toast";
import {
  AlertCircle, Box, Check, Clock, CreditCard, HardDrive, Infinity as InfinityIcon,
  Mail, RefreshCw, Wifi, X as XIcon,
} from "lucide-react";
import api from "@/lib/api";

// Vendor-facing package view: browse the catalog, see your pending
// plan-switch request (if any), and submit a new request. Approval is
// done by the platform owner on WHM.

interface HostingPackage {
  id: string;
  name: string;
  disk_quota_mb: number;
  disk_quota_unlimited: boolean;
  bandwidth_mb: number;
  bandwidth_unlimited: boolean;
  max_email_accounts: number;
  max_email_unlimited: boolean;
  max_databases: number;
  max_databases_unlimited: boolean;
  is_default?: boolean;
  description?: string;
}

interface PackageChangeRequest {
  id: string;
  from_package_name: string;
  to_package_id: string;
  to_package_name: string;
  note: string;
  status: "pending" | "approved" | "rejected";
  admin_response?: string;
  created_at: string;
  resolved_at?: string;
}

const fmtQuota = (mb: number, unlimited: boolean) => {
  if (unlimited) return "Unlimited";
  if (mb <= 0) return "—";
  if (mb >= 1024) return `${(mb / 1024).toFixed(mb % 1024 ? 1 : 0)} GB`;
  return `${mb} MB`;
};

const fmtCount = (n: number, unlimited: boolean) =>
  unlimited ? "Unlimited" : n <= 0 ? "—" : n.toString();

export default function PackagesPage() {
  const [loading, setLoading] = useState(true);
  const [packages, setPackages] = useState<HostingPackage[]>([]);
  const [request, setRequest] = useState<PackageChangeRequest | null>(null);
  const [showRequest, setShowRequest] = useState(false);
  const [target, setTarget] = useState<HostingPackage | null>(null);
  const [note, setNote] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const loadAll = async () => {
    setLoading(true);
    try {
      const [pkgRes, reqRes] = await Promise.all([
        api.get("/packages"),
        api.get("/packages/my-request").catch(() => null),
      ]);
      setPackages(pkgRes.data?.data || []);
      setRequest(reqRes?.data?.data || null);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to load packages");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { void loadAll(); }, []);

  const defaultPkg = useMemo(() => packages.find((p) => p.is_default), [packages]);

  const openRequest = (pkg: HostingPackage) => {
    if (request?.status === "pending") {
      toast.error("You already have a pending request");
      return;
    }
    setTarget(pkg);
    setNote("");
    setShowRequest(true);
  };

  const submitRequest = async () => {
    if (!target) return;
    setSubmitting(true);
    try {
      await api.post("/packages/request-change", {
        target_package_id: target.id,
        note: note.trim(),
      });
      toast.success("Request submitted — we\u2019ll confirm once payment is received");
      setShowRequest(false);
      setTarget(null);
      setNote("");
      await loadAll();
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Failed to submit request");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-panel-text flex items-center gap-2">
            <Box size={20} /> My Package
          </h1>
          <p className="text-sm text-panel-muted mt-1">
            View your current plan and request a change.
          </p>
        </div>
        <Button variant="ghost" onClick={loadAll} disabled={loading}>
          <RefreshCw size={16} className={loading ? "animate-spin" : ""} /> Refresh
        </Button>
      </div>

      {request?.status === "pending" && (
        <Card>
          <div className="flex items-start gap-3 p-4">
            <div className="rounded-full bg-yellow-500/10 p-2 border border-yellow-500/20">
              <Clock size={18} className="text-yellow-400" />
            </div>
            <div className="flex-1">
              <div className="text-sm font-medium text-panel-text">
                Plan change request pending
              </div>
              <div className="text-xs text-panel-muted mt-1">
                {request.from_package_name || "Current"} {"→"} <b>{request.to_package_name}</b>
                {"  · "}submitted {new Date(request.created_at).toLocaleString()}
              </div>
              {request.note && (
                <div className="mt-2 text-xs text-panel-muted border-l-2 border-panel-border pl-2">
                  {request.note}
                </div>
              )}
            </div>
          </div>
        </Card>
      )}

      {request?.status === "rejected" && (
        <Card>
          <div className="flex items-start gap-3 p-4">
            <div className="rounded-full bg-red-500/10 p-2 border border-red-500/20">
              <AlertCircle size={18} className="text-red-400" />
            </div>
            <div className="flex-1">
              <div className="text-sm font-medium text-panel-text">
                Last request rejected
              </div>
              <div className="text-xs text-panel-muted mt-1">
                {request.to_package_name}
                {request.admin_response ? ` — ${request.admin_response}` : ""}
              </div>
            </div>
          </div>
        </Card>
      )}

      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
        {loading && (
          <Card><div className="p-6 text-center text-panel-muted">Loading\u2026</div></Card>
        )}
        {!loading && packages.length === 0 && (
          <Card><div className="p-6 text-center text-panel-muted">No packages available</div></Card>
        )}
        {packages.map((pkg) => {
          const isCurrent = defaultPkg?.id === pkg.id;
          const isPendingTarget = request?.status === "pending" && request?.to_package_id === pkg.id;
          return (
            <Card key={pkg.id}>
              <div className="p-5">
                <div className="flex items-center justify-between">
                  <div className="text-lg font-semibold text-panel-text">{pkg.name}</div>
                  {isCurrent && (
                    <span className="text-[10px] uppercase tracking-wider px-2 py-0.5 rounded-full bg-green-500/10 text-green-400 border border-green-500/20">
                      Default
                    </span>
                  )}
                  {isPendingTarget && (
                    <span className="text-[10px] uppercase tracking-wider px-2 py-0.5 rounded-full bg-yellow-500/10 text-yellow-400 border border-yellow-500/20">
                      Requested
                    </span>
                  )}
                </div>
                {pkg.description && (
                  <p className="text-xs text-panel-muted mt-1">{pkg.description}</p>
                )}
                <ul className="mt-4 space-y-2 text-sm text-panel-text">
                  <li className="flex items-center gap-2">
                    <HardDrive size={14} className="text-panel-muted" />
                    <span>Disk: {fmtQuota(pkg.disk_quota_mb, pkg.disk_quota_unlimited)}</span>
                  </li>
                  <li className="flex items-center gap-2">
                    <Wifi size={14} className="text-panel-muted" />
                    <span>Bandwidth: {fmtQuota(pkg.bandwidth_mb, pkg.bandwidth_unlimited)}</span>
                  </li>
                  <li className="flex items-center gap-2">
                    <Mail size={14} className="text-panel-muted" />
                    <span>Email: {fmtCount(pkg.max_email_accounts, pkg.max_email_unlimited)}</span>
                  </li>
                  <li className="flex items-center gap-2">
                    <InfinityIcon size={14} className="text-panel-muted" />
                    <span>Databases: {fmtCount(pkg.max_databases, pkg.max_databases_unlimited)}</span>
                  </li>
                </ul>
                <div className="mt-4">
                  <Button
                    className="w-full"
                    variant={isCurrent ? "ghost" : "primary"}
                    disabled={isCurrent || request?.status === "pending"}
                    onClick={() => openRequest(pkg)}
                  >
                    {isCurrent ? (
                      <>
                        <Check size={16} /> Current Plan
                      </>
                    ) : (
                      <>
                        <CreditCard size={16} /> Request Switch
                      </>
                    )}
                  </Button>
                </div>
              </div>
            </Card>
          );
        })}
      </div>

      <Modal
        isOpen={showRequest}
        onClose={() => { if (!submitting) setShowRequest(false); }}
        title={`Request switch to ${target?.name ?? ""}`}
      >
        <div className="space-y-3 text-sm">
          <p className="text-panel-muted">
            Your request will be reviewed by the platform owner. Payment is
            confirmed externally — once received, the new plan will be
            applied to your account.
          </p>
          <label className="block">
            <span className="text-xs font-medium text-panel-text">
              Note (optional) — include your payment reference or any context
            </span>
            <textarea
              className="mt-1 w-full rounded-lg border border-panel-border bg-panel-bg px-3 py-2 text-sm text-panel-text focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500"
              rows={3}
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="e.g. Bank transfer ref #12345"
            />
          </label>
          <div className="flex justify-end gap-2 pt-2">
            <Button variant="ghost" onClick={() => setShowRequest(false)} disabled={submitting}>
              <XIcon size={16} /> Cancel
            </Button>
            <Button variant="primary" onClick={submitRequest} loading={submitting}>
              <Check size={16} /> Submit Request
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}
