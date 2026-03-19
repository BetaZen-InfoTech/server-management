import { useState, useEffect } from "react";
import { Card, Button, Modal } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import {
  Package, Plus, RefreshCw, Download, CheckCircle, AlertCircle,
  Server, Code, Database as DatabaseIcon, Globe, Shield, Terminal,
  Container, GitBranch, Mail, Flame, Layers, Settings,
  Loader, Trash2, Search, X
} from "lucide-react";
import EmailServerInstall from "@/components/EmailServerInstall";

// ──────────────────────────────────────────────────────
// Types
// ──────────────────────────────────────────────────────

interface InstalledPackage {
  id: string;
  name: string;
  version: string;
  latestVersion: string;
  category: string;
  icon: string;
  status: "up-to-date" | "update-available" | "outdated";
}

interface RuntimeVersion {
  version: string;
  full: string;
  installed: boolean;
  active?: boolean;
}

interface PHPExtension {
  name: string;
  installed: boolean;
  package: string;
}

interface FPMPool {
  name: string;
  file: string;
  pm: string;
  max_children: string;
  active: boolean;
  php_version: string;
}

type Tab = "runtimes" | "php-extensions" | "php-fpm" | "packages";

// ──────────────────────────────────────────────────────
// Runtime config
// ──────────────────────────────────────────────────────

const RUNTIME_META: Record<string, { label: string; icon: JSX.Element; color: string }> = {
  php: { label: "PHP", icon: <Code size={20} />, color: "text-purple-400" },
  python: { label: "Python", icon: <Terminal size={20} />, color: "text-yellow-400" },
  nodejs: { label: "Node.js", icon: <Server size={20} />, color: "text-green-400" },
  ruby: { label: "Ruby", icon: <Flame size={20} />, color: "text-red-400" },
  go: { label: "Go", icon: <Terminal size={20} />, color: "text-cyan-400" },
};

// ──────────────────────────────────────────────────────
// Main component
// ──────────────────────────────────────────────────────

export default function SoftwarePage() {
  const [tab, setTab] = useState<Tab>("runtimes");

  return (
    <div className="space-y-6">
      {/* Email Server */}
      <EmailServerInstall />

      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold text-panel-text">Software</h1>
          <p className="text-panel-muted text-sm mt-1">
            Manage runtimes, PHP extensions, FPM pools, and system packages
          </p>
        </div>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 bg-panel-surface border border-panel-border rounded-lg p-1">
        {([
          { key: "runtimes", label: "Runtimes", icon: <Layers size={14} /> },
          { key: "php-extensions", label: "PHP Extensions", icon: <Code size={14} /> },
          { key: "php-fpm", label: "PHP-FPM", icon: <Settings size={14} /> },
          { key: "packages", label: "System Packages", icon: <Package size={14} /> },
        ] as { key: Tab; label: string; icon: JSX.Element }[]).map((t) => (
          <button
            key={t.key}
            onClick={() => setTab(t.key)}
            className={`flex items-center gap-2 px-4 py-2 rounded-md text-sm font-medium transition-colors ${
              tab === t.key
                ? "bg-blue-600 text-white"
                : "text-panel-muted hover:text-panel-text hover:bg-panel-bg"
            }`}
          >
            {t.icon}
            {t.label}
          </button>
        ))}
      </div>

      {/* Tab content */}
      {tab === "runtimes" && <RuntimesTab />}
      {tab === "php-extensions" && <PHPExtensionsTab />}
      {tab === "php-fpm" && <PHPFPMTab />}
      {tab === "packages" && <PackagesTab />}
    </div>
  );
}

// ──────────────────────────────────────────────────────
// Runtimes Tab
// ──────────────────────────────────────────────────────

function RuntimesTab() {
  const [runtimes, setRuntimes] = useState<Record<string, RuntimeVersion[]>>({});
  const [loading, setLoading] = useState(true);
  const [actionLoading, setActionLoading] = useState<string | null>(null);

  useEffect(() => { fetchRuntimes(); }, []);

  const fetchRuntimes = async () => {
    setLoading(true);
    try {
      const res = await api.get("/software/runtimes");
      setRuntimes(res.data.data || {});
    } catch {
      setRuntimes({});
    } finally {
      setLoading(false);
    }
  };

  const handleInstall = async (runtime: string, version: string) => {
    const key = `${runtime}-${version}`;
    setActionLoading(key);
    try {
      await api.post("/software/runtimes/install", { runtime, version });
      toast.success(`${runtime} ${version} installation started`);
      fetchRuntimes();
    } catch {
      toast.error(`Failed to install ${runtime} ${version}`);
    } finally {
      setActionLoading(null);
    }
  };

  const handleUninstall = async (runtime: string, version: string) => {
    const key = `${runtime}-${version}-uninstall`;
    setActionLoading(key);
    try {
      await api.post("/software/runtimes/uninstall", { runtime, version });
      toast.success(`${runtime} ${version} removed`);
      fetchRuntimes();
    } catch {
      toast.error(`Failed to uninstall ${runtime} ${version}`);
    } finally {
      setActionLoading(null);
    }
  };

  if (loading) {
    return (
      <div className="space-y-4">
        {[1, 2, 3, 4, 5].map((i) => (
          <Card key={i}>
            <div className="p-5">
              <div className="h-20 bg-panel-border/20 rounded animate-pulse" />
            </div>
          </Card>
        ))}
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-end">
        <Button
          onClick={fetchRuntimes}
          className="flex items-center gap-2 px-3 py-2 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
        >
          <RefreshCw size={14} />
          Refresh
        </Button>
      </div>

      {Object.entries(RUNTIME_META).map(([key, meta]) => {
        const versions = runtimes[key] || [];
        return (
          <Card key={key}>
            <div className="p-5">
              <div className="flex items-center gap-3 mb-4">
                <div className={`p-2 rounded-lg bg-panel-bg ${meta.color}`}>
                  {meta.icon}
                </div>
                <div>
                  <h3 className="font-semibold text-panel-text">{meta.label}</h3>
                  <p className="text-xs text-panel-muted">
                    {versions.filter((v) => v.installed).length} of {versions.length} versions installed
                  </p>
                </div>
              </div>

              <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-3">
                {versions.map((v) => {
                  const installKey = `${key}-${v.version}`;
                  const uninstallKey = `${key}-${v.version}-uninstall`;
                  const isInstalling = actionLoading === installKey;
                  const isUninstalling = actionLoading === uninstallKey;

                  return (
                    <div
                      key={v.version}
                      className={`relative p-3 rounded-lg border transition-colors ${
                        v.installed
                          ? "bg-green-500/5 border-green-500/20"
                          : "bg-panel-bg border-panel-border"
                      }`}
                    >
                      <div className="flex items-center justify-between mb-2">
                        <span className="text-sm font-medium text-panel-text">
                          {v.version}
                        </span>
                        {v.installed ? (
                          <CheckCircle size={14} className="text-green-400" />
                        ) : (
                          <div className="w-3.5 h-3.5 rounded-full border border-panel-border" />
                        )}
                      </div>

                      {v.full && (
                        <code className="text-[10px] text-panel-muted font-mono block mb-2">
                          {v.full}
                        </code>
                      )}

                      {v.installed ? (
                        <button
                          onClick={() => handleUninstall(key, v.version)}
                          disabled={isUninstalling}
                          className="flex items-center gap-1 text-xs text-red-400 hover:text-red-300 transition-colors disabled:opacity-50"
                        >
                          {isUninstalling ? (
                            <Loader size={10} className="animate-spin" />
                          ) : (
                            <Trash2 size={10} />
                          )}
                          {isUninstalling ? "Removing..." : "Uninstall"}
                        </button>
                      ) : (
                        <button
                          onClick={() => handleInstall(key, v.version)}
                          disabled={isInstalling}
                          className="flex items-center gap-1 text-xs text-blue-400 hover:text-blue-300 transition-colors disabled:opacity-50"
                        >
                          {isInstalling ? (
                            <Loader size={10} className="animate-spin" />
                          ) : (
                            <Download size={10} />
                          )}
                          {isInstalling ? "Installing..." : "Install"}
                        </button>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          </Card>
        );
      })}
    </div>
  );
}

// ──────────────────────────────────────────────────────
// PHP Extensions Tab
// ──────────────────────────────────────────────────────

function PHPExtensionsTab() {
  const [phpVersion, setPHPVersion] = useState("8.2");
  const [extensions, setExtensions] = useState<PHPExtension[]>([]);
  const [loading, setLoading] = useState(true);
  const [actionLoading, setActionLoading] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [availableVersions, setAvailableVersions] = useState<string[]>([]);

  useEffect(() => {
    // Fetch installed PHP versions to populate selector
    api.get("/software/runtimes/php").then((res) => {
      const versions = (res.data.data || [])
        .filter((v: RuntimeVersion) => v.installed)
        .map((v: RuntimeVersion) => v.version);
      setAvailableVersions(versions);
      if (versions.length > 0 && !versions.includes(phpVersion)) {
        setPHPVersion(versions[0]);
      }
    }).catch(() => {});
  }, []);

  useEffect(() => { fetchExtensions(); }, [phpVersion]);

  const fetchExtensions = async () => {
    setLoading(true);
    try {
      const res = await api.get(`/software/php/${phpVersion}/extensions`);
      setExtensions(res.data.data || []);
    } catch {
      setExtensions([]);
    } finally {
      setLoading(false);
    }
  };

  const handleToggle = async (ext: PHPExtension) => {
    const action = ext.installed ? "uninstall" : "install";
    setActionLoading(ext.name);
    try {
      await api.post(`/software/php/${phpVersion}/extensions/${action}`, {
        extension: ext.name,
      });
      toast.success(`${ext.name} ${action === "install" ? "installed" : "removed"}`);
      fetchExtensions();
    } catch {
      toast.error(`Failed to ${action} ${ext.name}`);
    } finally {
      setActionLoading(null);
    }
  };

  const filtered = extensions.filter((ext) =>
    ext.name.toLowerCase().includes(search.toLowerCase())
  );
  const installedCount = extensions.filter((e) => e.installed).length;

  return (
    <div className="space-y-4">
      {/* Controls */}
      <Card>
        <div className="p-4 flex flex-col sm:flex-row items-start sm:items-center gap-4">
          <div className="flex items-center gap-2">
            <label className="text-sm font-medium text-panel-text whitespace-nowrap">PHP Version:</label>
            <select
              value={phpVersion}
              onChange={(e) => setPHPVersion(e.target.value)}
              className="px-3 py-1.5 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm focus:outline-none focus:border-blue-500"
            >
              {availableVersions.map((v) => (
                <option key={v} value={v}>PHP {v}</option>
              ))}
            </select>
          </div>

          <div className="flex-1 relative">
            <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search extensions..."
              className="w-full pl-9 pr-8 py-1.5 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm placeholder-panel-muted/50 focus:outline-none focus:border-blue-500"
            />
            {search && (
              <button
                onClick={() => setSearch("")}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-panel-muted hover:text-panel-text"
              >
                <X size={14} />
              </button>
            )}
          </div>

          <div className="text-sm text-panel-muted whitespace-nowrap">
            {installedCount} / {extensions.length} installed
          </div>

          <Button
            onClick={fetchExtensions}
            className="flex items-center gap-2 px-3 py-1.5 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
          >
            <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
            Refresh
          </Button>
        </div>
      </Card>

      {/* Extensions grid */}
      {loading ? (
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3">
          {[1,2,3,4,5,6,7,8,9,10].map((i) => (
            <div key={i} className="h-16 bg-panel-border/20 rounded-lg animate-pulse" />
          ))}
        </div>
      ) : (
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-3">
          {filtered.map((ext) => {
            const isLoading = actionLoading === ext.name;
            return (
              <div
                key={ext.name}
                className={`flex items-center gap-3 p-3 rounded-lg border transition-colors ${
                  ext.installed
                    ? "bg-green-500/5 border-green-500/20"
                    : "bg-panel-bg border-panel-border"
                }`}
              >
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium text-panel-text truncate">{ext.name}</p>
                  <p className="text-[10px] text-panel-muted font-mono truncate">{ext.package}</p>
                </div>
                <button
                  onClick={() => handleToggle(ext)}
                  disabled={isLoading}
                  className={`relative w-9 h-5 rounded-full transition-colors flex-shrink-0 ${
                    isLoading ? "opacity-50 cursor-not-allowed" : ""
                  } ${ext.installed ? "bg-green-500" : "bg-panel-border"}`}
                >
                  {isLoading ? (
                    <Loader size={12} className="absolute top-1 left-1/2 -translate-x-1/2 text-white animate-spin" />
                  ) : (
                    <span
                      className={`absolute top-0.5 left-0.5 w-4 h-4 rounded-full bg-white transition-transform ${
                        ext.installed ? "translate-x-4" : "translate-x-0"
                      }`}
                    />
                  )}
                </button>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ──────────────────────────────────────────────────────
// PHP-FPM Tab
// ──────────────────────────────────────────────────────

function PHPFPMTab() {
  const [phpVersion, setPHPVersion] = useState("8.2");
  const [pools, setPools] = useState<FPMPool[]>([]);
  const [fpmStatus, setFpmStatus] = useState<any>(null);
  const [loading, setLoading] = useState(true);
  const [restarting, setRestarting] = useState(false);
  const [availableVersions, setAvailableVersions] = useState<string[]>([]);

  useEffect(() => {
    api.get("/software/runtimes/php").then((res) => {
      const versions = (res.data.data || [])
        .filter((v: RuntimeVersion) => v.installed)
        .map((v: RuntimeVersion) => v.version);
      setAvailableVersions(versions);
      if (versions.length > 0 && !versions.includes(phpVersion)) {
        setPHPVersion(versions[0]);
      }
    }).catch(() => {});
  }, []);

  useEffect(() => { fetchData(); }, [phpVersion]);

  const fetchData = async () => {
    setLoading(true);
    try {
      const [poolsRes, statusRes] = await Promise.all([
        api.get(`/software/php/${phpVersion}/fpm/pools`),
        api.get(`/software/php/${phpVersion}/fpm/status`),
      ]);
      setPools(poolsRes.data.data || []);
      setFpmStatus(statusRes.data.data || null);
    } catch {
      setPools([]);
      setFpmStatus(null);
    } finally {
      setLoading(false);
    }
  };

  const handleRestart = async () => {
    setRestarting(true);
    try {
      await api.post(`/software/php/${phpVersion}/fpm/restart`);
      toast.success(`PHP-FPM ${phpVersion} restarted`);
      fetchData();
    } catch {
      toast.error(`Failed to restart PHP-FPM ${phpVersion}`);
    } finally {
      setRestarting(false);
    }
  };

  return (
    <div className="space-y-4">
      {/* Controls */}
      <Card>
        <div className="p-4 flex items-center justify-between">
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-2">
              <label className="text-sm font-medium text-panel-text whitespace-nowrap">PHP Version:</label>
              <select
                value={phpVersion}
                onChange={(e) => setPHPVersion(e.target.value)}
                className="px-3 py-1.5 bg-panel-bg border border-panel-border rounded-lg text-panel-text text-sm focus:outline-none focus:border-blue-500"
              >
                {availableVersions.map((v) => (
                  <option key={v} value={v}>PHP {v}</option>
                ))}
              </select>
            </div>

            {fpmStatus && (
              <div className="flex items-center gap-2">
                <div className={`w-2 h-2 rounded-full ${fpmStatus.running ? "bg-green-400" : "bg-red-400"}`} />
                <span className="text-sm text-panel-muted">
                  {fpmStatus.running ? "Running" : "Stopped"}
                  {fpmStatus.process_count && ` (${fpmStatus.process_count} workers)`}
                </span>
              </div>
            )}
          </div>

          <div className="flex items-center gap-2">
            <Button
              onClick={fetchData}
              className="flex items-center gap-2 px-3 py-1.5 bg-panel-surface border border-panel-border rounded-lg text-panel-muted hover:text-panel-text transition-colors text-sm"
            >
              <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
              Refresh
            </Button>
            <Button
              onClick={handleRestart}
              disabled={restarting}
              className="flex items-center gap-2 px-3 py-1.5 bg-yellow-600 hover:bg-yellow-700 disabled:opacity-50 text-white rounded-lg text-sm font-medium transition-colors"
            >
              {restarting ? <Loader size={14} className="animate-spin" /> : <RefreshCw size={14} />}
              {restarting ? "Restarting..." : "Restart FPM"}
            </Button>
          </div>
        </div>
      </Card>

      {/* Pools */}
      {loading ? (
        <div className="space-y-3">
          {[1, 2, 3].map((i) => (
            <div key={i} className="h-16 bg-panel-border/20 rounded-lg animate-pulse" />
          ))}
        </div>
      ) : pools.length > 0 ? (
        <Card>
          <div className="divide-y divide-panel-border">
            {pools.map((pool) => (
              <div key={pool.name} className="p-4 flex items-center gap-4">
                <div className="p-2 rounded-lg bg-panel-bg">
                  <Settings size={18} className="text-purple-400" />
                </div>
                <div className="flex-1 min-w-0">
                  <p className="font-medium text-panel-text">{pool.name}</p>
                  <p className="text-xs text-panel-muted">{pool.file}</p>
                </div>
                <div className="flex items-center gap-4 text-sm">
                  <div className="text-center">
                    <p className="text-panel-muted text-xs">Process Manager</p>
                    <code className="text-panel-text text-xs font-mono">{pool.pm}</code>
                  </div>
                  <div className="text-center">
                    <p className="text-panel-muted text-xs">Max Children</p>
                    <code className="text-panel-text text-xs font-mono">{pool.max_children}</code>
                  </div>
                  <div className={`w-2 h-2 rounded-full ${pool.active ? "bg-green-400" : "bg-red-400"}`} />
                </div>
              </div>
            ))}
          </div>
        </Card>
      ) : (
        <div className="text-center py-12">
          <Settings size={48} className="text-panel-muted/30 mx-auto mb-3" />
          <p className="text-panel-muted text-sm">No FPM pools found for PHP {phpVersion}</p>
        </div>
      )}
    </div>
  );
}

// ──────────────────────────────────────────────────────
// System Packages Tab (original)
// ──────────────────────────────────────────────────────

function PackagesTab() {
  const [packages, setPackages] = useState<InstalledPackage[]>([]);
  const [loading, setLoading] = useState(true);
  const [showInstallModal, setShowInstallModal] = useState(false);
  const [installName, setInstallName] = useState("");
  const [installVersion, setInstallVersion] = useState("");
  const [installing, setInstalling] = useState(false);

  useEffect(() => { fetchPackages(); }, []);

  const fetchPackages = async () => {
    setLoading(true);
    try {
      const res = await api.get("/software/packages");
      setPackages(res.data.data || []);
    } catch {
      setPackages([]);
    } finally {
      setLoading(false);
    }
  };

  const handleUpdate = async (id: string, name: string) => {
    try {
      await api.post("/software/install", { software: id });
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
      await api.post("/software/install", {
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
      case "python": return <Terminal size={24} className="text-yellow-400" />;
      default: return <Package size={24} className="text-blue-400" />;
    }
  };

  return (
    <>
      <div className="flex items-center justify-end gap-2">
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
    </>
  );
}
