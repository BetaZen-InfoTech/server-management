import React, { useEffect, useState } from "react";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { Sidebar, TopBar } from "@serverpanel/ui";
import { useAuthStore } from "@/store/auth";
import { apiClient } from "@serverpanel/api-client";
import { Toaster } from "react-hot-toast";
import {
  LayoutDashboard,
  Globe,
  Rocket,
  Database,
  Mail,
  Globe2,
  ShieldCheck,
  Archive,
  FileCode2,
  FolderOpen,
  Key,
  Clock,
  TerminalSquare,
  Box,
  Users,
  Terminal,
  UserCircle,
} from "lucide-react";

interface NavItem {
  label: string;
  icon: React.ReactNode;
  path: string;
  section?: string;
  // When set, hide this item unless the current user holds the named
  // permission. Used for tenant-admin-only surfaces like the Team page.
  requirePerm?: string;
}

const navItems: NavItem[] = [
  { section: "Overview", label: "Dashboard", icon: <LayoutDashboard size={18} />, path: "/dashboard" },

  // Hosting — what a customer manages day-to-day for their sites.
  { section: "Hosting", label: "My Domains", icon: <Globe size={18} />, path: "/domains" },
  { section: "Hosting", label: "Applications", icon: <Rocket size={18} />, path: "/apps" },
  { section: "Hosting", label: "WordPress", icon: <FileCode2 size={18} />, path: "/wordpress" },
  { section: "Hosting", label: "Databases", icon: <Database size={18} />, path: "/databases" },
  { section: "Hosting", label: "Email", icon: <Mail size={18} />, path: "/email" },

  // Network — DNS + TLS sit together; people usually edit them as a pair.
  { section: "Network", label: "DNS", icon: <Globe2 size={18} />, path: "/dns" },
  { section: "Network", label: "SSL/TLS", icon: <ShieldCheck size={18} />, path: "/ssl" },

  // Files & Code — anything the operator opens to push or inspect code.
  // The legacy "Deployments" item (single-app GitHub connect, /deployments)
  // is intentionally NOT in this list anymore — Deploy Software is the
  // canonical project-level deploy flow now and Deployments only confused
  // vendors who saw two near-identical entry points.
  { section: "Files & Code", label: "File Manager", icon: <FolderOpen size={18} />, path: "/files" },
  { section: "Files & Code", label: "Deploy Software", icon: <Rocket size={18} />, path: "/deploy-software" },
  { section: "Files & Code", label: "Cron Jobs", icon: <Clock size={18} />, path: "/cron" },
  { section: "Files & Code", label: "SSH Keys", icon: <Key size={18} />, path: "/ssh-keys" },
  { section: "Files & Code", label: "Terminal", icon: <TerminalSquare size={18} />, path: "/terminal" },

  // Account — billing-ish stuff + the optional tenant-admin team page.
  { section: "Account", label: "Backups", icon: <Archive size={18} />, path: "/backups" },
  { section: "Account", label: "My Package", icon: <Box size={18} />, path: "/packages" },
  // Team = tenant-admin only. Hidden for staff / developer / support /
  // customer — the backend route is also gated on user.create so the
  // hide is UX, not a security boundary.
  { section: "Account", label: "Team", icon: <Users size={18} />, path: "/team", requirePerm: "user.create" },
  // Shell Access — toggle login shell (normal / jailed / disabled) for
  // team members. Same perm gate as Team; backend returns 403 for
  // anyone without user.create.
  { section: "Account", label: "Shell Access", icon: <Terminal size={18} />, path: "/shell-access", requirePerm: "user.create" },
  { section: "Account", label: "My Profile", icon: <UserCircle size={18} />, path: "/profile" },
];

const pageTitles: Record<string, string> = {
  "/dashboard": "Dashboard",
  "/domains": "My Domains",
  "/apps": "Applications",
  "/databases": "Databases",
  "/email": "Email Accounts",
  "/dns": "DNS Management",
  "/ssl": "SSL/TLS Certificates",
  "/backups": "Backups",
  "/wordpress": "WordPress Sites",
  "/files": "File Manager",
  "/ssh-keys": "SSH Keys",
  "/cron": "Cron Jobs",
  "/deployments": "Deployments",
  "/deploy-software": "Deploy Software",
  "/terminal": "Terminal",
  "/packages": "My Package",
  "/team": "My Team",
  "/shell-access": "Manage Shell Access",
  "/profile": "My Profile",
};

export default function DashboardLayout() {
  const location = useLocation();
  const navigate = useNavigate();
  const { user, logout } = useAuthStore();
  const [versionName, setVersionName] = useState("");
  const [versionNumber, setVersionNumber] = useState("");

  useEffect(() => {
    apiClient.get("/api/v1/version").then((res) => {
      const d = res.data?.data || {};
      setVersionName(d.name || "");
      setVersionNumber(d.version || "");
    }).catch(() => {});
  }, []);

  const currentTitle =
    Object.entries(pageTitles).find(([path]) =>
      location.pathname.startsWith(path)
    )?.[1] || "Dashboard";

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

  return (
    <div className="flex h-screen overflow-hidden">
      <Sidebar
        items={navItems.filter(
          (it) => !it.requirePerm || (user?.permissions || []).includes(it.requirePerm)
        )}
        currentPath={location.pathname}
        onNavigate={(path) => navigate(path)}
        brand="Betazen Server Panel"
      />
      <div className="flex-1 flex flex-col overflow-hidden">
        <TopBar
          title={currentTitle}
          userName={user?.name || "User"}
          versionName={versionName}
          versionNumber={versionNumber}
          onLogout={handleLogout}
          onProfileClick={() => navigate("/profile")}
        />
        <main className="flex-1 overflow-y-auto p-6 bg-panel-bg">
          <Outlet />
        </main>
        <footer className="px-6 py-2 border-t border-panel-border bg-panel-surface/40 text-[11px] text-panel-muted flex items-center justify-between">
          <span>&copy; {new Date().getFullYear()} <a href="https://betazeninfotech.com" target="_blank" rel="noopener noreferrer" className="text-blue-400 hover:underline">betazeninfotech.com</a> &middot; All rights reserved</span>
          <span>Betazen Server Panel v{versionNumber}</span>
        </footer>
      </div>
      <Toaster
        position="top-right"
        toastOptions={{
          style: {
            background: "#1e1e2e",
            color: "#cdd6f4",
            border: "1px solid #313244",
          },
        }}
      />
    </div>
  );
}
