import { useState, useEffect } from "react";
import { Card, Button, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Package, Plus, RefreshCw, Download, CheckCircle, AlertCircle,
  Server, Code, Database as DatabaseIcon, Globe, Shield, Terminal,
  Container, GitBranch, Mail, Flame
} from "lucide-react";
import EmailServerInstall from "@/components/EmailServerInstall";

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
  const [showInstallModal, setShowInstallModal] = useState(false);
  const [installName, setInstallName] = useState("");
  const [installVersion, setInstallVersion] = useState("");
  const [installing, setInstalling] = useState(false);

  useEffect(() => {
    fetchPackages();
  }, []);

  const fetchPackages = async () => {
    setLoading(true);
    try {
      const res = await api.get("/whm/software/packages");
      setPackages(res.data.data || []);
    } catch {
      setPackages([]);
    } finally {
      setLoading(false);
    }
  };

  const handleUpdate = async (id: string, name: string) => {
    try {
      await api.post("/whm/software/install", { software: id });
      toast.success(`${name} update initiated`);
      fetchPackages();
    } catch {
      toast.error(`Failed to update ${name}`);
    }
  };

  const handleInstall = async () => {
    if (!installName.trim()) {
      toast.error("Package name is required");
      return;
    }
    setInstalling(true);
    try {
      await api.post("/whm/software/install", {
        software: installName.trim(),
        version: installVersion.trim(),
      });
      toast.success(`${installName} installation initiated`);
      setShowInstallModal(false);
      setInstallName("");
      setInstallVersion("");
      fetchPackages();
    } catch {
      toast.error(`Failed to install ${installName}`);
    } finally {
      setInstalling(false);
    }
  };

  const getIconForPackage = (icon: string) => {
    switch (icon) {
      case "nginx": return <Globe size={24} className="text-green-400" />;
      case "php": return <Code size={24} className="text-purple-400" />;
      case "mongodb": return <DatabaseIcon size={24} className="text-green-400" />;
      case "nodejs": return <Server size={24} className="text-green-400" />;
      case "go": return <Terminal size={24} className="text-cyan-400" />;
      case "dev": return <GitBranch size={24} className="text-orange-400" />;
      case "docker": return <Container size={24} className="text-blue-400" />;
      case "firewall": return <Shield size={24} className="text-red-400" />;
      case "ssl": return <Shield size={24} className="text-emerald-400" />;
      case "cache": return <Flame size={24} className="text-red-400" />;
      case "mail": return <Mail size={24} className="text-yellow-400" />;
      default: return <Package size={24} className="text-blue-400" />;
    }
  };

  return (
    <div className="space-y-6">
      {/* Email Server Installation Section */}
      <EmailServerInstall />

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
            onClick={() => setShowInstallModal(true)}
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
      ) : packages.length > 0 ? (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
          {packages.map((pkg) => (
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
        <div className="text-center py-12">
          <Package size={48} className="text-panel-muted/30 mx-auto mb-3" />
          <p className="text-panel-muted text-sm">No installed packages detected</p>
          <p className="text-panel-muted/60 text-xs mt-1">
            Click "Install Package" to add software to your server
          </p>
        </div>
      )}

      {/* Install Package Modal */}
      <Modal isOpen={showInstallModal} title="Install Package" onClose={() => setShowInstallModal(false)} size="sm">
          <div className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1">
                Package Name
              </label>
              <input
                type="text"
                value={installName}
                onChange={(e) => setInstallName(e.target.value)}
                placeholder="e.g. nginx, redis-server, php8.3-fpm"
                className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm placeholder-panel-muted/50 focus:outline-none focus:border-blue-500"
              />
              <p className="text-xs text-panel-muted mt-1">
                Enter the apt package name as it appears in the repository
              </p>
            </div>
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1">
                Version (optional)
              </label>
              <input
                type="text"
                value={installVersion}
                onChange={(e) => setInstallVersion(e.target.value)}
                placeholder="Leave empty for latest"
                className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm placeholder-panel-muted/50 focus:outline-none focus:border-blue-500"
              />
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button
                onClick={() => setShowInstallModal(false)}
                className="px-4 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text text-sm transition-colors"
              >
                Cancel
              </Button>
              <Button
                onClick={handleInstall}
                disabled={installing || !installName.trim()}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 disabled:opacity-50 text-white rounded-lg text-sm font-medium transition-colors"
              >
                {installing ? (
                  <RefreshCw size={14} className="animate-spin" />
                ) : (
                  <Download size={14} />
                )}
                {installing ? "Installing..." : "Install"}
              </Button>
            </div>
          </div>
      </Modal>
    </div>
  );
}
