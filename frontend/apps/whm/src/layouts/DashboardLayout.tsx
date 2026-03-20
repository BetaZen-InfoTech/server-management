import { useNavigate, useLocation, Outlet } from "react-router-dom";
import { Sidebar, TopBar } from "@serverpanel/ui";
import type { SidebarItem } from "@serverpanel/ui";
import { useAuthStore } from "@/store/auth";
import { apiClient } from "@serverpanel/api-client";
import {
  LayoutDashboard, Globe, AppWindow, Database, Mail, Globe2,
  ShieldCheck, Archive, Blocks, Flame, Package, Activity,
  FileText, Clock, FolderOpen, Key, Cpu, HardDrive,
  Bell, ClipboardList, Settings, Wrench, GitBranch, Users,
  TerminalSquare
} from "lucide-react";

const navItems: SidebarItem[] = [
  { label: "Dashboard", icon: <LayoutDashboard size={18} />, path: "/dashboard" },
  { label: "Domains", icon: <Globe size={18} />, path: "/domains" },
  { label: "Applications", icon: <AppWindow size={18} />, path: "/apps" },
  { label: "Databases", icon: <Database size={18} />, path: "/databases" },
  { label: "Email", icon: <Mail size={18} />, path: "/email" },
  { label: "DNS Zones", icon: <Globe2 size={18} />, path: "/dns" },
  { label: "SSL/TLS", icon: <ShieldCheck size={18} />, path: "/ssl" },
  { label: "Backups", icon: <Archive size={18} />, path: "/backups" },
  { label: "WordPress", icon: <Blocks size={18} />, path: "/wordpress" },
  { label: "Firewall", icon: <Flame size={18} />, path: "/firewall" },
  { label: "Software", icon: <Package size={18} />, path: "/software" },
  { label: "Monitoring", icon: <Activity size={18} />, path: "/monitoring" },
  { label: "Logs", icon: <FileText size={18} />, path: "/logs" },
  { label: "Cron Jobs", icon: <Clock size={18} />, path: "/cron" },
  { label: "File Manager", icon: <FolderOpen size={18} />, path: "/files" },
  { label: "SSH Keys", icon: <Key size={18} />, path: "/ssh-keys" },
  { label: "Processes", icon: <Cpu size={18} />, path: "/processes" },
  { label: "Resources", icon: <HardDrive size={18} />, path: "/resources" },
  { label: "Notifications", icon: <Bell size={18} />, path: "/notifications" },
  { label: "Audit Log", icon: <ClipboardList size={18} />, path: "/audit" },
  { label: "Configuration", icon: <Settings size={18} />, path: "/config" },
  { label: "Maintenance", icon: <Wrench size={18} />, path: "/maintenance" },
  { label: "Deployments", icon: <GitBranch size={18} />, path: "/deploy" },
  { label: "Users & RBAC", icon: <Users size={18} />, path: "/users" },
  { label: "Terminal", icon: <TerminalSquare size={18} />, path: "/terminal" },
];

export default function DashboardLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const { user, logout } = useAuthStore();

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

  return (
    <div className="flex h-screen overflow-hidden">
      <Sidebar
        items={navItems}
        currentPath={location.pathname}
        onNavigate={(path) => navigate(path)}
        brand="ServerPanel WHM"
      />
      <div className="flex-1 flex flex-col overflow-hidden">
        <TopBar title={pageTitle} userName={user?.name} onLogout={handleLogout} />
        <main className="flex-1 overflow-y-auto p-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
