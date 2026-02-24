import { Routes, Route, Navigate } from "react-router-dom";
import { Toaster } from "react-hot-toast";
import { useAuthStore } from "@/store/auth";
import DashboardLayout from "@/layouts/DashboardLayout";
import LoginPage from "@/pages/LoginPage";
import DashboardPage from "@/pages/DashboardPage";
import DomainsPage from "@/pages/DomainsPage";
import AppsPage from "@/pages/AppsPage";
import DatabasesPage from "@/pages/DatabasesPage";
import EmailPage from "@/pages/EmailPage";
import DnsPage from "@/pages/DnsPage";
import SslPage from "@/pages/SslPage";
import BackupsPage from "@/pages/BackupsPage";
import WordPressPage from "@/pages/WordPressPage";
import FirewallPage from "@/pages/FirewallPage";
import SoftwarePage from "@/pages/SoftwarePage";
import MonitoringPage from "@/pages/MonitoringPage";
import LogsPage from "@/pages/LogsPage";
import CronPage from "@/pages/CronPage";
import FilesPage from "@/pages/FilesPage";
import SshKeysPage from "@/pages/SshKeysPage";
import ProcessesPage from "@/pages/ProcessesPage";
import ResourcesPage from "@/pages/ResourcesPage";
import NotificationsPage from "@/pages/NotificationsPage";
import AuditPage from "@/pages/AuditPage";
import ConfigPage from "@/pages/ConfigPage";
import MaintenancePage from "@/pages/MaintenancePage";
import DeployPage from "@/pages/DeployPage";
import UsersPage from "@/pages/UsersPage";

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { isAuthenticated } = useAuthStore();
  return isAuthenticated ? <>{children}</> : <Navigate to="/login" replace />;
}

export default function App() {
  return (
    <>
      <Toaster
        position="top-right"
        toastOptions={{
          className: "!bg-panel-surface !text-panel-text !border !border-panel-border",
        }}
      />
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route
          element={
            <ProtectedRoute>
              <DashboardLayout />
            </ProtectedRoute>
          }
        >
          <Route path="/dashboard" element={<DashboardPage />} />
          <Route path="/domains" element={<DomainsPage />} />
          <Route path="/apps" element={<AppsPage />} />
          <Route path="/databases" element={<DatabasesPage />} />
          <Route path="/email" element={<EmailPage />} />
          <Route path="/dns" element={<DnsPage />} />
          <Route path="/ssl" element={<SslPage />} />
          <Route path="/backups" element={<BackupsPage />} />
          <Route path="/wordpress" element={<WordPressPage />} />
          <Route path="/firewall" element={<FirewallPage />} />
          <Route path="/software" element={<SoftwarePage />} />
          <Route path="/monitoring" element={<MonitoringPage />} />
          <Route path="/logs" element={<LogsPage />} />
          <Route path="/cron" element={<CronPage />} />
          <Route path="/files" element={<FilesPage />} />
          <Route path="/ssh-keys" element={<SshKeysPage />} />
          <Route path="/processes" element={<ProcessesPage />} />
          <Route path="/resources" element={<ResourcesPage />} />
          <Route path="/notifications" element={<NotificationsPage />} />
          <Route path="/audit" element={<AuditPage />} />
          <Route path="/config" element={<ConfigPage />} />
          <Route path="/maintenance" element={<MaintenancePage />} />
          <Route path="/deploy" element={<DeployPage />} />
          <Route path="/users" element={<UsersPage />} />
        </Route>
        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </>
  );
}
