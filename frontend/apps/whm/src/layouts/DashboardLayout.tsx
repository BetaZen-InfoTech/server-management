import { useState, useEffect } from "react";
import { useNavigate, useLocation, Outlet } from "react-router-dom";
import { Sidebar, TopBar } from "@serverpanel/ui";
import type { SidebarItem } from "@serverpanel/ui";
import { useAuthStore } from "@/store/auth";
import { apiClient } from "@serverpanel/api-client";
import {
  LayoutDashboard, Globe, AppWindow, Database, Mail, Globe2,
  ShieldCheck, Archive, Blocks, Flame, Package, Activity,
  FileText, Clock, FolderOpen, Key, Cpu, HardDrive,
  Bell, ClipboardList, Settings, Wrench, Users,
  TerminalSquare, Box, Server, ArrowLeftRight, Building2, Rocket
} from "lucide-react";

interface NavItem extends SidebarItem {
  adminOnly?: boolean;
}

const navItems: NavItem[] = [
  { label: "Dashboard", icon: <LayoutDashboard size={18} />, path: "/dashboard" },
  { label: "Domains", icon: <Globe size={18} />, path: "/domains" },
  { label: "Packages", icon: <Box size={18} />, path: "/packages" },
  { label: "Applications", icon: <AppWindow size={18} />, path: "/apps" },
  { label: "Databases", icon: <Database size={18} />, path: "/databases" },
  { label: "Email", icon: <Mail size={18} />, path: "/email" },
  { label: "DNS Zones", icon: <Globe2 size={18} />, path: "/dns" },
  { label: "SSL/TLS", icon: <ShieldCheck size={18} />, path: "/ssl" },
  { label: "Backups", icon: <Archive size={18} />, path: "/backups" },
  { label: "Transfer", icon: <ArrowLeftRight size={18} />, path: "/transfer" },
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
  { label: "Server Settings", icon: <Server size={18} />, path: "/server-settings" },
  { label: "Maintenance", icon: <Wrench size={18} />, path: "/maintenance" },
  { label: "Deploy Software", icon: <Rocket size={18} />, path: "/deploy-software" },
  { label: "Vendors", icon: <Building2 size={18} />, path: "/vendors", adminOnly: true },
  { label: "Users & RBAC", icon: <Users size={18} />, path: "/users" },
  { label: "Terminal", icon: <TerminalSquare size={18} />, path: "/terminal" },
];

export default function DashboardLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const { user, logout } = useAuthStore();
  const [serverIP, setServerIP] = useState("");
  const [versionName, setVersionName] = useState("");
  const [versionNumber, setVersionNumber] = useState("");

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

  const isAdmin = user?.role === "admin" || (user?.permissions?.includes("server.manage") ?? false);
  const visibleItems: SidebarItem[] = navItems
    .filter((item) => !item.adminOnly || isAdmin)
    .map((item): SidebarItem => ({ label: item.label, icon: item.icon, path: item.path, badge: item.badge }));

  return (
    <div className="flex h-screen overflow-hidden">
      <Sidebar
        items={visibleItems}
        currentPath={location.pathname}
        onNavigate={(path) => navigate(path)}
        brand="ServerPanel WHM"
      />
      <div className="flex-1 flex flex-col overflow-hidden">
        <TopBar
          title={pageTitle}
          userName={user?.name}
          serverIP={serverIP}
          versionName={versionName}
          versionNumber={versionNumber}
          onLogout={handleLogout}
        />
        <main className="flex-1 overflow-y-auto p-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
