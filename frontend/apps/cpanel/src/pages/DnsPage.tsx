import React, { useEffect, useState } from "react";
import { Card, Button, Table, Modal, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Globe, Plus, Trash2, Pencil, Search, RefreshCw } from "lucide-react";

interface DnsRecord {
  id: string;
  zone: string;
  type: string;
  name: string;
  value: string;
  ttl: number;
  priority?: number;
}

interface DnsZone {
  domain: string;
  records: DnsRecord[];
}

export default function DnsPage() {
  const [zones, setZones] = useState<DnsZone[]>([]);
  const [selectedZone, setSelectedZone] = useState<string>("");
  const [records, setRecords] = useState<DnsRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [showAdd, setShowAdd] = useState(false);
  const [showEdit, setShowEdit] = useState(false);
  const [search, setSearch] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [editRecord, setEditRecord] = useState<DnsRecord | null>(null);
  const [form, setForm] = useState({
    type: "A",
    name: "",
    value: "",
    ttl: "3600",
    priority: "",
  });

  const fetchZones = async () => {
    try {
      const res = await api.get("/dns/zones");
      setZones(res.data);
      if (res.data.length > 0 && !selectedZone) {
        setSelectedZone(res.data[0].domain);
        setRecords(res.data[0].records || []);
      }
    } catch {
      toast.error("Failed to load DNS zones");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchZones();
  }, []);

  const fetchRecords = async (zone: string) => {
    try {
      const res = await api.get(`/dns/zones/${zone}/records`);
      setRecords(res.data);
    } catch {
      toast.error("Failed to load DNS records");
    }
  };

  const handleZoneChange = (zone: string) => {
    setSelectedZone(zone);
    fetchRecords(zone);
  };

  const handleAddRecord = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.name.trim() || !form.value.trim()) {
      toast.error("Please fill in name and value");
      return;
    }
    setSubmitting(true);
    try {
      await api.post(`/dns/zones/${selectedZone}/records`, {
        type: form.type,
        name: form.name,
        value: form.value,
        ttl: parseInt(form.ttl),
        priority: form.priority ? parseInt(form.priority) : undefined,
      });
      toast.success("DNS record added");
      setShowAdd(false);
      setForm({ type: "A", name: "", value: "", ttl: "3600", priority: "" });
      fetchRecords(selectedZone);
    } catch (err: any) {
      toast.error(err.response?.data?.message || "Failed to add DNS record");
    } finally {
      setSubmitting(false);
    }
  };

  const handleEditRecord = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!editRecord) return;
    setSubmitting(true);
    try {
      await api.put(`/dns/zones/${selectedZone}/records/${editRecord.id}`, {
        type: form.type,
        name: form.name,
        value: form.value,
        ttl: parseInt(form.ttl),
        priority: form.priority ? parseInt(form.priority) : undefined,
      });
      toast.success("DNS record updated");
      setShowEdit(false);
      setEditRecord(null);
      fetchRecords(selectedZone);
    } catch (err: any) {
      toast.error(err.response?.data?.message || "Failed to update DNS record");
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm("Delete this DNS record?")) return;
    try {
      await api.delete(`/dns/zones/${selectedZone}/records/${id}`);
      toast.success("DNS record deleted");
      setRecords((prev) => prev.filter((r) => r.id !== id));
    } catch {
      toast.error("Failed to delete DNS record");
    }
  };

  const openEdit = (record: DnsRecord) => {
    setEditRecord(record);
    setForm({
      type: record.type,
      name: record.name,
      value: record.value,
      ttl: String(record.ttl),
      priority: record.priority ? String(record.priority) : "",
    });
    setShowEdit(true);
  };

  const filtered = records.filter(
    (r) =>
      r.name.toLowerCase().includes(search.toLowerCase()) ||
      r.value.toLowerCase().includes(search.toLowerCase()) ||
      r.type.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      key: "type",
      header: "Type",
      render: (item: DnsRecord) => (
        <span className="inline-flex items-center px-2 py-0.5 rounded bg-panel-bg text-xs font-mono font-medium text-brand-400">
          {item.type}
        </span>
      ),
    },
    {
      key: "name",
      header: "Name",
      render: (item: DnsRecord) => (
        <span className="font-medium text-white font-mono text-xs">
          {item.name}
        </span>
      ),
    },
    {
      key: "value",
      header: "Value",
      render: (item: DnsRecord) => (
        <span className="text-panel-text font-mono text-xs truncate max-w-xs block">
          {item.value}
        </span>
      ),
    },
    {
      key: "ttl",
      header: "TTL",
      render: (item: DnsRecord) => (
        <span className="text-panel-muted">{item.ttl}s</span>
      ),
    },
    {
      key: "actions",
      header: "",
      render: (item: DnsRecord) => (
        <div className="flex items-center gap-2 justify-end">
          <button
            onClick={() => openEdit(item)}
            className="text-panel-muted hover:text-brand-400 transition-colors"
            title="Edit"
          >
            <Pencil size={16} />
          </button>
          <button
            onClick={() => handleDelete(item.id)}
            className="text-panel-muted hover:text-red-400 transition-colors"
            title="Delete"
          >
            <Trash2 size={16} />
          </button>
        </div>
      ),
    },
  ];

  const recordForm = (onSubmit: (e: React.FormEvent) => void) => (
    <form onSubmit={onSubmit} className="space-y-4">
      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="block text-sm font-medium text-panel-text mb-1.5">
            Record Type
          </label>
          <select
            value={form.type}
            onChange={(e) => setForm({ ...form, type: e.target.value })}
            className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text focus:outline-none focus:ring-2 focus:ring-brand-500"
          >
            {["A", "AAAA", "CNAME", "MX", "TXT", "NS", "SRV", "CAA"].map(
              (t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              )
            )}
          </select>
        </div>
        <div>
          <label className="block text-sm font-medium text-panel-text mb-1.5">
            TTL (seconds)
          </label>
          <input
            type="number"
            value={form.ttl}
            onChange={(e) => setForm({ ...form, ttl: e.target.value })}
            className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text focus:outline-none focus:ring-2 focus:ring-brand-500"
          />
        </div>
      </div>
      <div>
        <label className="block text-sm font-medium text-panel-text mb-1.5">
          Name
        </label>
        <input
          type="text"
          value={form.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
          placeholder="@ or subdomain"
          className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
        />
      </div>
      <div>
        <label className="block text-sm font-medium text-panel-text mb-1.5">
          Value
        </label>
        <input
          type="text"
          value={form.value}
          onChange={(e) => setForm({ ...form, value: e.target.value })}
          placeholder="IP address or hostname"
          className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
        />
      </div>
      {(form.type === "MX" || form.type === "SRV") && (
        <div>
          <label className="block text-sm font-medium text-panel-text mb-1.5">
            Priority
          </label>
          <input
            type="number"
            value={form.priority}
            onChange={(e) => setForm({ ...form, priority: e.target.value })}
            placeholder="10"
            className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
          />
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
          {editRecord ? "Update Record" : "Add Record"}
        </Button>
      </div>
    </form>
  );

  return (
    <div className="space-y-6">
      <Card
        title="DNS Management"
        description="Manage DNS records for your domains"
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => fetchRecords(selectedZone)}
            >
              <RefreshCw size={16} />
            </Button>
            <Button
              size="sm"
              onClick={() => {
                setForm({
                  type: "A",
                  name: "",
                  value: "",
                  ttl: "3600",
                  priority: "",
                });
                setShowAdd(true);
              }}
              disabled={!selectedZone}
            >
              <Plus size={16} className="mr-1" /> Add Record
            </Button>
          </div>
        }
      >
        <div className="flex items-center gap-4 mb-4">
          <div>
            <select
              value={selectedZone}
              onChange={(e) => handleZoneChange(e.target.value)}
              className="px-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text focus:outline-none focus:ring-2 focus:ring-brand-500"
            >
              {zones.length === 0 && (
                <option value="">No DNS zones</option>
              )}
              {zones.map((z) => (
                <option key={z.domain} value={z.domain}>
                  {z.domain}
                </option>
              ))}
            </select>
          </div>
          <div className="relative flex-1 max-w-xs">
            <Search
              size={16}
              className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted"
            />
            <input
              type="text"
              placeholder="Search records..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-9 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
        </div>
        <Table
          columns={columns}
          data={filtered as any}
          loading={loading}
          emptyMessage="No DNS records found for this zone."
        />
      </Card>

      <Modal
        isOpen={showAdd}
        onClose={() => setShowAdd(false)}
        title="Add DNS Record"
      >
        {recordForm(handleAddRecord)}
      </Modal>

      <Modal
        isOpen={showEdit}
        onClose={() => {
          setShowEdit(false);
          setEditRecord(null);
        }}
        title="Edit DNS Record"
      >
        {recordForm(handleEditRecord)}
      </Modal>
    </div>
  );
}
