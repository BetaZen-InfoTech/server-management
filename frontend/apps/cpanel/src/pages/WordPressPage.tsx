import React, { useEffect, useState, useCallback, useMemo } from "react";
import { Card, Button, Modal, StatusBadge, confirmAction } from "@serverpanel/ui";
import api from "@/lib/api";
import { useAuthStore } from "@/store/auth";
import toast from "react-hot-toast";
import {
  FileCode2,
  Plus,
  ExternalLink,
  Trash2,
  RefreshCw,
  Search,
  AlertTriangle,
  LogIn,
  Users,
  UserPlus,
  RotateCw,
  ShieldCheck,
  Puzzle,
  Wand2,
  CheckCircle2,
  XCircle,
  AlertCircle,
  Globe,
  RadioTower,
  Power,
  Sparkles,
  Unlink,
} from "lucide-react";

interface WordPressSite {
  id: string;
  domain: string;
  user: string;
  path: string;
  version: string;
  site_url: string;
  admin_url: string;
  auto_update: boolean;
  maintenance_mode: boolean;
  created_at: string;
  updated_at?: string;
}

interface DomainItem {
  id: string;
  domain: string;
  status: string;
}

interface WPUser {
  ID: string;
  user_login: string;
  user_email: string;
  display_name: string;
  roles: string;
}

interface WPPlugin {
  name: string;
  status: string;
  version: string;
  update_available: boolean;
}

interface WPSecurityCheck {
  name: string;
  status: string;
  message: string;
  details?: string[];
}

interface WPSecurityScan {
  overall_status: string;
  checks: WPSecurityCheck[];
  scanned_at: string;
}

// WordPress versions — "latest" + a handful of recent majors. Picked by the
// operator in the install wizard; defaults to latest.
const WP_VERSIONS = ["latest", "6.5", "6.4", "6.3", "6.2"];

// Locales the panel offers up-front. The backend accepts any valid WP locale
// code; this is just a curated short-list that covers ~90% of installs.
const WP_LOCALES = [
  { code: "en_US", label: "English (US)" },
  { code: "en_GB", label: "English (UK)" },
  { code: "es_ES", label: "Spanish" },
  { code: "fr_FR", label: "French" },
  { code: "de_DE", label: "German" },
  { code: "pt_BR", label: "Portuguese (Brazil)" },
  { code: "it_IT", label: "Italian" },
  { code: "nl_NL", label: "Dutch" },
  { code: "ru_RU", label: "Russian" },
  { code: "ja", label: "Japanese" },
  { code: "zh_CN", label: "Chinese (Simplified)" },
  { code: "hi_IN", label: "Hindi" },
];

const WP_ROLES = ["administrator", "editor", "author", "contributor", "subscriber"];

const inputClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500";
const selectClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text focus:outline-none focus:ring-2 focus:ring-brand-500";
const labelClass = "block text-sm font-medium text-panel-text mb-1.5";

const defaultForm = {
  site_title: "",
  domain: "",
  path: "",
  admin_user: "admin",
  admin_email: "",
  admin_pass: "",
  version: "latest",
  locale: "en_US",
  auto_update: true,
};

// Simple, readable random password generator for the install wizard.
// 16 chars, mixed case + digits + symbols. Good enough as a starter
// credential — the operator can always type their own.
function generatePassword(): string {
  const charset = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%^&*";
  let out = "";
  for (let i = 0; i < 16; i++) {
    out += charset.charAt(Math.floor(Math.random() * charset.length));
  }
  return out;
}

export default function WordPressPage() {
  const username = useAuthStore((s) => s.user?.username || "");
  const [sites, setSites] = useState<WordPressSite[]>([]);
  const [domains, setDomains] = useState<DomainItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [rescanning, setRescanning] = useState(false);
  const [search, setSearch] = useState("");

  // Install wizard
  const [showInstall, setShowInstall] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [form, setForm] = useState(defaultForm);
  const [conflict, setConflict] = useState<string | null>(null);
  const [checkingConflict, setCheckingConflict] = useState(false);

  // Users modal
  const [showUsers, setShowUsers] = useState(false);
  const [usersSite, setUsersSite] = useState<WordPressSite | null>(null);
  const [wpUsers, setWpUsers] = useState<WPUser[]>([]);
  const [loadingUsers, setLoadingUsers] = useState(false);
  const [showAddUser, setShowAddUser] = useState(false);
  const [userForm, setUserForm] = useState({ username: "", email: "", password: "", role: "editor" });
  const [creatingUser, setCreatingUser] = useState(false);

  // Plugins + Themes modal (shared surface, tabbed). Matches WP
  // Toolkit's per-site drawer where operators flip between the two
  // collections without leaving the site context.
  const [showPlugins, setShowPlugins] = useState(false);
  const [pluginsSite, setPluginsSite] = useState<WordPressSite | null>(null);
  const [pluginsTab, setPluginsTab] = useState<"plugins" | "themes">("plugins");
  const [plugins, setPlugins] = useState<WPPlugin[]>([]);
  const [loadingPlugins, setLoadingPlugins] = useState(false);
  const [newPluginSlug, setNewPluginSlug] = useState("");
  const [installingPlugin, setInstallingPlugin] = useState(false);
  // Themes state mirrors plugins — fetched on first switch to the Themes
  // tab to keep the initial modal open fast.
  const [themes, setThemes] = useState<Array<{ name: string; status: string; version: string; update_available: boolean }>>([]);
  const [loadingThemes, setLoadingThemes] = useState(false);
  const [newThemeSlug, setNewThemeSlug] = useState("");
  const [installingTheme, setInstallingTheme] = useState(false);
  // Per-row busy flag for plugin/theme actions, keyed by "{kind}:{slug}"
  // so simultaneous clicks on different rows don't block each other.
  const [assetBusy, setAssetBusy] = useState<Record<string, string>>({});
  const markAssetBusy = (key: string, action: string | null) =>
    setAssetBusy((prev) => { const next = { ...prev }; if (action) next[key] = action; else delete next[key]; return next; });

  // Security scan modal
  const [showScan, setShowScan] = useState(false);
  const [scanSite, setScanSite] = useState<WordPressSite | null>(null);
  const [scanResult, setScanResult] = useState<WPSecurityScan | null>(null);
  const [scanning, setScanning] = useState(false);

  // Per-row inline saving flags keyed by site.id
  const [rowSaving, setRowSaving] = useState<Record<string, string | null>>({});

  const setRowBusy = (id: string, flag: string | null) =>
    setRowSaving((prev) => ({ ...prev, [id]: flag }));

  const fetchSites = async () => {
    setLoading(true);
    try {
      const res = await api.get("/wordpress");
      setSites(res.data.data || []);
    } catch {
      toast.error("Failed to load WordPress sites");
    } finally {
      setLoading(false);
    }
  };

  const fetchDomains = async () => {
    try {
      const res = await api.get("/domains");
      setDomains((res.data.data || []).filter((d: DomainItem) => d.status === "active"));
    } catch {
      // Keep empty — the install dropdown will show an empty list.
    }
  };

  useEffect(() => {
    fetchSites();
    fetchDomains();
  }, []);

  // Debounced conflict check whenever the operator changes domain or path
  // in the install wizard. Prevents the "install succeeded then actually
  // failed on disk" surprise.
  const checkConflict = useCallback(async (domain: string, path: string) => {
    if (!domain) {
      setConflict(null);
      return;
    }
    setCheckingConflict(true);
    try {
      const res = await api.get("/wordpress/check-conflict", { params: { domain, path } });
      const data = res.data.data;
      setConflict(data?.conflict ? data.message : null);
    } catch {
      setConflict(null);
    } finally {
      setCheckingConflict(false);
    }
  }, []);

  useEffect(() => {
    const timer = setTimeout(() => {
      if (form.domain) checkConflict(form.domain, form.path);
      else setConflict(null);
    }, 300);
    return () => clearTimeout(timer);
  }, [form.domain, form.path, checkConflict]);

  // Rescan — asks the backend to reconcile the WordPress index by
  // walking every user in the caller's tenant. The backend derives
  // the target usernames from the JWT's tenant_id now (cpanel_routes
  // → RescanTenant), so the frontend no longer supplies `?user=` —
  // previously any string we passed was honoured, which let a crafted
  // request scan another tenant's filesystem.
  const handleRescan = async () => {
    setRescanning(true);
    try {
      const res = await api.post("/wordpress/rescan");
      const found = res.data.data?.count ?? res.data.data?.synced ?? null;
      toast.success(
        found !== null ? `Rescan complete — ${found} site${found === 1 ? "" : "s"} tracked` : "Rescan complete",
      );
      fetchSites();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Rescan failed");
    } finally {
      setRescanning(false);
    }
  };

  const handleInstall = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!form.domain || !form.site_title || !form.admin_email || !form.admin_pass || !form.admin_user) {
      toast.error("Please fill in all required fields");
      return;
    }
    if (conflict) {
      toast.error("Cannot install — a WordPress site already exists at this location");
      return;
    }
    setSubmitting(true);
    try {
      const payload: Record<string, unknown> = {
        site_title: form.site_title,
        domain: form.domain,
        path: form.path,
        admin_user: form.admin_user,
        admin_email: form.admin_email,
        admin_pass: form.admin_pass,
        auto_update: form.auto_update,
      };
      if (form.version && form.version !== "latest") payload.version = form.version;
      if (form.locale) payload.locale = form.locale;
      await api.post("/wordpress/install", payload);
      toast.success(`WordPress installing on ${form.domain}…`);
      setShowInstall(false);
      setForm(defaultForm);
      fetchSites();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to install WordPress");
    } finally {
      setSubmitting(false);
    }
  };

  const handleUpdate = async (site: WordPressSite) => {
    setRowBusy(site.id, "update");
    try {
      await api.post(`/wordpress/${site.id}/update`);
      toast.success(`Update started for ${site.domain}`);
      fetchSites();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update WordPress");
    } finally {
      setRowBusy(site.id, null);
    }
  };

  const handleAutoLogin = async (site: WordPressSite) => {
    setRowBusy(site.id, "login");
    const toastId = `wp-login-${site.id}`;
    toast.loading("Generating login link…", { id: toastId });
    try {
      const res = await api.post(`/wordpress/${site.id}/auto-login`);
      const url = res.data.data?.login_url;
      toast.dismiss(toastId);
      if (url) {
        window.open(url, "_blank", "noopener,noreferrer");
        toast.success("Opening WP Admin…");
      } else {
        toast.error("Backend did not return a login URL");
      }
    } catch (err: any) {
      toast.dismiss(toastId);
      toast.error(err?.response?.data?.error?.message || "Auto-login failed");
    } finally {
      setRowBusy(site.id, null);
    }
  };

  const toggleMaintenance = async (site: WordPressSite) => {
    setRowBusy(site.id, "maintenance");
    const next = !site.maintenance_mode;
    try {
      await api.patch(`/wordpress/${site.id}/maintenance`, { enabled: next });
      toast.success(`Maintenance mode ${next ? "enabled" : "disabled"}`);
      setSites((prev) => prev.map((s) => (s.id === site.id ? { ...s, maintenance_mode: next } : s)));
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to toggle maintenance mode");
    } finally {
      setRowBusy(site.id, null);
    }
  };

  const toggleAutoUpdate = async (site: WordPressSite) => {
    setRowBusy(site.id, "auto_update");
    const next = !site.auto_update;
    try {
      await api.patch(`/wordpress/${site.id}/auto-update`, { enabled: next });
      toast.success(`Auto-update ${next ? "enabled" : "disabled"}`);
      setSites((prev) => prev.map((s) => (s.id === site.id ? { ...s, auto_update: next } : s)));
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to toggle auto-update");
    } finally {
      setRowBusy(site.id, null);
    }
  };

  const handleDelete = async (site: WordPressSite) => {
    if (
      !(await confirmAction({
        title: "Delete WordPress site?",
        description: `Delete WordPress on "${site.domain}${site.path ? "/" + site.path : ""}"? All files and the database will be removed. This cannot be undone.`,
        danger: true,
        confirmLabel: "Delete site",
      }))
    )
      return;
    try {
      await api.delete(`/wordpress/${site.id}`);
      toast.success(`Removed ${site.domain}`);
      setSites((prev) => prev.filter((s) => s.id !== site.id));
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to delete WordPress site");
    }
  };

  // --- Users modal ---------------------------------------------------------
  const openUsers = async (site: WordPressSite) => {
    setUsersSite(site);
    setShowUsers(true);
    setShowAddUser(false);
    setLoadingUsers(true);
    try {
      const res = await api.get(`/wordpress/${site.id}/users`);
      setWpUsers(res.data.data || []);
    } catch {
      toast.error("Failed to load WordPress users");
      setWpUsers([]);
    } finally {
      setLoadingUsers(false);
    }
  };

  const handleCreateUser = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!usersSite) return;
    if (!userForm.username || !userForm.email || !userForm.password) {
      toast.error("Username, email and password are required");
      return;
    }
    setCreatingUser(true);
    try {
      await api.post(`/wordpress/${usersSite.id}/users`, userForm);
      toast.success(`User "${userForm.username}" created`);
      setShowAddUser(false);
      setUserForm({ username: "", email: "", password: "", role: "editor" });
      const res = await api.get(`/wordpress/${usersSite.id}/users`);
      setWpUsers(res.data.data || []);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to create user");
    } finally {
      setCreatingUser(false);
    }
  };

  const handleChangeRole = async (wpUserID: string, role: string) => {
    if (!usersSite) return;
    const prev = wpUsers;
    setWpUsers(wpUsers.map((u) => (u.ID === wpUserID ? { ...u, roles: role } : u)));
    try {
      await api.patch(`/wordpress/${usersSite.id}/users/${wpUserID}`, { role });
      toast.success("Role updated");
    } catch {
      toast.error("Failed to update role");
      setWpUsers(prev);
    }
  };

  const handleDeleteUser = async (u: WPUser) => {
    if (!usersSite) return;
    if (
      !(await confirmAction({
        title: "Delete WordPress user?",
        description: `Delete "${u.user_login}"? Their content will be reassigned to the primary admin.`,
        danger: true,
        confirmLabel: "Delete user",
      }))
    )
      return;
    try {
      await api.delete(`/wordpress/${usersSite.id}/users/${u.ID}`);
      setWpUsers((prev) => prev.filter((x) => x.ID !== u.ID));
      toast.success(`Deleted ${u.user_login}`);
    } catch {
      toast.error("Failed to delete user");
    }
  };

  // --- Plugins + Themes modal ----------------------------------------------
  const openPlugins = async (site: WordPressSite) => {
    setPluginsSite(site);
    setShowPlugins(true);
    setPluginsTab("plugins");
    setNewPluginSlug("");
    setNewThemeSlug("");
    setThemes([]); // force a fresh fetch when user switches to Themes tab
    setLoadingPlugins(true);
    try {
      const res = await api.get(`/wordpress/${site.id}/plugins`);
      setPlugins(res.data.data || []);
    } catch {
      toast.error("Failed to load plugins");
      setPlugins([]);
    } finally {
      setLoadingPlugins(false);
    }
  };

  const handleInstallPlugin = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!pluginsSite) return;
    const slug = newPluginSlug.trim();
    if (!slug) {
      toast.error("Enter a plugin slug (e.g. wordpress-seo)");
      return;
    }
    setInstallingPlugin(true);
    try {
      await api.post(`/wordpress/${pluginsSite.id}/plugins`, { slug });
      toast.success(`Installed ${slug}`);
      setNewPluginSlug("");
      const res = await api.get(`/wordpress/${pluginsSite.id}/plugins`);
      setPlugins(res.data.data || []);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to install plugin");
    } finally {
      setInstallingPlugin(false);
    }
  };

  // --- Plugin lifecycle (activate / deactivate / update / delete) ---
  // All four target `/wordpress/:id/plugins/:slug/...` on the backend.
  // We reload the full plugin list after each op so status + version
  // columns reflect the wp-cli result, which is cheaper than tracking
  // individual row state.
  const refreshPlugins = async () => {
    if (!pluginsSite) return;
    const res = await api.get(`/wordpress/${pluginsSite.id}/plugins`);
    setPlugins(res.data.data || []);
  };
  const pluginAction = async (slug: string, action: "activate" | "deactivate" | "update" | "delete") => {
    if (!pluginsSite) return;
    const key = `plugin:${slug}`;
    markAssetBusy(key, action);
    try {
      if (action === "delete") {
        if (!(await confirmAction({ title: "Delete plugin?", description: `Delete "${slug}" — files will be removed from ${pluginsSite.domain}.`, danger: true, confirmLabel: "Delete" }))) return;
        await api.delete(`/wordpress/${pluginsSite.id}/plugins/${encodeURIComponent(slug)}`);
      } else {
        await api.post(`/wordpress/${pluginsSite.id}/plugins/${encodeURIComponent(slug)}/${action}`);
      }
      toast.success(`Plugin ${action}d`);
      await refreshPlugins();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || `Failed to ${action} plugin`);
    } finally {
      markAssetBusy(key, null);
    }
  };
  const updateAllPlugins = async () => {
    if (!pluginsSite) return;
    markAssetBusy("plugin:*", "update");
    try {
      await api.post(`/wordpress/${pluginsSite.id}/plugins/update`);
      toast.success("All plugins updated");
      await refreshPlugins();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update plugins");
    } finally {
      markAssetBusy("plugin:*", null);
    }
  };

  // --- Themes: list / install / activate / update / delete --------------
  const refreshThemes = async () => {
    if (!pluginsSite) return;
    setLoadingThemes(true);
    try {
      const res = await api.get(`/wordpress/${pluginsSite.id}/themes`);
      setThemes(res.data.data || []);
    } catch {
      setThemes([]);
    } finally {
      setLoadingThemes(false);
    }
  };
  const handleInstallTheme = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!pluginsSite) return;
    const slug = newThemeSlug.trim();
    if (!slug) {
      toast.error("Enter a theme slug (e.g. twentytwentyfour)");
      return;
    }
    setInstallingTheme(true);
    try {
      await api.post(`/wordpress/${pluginsSite.id}/themes`, { slug });
      toast.success(`Installed ${slug}`);
      setNewThemeSlug("");
      await refreshThemes();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to install theme");
    } finally {
      setInstallingTheme(false);
    }
  };
  const themeAction = async (slug: string, action: "activate" | "update" | "delete") => {
    if (!pluginsSite) return;
    const key = `theme:${slug}`;
    markAssetBusy(key, action);
    try {
      if (action === "delete") {
        if (!(await confirmAction({ title: "Delete theme?", description: `Delete theme "${slug}" from ${pluginsSite.domain}? The active theme cannot be deleted.`, danger: true, confirmLabel: "Delete" }))) return;
        await api.delete(`/wordpress/${pluginsSite.id}/themes/${encodeURIComponent(slug)}`);
      } else {
        await api.post(`/wordpress/${pluginsSite.id}/themes/${encodeURIComponent(slug)}/${action}`);
      }
      toast.success(`Theme ${action}d`);
      await refreshThemes();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || `Failed to ${action} theme`);
    } finally {
      markAssetBusy(key, null);
    }
  };
  const updateAllThemes = async () => {
    if (!pluginsSite) return;
    markAssetBusy("theme:*", "update");
    try {
      await api.post(`/wordpress/${pluginsSite.id}/themes/update`);
      toast.success("All themes updated");
      await refreshThemes();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update themes");
    } finally {
      markAssetBusy("theme:*", null);
    }
  };

  // Detach — remove the site from the tracker without touching files.
  // Prompts with the destructive-lite styling (not red) because it's
  // reversible via Rescan.
  const detachSite = async (site: WordPressSite) => {
    if (!(await confirmAction({
      title: "Detach site?",
      description: `Stop managing ${site.domain} in WordPress Manager. Files, database, and users are left untouched. A Rescan won't re-add it (we drop a .wp-toolkit-ignore marker).`,
      confirmLabel: "Detach",
    }))) return;
    try {
      await api.post(`/wordpress/${site.id}/detach`);
      toast.success(`${site.domain} detached`);
      fetchSites();
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to detach site");
    }
  };

  // When the modal flips to the Themes tab for the first time, lazy-load
  // the theme list so the initial plugin view stays snappy.
  useEffect(() => {
    if (showPlugins && pluginsTab === "themes" && pluginsSite && themes.length === 0 && !loadingThemes) {
      void refreshThemes();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [showPlugins, pluginsTab, pluginsSite]);

  // --- Security scan modal -------------------------------------------------
  const runSecurityScan = async (site: WordPressSite) => {
    setScanSite(site);
    setShowScan(true);
    setScanResult(null);
    setScanning(true);
    try {
      const res = await api.post(`/wordpress/${site.id}/security-scan`);
      setScanResult(res.data.data || null);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Security scan failed");
      setShowScan(false);
    } finally {
      setScanning(false);
    }
  };

  const filtered = useMemo(
    () =>
      sites.filter(
        (s) =>
          s.domain.toLowerCase().includes(search.toLowerCase()) ||
          (s.site_url || "").toLowerCase().includes(search.toLowerCase()) ||
          (s.path || "").toLowerCase().includes(search.toLowerCase()),
      ),
    [sites, search],
  );

  // Small visual helper — pills used to show auto-update + maintenance
  // state on each card so the operator can see at a glance without
  // opening a settings drawer.
  const pill = (
    label: string,
    active: boolean,
    tone: "green" | "amber",
    onClick?: () => void,
    disabled?: boolean,
  ) => {
    const colorsActive = tone === "green" ? "bg-green-500/15 text-green-400 border-green-500/30" : "bg-amber-500/15 text-amber-400 border-amber-500/30";
    const colorsOff = "bg-panel-bg text-panel-muted border-panel-border";
    return (
      <button
        type="button"
        onClick={onClick}
        disabled={disabled || !onClick}
        className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full border text-[11px] font-medium transition-colors ${
          active ? colorsActive : colorsOff
        } ${onClick && !disabled ? "hover:brightness-110 cursor-pointer" : "cursor-default"} disabled:opacity-60`}
        title={onClick ? `Click to ${active ? "disable" : "enable"}` : undefined}
      >
        <span className={`h-1.5 w-1.5 rounded-full ${active ? (tone === "green" ? "bg-green-400" : "bg-amber-400") : "bg-panel-muted"}`} />
        {label}: {active ? "On" : "Off"}
      </button>
    );
  };

  const scanIcon = (status: string) => {
    if (status === "pass" || status === "ok") return <CheckCircle2 size={16} className="text-green-400 shrink-0 mt-0.5" />;
    if (status === "warn" || status === "warning") return <AlertCircle size={16} className="text-amber-400 shrink-0 mt-0.5" />;
    return <XCircle size={16} className="text-red-400 shrink-0 mt-0.5" />;
  };

  return (
    <div className="space-y-6">
      <Card
        title="WordPress Sites"
        description="Install, secure, and manage WordPress across your domains"
        actions={
          <div className="flex items-center gap-2">
            <Button size="sm" variant="secondary" onClick={handleRescan} loading={rescanning} title="Rescan disk for untracked WordPress installs">
              <RadioTower size={14} className="mr-1.5" />
              Rescan
            </Button>
            <Button size="sm" variant="secondary" onClick={fetchSites} disabled={loading}>
              <RefreshCw size={14} className={`mr-1.5 ${loading ? "animate-spin" : ""}`} />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setShowInstall(true)}>
              <Plus size={16} className="mr-1" /> Install WordPress
            </Button>
          </div>
        }
      >
        <div className="mb-4">
          <div className="relative max-w-xs">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
            <input
              type="text"
              placeholder="Search by domain or path…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full pl-9 pr-4 py-2 bg-panel-bg border border-panel-border rounded-lg text-sm text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500"
            />
          </div>
        </div>

        {loading ? (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {[1, 2, 3, 4].map((i) => (
              <div key={i} className="h-44 bg-panel-bg/50 border border-panel-border rounded-lg animate-pulse" />
            ))}
          </div>
        ) : filtered.length === 0 ? (
          <div className="text-center py-12">
            <FileCode2 size={40} className="mx-auto text-panel-muted mb-3" />
            <p className="text-panel-text font-medium">
              {search ? "No sites match your search" : "No WordPress sites installed"}
            </p>
            <p className="text-sm text-panel-muted mt-1">
              {search ? "Try a different search term." : "Install WordPress with one click, or run Rescan to pick up existing installs."}
            </p>
            {!search && (
              <Button size="sm" className="mt-4" onClick={() => setShowInstall(true)}>
                <Plus size={14} className="mr-1" /> Install WordPress
              </Button>
            )}
          </div>
        ) : (
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
            {filtered.map((site) => {
              const busy = rowSaving[site.id];
              return (
                <div
                  key={site.id}
                  className="bg-panel-bg border border-panel-border rounded-lg p-4 hover:border-brand-500/40 transition-colors"
                >
                  {/* Header — domain is the dominant identifier. */}
                  <div className="flex items-start justify-between gap-3 mb-3">
                    <div className="flex items-start gap-3 min-w-0 flex-1">
                      <div className="w-10 h-10 bg-blue-500/10 rounded-lg flex items-center justify-center shrink-0">
                        <FileCode2 size={20} className="text-blue-400" />
                      </div>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2 min-w-0">
                          <Globe size={14} className="text-panel-muted shrink-0" />
                          <h3 className="font-semibold text-white text-base truncate" title={site.domain}>
                            {site.domain}
                          </h3>
                        </div>
                        <p className="text-xs text-panel-muted font-mono mt-0.5 truncate">
                          {site.domain}
                          {site.path ? `/${site.path.replace(/^\//, "")}` : "/"}
                        </p>
                      </div>
                    </div>
                    <StatusBadge status={site.maintenance_mode ? "warning" : "active"} />
                  </div>

                  {/* Metadata strip */}
                  <div className="grid grid-cols-2 gap-x-4 gap-y-1.5 text-xs mb-3">
                    <div className="flex items-center justify-between">
                      <span className="text-panel-muted">WP Version</span>
                      <code className="text-panel-text font-mono">v{site.version || "?"}</code>
                    </div>
                    <div className="flex items-center justify-between">
                      <span className="text-panel-muted">Last Updated</span>
                      <span className="text-panel-text">
                        {site.updated_at ? new Date(site.updated_at).toLocaleDateString() : "—"}
                      </span>
                    </div>
                  </div>

                  {/* Pills — both togglable in-place */}
                  <div className="flex flex-wrap items-center gap-2 mb-3">
                    {pill("Auto-update", site.auto_update, "green", () => toggleAutoUpdate(site), busy === "auto_update")}
                    {pill("Maintenance", site.maintenance_mode, "amber", () => toggleMaintenance(site), busy === "maintenance")}
                  </div>

                  {/* Actions */}
                  <div className="flex items-center flex-wrap gap-1 pt-3 border-t border-panel-border">
                    <button
                      onClick={() => window.open(site.site_url || `https://${site.domain}${site.path ? "/" + site.path : ""}`, "_blank", "noopener,noreferrer")}
                      className="p-1.5 rounded text-panel-muted hover:text-brand-400 hover:bg-panel-surface transition-colors"
                      title="Open site"
                    >
                      <ExternalLink size={15} />
                    </button>
                    <button
                      onClick={() => handleAutoLogin(site)}
                      disabled={busy === "login"}
                      className="p-1.5 rounded text-panel-muted hover:text-emerald-400 hover:bg-panel-surface transition-colors disabled:opacity-50"
                      title="One-click WP Admin login"
                    >
                      <LogIn size={15} />
                    </button>
                    <button
                      onClick={() => handleUpdate(site)}
                      disabled={busy === "update"}
                      className="p-1.5 rounded text-panel-muted hover:text-blue-400 hover:bg-panel-surface transition-colors disabled:opacity-50"
                      title="Update WordPress core"
                    >
                      <RotateCw size={15} className={busy === "update" ? "animate-spin" : ""} />
                    </button>
                    <button
                      onClick={() => openUsers(site)}
                      className="p-1.5 rounded text-panel-muted hover:text-purple-400 hover:bg-panel-surface transition-colors"
                      title="Manage WP users"
                    >
                      <Users size={15} />
                    </button>
                    <button
                      onClick={() => openPlugins(site)}
                      className="p-1.5 rounded text-panel-muted hover:text-indigo-400 hover:bg-panel-surface transition-colors"
                      title="Manage plugins & themes"
                    >
                      <Puzzle size={15} />
                    </button>
                    <button
                      onClick={() => runSecurityScan(site)}
                      className="p-1.5 rounded text-panel-muted hover:text-amber-400 hover:bg-panel-surface transition-colors"
                      title="Run security scan"
                    >
                      <ShieldCheck size={15} />
                    </button>
                    <div className="flex-1" />
                    <button
                      onClick={() => detachSite(site)}
                      className="p-1.5 rounded text-panel-muted hover:text-sky-400 hover:bg-panel-surface transition-colors"
                      title="Detach (stop managing, keep files)"
                    >
                      <Unlink size={15} />
                    </button>
                    <button
                      onClick={() => handleDelete(site)}
                      className="p-1.5 rounded text-panel-muted hover:text-red-400 hover:bg-panel-surface transition-colors"
                      title="Delete site (removes files + database)"
                    >
                      <Trash2 size={15} />
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </Card>

      {/* ─────────────────────────── Install wizard ─────────────────────────── */}
      <Modal isOpen={showInstall} onClose={() => setShowInstall(false)} title="Install WordPress" size="lg">
        <form onSubmit={handleInstall} className="space-y-4">
          <div>
            <label className={labelClass}>Site Title *</label>
            <input
              type="text"
              required
              placeholder="My WordPress Site"
              value={form.site_title}
              onChange={(e) => setForm({ ...form, site_title: e.target.value })}
              className={inputClass}
            />
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Domain *</label>
              <select required value={form.domain} onChange={(e) => setForm({ ...form, domain: e.target.value })} className={selectClass}>
                <option value="">Select a domain…</option>
                {domains.map((d) => (
                  <option key={d.id} value={d.domain}>
                    {d.domain}
                  </option>
                ))}
              </select>
              {domains.length === 0 && (
                <p className="text-[11px] text-amber-400 mt-1">No active domains yet — add one on the Domains page first.</p>
              )}
            </div>
            <div>
              <label className={labelClass}>Install Path</label>
              <div className="flex items-center gap-2">
                <span className="text-xs text-panel-muted font-mono whitespace-nowrap truncate">
                  {form.domain || "example.com"}/
                </span>
                <input
                  type="text"
                  placeholder="(document root)"
                  value={form.path}
                  onChange={(e) => setForm({ ...form, path: e.target.value })}
                  className={inputClass}
                />
              </div>
              <p className="text-[11px] text-panel-muted mt-1">Leave empty to install in the document root. e.g. "blog"</p>
            </div>
          </div>

          {checkingConflict && (
            <p className="text-[11px] text-panel-muted">Checking for conflicts…</p>
          )}
          {conflict && (
            <div className="flex items-start gap-2 p-3 bg-amber-500/10 border border-amber-500/30 rounded-lg">
              <AlertTriangle size={16} className="text-amber-400 shrink-0 mt-0.5" />
              <p className="text-sm text-amber-300">{conflict}</p>
            </div>
          )}

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>WordPress Version</label>
              <select value={form.version} onChange={(e) => setForm({ ...form, version: e.target.value })} className={selectClass}>
                {WP_VERSIONS.map((v) => (
                  <option key={v} value={v}>
                    {v === "latest" ? "Latest (recommended)" : v}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label className={labelClass}>Locale</label>
              <select value={form.locale} onChange={(e) => setForm({ ...form, locale: e.target.value })} className={selectClass}>
                {WP_LOCALES.map((l) => (
                  <option key={l.code} value={l.code}>
                    {l.label}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Admin Username *</label>
              <input
                type="text"
                required
                placeholder="admin"
                value={form.admin_user}
                onChange={(e) => setForm({ ...form, admin_user: e.target.value })}
                className={inputClass}
              />
            </div>
            <div>
              <label className={labelClass}>Admin Email *</label>
              <input
                type="email"
                required
                placeholder="admin@example.com"
                value={form.admin_email}
                onChange={(e) => setForm({ ...form, admin_email: e.target.value })}
                className={inputClass}
              />
            </div>
          </div>

          <div>
            <label className={labelClass}>Admin Password *</label>
            <div className="flex items-stretch gap-2">
              <input
                type="text"
                required
                minLength={8}
                placeholder="Min. 8 characters"
                value={form.admin_pass}
                onChange={(e) => setForm({ ...form, admin_pass: e.target.value })}
                className={`${inputClass} font-mono`}
              />
              <button
                type="button"
                onClick={() => setForm((f) => ({ ...f, admin_pass: generatePassword() }))}
                className="shrink-0 inline-flex items-center gap-1.5 px-3 py-2 border border-panel-border rounded-lg text-sm text-panel-text hover:bg-panel-surface transition-colors"
                title="Generate a strong password"
              >
                <Wand2 size={14} /> Generate
              </button>
            </div>
            <p className="text-[11px] text-panel-muted mt-1">Shown in plaintext so you can copy it now — it will not be retrievable later.</p>
          </div>

          <label className="flex items-start gap-3 p-3 bg-panel-bg border border-panel-border rounded-lg cursor-pointer hover:border-panel-border/60">
            <input
              type="checkbox"
              checked={form.auto_update}
              onChange={(e) => setForm({ ...form, auto_update: e.target.checked })}
              className="mt-0.5 h-4 w-4 rounded border-panel-border bg-panel-bg text-brand-600 focus:ring-brand-500/40"
            />
            <div className="flex-1">
              <div className="text-sm font-medium text-panel-text">Enable WordPress core auto-updates</div>
              <p className="text-xs text-panel-muted mt-0.5">
                Automatically install minor core releases (security & maintenance). You can change this later from the site card.
              </p>
            </div>
          </label>

          <div className="flex justify-end gap-3 pt-2">
            <Button variant="secondary" type="button" onClick={() => setShowInstall(false)}>
              Cancel
            </Button>
            <Button type="submit" loading={submitting} disabled={!!conflict || domains.length === 0}>
              Install WordPress
            </Button>
          </div>
        </form>
      </Modal>

      {/* ─────────────────────────── Users modal ─────────────────────────── */}
      <Modal
        isOpen={showUsers}
        onClose={() => {
          setShowUsers(false);
          setShowAddUser(false);
        }}
        title={usersSite ? `WordPress users — ${usersSite.domain}` : "WordPress users"}
        size="lg"
      >
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <p className="text-sm text-panel-muted">
              {wpUsers.length} user{wpUsers.length !== 1 ? "s" : ""}
            </p>
            <Button size="sm" onClick={() => setShowAddUser((v) => !v)}>
              <UserPlus size={14} className="mr-1" />
              {showAddUser ? "Close" : "Add User"}
            </Button>
          </div>

          {showAddUser && (
            <form onSubmit={handleCreateUser} className="p-3 bg-panel-bg border border-panel-border rounded-lg space-y-3">
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className={labelClass}>Username *</label>
                  <input
                    type="text"
                    required
                    placeholder="johndoe"
                    value={userForm.username}
                    onChange={(e) => setUserForm({ ...userForm, username: e.target.value })}
                    className={inputClass}
                  />
                </div>
                <div>
                  <label className={labelClass}>Email *</label>
                  <input
                    type="email"
                    required
                    placeholder="john@example.com"
                    value={userForm.email}
                    onChange={(e) => setUserForm({ ...userForm, email: e.target.value })}
                    className={inputClass}
                  />
                </div>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className={labelClass}>Password *</label>
                  <input
                    type="password"
                    required
                    minLength={6}
                    placeholder="Min. 6 characters"
                    value={userForm.password}
                    onChange={(e) => setUserForm({ ...userForm, password: e.target.value })}
                    className={inputClass}
                  />
                </div>
                <div>
                  <label className={labelClass}>Role</label>
                  <select
                    value={userForm.role}
                    onChange={(e) => setUserForm({ ...userForm, role: e.target.value })}
                    className={selectClass}
                  >
                    {WP_ROLES.map((r) => (
                      <option key={r} value={r}>
                        {r.charAt(0).toUpperCase() + r.slice(1)}
                      </option>
                    ))}
                  </select>
                </div>
              </div>
              <div className="flex justify-end gap-2">
                <Button size="sm" variant="secondary" type="button" onClick={() => setShowAddUser(false)}>
                  Cancel
                </Button>
                <Button size="sm" type="submit" loading={creatingUser}>
                  Create User
                </Button>
              </div>
            </form>
          )}

          {loadingUsers ? (
            <div className="space-y-2">
              {[1, 2, 3].map((i) => (
                <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
              ))}
            </div>
          ) : wpUsers.length > 0 ? (
            <div className="divide-y divide-panel-border border border-panel-border rounded-lg overflow-hidden">
              {wpUsers.map((u) => (
                <div key={u.ID} className="flex items-center justify-between p-3 bg-panel-surface hover:bg-panel-bg transition-colors">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 min-w-0">
                      <span className="font-medium text-sm text-panel-text truncate">{u.user_login}</span>
                      <span className="text-xs text-panel-muted truncate">{u.user_email}</span>
                    </div>
                    {u.display_name && u.display_name !== u.user_login && (
                      <p className="text-[11px] text-panel-muted mt-0.5 truncate">{u.display_name}</p>
                    )}
                  </div>
                  <div className="flex items-center gap-2 ml-3 shrink-0">
                    <select
                      value={u.roles || "subscriber"}
                      onChange={(e) => handleChangeRole(u.ID, e.target.value)}
                      className="px-2 py-1 bg-panel-bg border border-panel-border rounded text-xs text-panel-text focus:outline-none focus:ring-1 focus:ring-brand-500"
                    >
                      {WP_ROLES.map((r) => (
                        <option key={r} value={r}>
                          {r.charAt(0).toUpperCase() + r.slice(1)}
                        </option>
                      ))}
                    </select>
                    {u.ID !== "1" && (
                      <button
                        onClick={() => handleDeleteUser(u)}
                        className="p-1 rounded hover:bg-red-500/10 text-panel-muted hover:text-red-400 transition-colors"
                        title="Delete user"
                      >
                        <Trash2 size={12} />
                      </button>
                    )}
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-center text-panel-muted text-sm py-6">No users found</p>
          )}
        </div>
      </Modal>

      {/* ─────────────── Plugins + Themes modal (tabbed) ─────────────── */}
      <Modal
        isOpen={showPlugins}
        onClose={() => setShowPlugins(false)}
        title={pluginsSite ? `${pluginsSite.domain}` : "Plugins & Themes"}
        size="lg"
      >
        <div className="space-y-4">
          {/* Tabs — keep plugin and theme management in the same surface
              (WP Toolkit pattern). Switching to Themes triggers a lazy
              fetch via the useEffect above. */}
          <div className="flex border-b border-panel-border">
            {(["plugins", "themes"] as const).map((t) => (
              <button
                key={t}
                type="button"
                onClick={() => setPluginsTab(t)}
                className={`px-4 py-2 text-sm font-medium capitalize -mb-px border-b-2 transition-colors ${
                  pluginsTab === t
                    ? "border-blue-500 text-blue-400"
                    : "border-transparent text-panel-muted hover:text-panel-text"
                }`}
              >
                {t}
              </button>
            ))}
          </div>

          {pluginsTab === "plugins" && (
            <>
              <div className="flex items-center gap-2">
                <form onSubmit={handleInstallPlugin} className="flex items-center gap-2 flex-1">
                  <input
                    type="text"
                    placeholder="Plugin slug (e.g. wordpress-seo, woocommerce)"
                    value={newPluginSlug}
                    onChange={(e) => setNewPluginSlug(e.target.value)}
                    className={inputClass}
                  />
                  <Button size="sm" type="submit" loading={installingPlugin}>
                    <Plus size={14} className="mr-1" /> Install
                  </Button>
                </form>
                {plugins.some((p) => p.update_available) && (
                  <Button size="sm" variant="secondary" onClick={updateAllPlugins} loading={assetBusy["plugin:*"] === "update"}>
                    <RefreshCw size={14} className="mr-1" /> Update All
                  </Button>
                )}
              </div>
              <p className="text-[11px] text-panel-muted">
                Find plugin slugs on wordpress.org — the slug is the last segment of the plugin URL.
              </p>

              {loadingPlugins ? (
                <div className="space-y-2">
                  {[1, 2, 3, 4].map((i) => (
                    <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
                  ))}
                </div>
              ) : plugins.length > 0 ? (
                <div className="divide-y divide-panel-border border border-panel-border rounded-lg overflow-hidden">
                  {plugins.map((p) => {
                    const busy = assetBusy[`plugin:${p.name}`];
                    const isActive = p.status === "active";
                    return (
                      <div key={p.name} className="flex items-center justify-between gap-3 p-3 bg-panel-surface">
                        <div className="flex items-center gap-3 min-w-0 flex-1">
                          <Puzzle size={14} className="text-indigo-400 shrink-0" />
                          <div className="min-w-0">
                            <div className="text-sm font-medium text-panel-text truncate">{p.name}</div>
                            <div className="text-[11px] text-panel-muted">
                              v{p.version || "?"}
                              {p.update_available && (
                                <span className="ml-2 text-amber-400">• update available</span>
                              )}
                            </div>
                          </div>
                        </div>
                        <div className="flex items-center gap-2 shrink-0">
                          <StatusBadge status={isActive ? "active" : p.status === "inactive" ? "inactive" : "warning"} />
                          {p.update_available && (
                            <Button size="sm" variant="ghost" onClick={() => pluginAction(p.name, "update")} loading={busy === "update"} title="Update plugin">
                              <RefreshCw size={13} />
                            </Button>
                          )}
                          {isActive ? (
                            <Button size="sm" variant="ghost" onClick={() => pluginAction(p.name, "deactivate")} loading={busy === "deactivate"} title="Deactivate">
                              <Power size={13} />
                            </Button>
                          ) : (
                            <Button size="sm" variant="ghost" onClick={() => pluginAction(p.name, "activate")} loading={busy === "activate"} title="Activate">
                              <Power size={13} />
                            </Button>
                          )}
                          <Button size="sm" variant="ghost" onClick={() => pluginAction(p.name, "delete")} loading={busy === "delete"} title="Delete">
                            <Trash2 size={13} className="text-red-400" />
                          </Button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              ) : (
                <p className="text-center text-panel-muted text-sm py-6">No plugins installed yet</p>
              )}
            </>
          )}

          {pluginsTab === "themes" && (
            <>
              <div className="flex items-center gap-2">
                <form onSubmit={handleInstallTheme} className="flex items-center gap-2 flex-1">
                  <input
                    type="text"
                    placeholder="Theme slug (e.g. twentytwentyfour, astra)"
                    value={newThemeSlug}
                    onChange={(e) => setNewThemeSlug(e.target.value)}
                    className={inputClass}
                  />
                  <Button size="sm" type="submit" loading={installingTheme}>
                    <Plus size={14} className="mr-1" /> Install
                  </Button>
                </form>
                {themes.some((t) => t.update_available) && (
                  <Button size="sm" variant="secondary" onClick={updateAllThemes} loading={assetBusy["theme:*"] === "update"}>
                    <RefreshCw size={14} className="mr-1" /> Update All
                  </Button>
                )}
              </div>
              <p className="text-[11px] text-panel-muted">
                Installing a theme doesn't activate it — use the activate button to switch the site over.
              </p>

              {loadingThemes ? (
                <div className="space-y-2">
                  {[1, 2, 3, 4].map((i) => (
                    <div key={i} className="h-12 bg-panel-border/20 rounded animate-pulse" />
                  ))}
                </div>
              ) : themes.length > 0 ? (
                <div className="divide-y divide-panel-border border border-panel-border rounded-lg overflow-hidden">
                  {themes.map((t) => {
                    const busy = assetBusy[`theme:${t.name}`];
                    const isActive = t.status === "active";
                    return (
                      <div key={t.name} className="flex items-center justify-between gap-3 p-3 bg-panel-surface">
                        <div className="flex items-center gap-3 min-w-0 flex-1">
                          <Sparkles size={14} className="text-purple-400 shrink-0" />
                          <div className="min-w-0">
                            <div className="text-sm font-medium text-panel-text truncate">{t.name}</div>
                            <div className="text-[11px] text-panel-muted">
                              v{t.version || "?"}
                              {t.update_available && (
                                <span className="ml-2 text-amber-400">• update available</span>
                              )}
                            </div>
                          </div>
                        </div>
                        <div className="flex items-center gap-2 shrink-0">
                          <StatusBadge status={isActive ? "active" : t.status === "parent" ? "inactive" : "inactive"} />
                          {t.update_available && (
                            <Button size="sm" variant="ghost" onClick={() => themeAction(t.name, "update")} loading={busy === "update"} title="Update theme">
                              <RefreshCw size={13} />
                            </Button>
                          )}
                          {!isActive && (
                            <Button size="sm" variant="ghost" onClick={() => themeAction(t.name, "activate")} loading={busy === "activate"} title="Activate">
                              <Power size={13} />
                            </Button>
                          )}
                          {!isActive && (
                            <Button size="sm" variant="ghost" onClick={() => themeAction(t.name, "delete")} loading={busy === "delete"} title="Delete">
                              <Trash2 size={13} className="text-red-400" />
                            </Button>
                          )}
                        </div>
                      </div>
                    );
                  })}
                </div>
              ) : (
                <p className="text-center text-panel-muted text-sm py-6">No themes installed yet</p>
              )}
            </>
          )}
        </div>
      </Modal>

      {/* ────────────────────── Security scan result modal ────────────────────── */}
      <Modal
        isOpen={showScan}
        onClose={() => setShowScan(false)}
        title={scanSite ? `Security scan — ${scanSite.domain}` : "Security scan"}
        size="lg"
      >
        <div className="space-y-4">
          {scanning ? (
            <div className="py-8 text-center">
              <div className="inline-flex items-center gap-2 text-panel-muted text-sm">
                <RefreshCw size={16} className="animate-spin" />
                Running security audit…
              </div>
            </div>
          ) : scanResult ? (
            <>
              <div
                className={`p-4 rounded-lg border flex items-center justify-between ${
                  scanResult.overall_status === "pass" || scanResult.overall_status === "ok"
                    ? "bg-green-500/10 border-green-500/30 text-green-300"
                    : scanResult.overall_status === "warn" || scanResult.overall_status === "warning"
                    ? "bg-amber-500/10 border-amber-500/30 text-amber-300"
                    : "bg-red-500/10 border-red-500/30 text-red-300"
                }`}
              >
                <div>
                  <div className="text-xs uppercase tracking-wide opacity-80">Overall status</div>
                  <div className="text-lg font-semibold">{(scanResult.overall_status || "unknown").toUpperCase()}</div>
                </div>
                <div className="text-right">
                  <div className="text-xs uppercase tracking-wide opacity-80">Scanned</div>
                  <div className="text-sm">
                    {scanResult.scanned_at ? new Date(scanResult.scanned_at).toLocaleString() : "just now"}
                  </div>
                </div>
              </div>

              <div className="space-y-2">
                {(scanResult.checks || []).map((c, idx) => (
                  <div key={idx} className="flex items-start gap-3 p-3 bg-panel-bg border border-panel-border rounded-lg">
                    {scanIcon(c.status)}
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center justify-between gap-2">
                        <span className="text-sm font-medium text-panel-text">{c.name}</span>
                        <span
                          className={`text-[11px] uppercase tracking-wide font-semibold ${
                            c.status === "pass" || c.status === "ok"
                              ? "text-green-400"
                              : c.status === "warn" || c.status === "warning"
                              ? "text-amber-400"
                              : "text-red-400"
                          }`}
                        >
                          {c.status}
                        </span>
                      </div>
                      <p className="text-xs text-panel-muted mt-0.5">{c.message}</p>
                      {c.details && c.details.length > 0 && (
                        <ul className="mt-2 space-y-0.5 text-[11px] text-panel-muted list-disc list-inside">
                          {c.details.map((d, i) => (
                            <li key={i} className="font-mono">{d}</li>
                          ))}
                        </ul>
                      )}
                    </div>
                  </div>
                ))}
                {(!scanResult.checks || scanResult.checks.length === 0) && (
                  <p className="text-center text-panel-muted text-sm py-6">Scan returned no checks.</p>
                )}
              </div>

              <div className="flex justify-end gap-2 pt-2">
                <Button size="sm" variant="secondary" onClick={() => scanSite && runSecurityScan(scanSite)}>
                  <RefreshCw size={14} className="mr-1" />
                  Re-run scan
                </Button>
                <Button size="sm" onClick={() => setShowScan(false)}>
                  Close
                </Button>
              </div>
            </>
          ) : (
            <p className="text-center text-panel-muted text-sm py-6">No scan result</p>
          )}
        </div>
      </Modal>
    </div>
  );
}
