import React, { useEffect, useState } from "react";
import { Card, Button, Table, Modal, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { Globe, Plus, Trash2, ExternalLink, Search } from "lucide-react";

interface Domain {
  id: string;
  domain: string;
  status: string;
  ssl: boolean;
  createdAt: string;
  type: string;
}

export default function DomainsPage() {
  const [domains, setDomains] = useState<Domain[]>([]);
  const [loading, setLoading] = useState(true);
  const [showAdd, setShowAdd] = useState(false);
  const [search, setSearch] = useState("");
  const [newDomain, setNewDomain] = useState("");
  const [domainType, setDomainType] = useState("addon");
  const [submitting, setSubmitting] = useState(false);

  const fetchDomains = async () => {
    try {
      const res = await api.get("/domains");
      setDomains(res.data);
    } catch {
      toast.error("Failed to load domains");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchDomains();
  }, []);

  const handleAddDomain = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newDomain.trim()) {
      toast.error("Please enter a domain name");
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/domains", { domain: newDomain, type: domainType });
      toast.success("Domain added successfully");
      setShowAdd(false);
      setNewDomain("");
      fetchDomains();
    } catch (err: any) {
      toast.error(err.response?.data?.message || "Failed to add domain");
    } finally {
      setSubmitting(false);
    }
  };

  const handleDelete = async (id: string, domain: string) => {
    if (!confirm(`Are you sure you want to remove ${domain}?`)) return;
    try {
      await api.delete(`/domains/${id}`);
      toast.success("Domain removed successfully");
      setDomains((prev) => prev.filter((d) => d.id !== id));
    } catch {
      toast.error("Failed to remove domain");
    }
  };

  const filtered = domains.filter((d) =>
    d.domain.toLowerCase().includes(search.toLowerCase())
  );

  const columns = [
    {
      key: "domain",
      header: "Domain",
      render: (item: Domain) => (
        <div className="flex items-center gap-2">
          <Globe size={16} className="text-brand-400" />
          <span className="font-medium text-white">{item.domain}</span>
        </div>
      ),
    },
    {
      key: "type",
      header: "Type",
      render: (item: Domain) => (
        <span className="text-panel-muted capitalize">{item.type}</span>
      ),
    },
    {
      key: "status",
      header: "Status",
      render: (item: Domain) => <StatusBadge status={item.status} />,
    },
    {
      key: "ssl",
      header: "SSL",
      render: (item: Domain) => (
        <span
          className={`text-sm ${item.ssl ? "text-green-400" : "text-yellow-400"}`}
        >
          {item.ssl ? "Active" : "Not Configured"}
        </span>
      ),
    },
    {
      key: "createdAt",
      header: "Created",
      render: (item: Domain) => (
        <span className="text-panel-muted">{item.createdAt}</span>
      ),
    },
    {
      key: "actions",
      header: "",
      render: (item: Domain) => (
        <div className="flex items-center gap-2 justify-end">
          <button
            className="text-panel-muted hover:text-brand-400 transition-colors"
            title="Visit"
          >
            <ExternalLink size={16} />
          </button>
          <button
            onClick={() => handleDelete(item.id, item.domain)}
            className="text-panel-muted hover:text-red-400 transition-colors"
            title="Remove"
          >
            <Trash2 size={16} />
          </button>
        </div>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      <Card
        title="My Domains"
        description="Manage domains associated with your account"
        actions={
          <Button size="sm" onClick={() => setShowAdd(true)}>
            <Plus size={16} className="mr-1" /> Add Domain
          </Button>
        }
      >
        <div className="mb-4">
          <div className="relative max-w-xs">
            <Search
              size={16}
              className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted"
            />
            <input
              type="text"
              placeholder="Search domains..."
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
          emptyMessage="No domains found. Add your first domain to get started."
        />
      </Card>

      <Modal
        isOpen={showAdd}
        onClose={() => setShowAdd(false)}
        title="Add Domain"
      >
        <form onSubmit={handleAddDomain} className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Domain Name
            </label>
            <input
              type="text"
              value={newDomain}
              onChange={(e) => setNewDomain(e.target.value)}
              placeholder="example.com"
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-panel-text mb-1.5">
              Domain Type
            </label>
            <select
              value={domainType}
              onChange={(e) => setDomainType(e.target.value)}
              className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text focus:outline-none focus:ring-2 focus:ring-brand-500"
            >
              <option value="addon">Addon Domain</option>
              <option value="subdomain">Subdomain</option>
              <option value="alias">Alias Domain</option>
            </select>
          </div>
          <div className="flex justify-end gap-3 pt-2">
            <Button
              variant="secondary"
              type="button"
              onClick={() => setShowAdd(false)}
            >
              Cancel
            </Button>
            <Button type="submit" loading={submitting}>
              Add Domain
            </Button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
