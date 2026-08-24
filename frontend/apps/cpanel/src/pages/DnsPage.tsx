import React, { useState, useEffect, useMemo, useRef } from "react";
import {
  Card,
  Button,
  StatusBadge,
  confirmAction,
  RECORD_TYPES,
  FILTER_TYPES,
  PRIORITY_TYPES,
  RECORD_HELP,
  defaultTTLFor,
  minTTLFor,
  normalizeFqdn,
  validateZoneName,
  BulkTTLModal,
  type BulkTTLResponse,
} from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Globe2,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  Pencil,
  FileText,
  ArrowLeft,
  Save,
  Download,
  AlertTriangle,
  ChevronDown,
  Clock,
} from "lucide-react";

interface DnsRecord {
  id: string;
  type: string;
  name: string;
  value: string;
  ttl: number;
  priority?: number;
  proxy_mode?: string; // "" | "default" | "on" | "off" (per-record override)
}

interface DnsZone {
  id?: string;
  domain: string;
  records_count?: number;
  status?: string;
  updated_at?: string;
  proxy_mode?: string; // "" | "default" | "on" | "off" (per-domain Cloudflare orange-cloud)
}

// Cloudflare can only orange-cloud (proxy) these record types.
const PROXYABLE_TYPES = new Set(["A", "AAAA", "CNAME"]);

interface PendingRow {
  tempId: string;
  type: string;
  name: string;
  ttl: number;
  value: string;
  priority: string;
  origId: string;
  origRecord?: DnsRecord;
  nameError?: string;
}

let nextTempId = 0;
const newTempId = () => `tmp-${++nextTempId}`;

export default function DnsPage() {
  const [zones, setZones] = useState<DnsZone[]>([]);
  const [loading, setLoading] = useState(true);
  const [zoneSearch, setZoneSearch] = useState("");
  // Bulk TTL update — sweeps every zone the calling vendor owns and
  // retunes any record whose type matches the modal's selection.
  // Backend CallerScope automatically restricts to this tenant.
  const [showBulkTTL, setShowBulkTTL] = useState(false);

  const [selectedZone, setSelectedZone] = useState<DnsZone | null>(null);
  const [records, setRecords] = useState<DnsRecord[]>([]);
  const [loadingRecords, setLoadingRecords] = useState(false);
  const [recordSearch, setRecordSearch] = useState("");
  const [typeFilter, setTypeFilter] = useState<string>("all");
  const [pending, setPending] = useState<PendingRow[]>([]);
  const [savingAll, setSavingAll] = useState(false);
  const [showAddDropdown, setShowAddDropdown] = useState(false);
  const addDropdownRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    fetchZones();
  }, []);

  useEffect(() => {
    if (!showAddDropdown) return;
    const handler = (e: MouseEvent) => {
      if (
        addDropdownRef.current &&
        !addDropdownRef.current.contains(e.target as Node)
      ) {
        setShowAddDropdown(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [showAddDropdown]);

  const fetchZones = async () => {
    setLoading(true);
    try {
      const res = await api.get("/dns/zones");
      setZones(res.data.data || []);
    } catch {
      toast.error("Failed to load DNS zones");
    } finally {
      setLoading(false);
    }
  };

  const openZone = async (zone: DnsZone) => {
    setSelectedZone(zone);
    setPending([]);
    setRecordSearch("");
    setTypeFilter("all");
    await fetchRecords(zone.domain);
  };

  const fetchRecords = async (domain: string) => {
    setLoadingRecords(true);
    try {
      const res = await api.get(`/dns/zones/${domain}/records`);
      setRecords(res.data.data || []);
    } catch {
      toast.error("Failed to load DNS records");
      setRecords([]);
    } finally {
      setLoadingRecords(false);
    }
  };

  // Cloudflare orange-cloud overrides (3-level: system → domain → record).
  const changeZoneProxyMode = async (mode: string) => {
    if (!selectedZone) return;
    const prev = selectedZone.proxy_mode || "default";
    setSelectedZone({ ...selectedZone, proxy_mode: mode });
    try {
      await api.post(`/dns/zones/${selectedZone.domain}/proxy-mode`, { mode });
      toast.success("Domain proxy mode updated");
      fetchRecords(selectedZone.domain);
    } catch (err: any) {
      setSelectedZone({ ...selectedZone, proxy_mode: prev });
      toast.error(err?.response?.data?.error?.message || "Failed to update proxy mode");
    }
  };

  const changeRecordProxyMode = async (record: DnsRecord, mode: string) => {
    if (!selectedZone) return;
    setRecords((prevRecs) =>
      prevRecs.map((x) => (x.id === record.id ? { ...x, proxy_mode: mode } : x))
    );
    try {
      await api.post(
        `/dns/zones/${selectedZone.domain}/records/${record.id}/proxy-mode`,
        { mode }
      );
      toast.success("Record proxy mode updated");
    } catch (err: any) {
      setRecords((prevRecs) =>
        prevRecs.map((x) =>
          x.id === record.id ? { ...x, proxy_mode: record.proxy_mode } : x
        )
      );
      toast.error(err?.response?.data?.error?.message || "Failed to update proxy mode");
    }
  };

  const backToZones = () => {
    setSelectedZone(null);
    setPending([]);
    setRecords([]);
    fetchZones();
  };

  const handleExport = () => {
    if (!selectedZone) return;
    window.open(`/api/v1/cpanel/dns/zones/${selectedZone.domain}/export`, "_blank");
  };

  const addPendingRow = (type: string) => {
    setPending((prev) => [
      {
        tempId: newTempId(),
        type,
        name: "",
        ttl: defaultTTLFor(type),
        value: "",
        priority: type === "MX" ? "10" : "",
        origId: "",
      },
      ...prev,
    ]);
    setShowAddDropdown(false);
  };

  const editExistingRecord = (r: DnsRecord) => {
    if (pending.some((p) => p.origId === r.id)) return;
    setPending((prev) => [
      {
        tempId: newTempId(),
        type: r.type,
        name: r.name,
        ttl: r.ttl,
        value: r.value,
        priority: r.priority != null ? String(r.priority) : "",
        origId: r.id,
        origRecord: r,
      },
      ...prev,
    ]);
  };

  const updatePending = (tempId: string, patch: Partial<PendingRow>) => {
    setPending((prev) =>
      prev.map((p) => (p.tempId === tempId ? { ...p, ...patch } : p))
    );
  };

  const removePending = (tempId: string) => {
    setPending((prev) => prev.filter((p) => p.tempId !== tempId));
  };

  const handleNameBlur = (row: PendingRow) => {
    if (!selectedZone) return;
    const normalized = normalizeFqdn(row.name, selectedZone.domain);
    const v = validateZoneName(normalized);
    updatePending(row.tempId, {
      name: normalized,
      nameError: v.ok ? undefined : v.error,
    });
  };

  const buildPayloadFor = (row: PendingRow) => {
    const payload: any = {
      type: row.type,
      name: row.name.trim(),
      value: row.value.trim(),
      ttl: Math.max(minTTLFor(row.type), Number(row.ttl) || defaultTTLFor(row.type)),
    };
    if (PRIORITY_TYPES.has(row.type) && row.priority !== "") {
      payload.priority = parseInt(row.priority, 10);
    }
    return payload;
  };

  const rowValidationError = (row: PendingRow): string | null => {
    if (!row.name.trim()) return "Name is required.";
    const v = validateZoneName(row.name);
    if (!v.ok) return v.error || "Invalid name.";
    if (!row.value.trim()) return "Value is required.";
    return null;
  };

  const saveSingle = async (row: PendingRow) => {
    if (!selectedZone) return;
    const err = rowValidationError(row);
    if (err) {
      toast.error(err);
      return;
    }
    try {
      if (row.origId) {
        await api.put(
          `/dns/zones/${selectedZone.domain}/records/${row.origId}`,
          buildPayloadFor(row)
        );
        toast.success("DNS record updated");
      } else {
        await api.post(
          `/dns/zones/${selectedZone.domain}/records`,
          buildPayloadFor(row)
        );
        toast.success("DNS record added");
      }
      removePending(row.tempId);
      fetchRecords(selectedZone.domain);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to save record");
    }
  };

  const saveAll = async () => {
    if (!selectedZone || pending.length === 0) return;
    let firstError: string | null = null;
    const validated = pending.map((p) => {
      const e = rowValidationError(p);
      if (e && !firstError) firstError = e;
      return { row: p, error: e };
    });
    setPending((prev) =>
      prev.map((p) => {
        const m = validated.find((v) => v.row.tempId === p.tempId);
        return m && m.error ? { ...p, nameError: m.error } : p;
      })
    );
    if (firstError) {
      toast.error(firstError);
      return;
    }

    setSavingAll(true);
    const adds = pending.filter((p) => !p.origId);
    const edits = pending.filter((p) => p.origId);
    let okCount = 0;
    let failCount = 0;
    const stillPending: PendingRow[] = [];

    try {
      if (adds.length > 0) {
        const res = await api.post(
          `/dns/zones/${selectedZone.domain}/records/bulk`,
          { records: adds.map(buildPayloadFor) }
        );
        const data = res.data.data;
        okCount += data.success || 0;
        failCount += data.failed || 0;
        (data.items || []).forEach((it: any, idx: number) => {
          if (!it.success) {
            stillPending.push({ ...adds[idx], nameError: it.error });
          }
        });
      }
      for (const row of edits) {
        try {
          await api.put(
            `/dns/zones/${selectedZone.domain}/records/${row.origId}`,
            buildPayloadFor(row)
          );
          okCount++;
        } catch (err: any) {
          failCount++;
          stillPending.push({
            ...row,
            nameError: err?.response?.data?.error?.message || "Update failed",
          });
        }
      }
      if (failCount === 0) {
        toast.success(`Saved ${okCount} record${okCount === 1 ? "" : "s"}`);
      } else if (okCount === 0) {
        toast.error(`Failed to save ${failCount} record${failCount === 1 ? "" : "s"}`);
      } else {
        toast(`Saved ${okCount}, failed ${failCount}`, { icon: "⚠️" });
      }
      setPending(stillPending);
      fetchRecords(selectedZone.domain);
    } finally {
      setSavingAll(false);
    }
  };

  const handleDeleteRecord = async (record: DnsRecord) => {
    if (!selectedZone) return;
    if (
      !(await confirmAction({
        title: "Delete DNS record?",
        description: `Delete ${record.type} record for ${record.name}?`,
        danger: true,
        confirmLabel: "Delete",
      }))
    )
      return;
    try {
      if (record.id) {
        await api.delete(`/dns/zones/${selectedZone.domain}/records/${record.id}`);
      } else {
        await api.delete(
          `/dns/zones/${selectedZone.domain}/records/_?name=${encodeURIComponent(
            record.name
          )}&type=${encodeURIComponent(record.type)}`
        );
      }
      toast.success("DNS record deleted");
      fetchRecords(selectedZone.domain);
    } catch {
      toast.error("Failed to delete DNS record");
    }
  };

  const editingIds = useMemo(
    () => new Set(pending.filter((p) => p.origId).map((p) => p.origId)),
    [pending]
  );

  const filteredRecords = useMemo(() => {
    const q = recordSearch.trim().toLowerCase();
    return records.filter((r) => {
      if (editingIds.has(r.id)) return false;
      if (typeFilter !== "all" && r.type !== typeFilter) return false;
      if (
        q &&
        !r.name.toLowerCase().includes(q) &&
        !r.value.toLowerCase().includes(q) &&
        !r.type.toLowerCase().includes(q)
      )
        return false;
      return true;
    });
  }, [records, editingIds, typeFilter, recordSearch]);

  const counts = useMemo(() => {
    const acc: Record<string, number> = { all: records.length };
    for (const r of records) acc[r.type] = (acc[r.type] || 0) + 1;
    return acc;
  }, [records]);

  const filteredZones = zones.filter((z) =>
    z.domain.toLowerCase().includes(zoneSearch.toLowerCase())
  );

  // ---------- Zone records detail view ----------
  if (selectedZone) {
    const zone = selectedZone;
    return (
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <button
              onClick={backToZones}
              className="p-2 rounded-lg hover:bg-panel-surface text-panel-muted hover:text-panel-text transition-colors"
              title="Back to zones"
            >
              <ArrowLeft size={18} />
            </button>
            <div className="flex items-center gap-3">
              <div className="p-2 bg-cyan-500/10 border border-cyan-500/20 rounded-lg">
                <Globe2 size={20} className="text-cyan-400" />
              </div>
              <div>
                <h1 className="text-xl font-bold text-panel-text">
                  Zone Records:{" "}
                  <span className="text-cyan-400 font-mono">{zone.domain}</span>
                </h1>
                <p className="text-panel-muted text-sm mt-0.5">
                  {records.length} record{records.length === 1 ? "" : "s"}
                  {pending.length > 0 && (
                    <span className="ml-2 text-yellow-400">
                      · {pending.length} unsaved
                    </span>
                  )}
                </p>
              </div>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <select
              value={zone.proxy_mode || "default"}
              onChange={(e) => changeZoneProxyMode(e.target.value)}
              title="Cloudflare orange-cloud default for every record in this domain (records can override)"
              className="bg-panel-surface border border-panel-border rounded-lg px-2 py-2 text-xs text-panel-text focus:outline-none focus:ring-2 focus:ring-brand-500/40"
            >
              <option value="default">Domain proxy: Default (system)</option>
              <option value="on">Domain proxy: Proxied</option>
              <option value="off">Domain proxy: DNS-only</option>
            </select>
            <Button variant="secondary" size="sm" onClick={() => fetchRecords(zone.domain)}>
              <RefreshCw size={14} className={loadingRecords ? "animate-spin mr-1" : "mr-1"} />
              Refresh
            </Button>
            <Button variant="secondary" size="sm" onClick={handleExport} title="Download zone file">
              <Download size={14} className="mr-1" /> Export Zone
            </Button>
            <button
              onClick={saveAll}
              disabled={pending.length === 0 || savingAll}
              className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm font-medium transition-colors ${
                pending.length > 0
                  ? "bg-brand-600 hover:bg-brand-700 text-white"
                  : "bg-panel-surface border border-panel-border text-panel-muted/50 cursor-not-allowed"
              }`}
            >
              <Save size={14} />
              {savingAll
                ? "Saving..."
                : `Save All Records${pending.length > 0 ? ` (${pending.length})` : ""}`}
            </button>
            <div className="relative" ref={addDropdownRef}>
              <button
                onClick={() => setShowAddDropdown((s) => !s)}
                className="flex items-center gap-1.5 px-3 py-1.5 bg-brand-600 hover:bg-brand-700 text-white rounded-lg text-sm font-medium transition-colors"
              >
                <Plus size={14} /> Add Record <ChevronDown size={12} />
              </button>
              {showAddDropdown && (
                <div className="absolute right-0 top-full mt-1 z-20 bg-panel-surface border border-panel-border rounded-lg shadow-lg w-56 max-h-80 overflow-y-auto">
                  {RECORD_TYPES.map((t) => (
                    <button
                      key={t}
                      onClick={() => addPendingRow(t)}
                      className="w-full text-left px-3 py-1.5 text-sm text-panel-text hover:bg-panel-bg transition-colors"
                    >
                      Add <span className="font-mono">"{t}"</span> Record
                    </button>
                  ))}
                </div>
              )}
            </div>
          </div>
        </div>

        <Card>
          <div className="mb-4 flex flex-col md:flex-row md:items-center gap-3">
            <div className="relative flex-1">
              <Search
                size={16}
                className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted"
              />
              <input
                type="text"
                placeholder="Search by name or value..."
                value={recordSearch}
                onChange={(e) => setRecordSearch(e.target.value)}
                className="w-full pl-9 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
              />
            </div>
            <div className="flex flex-wrap items-center gap-1 bg-panel-bg border border-panel-border rounded-lg p-1">
              <button
                onClick={() => setTypeFilter("all")}
                className={`px-3 py-1.5 rounded-md text-xs font-medium transition-colors ${
                  typeFilter === "all"
                    ? "bg-brand-600 text-white"
                    : "text-panel-muted hover:text-panel-text"
                }`}
              >
                All ({counts.all || 0})
              </button>
              {FILTER_TYPES.map((t) => (
                <button
                  key={t}
                  onClick={() => setTypeFilter(t)}
                  className={`px-3 py-1.5 rounded-md text-xs font-medium transition-colors ${
                    typeFilter === t
                      ? "bg-brand-600 text-white"
                      : "text-panel-muted hover:text-panel-text"
                  }`}
                >
                  {t} ({counts[t] || 0})
                </button>
              ))}
            </div>
          </div>

          {loadingRecords ? (
            <div className="p-8">
              <div className="space-y-3">
                {[1, 2, 3, 4].map((i) => (
                  <div
                    key={i}
                    className="h-10 bg-panel-border/20 rounded animate-pulse"
                  />
                ))}
              </div>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-panel-border bg-panel-bg/40 text-left text-xs uppercase tracking-wider text-panel-muted">
                    <th className="px-4 py-2 font-medium w-32">Type</th>
                    <th className="px-4 py-2 font-medium">Name</th>
                    <th className="px-4 py-2 font-medium w-24">TTL</th>
                    <th className="px-4 py-2 font-medium">Value</th>
                    <th className="px-4 py-2 font-medium w-32">Proxy</th>
                    <th className="px-4 py-2 font-medium w-40 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-panel-border/40">
                  {pending.map((row) => (
                    <PendingRowEditor
                      key={row.tempId}
                      row={row}
                      domain={zone.domain}
                      onChange={(patch) => updatePending(row.tempId, patch)}
                      onNameBlur={() => handleNameBlur(row)}
                      onSave={() => saveSingle(row)}
                      onCancel={() => removePending(row.tempId)}
                    />
                  ))}
                  {filteredRecords.length === 0 && pending.length === 0 ? (
                    <tr>
                      <td colSpan={6} className="text-center py-12 px-4">
                        <FileText
                          size={36}
                          className="text-panel-muted/20 mx-auto mb-3"
                        />
                        <p className="text-panel-muted text-sm">
                          {recordSearch || typeFilter !== "all"
                            ? "No records match your filters."
                            : `No DNS records for ${zone.domain}. Click Add Record to create one.`}
                        </p>
                      </td>
                    </tr>
                  ) : (
                    filteredRecords.map((r) => (
                      <tr key={r.id || `${r.type}-${r.name}-${r.value}`}>
                        <td className="px-4 py-2">
                          <span className="inline-flex items-center px-2 py-0.5 rounded bg-brand-500/10 text-brand-400 border border-brand-500/20 text-xs font-mono font-bold">
                            {r.type}
                          </span>
                        </td>
                        <td className="px-4 py-2 font-mono text-panel-text">{r.name}</td>
                        <td className="px-4 py-2 text-panel-muted">{r.ttl}s</td>
                        <td className="px-4 py-2 font-mono text-panel-muted truncate max-w-[260px]">
                          {r.value}
                          {PRIORITY_TYPES.has(r.type) && r.priority != null && (
                            <span className="ml-2 text-xs text-panel-muted/70">
                              prio {r.priority}
                            </span>
                          )}
                        </td>
                        <td className="px-4 py-2">
                          {PROXYABLE_TYPES.has((r.type || "").toUpperCase()) ? (
                            <select
                              value={r.proxy_mode || "default"}
                              onChange={(e) => changeRecordProxyMode(r, e.target.value)}
                              className="bg-panel-bg border border-panel-border rounded px-2 py-1 text-xs text-panel-text focus:outline-none focus:ring-2 focus:ring-brand-500/40"
                              title="Cloudflare orange-cloud for this record (overrides the domain default)"
                            >
                              <option value="default">Default</option>
                              <option value="on">Proxied</option>
                              <option value="off">DNS-only</option>
                            </select>
                          ) : (
                            <span className="text-xs text-panel-muted/50">—</span>
                          )}
                        </td>
                        <td className="px-4 py-2">
                          <div className="flex items-center justify-end gap-1">
                            <button
                              onClick={() => editExistingRecord(r)}
                              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-brand-400 transition-colors"
                              title="Edit"
                            >
                              <Pencil size={14} />
                            </button>
                            <button
                              onClick={() => handleDeleteRecord(r)}
                              className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
                              title="Delete"
                            >
                              <Trash2 size={14} />
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          )}
        </Card>
      </div>
    );
  }

  // ---------- Zone list view ----------
  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">DNS Zones</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage DNS zones and records for your domains
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="secondary" size="sm" onClick={fetchZones}>
            <RefreshCw size={14} className={loading ? "animate-spin mr-1" : "mr-1"} />
            Refresh
          </Button>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setShowBulkTTL(true)}
            title="Update TTL across all your domains, by record type"
          >
            <Clock size={14} className="mr-1" />
            Bulk TTL update
          </Button>
        </div>
      </div>

      <Card>
        <div className="mb-4">
          <div className="relative max-w-xs">
            <Search
              size={16}
              className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted"
            />
            <input
              type="text"
              placeholder="Search zones..."
              value={zoneSearch}
              onChange={(e) => setZoneSearch(e.target.value)}
              className="w-full pl-9 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
        </div>

        {loading ? (
          <div className="p-8">
            <div className="space-y-3">
              {[1, 2, 3, 4].map((i) => (
                <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          </div>
        ) : filteredZones.length > 0 ? (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-panel-border bg-panel-bg/40 text-left text-xs uppercase tracking-wider text-panel-muted">
                  <th className="px-4 py-2 font-medium">Domain</th>
                  <th className="px-4 py-2 font-medium">Records</th>
                  <th className="px-4 py-2 font-medium">Status</th>
                  <th className="px-4 py-2 font-medium">Last Updated</th>
                  <th className="px-4 py-2 font-medium text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-panel-border/40">
                {filteredZones.map((z) => (
                  <tr key={z.id || z.domain}>
                    <td className="px-4 py-2">
                      <button
                        onClick={() => openZone(z)}
                        className="flex items-center gap-2 hover:text-brand-400 transition-colors"
                      >
                        <Globe2 size={14} className="text-cyan-400" />
                        <span className="font-medium text-panel-text">{z.domain}</span>
                      </button>
                    </td>
                    <td className="px-4 py-2 text-panel-muted">
                      {z.records_count ?? "—"}
                    </td>
                    <td className="px-4 py-2">
                      <StatusBadge status={z.status || "active"} />
                    </td>
                    <td className="px-4 py-2 text-panel-muted">
                      {z.updated_at ? new Date(z.updated_at).toLocaleDateString() : "—"}
                    </td>
                    <td className="px-4 py-2">
                      <div className="flex items-center justify-end gap-1">
                        <button
                          onClick={() => openZone(z)}
                          title="Manage Records"
                          className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-brand-400 transition-colors"
                        >
                          <Pencil size={14} />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="text-center py-16 px-4">
            <Globe2 size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">
              No DNS zones found
            </h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {zoneSearch
                ? "No DNS zones match your search."
                : "Add a domain on the Domains page — a DNS zone is created automatically."}
            </p>
          </div>
        )}
      </Card>

      <BulkTTLModal
        isOpen={showBulkTTL}
        onClose={() => setShowBulkTTL(false)}
        scopeLabel="all your domains"
        submit={async (types, ttl): Promise<BulkTTLResponse> => {
          const res = await api.post("/dns/bulk-ttl", { types, ttl });
          const data = res.data?.data as BulkTTLResponse;
          // The modal only mounts on the zone-list view (the
          // selectedZone branch returns into a different render
          // above), so refreshing the list is the only update needed.
          await fetchZones();
          if (data.total_records_updated > 0) {
            toast.success(
              `Updated ${data.total_records_updated} record${
                data.total_records_updated === 1 ? "" : "s"
              } across ${data.domains_affected} domain${
                data.domains_affected === 1 ? "" : "s"
              }`
            );
          } else {
            toast(`No records matched — searched ${data.domains_considered} zone${data.domains_considered === 1 ? "" : "s"}`);
          }
          return data;
        }}
      />
    </div>
  );
}

function PendingRowEditor({
  row,
  domain,
  onChange,
  onNameBlur,
  onSave,
  onCancel,
}: {
  row: PendingRow;
  domain: string;
  onChange: (patch: Partial<PendingRow>) => void;
  onNameBlur: () => void;
  onSave: () => void;
  onCancel: () => void;
}) {
  const help = RECORD_HELP[row.type] || RECORD_HELP.A;
  const isEdit = !!row.origId;
  return (
    <tr className="bg-brand-500/5">
      <td className="px-4 py-2 align-top">
        <select
          value={row.type}
          onChange={(e) => {
            const newType = e.target.value;
            const patch: Partial<PendingRow> = { type: newType };
            if (!isEdit) {
              patch.ttl = defaultTTLFor(newType);
              if (newType === "MX" && !row.priority) patch.priority = "10";
              if (!PRIORITY_TYPES.has(newType)) patch.priority = "";
            }
            onChange(patch);
          }}
          className="w-full px-2 py-1.5 bg-panel-bg border border-panel-border rounded text-panel-text text-xs font-mono focus:outline-none focus:ring-2 focus:ring-brand-500/40"
        >
          {RECORD_TYPES.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
      </td>
      <td className="px-4 py-2 align-top">
        <input
          type="text"
          value={row.name}
          onChange={(e) =>
            onChange({ name: e.target.value, nameError: undefined })
          }
          onBlur={onNameBlur}
          placeholder={`@ or sub.${domain}`}
          className={`w-full px-2.5 py-1.5 bg-panel-bg border rounded text-panel-text font-mono text-sm focus:outline-none focus:ring-2 focus:ring-brand-500/40 transition-colors ${
            row.nameError ? "border-red-500/60" : "border-panel-border"
          }`}
        />
        {row.nameError && (
          <p className="text-[11px] text-red-400 mt-1 flex items-start gap-1">
            <AlertTriangle size={11} className="mt-0.5 shrink-0" />
            {row.nameError}
          </p>
        )}
      </td>
      <td className="px-4 py-2 align-top">
        <input
          type="number"
          min={minTTLFor(row.type)}
          value={row.ttl}
          onChange={(e) =>
            onChange({ ttl: parseInt(e.target.value, 10) || defaultTTLFor(row.type) })
          }
          className="w-full px-2 py-1.5 bg-panel-bg border border-panel-border rounded text-panel-text text-sm focus:outline-none focus:ring-2 focus:ring-brand-500/40"
        />
      </td>
      <td className="px-4 py-2 align-top">
        <div className="space-y-1">
          {PRIORITY_TYPES.has(row.type) ? (
            <div className="flex gap-1">
              <input
                type="number"
                min={0}
                placeholder={row.type === "MX" ? "10" : "0"}
                value={row.priority}
                onChange={(e) => onChange({ priority: e.target.value })}
                title={row.type === "CAA" ? "Flags" : "Priority"}
                className="w-16 px-2 py-1.5 bg-panel-bg border border-panel-border rounded text-panel-text text-sm font-mono focus:outline-none focus:ring-2 focus:ring-brand-500/40"
              />
              <input
                type="text"
                value={row.value}
                onChange={(e) => onChange({ value: e.target.value })}
                placeholder={help.placeholder}
                className="flex-1 px-2.5 py-1.5 bg-panel-bg border border-panel-border rounded text-panel-text font-mono text-sm focus:outline-none focus:ring-2 focus:ring-brand-500/40"
              />
            </div>
          ) : (
            <input
              type="text"
              value={row.value}
              onChange={(e) => onChange({ value: e.target.value })}
              placeholder={help.placeholder}
              className="w-full px-2.5 py-1.5 bg-panel-bg border border-panel-border rounded text-panel-text font-mono text-sm focus:outline-none focus:ring-2 focus:ring-brand-500/40"
            />
          )}
          <p className="text-[11px] text-panel-muted">{help.hint}</p>
        </div>
      </td>
      <td className="px-4 py-2 align-top">
        <div className="flex items-center justify-end gap-2">
          <button
            onClick={onSave}
            className="inline-flex items-center gap-1 px-2.5 py-1.5 bg-brand-600 hover:bg-brand-700 text-white rounded text-xs font-medium"
          >
            <Save size={11} /> Save Record
          </button>
          <button
            onClick={onCancel}
            className="text-xs text-panel-muted hover:text-panel-text underline"
          >
            Cancel
          </button>
        </div>
      </td>
    </tr>
  );
}
