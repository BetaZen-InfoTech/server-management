import React from "react";
import { Outlet, useLocation, useNavigate } from "react-router-dom";
import { Sidebar, TopBar } from "@serverpanel/ui";
import { useAuthStore } from "@/store/auth";
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
};

export default function DashboardLayout() {
  const location = useLocation();
  const navigate = useNavigate();
  const { user, logout } = useAuthStore();

  const currentTitle =
    Object.entries(pageTitles).find(([path]) =>
      location.pathname.startsWith(path)
    )?.[1] || "Dashboard";

  const handleLogout = () => {
    logout();
    navigate("/login");
  };

  return (
    <div className="flex h-screen overflow-hidden">
      <Sidebar
        items={navItems}
        currentPath={location.pathname}
        onNavigate={(path) => navigate(path)}
        brand="ServerPanel"
      />
      <div className="flex-1 flex flex-col overflow-hidden">
        <TopBar
          title={currentTitle}
          userName={user?.name || "User"}
          onLogout={handleLogout}
        />
        <main className="flex-1 overflow-y-auto p-6 bg-panel-bg">
          <Outlet />
        </main>
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
