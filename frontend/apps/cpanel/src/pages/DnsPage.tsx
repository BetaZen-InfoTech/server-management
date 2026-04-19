import React, { useEffect, useState } from "react";
import { Card, Button, Table, Modal, StatusBadge, confirmAction } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Globe2,
  Plus,
  Trash2,
  Pencil,
  Search,
  RefreshCw,
  ArrowLeft,
  Download,
  FileText,
} from "lucide-react";

interface DnsRecord {
  id: string;
  type: string;
  name: string;
  value: string;
  ttl: number;
  priority?: number;
}

interface DnsZone {
  id?: string;
  domain: string;
  records_count?: number;
  status?: string;
  updated_at?: string;
  // Legacy shape: some backends return records inline on /dns/zones. We
  // handle both — if `records` is populated we pre-seed the detail view,
  // otherwise we fetch from /dns/zones/:domain/records on open.
  records?: DnsRecord[];
}

// Record types supported by the backend + common DNS types. Mirrors the WHM
// page (A, AAAA, CNAME, MX, TXT, NS, CAA) plus SRV, PTR, and SPF to give
// vendors parity with every record cPanel has historically exposed.
const RECORD_TYPES = [
  "A",
  "AAAA",
  "CNAME",
  "MX",
  "TXT",
  "SRV",
  "NS",
  "CAA",
  "PTR",
];

// Copy-level guidance for each record type so vendors aren't guessing at
// what to type in the Value field. Matches WHM's inline hints.
const RECORD_HELP: Record<string, { value: string; example: string; name?: string }> = {
  A: {
    value: "IPv4 address the hostname should resolve to.",
    example: "192.168.1.50",
    name: "Use @ for the root domain, or a subdomain like www.",
  },
  AAAA: {
    value: "IPv6 address the hostname should resolve to.",
    example: "2001:db8::1",
  },
  CNAME: {
    value: "Target hostname. CNAMEs must not coexist with other records on the same name.",
    example: "target.example.com.",
  },
  MX: {
    value: "Mail server hostname. Lower Priority = preferred. Always end with a dot.",
    example: "mail.example.com.",
  },
  TXT: {
    value: "Free-form text. Used for SPF, DKIM, verification, etc.",
    example: "v=spf1 include:_spf.google.com ~all",
  },
  SRV: {
    value: "Service location: priority weight port target.",
    example: "10 60 5060 sipserver.example.com.",
    name: "Format: _service._proto.name (e.g. _sip._tcp)",
  },
  NS: {
    value: "Authoritative nameserver hostname. End with a dot.",
    example: "ns1.example.com.",
  },
  CAA: {
    value: "Certificate Authority Authorization. flags tag \"value\".",
    example: "0 issue \"letsencrypt.org\"",
  },
  PTR: {
    value: "Reverse-DNS target hostname. End with a dot.",
    example: "host.example.com.",
  },
};

const inputClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";
const selectClass = inputClass;

export default function DnsPage() {
  const [zones, setZones] = useState<DnsZone[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");

  // Zone detail view
  const [selectedZone, setSelectedZone] = useState<DnsZone | null>(null);
  const [records, setRecords] = useState<DnsRecord[]>([]);
  const [loadingRecords, setLoadingRecords] = useState(false);
  const [recordSearch, setRecordSearch] = useState("");

  // Add / edit record modals
  const [showAdd, setShowAdd] = useState(false);
  const [showEdit, setShowEdit] = useState(false);
  const [editRecord, setEditRecord] = useState<DnsRecord | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [form, setForm] = useState({
    type: "A",
    name: "",
    value: "",
    ttl: "3600",
    priority: "",
  });

  useEffect(() => {
    fetchZones();
  }, []);

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
    setRecordSearch("");
    // If the list already included inline records, show them immediately
    // while we refresh in the background.
    if (zone.records && zone.records.length) setRecords(zone.records);
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

  const backToZones = () => {
    setSelectedZone(null);
    setRecords([]);
    fetchZones();
  };

  const buildPayload = () => {
    const payload: any = {
      type: form.type,
      name: form.name.trim(),
      value: form.value.trim(),
      ttl: parseInt(form.ttl, 10) || 3600,
    };
    if (form.priority && (form.type === "MX" || form.type === "SRV" || form.type === "CAA")) {
      payload.priority = parseInt(form.priority, 10);
    }
    return payload;
  };

  const handleAddRecord = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedZone) return;
    if (!form.name.trim() || !form.value.trim()) {
      toast.error("Name and value are required");
      return;
    }
    setSubmitting(true);
    try {
      await api.post(`/dns/zones/${selectedZone.domain}/records`, buildPayload());
      toast.success("DNS record added");
      setShowAdd(false);
      resetForm();
      fetchRecords(selectedZone.domain);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || err?.response?.data?.message || "Failed to add DNS record");
    } finally {
      setSubmitting(false);
    }
  };

  const handleEditRecord = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedZone || !editRecord) return;
    setSubmitting(true);
    try {
      await api.put(
        `/dns/zones/${selectedZone.domain}/records/${editRecord.id}`,
        buildPayload()
      );
      toast.success("DNS record updated");
      setShowEdit(false);
      setEditRecord(null);
      resetForm();
      fetchRecords(selectedZone.domain);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || err?.response?.data?.message || "Failed to update DNS record");
    } finally {
      setSubmitting(false);
    }
  };

  const handleDeleteRecord = async (r: DnsRecord) => {
    if (!selectedZone) return;
    if (
      !(await confirmAction({
        title: "Delete DNS record?",
        description: `Delete ${r.type} record for ${r.name}?`,
        danger: true,
        confirmLabel: "Delete",
      }))
    )
      return;
    try {
      if (r.id) {
        await api.delete(`/dns/zones/${selectedZone.domain}/records/${r.id}`);
      } else {
        // Legacy record with no Mongo ID — fall back to name+type delete
        await api.delete(
          `/dns/zones/${selectedZone.domain}/records/_?name=${encodeURIComponent(
            r.name
          )}&type=${encodeURIComponent(r.type)}`
        );
      }
      toast.success("DNS record deleted");
      setRecords((prev) => prev.filter((x) => x.id !== r.id || x.name !== r.name));
      fetchRecords(selectedZone.domain);
    } catch {
      toast.error("Failed to delete DNS record");
    }
  };

  const openEdit = (r: DnsRecord) => {
    setEditRecord(r);
    setForm({
      type: r.type,
      name: r.name,
      value: r.value,
      ttl: String(r.ttl || 3600),
      priority: r.priority ? String(r.priority) : "",
    });
    setShowEdit(true);
  };

  const openAdd = () => {
    resetForm();
    setShowAdd(true);
  };

  const resetForm = () => {
    setForm({ type: "A", name: "", value: "", ttl: "3600", priority: "" });
  };

  const handleExport = () => {
    if (!selectedZone) return;
    // Open in new tab. The browser will render the zone file as text and
    // the user can save it with Ctrl/Cmd+S — same anchor-click pattern as
    // FilesPage download.
    const url = `/api/v1/cpanel/dns/zones/${selectedZone.domain}/export`;
    window.open(url, "_blank");
  };

  const filteredZones = zones.filter((z) =>
    z.domain.toLowerCase().includes(search.toLowerCase())
  );
  const filteredRecords = records.filter(
    (r) =>
      r.name.toLowerCase().includes(recordSearch.toLowerCase()) ||
      r.value.toLowerCase().includes(recordSearch.toLowerCase()) ||
      r.type.toLowerCase().includes(recordSearch.toLowerCase())
  );

  const zoneColumns = [
    {
      header: "Domain",
      accessor: (z: DnsZone) => (
        <button
          onClick={() => openZone(z)}
          className="flex items-center gap-2 hover:text-blue-400 transition-colors"
        >
          <Globe2 size={14} className="text-cyan-400" />
          <span className="font-medium text-panel-text">{z.domain}</span>
        </button>
      ),
    },
    {
      header: "Records",
      accessor: (z: DnsZone) => (
        <span className="text-panel-muted text-sm">
          {z.records_count ?? z.records?.length ?? "—"}
        </span>
      ),
    },
    {
      header: "Status",
      accessor: (z: DnsZone) => <StatusBadge status={z.status || "active"} />,
    },
    {
      header: "Last Updated",
      accessor: (z: DnsZone) => (
        <span className="text-panel-muted text-sm">
          {z.updated_at ? new Date(z.updated_at).toLocaleDateString() : "—"}
        </span>
      ),
    },
    {
      header: "Actions",
      accessor: (z: DnsZone) => (
        <div className="flex items-center justify-end gap-1">
          <button
            onClick={() => openZone(z)}
            title="Manage Records"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
          >
            <Pencil size={14} />
          </button>
        </div>
      ),
    },
  ];

  const recordColumns = [
    {
      header: "Type",
      accessor: (r: DnsRecord) => (
        <span className="inline-flex items-center px-2 py-0.5 rounded bg-blue-500/10 border border-blue-500/20 text-xs font-mono font-bold text-blue-400">
          {r.type}
        </span>
      ),
    },
    {
      header: "Name",
      accessor: (r: DnsRecord) => (
        <code className="text-sm font-mono text-panel-text">{r.name}</code>
      ),
    },
    {
      header: "Value",
      accessor: (r: DnsRecord) => (
        <code className="text-sm font-mono text-panel-muted truncate max-w-[300px] block">
          {r.value}
        </code>
      ),
    },
    {
      header: "TTL",
      accessor: (r: DnsRecord) => <span className="text-panel-muted">{r.ttl}s</span>,
    },
    ...(records.some((r) => r.priority !== undefined)
      ? [
          {
            header: "Priority",
            accessor: (r: DnsRecord) => (
              <span className="text-panel-muted">{r.priority ?? "—"}</span>
            ),
          },
        ]
      : []),
    {
      header: "Actions",
      accessor: (r: DnsRecord) => (
        <div className="flex items-center justify-end gap-1">
          <button
            onClick={() => openEdit(r)}
            title="Edit"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-blue-400 transition-colors"
          >
            <Pencil size={14} />
          </button>
          <button
            onClick={() => handleDeleteRecord(r)}
            title="Delete"
            className="p-1.5 rounded hover:bg-panel-bg text-panel-muted hover:text-red-400 transition-colors"
          >
            <Trash2 size={14} />
          </button>
        </div>
      ),
    },
  ];

  const recordForm = (onSubmit: (e: React.FormEvent) => void, isEdit: boolean) => {
    const help = RECORD_HELP[form.type] || RECORD_HELP.A;
    return (
      <form onSubmit={onSubmit} className="space-y-4">
        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className={labelClass}>Record Type *</label>
            <select
              value={form.type}
              onChange={(e) => setForm({ ...form, type: e.target.value })}
              className={selectClass}
            >
              {RECORD_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </div>
          <div>
            <label className={labelClass}>TTL (seconds)</label>
            <input
              type="number"
              min={60}
              value={form.ttl}
              onChange={(e) => setForm({ ...form, ttl: e.target.value })}
              className={inputClass}
            />
            <p className="text-xs text-panel-muted mt-1">
              How long resolvers cache this record. 3600 = 1 hour.
            </p>
          </div>
        </div>

        <div>
          <label className={labelClass}>Name *</label>
          <input
            type="text"
            required
            placeholder={
              form.type === "SRV"
                ? "_sip._tcp"
                : `@ or subdomain.${selectedZone?.domain || "example.com"}`
            }
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            className={inputClass}
          />
          <p className="text-xs text-panel-muted mt-1">
            {help.name || "Use @ for the root domain, or a subdomain (e.g. www, api)."}
          </p>
        </div>

        <div>
          <label className={labelClass}>Value *</label>
          <input
            type="text"
            required
            placeholder={help.example}
            value={form.value}
            onChange={(e) => setForm({ ...form, value: e.target.value })}
            className={inputClass}
          />
          <p className="text-xs text-panel-muted mt-1">
            {help.value}{" "}
            <span className="text-panel-text">
              Example: <code className="font-mono">{help.example}</code>
            </span>
          </p>
        </div>

        {(form.type === "MX" || form.type === "SRV" || form.type === "CAA") && (
          <div>
            <label className={labelClass}>
              {form.type === "CAA" ? "Flags" : "Priority"}
            </label>
            <input
              type="number"
              min={0}
              placeholder={form.type === "MX" ? "10" : "0"}
              value={form.priority}
              onChange={(e) => setForm({ ...form, priority: e.target.value })}
              className={inputClass}
            />
            <p className="text-xs text-panel-muted mt-1">
              {form.type === "MX"
                ? "Lower is preferred. Primary MX is usually 10."
                : form.type === "SRV"
                  ? "Used alongside weight/port/target in the Value field."
                  : "0 for non-critical, 128 for critical (RFC 8659)."}
            </p>
          </div>
        )}

        <div className="flex justify-end gap-3 pt-2">
          <Button
            variant="secondary"
            type="button"
            onClick={() => {
              setShowAdd(false);
              setShowEdit(false);
              setEditRecord(null);
            }}
          >
            Cancel
          </Button>
          <Button type="submit" loading={submitting}>
            {isEdit ? "Update Record" : "Add Record"}
          </Button>
        </div>
      </form>
    );
  };

  // ---------- Zone detail view ----------
  if (selectedZone) {
    return (
      <div className="space-y-6">
        {/* Header — domain is prominently shown */}
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
                  DNS Zone:{" "}
                  <span className="text-cyan-400 font-mono">{selectedZone.domain}</span>
                </h1>
                <p className="text-panel-muted text-sm mt-0.5">
                  {records.length} record{records.length === 1 ? "" : "s"}
                </p>
              </div>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Button
              variant="secondary"
              size="sm"
              onClick={() => fetchRecords(selectedZone.domain)}
            >
              <RefreshCw size={14} className={loadingRecords ? "animate-spin mr-1" : "mr-1"} />
              Refresh
            </Button>
            <Button variant="secondary" size="sm" onClick={handleExport} title="Download zone file">
              <Download size={14} className="mr-1" />
              Export Zone
            </Button>
            <Button size="sm" onClick={openAdd}>
              <Plus size={14} className="mr-1" />
              Add Record
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
                placeholder="Search records..."
                value={recordSearch}
                onChange={(e) => setRecordSearch(e.target.value)}
                className="w-full pl-9 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-blue-500/40"
              />
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
          ) : filteredRecords.length > 0 ? (
            <Table columns={recordColumns} data={filteredRecords as any} />
          ) : (
            <div className="text-center py-16 px-4">
              <FileText size={48} className="text-panel-muted/20 mx-auto mb-4" />
              <h3 className="text-lg font-medium text-panel-text mb-1">
                No DNS records
              </h3>
              <p className="text-panel-muted text-sm mb-6">
                Add DNS records for {selectedZone.domain}
              </p>
              <Button onClick={openAdd}>
                <Plus size={14} className="mr-1" />
                Add Record
              </Button>
            </div>
          )}
        </Card>

        <Modal
          isOpen={showAdd}
          onClose={() => {
            setShowAdd(false);
            resetForm();
          }}
          title={`Add DNS Record — ${selectedZone.domain}`}
          size="lg"
        >
          {recordForm(handleAddRecord, false)}
        </Modal>

        <Modal
          isOpen={showEdit}
          onClose={() => {
            setShowEdit(false);
            setEditRecord(null);
            resetForm();
          }}
          title={`Edit DNS Record — ${selectedZone.domain}`}
          size="lg"
        >
          {recordForm(handleEditRecord, true)}
        </Modal>
      </div>
    );
  }

  // ---------- Zones list view ----------
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
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-9 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-blue-500/40"
            />
          </div>
        </div>

        {loading ? (
          <div className="p-8">
            <div className="space-y-3">
              {[1, 2, 3, 4].map((i) => (
                <div
                  key={i}
                  className="h-12 bg-panel-border/20 rounded animate-pulse"
                />
              ))}
            </div>
          </div>
        ) : filteredZones.length > 0 ? (
          <Table columns={zoneColumns} data={filteredZones as any} />
        ) : (
          <div className="text-center py-16 px-4">
            <Globe2 size={48} className="text-panel-muted/20 mx-auto mb-4" />
            <h3 className="text-lg font-medium text-panel-text mb-1">
              No DNS zones found
            </h3>
            <p className="text-panel-muted text-sm mb-6 max-w-md mx-auto">
              {search
                ? "No DNS zones match your search."
                : "Add a domain on the Domains page — a DNS zone is created automatically."}
            </p>
          </div>
        )}
      </Card>
    </div>
  );
}
