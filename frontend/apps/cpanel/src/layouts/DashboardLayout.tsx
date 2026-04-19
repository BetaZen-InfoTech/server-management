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
  GitBranch,
  TerminalSquare,
} from "lucide-react";

const navItems = [
  { label: "Dashboard", icon: <LayoutDashboard size={18} />, path: "/dashboard" },
  { label: "My Domains", icon: <Globe size={18} />, path: "/domains" },
  { label: "Applications", icon: <Rocket size={18} />, path: "/apps" },
  { label: "Databases", icon: <Database size={18} />, path: "/databases" },
  { label: "Email", icon: <Mail size={18} />, path: "/email" },
  { label: "DNS", icon: <Globe2 size={18} />, path: "/dns" },
  { label: "SSL/TLS", icon: <ShieldCheck size={18} />, path: "/ssl" },
  { label: "Backups", icon: <Archive size={18} />, path: "/backups" },
  { label: "WordPress", icon: <FileCode2 size={18} />, path: "/wordpress" },
  { label: "File Manager", icon: <FolderOpen size={18} />, path: "/files" },
  { label: "SSH Keys", icon: <Key size={18} />, path: "/ssh-keys" },
  { label: "Cron Jobs", icon: <Clock size={18} />, path: "/cron" },
  { label: "Deployments", icon: <GitBranch size={18} />, path: "/deployments" },
  { label: "Terminal", icon: <TerminalSquare size={18} />, path: "/terminal" },
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
  "/terminal": "Terminal",
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
        items={navItems}
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
