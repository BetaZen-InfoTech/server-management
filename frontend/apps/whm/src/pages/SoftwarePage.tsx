import { useState, useEffect } from "react";
import { Card, Button, StatusBadge } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Package, Plus, RefreshCw, Download, CheckCircle, AlertCircle,
  Server, Code, Database as DatabaseIcon, Globe
} from "lucide-react";

interface InstalledPackage {
  id: string;
  name: string;
  version: string;
  latestVersion: string;
  category: string;
  icon: string;
  status: "up-to-date" | "update-available" | "outdated";
}

export default function SoftwarePage() {
  const [packages, setPackages] = useState<InstalledPackage[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetchPackages();
  }, []);

  const fetchPackages = async () => {
    setLoading(true);
    try {
      const res = await api.get("/software/packages");
      setPackages(res.data || []);
    } catch {
      // Use placeholder data
      setPackages([]);
    } finally {
      setLoading(false);
    }
  };

  const handleUpdate = async (id: string, name: string) => {
    try {
      await api.post(`/software/packages/${id}/update`);
      toast.success(`${name} update initiated`);
      fetchPackages();
    } catch {
      toast.error(`Failed to update ${name}`);
    }
  };

  const getIconForPackage = (icon: string) => {
    switch (icon) {
      case "nginx": return <Globe size={24} className="text-green-400" />;
      case "php": return <Code size={24} className="text-purple-400" />;
      case "mongodb": return <DatabaseIcon size={24} className="text-green-400" />;
      case "nodejs": return <Server size={24} className="text-green-400" />;
      default: return <Package size={24} className="text-blue-400" />;
    }
  };

  // Placeholder software cards when no API data
  const placeholderSoftware = [
    { name: "Nginx", version: "1.26.2", category: "Web Server", icon: "nginx" },
    { name: "PHP 8.3", version: "8.3.12", category: "Runtime", icon: "php" },
    { name: "PHP 8.2", version: "8.2.24", category: "Runtime", icon: "php" },
    { name: "MongoDB", version: "7.0.14", category: "Database", icon: "mongodb" },
    { name: "Node.js 20", version: "20.18.0", category: "Runtime", icon: "nodejs" },
    { name: "Node.js 22", version: "22.9.0", category: "Runtime", icon: "nodejs" },
    { name: "Certbot", version: "2.11.0", category: "SSL", icon: "ssl" },
    { name: "Redis", version: "7.2.6", category: "Cache", icon: "cache" },
  ];

  const displayPackages = packages.length > 0 ? packages : [];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Software</h1>
          <p className="text-panel-muted text-sm mt-1">
            View and manage installed software packages
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button
            onClick={fetchPackages}
            className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
          <Button
            onClick={() => toast("Install Package modal coming soon")}
            className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
          >
            <Plus size={14} />
            Install Package
          </Button>
        </div>
      </div>

      {loading ? (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
          {[1, 2, 3, 4, 5, 6, 7, 8].map((i) => (
            <Card key={i}>
              <div className="p-5">
                <div className="h-32 bg-panel-border/20 rounded animate-pulse" />
              </div>
            </Card>
          ))}
        </div>
      ) : displayPackages.length > 0 ? (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
          {displayPackages.map((pkg) => (
            <Card key={pkg.id}>
              <div className="p-5">
                <div className="flex items-start justify-between mb-3">
                  <div className="p-2 rounded-lg bg-panel-bg">
                    {getIconForPackage(pkg.icon)}
                  </div>
                  {pkg.status === "up-to-date" ? (
                    <CheckCircle size={16} className="text-green-400" />
                  ) : (
                    <AlertCircle size={16} className="text-yellow-400" />
                  )}
                </div>
                <h3 className="font-semibold text-panel-text mb-0.5">{pkg.name}</h3>
                <p className="text-xs text-panel-muted mb-3">{pkg.category}</p>
                <div className="flex items-center justify-between">
                  <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono">
                    v{pkg.version}
                  </code>
                  {pkg.status !== "up-to-date" && (
                    <button
                      onClick={() => handleUpdate(pkg.id, pkg.name)}
                      className="flex items-center gap-1 text-xs text-blue-400 hover:text-blue-300 transition-colors"
                    >
                      <Download size={12} />
                      Update
                    </button>
                  )}
                </div>
              </div>
            </Card>
          ))}
        </div>
      ) : (
        <>
          {/* Show placeholder software info */}
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            {placeholderSoftware.map((pkg) => (
              <Card key={pkg.name}>
                <div className="p-5">
                  <div className="flex items-start justify-between mb-3">
                    <div className="p-2 rounded-lg bg-panel-bg">
                      {getIconForPackage(pkg.icon)}
                    </div>
                    <CheckCircle size={16} className="text-green-400" />
                  </div>
                  <h3 className="font-semibold text-panel-text mb-0.5">{pkg.name}</h3>
                  <p className="text-xs text-panel-muted mb-3">{pkg.category}</p>
                  <code className="text-xs bg-panel-bg px-2 py-0.5 rounded text-panel-muted font-mono">
                    v{pkg.version}
                  </code>
                </div>
              </Card>
            ))}
          </div>

          <div className="text-center py-4">
            <p className="text-xs text-panel-muted">
              Showing placeholder data. Connect to the API to see real package information.
            </p>
          </div>
        </>
      )}
    </div>
  );
}
