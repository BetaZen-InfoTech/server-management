import { useState, useEffect } from "react";
import { useNavigate, useLocation, Outlet } from "react-router-dom";
import { Sidebar, TopBar, OfflineOverlay } from "@serverpanel/ui";
import type { SidebarItem } from "@serverpanel/ui";
import { useAuthStore } from "@/store/auth";
import { apiClient } from "@serverpanel/api-client";
import {
  LayoutDashboard, Globe, AppWindow, Database, Mail, Globe2,
  ShieldCheck, Archive, Blocks, Flame, Package, Activity,
  FileText, Clock, FolderOpen, Key, Cpu, HardDrive,
  Bell, ClipboardList, Settings, Wrench, Users,
  TerminalSquare, Box, Server, ArrowLeftRight, Building2, Rocket, Gauge,
  FileCode2, Power, RotateCcw, UserCircle, BarChart3, Code2, BookOpen, Mails, Cloud
} from "lucide-react";

interface NavItem extends SidebarItem {
  adminOnly?: boolean;
}

// adminOnly = "platform owner" tier. Hidden from vendors and the team
// roles that may legitimately reach WHM (legacy seeds with the wrong
// role). The backend tightens the matching routes too — the sidebar
// hide is UX, not a security boundary.
//
// `section` groups related items under a tiny uppercase header in the
// sidebar. The Sidebar component's built-in search filters across both
// label AND section, so typing "files" matches every item in that group.
const navItems: NavItem[] = [
  // Overview — single home pane.
  { section: "Overview", label: "Dashboard", icon: <LayoutDashboard size={18} />, path: "/dashboard" },

  // Hosting — the day-to-day tenant resources every admin manages.
  { section: "Hosting", label: "Domains", icon: <Globe size={18} />, path: "/domains" },
  { section: "Hosting", label: "Packages", icon: <Box size={18} />, path: "/packages" },
  { section: "Hosting", label: "Applications", icon: <AppWindow size={18} />, path: "/apps" },
  { section: "Hosting", label: "WordPress", icon: <Blocks size={18} />, path: "/wordpress" },
  { section: "Hosting", label: "Databases", icon: <Database size={18} />, path: "/databases" },
  { section: "Hosting", label: "Email", icon: <Mail size={18} />, path: "/email" },

  // Network — DNS / TLS belong together; the operator usually edits them
  // in the same sitting (zone update + cert reissue).
  { section: "Network", label: "DNS Zones", icon: <Globe2 size={18} />, path: "/dns" },
  // Cloudflare — centralized account config + (later) zone/DNS sync. Owner-
  // only; the backend gates every write on server.manage.
  { section: "Network", label: "Cloudflare", icon: <Cloud size={18} />, path: "/cloudflare", adminOnly: true },
  { section: "Network", label: "SSL/TLS", icon: <ShieldCheck size={18} />, path: "/ssl" },
  { section: "Network", label: "Firewall", icon: <Flame size={18} />, path: "/firewall", adminOnly: true },

  // Files & Code — anything the operator opens to push or inspect code.
  { section: "Files & Code", label: "File Manager", icon: <FolderOpen size={18} />, path: "/files" },
  { section: "Files & Code", label: "Deploy Software", icon: <Rocket size={18} />, path: "/deploy-software", adminOnly: true },
  { section: "Files & Code", label: "Cron Jobs", icon: <Clock size={18} />, path: "/cron" },
  { section: "Files & Code", label: "SSH Keys", icon: <Key size={18} />, path: "/ssh-keys" },
  { section: "Files & Code", label: "Terminal", icon: <TerminalSquare size={18} />, path: "/terminal" },

  // Server — host-level visibility + maintenance windows.
  { section: "Server", label: "Monitoring", icon: <Activity size={18} />, path: "/monitoring" },
  { section: "Server", label: "Reports", icon: <BarChart3 size={18} />, path: "/reports" },
  { section: "Server", label: "Server Information", icon: <Server size={18} />, path: "/server-info", adminOnly: true },
  { section: "Server", label: "Service Status", icon: <Gauge size={18} />, path: "/service-status", adminOnly: true },
  // Mail Issues — single-page diagnostic + 1-click auto-heal for the
  // Dovecot + Postfix + OpenDKIM stack. Surfaces the same checks
  // scripts/_diag_mail_stack.py runs, with per-row Symptom + step-by-
  // step Resolution + a "Common Symptoms → Resolution" knowledge
  // base table at the bottom.
  { section: "Server", label: "Mail Issues", icon: <Wrench size={18} />, path: "/mail-issues", adminOnly: true },
  { section: "Server", label: "Logs", icon: <FileText size={18} />, path: "/logs" },
  { section: "Server", label: "Processes", icon: <Cpu size={18} />, path: "/processes", adminOnly: true },
  { section: "Server", label: "Resources", icon: <HardDrive size={18} />, path: "/resources", adminOnly: true },
  { section: "Server", label: "Software", icon: <Package size={18} />, path: "/software", adminOnly: true },
  { section: "Server", label: "Backups", icon: <Archive size={18} />, path: "/backups" },
  { section: "Server", label: "Transfer", icon: <ArrowLeftRight size={18} />, path: "/transfer", adminOnly: true },
  // Transfer Tokens — minted on THIS server when it's the source of a
  // migration. The destination panel pastes them in step 1 of its
  // wizard so the destination never sees this server's root password.
  { section: "Server", label: "Transfer Tokens", icon: <Key size={18} />, path: "/transfer-tokens", adminOnly: true },
  { section: "Server", label: "Maintenance", icon: <Wrench size={18} />, path: "/maintenance", adminOnly: true },
  // WHM-style host management. Hostname + reboots sit in Server because
  // they affect the whole machine; database/PHP editors live in
  // Databases/Files & Code via cross-links from their respective groups.
  { section: "Server", label: "Change Hostname", icon: <Globe2 size={18} />, path: "/change-hostname", adminOnly: true },
  { section: "Server", label: "Graceful Reboot", icon: <RotateCcw size={18} />, path: "/reboot/graceful", adminOnly: true },
  { section: "Server", label: "Forceful Reboot", icon: <Power size={18} />, path: "/reboot/forceful", adminOnly: true },

  // Databases — advanced admin knobs that mirror WHM's Database Services.
  { section: "Hosting", label: "Edit DB Configuration", icon: <Database size={18} />, path: "/edit-db-config", adminOnly: true },
  { section: "Hosting", label: "MongoDB", icon: <Database size={18} />, path: "/edit-mongo-config", adminOnly: true },
  { section: "Hosting", label: "Repair Databases", icon: <Wrench size={18} />, path: "/repair-databases", adminOnly: true },

  // Files & Code — per-PHP-version php.ini editor sits next to Software.
  { section: "Files & Code", label: "MultiPHP INI Editor", icon: <FileCode2 size={18} />, path: "/multiphp-ini", adminOnly: true },

  // Admin — shell access + bandwidth caps live with the rest of the
  // per-user admin tools.
  { section: "Admin", label: "Shell Access", icon: <TerminalSquare size={18} />, path: "/shell-access", adminOnly: true },
  { section: "Admin", label: "Bandwidth Limit", icon: <Gauge size={18} />, path: "/bandwidth-limit", adminOnly: true },

  // Admin — observability + access control. Audit + Notifications live
  // here so they're one click from the rest of the admin tooling.
  { section: "Admin", label: "My Profile", icon: <UserCircle size={18} />, path: "/profile" },
  { section: "Admin", label: "Notifications", icon: <Bell size={18} />, path: "/notifications" },
  { section: "Admin", label: "Audit Log", icon: <ClipboardList size={18} />, path: "/audit" },
  { section: "Admin", label: "Vendors", icon: <Building2 size={18} />, path: "/vendors", adminOnly: true },
  { section: "Admin", label: "Users & RBAC", icon: <Users size={18} />, path: "/users" },
  { section: "Admin", label: "Configuration", icon: <Settings size={18} />, path: "/config", adminOnly: true },
  { section: "Admin", label: "Server Settings", icon: <Server size={18} />, path: "/server-settings", adminOnly: true },

  // Developer — programmatic API tokens + outbound webhooks. Lives in its
  // own section because the surface is meaningfully distinct from per-tenant
  // admin: it controls how external systems integrate with the panel.
  { section: "Developer", label: "API & Webhooks", icon: <Code2 size={18} />, path: "/developer" },
  // Mail Suite — admin surface for the separate mail-suite product.
  // Owner-only on the API; we surface the nav for all admin roles and
  // let the backend gate.
  { section: "Developer", label: "Mail Suite", icon: <Mails size={18} />, path: "/mail-suite", adminOnly: true },
  // Owner-facing how-to guide: vendor management, server settings,
  // transfers, monitoring. Different content from the vendor /help in
  // user-panel — same component shape, different audience.
  { section: "Developer", label: "Owner Guide", icon: <BookOpen size={18} />, path: "/help" },
  // External-link sentinel — onNavigate below intercepts paths
  // starting with "/docs/" and opens them in a new tab instead of
  // running them through React Router (which would 404).
  { section: "Developer", label: "API Docs", icon: <BookOpen size={18} />, path: "/docs/api/" },
];

export default function DashboardLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const { user, logout } = useAuthStore();
  const [serverIP, setServerIP] = useState("");
  const [versionName, setVersionName] = useState("");
  const [versionNumber, setVersionNumber] = useState("");
  // Branded panel name + logo, fetched from the public branding endpoint.
  // Defaults match the previous hardcoded chrome strings so the experience
  // pre-3.0.32 (no branding doc in mongo) is bit-identical.
  const [brandName, setBrandName] = useState("Betazen Server Panel");
  const [brandLogo, setBrandLogo] = useState("");
  // Mobile drawer state — false closes the off-canvas Sidebar, the
  // hamburger in TopBar opens it. Auto-closed by the Sidebar component
  // whenever a nav item is tapped (see handleNavigate inside Sidebar).
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  // Close the drawer whenever the route changes (covers the case where
  // navigation happened via something OTHER than a nav-row click).
  useEffect(() => {
    setMobileNavOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    apiClient.get("/api/v1/whm/monitor/system").then((res) => {
      setServerIP(res.data?.data?.ip || "");
    }).catch(() => {});
    // /version is a public endpoint — no auth required, so the topbar
    // renders the product name/number even before login drives a refetch.
    apiClient.get("/api/v1/version").then((res) => {
      const d = res.data?.data || {};
      setVersionName(d.name || "");
      setVersionNumber(d.version || "");
    }).catch(() => {});
    // Branding (logo + favicon + panel name) — public, swaps the chrome
    // and the browser tab favicon to whatever the operator uploaded in
    // Server Settings → Branding. Falls back to the bundled defaults.
    apiClient.get("/api/v1/branding").then((res) => {
      const d = res.data?.data || {};
      const name = d.panel_name || "Betazen Server Panel";
      setBrandName(name);
      setBrandLogo(d.logo_data_url || "");
      // Browser tab title + favicon. document.title swaps trivially;
      // favicon needs a <link> swap because the index.html doesn't ship
      // one (we inject at runtime so the operator-uploaded image wins).
      document.title = name + " — WHM";
      if (d.favicon_data_url) {
        let link = document.querySelector<HTMLLinkElement>("link[rel='icon']");
        if (!link) {
          link = document.createElement("link");
          link.rel = "icon";
          document.head.appendChild(link);
        }
        link.href = d.favicon_data_url;
      }
    }).catch(() => {});
  }, []);

  const handleLogout = async () => {
    const refreshToken = localStorage.getItem("refresh_token");
    if (refreshToken) {
      try {
        await apiClient.post("/api/v1/auth/logout", { refresh_token: refreshToken });
      } catch {}
    }
    logout();
    navigate("/login");
  };

  const pageTitle = navItems.find((item) => location.pathname.startsWith(item.path))?.label ?? "Dashboard";

  // Canonical role for the platform owner is "vendor_owner". The
  // legacy "admin" string still appears in some seeded DB rows so we
  // accept it as an alias. server.manage permission is the
  // belt-and-braces fallback for accounts whose role is something
  // odd but who were granted the perm explicitly.
  const isAdmin =
    user?.role === "vendor_owner" ||
    user?.role === "admin" ||
    (user?.permissions?.includes("server.manage") ?? false);
  const visibleItems: SidebarItem[] = navItems
    .filter((item) => !item.adminOnly || isAdmin)
    .map((item): SidebarItem => ({ label: item.label, icon: item.icon, path: item.path, badge: item.badge, section: item.section }));

  return (
    <div className="flex h-screen overflow-hidden">
      {/* OfflineOverlay paints a full-screen modal block whenever
          navigator.onLine flips false — sits at z-[60] above the
          mobile drawer / Modal so nothing in the panel can be
          interacted with while the network is gone. Cheap when
          online (returns null). */}
      <OfflineOverlay />
      <Sidebar
        items={visibleItems}
        currentPath={location.pathname}
        onNavigate={(path) => {
          // Sentinel: items pointing at /docs/* are static-served
          // outside the SPA. Open in a new tab so the operator
          // doesn't lose their place in the panel; React Router
          // would 404 on these anyway.
          if (path.startsWith("/docs/")) {
            window.open(path, "_blank", "noopener,noreferrer");
            return;
          }
          navigate(path);
        }}
        brand={`${brandName} WHM`}
        logo={brandLogo || undefined}
        mobileOpen={mobileNavOpen}
        onMobileClose={() => setMobileNavOpen(false)}
      />
      <div className="flex-1 flex flex-col overflow-hidden min-w-0">
        <TopBar
          title={pageTitle}
          userName={user?.name}
          serverIP={serverIP}
          versionName={versionName}
          versionNumber={versionNumber}
          onLogout={handleLogout}
          onProfileClick={() => navigate("/profile")}
          onMenuClick={() => setMobileNavOpen(true)}
        />
        <main className="flex-1 overflow-y-auto p-3 sm:p-6">
          <Outlet />
        </main>
        <footer className="px-4 sm:px-6 py-2 border-t border-panel-border bg-panel-surface/40 text-[11px] text-panel-muted flex items-center justify-between gap-3 flex-wrap">
          <span className="truncate">&copy; {new Date().getFullYear()} <a href="https://betazeninfotech.com" target="_blank" rel="noopener noreferrer" className="text-blue-400 hover:underline">betazeninfotech.com</a> &middot; All rights reserved</span>
          <span className="shrink-0">{brandName} v{versionNumber}</span>
        </footer>
      </div>
    </div>
  );
}
