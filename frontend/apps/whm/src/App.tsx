import { Routes, Route, Navigate } from "react-router-dom";
import { Toaster } from "react-hot-toast";
import { ConfirmHost } from "@serverpanel/ui";
import { useAuthStore } from "@/store/auth";
import DashboardLayout from "@/layouts/DashboardLayout";
import LoginPage from "@/pages/LoginPage";
import OtpPage from "@/pages/OtpPage";
import ForgotPasswordPage from "@/pages/ForgotPasswordPage";
import ResetPasswordPage from "@/pages/ResetPasswordPage";
import SessionsPage from "@/pages/SessionsPage";
import DashboardPage from "@/pages/DashboardPage";
import DomainsPage from "@/pages/DomainsPage";
import AppsPage from "@/pages/AppsPage";
import DatabasesPage from "@/pages/DatabasesPage";
import EmailPage from "@/pages/EmailPage";
import MailIssuesPage from "@/pages/MailIssuesPage";
import DnsPage from "@/pages/DnsPage";
import CloudflarePage from "@/pages/CloudflarePage";
import SSLPage from "@/pages/SSLPage";
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
import DeploySoftwarePage from "@/pages/DeploySoftwarePage";
import UsersPage from "@/pages/UsersPage";
import VendorsPage from "@/pages/VendorsPage";
import PackagesPage from "@/pages/PackagesPage";
import ServerSettingsPage from "@/pages/ServerSettingsPage";
import TerminalPage from "@/pages/TerminalPage";
import TransferPage from "@/pages/TransferPage";
import TransferTokensPage from "@/pages/TransferTokensPage";
import ServerInformationPage from "@/pages/ServerInformationPage";
import ServiceStatusPage from "@/pages/ServiceStatusPage";
import ChangeHostnamePage from "@/pages/ChangeHostnamePage";
import RebootPage from "@/pages/RebootPage";
import RepairDatabasesPage from "@/pages/RepairDatabasesPage";
import EditDatabaseConfigPage from "@/pages/EditDatabaseConfigPage";
import EditMongoDBConfigPage from "@/pages/EditMongoDBConfigPage";
import MultiPhpIniEditorPage from "@/pages/MultiPhpIniEditorPage";
import ShellAccessPage from "@/pages/ShellAccessPage";
import BandwidthLimitPage from "@/pages/BandwidthLimitPage";
import ProfilePage from "@/pages/ProfilePage";
import ReportsPage from "@/pages/ReportsPage";
import DeveloperPage from "@/pages/DeveloperPage";
import MailSuitePage from "@/pages/MailSuitePage";
import HelpPage from "@/pages/HelpPage";

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { isAuthenticated } = useAuthStore();

  // During Zustand hydration, isAuthenticated is false (default).
  // Check localStorage directly as a synchronous fallback.
  if (!isAuthenticated) {
    try {
      const stored = localStorage.getItem("whm-auth");
      if (stored) {
        const parsed = JSON.parse(stored);
        if (parsed?.state?.isAuthenticated) {
          return <>{children}</>;
        }
      }
    } catch {
      // ignore parse errors
    }
    return <Navigate to="/login" replace />;
  }

  return <>{children}</>;
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
      <ConfirmHost />
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        {/* Password recovery — public, no ProtectedRoute wrapper. Reachable
            from the Login page's "Forgot password?" link and from the
            emailed reset link. */}
        <Route path="/forgot-password" element={<ForgotPasswordPage />} />
        <Route path="/reset-password" element={<ResetPasswordPage />} />
        {/* Passwordless email-OTP login — public. Reachable from the
            Login page's "Sign in with a code" link and from the one-
            click magic URL in the OTP email (which prefills email+code). */}
        <Route path="/otp" element={<OtpPage />} />
        <Route
          element={
            <ProtectedRoute>
              <DashboardLayout />
            </ProtectedRoute>
          }
        >
          <Route path="/dashboard" element={<DashboardPage />} />
          <Route path="/domains" element={<DomainsPage />} />
          <Route path="/packages" element={<PackagesPage />} />
          <Route path="/apps" element={<AppsPage />} />
          <Route path="/databases" element={<DatabasesPage />} />
          <Route path="/email" element={<EmailPage />} />
          <Route path="/dns" element={<DnsPage />} />
          <Route path="/cloudflare" element={<CloudflarePage />} />
          <Route path="/ssl" element={<SSLPage />} />
          <Route path="/backups" element={<BackupsPage />} />
          <Route path="/wordpress" element={<WordPressPage />} />
          <Route path="/firewall" element={<FirewallPage />} />
          <Route path="/software" element={<SoftwarePage />} />
          <Route path="/monitoring" element={<MonitoringPage />} />
          <Route path="/reports" element={<ReportsPage />} />
          <Route path="/logs" element={<LogsPage />} />
          <Route path="/cron" element={<CronPage />} />
          <Route path="/files" element={<FilesPage />} />
          <Route path="/ssh-keys" element={<SshKeysPage />} />
          <Route path="/processes" element={<ProcessesPage />} />
          <Route path="/resources" element={<ResourcesPage />} />
          <Route path="/notifications" element={<NotificationsPage />} />
          <Route path="/audit" element={<AuditPage />} />
          <Route path="/config" element={<ConfigPage />} />
          <Route path="/server-settings" element={<ServerSettingsPage />} />
          <Route path="/maintenance" element={<MaintenancePage />} />
          <Route path="/deploy-software" element={<DeploySoftwarePage />} />
          <Route path="/users" element={<UsersPage />} />
          <Route path="/vendors" element={<VendorsPage />} />
          <Route path="/terminal" element={<TerminalPage />} />
          <Route path="/transfer" element={<TransferPage />} />
          <Route path="/transfer-tokens" element={<TransferTokensPage />} />
          <Route path="/server-info" element={<ServerInformationPage />} />
          <Route path="/service-status" element={<ServiceStatusPage />} />
          <Route path="/mail-issues" element={<MailIssuesPage />} />
          <Route path="/change-hostname" element={<ChangeHostnamePage />} />
          <Route path="/reboot/graceful" element={<RebootPage mode="graceful" />} />
          <Route path="/reboot/forceful" element={<RebootPage mode="forceful" />} />
          <Route path="/repair-databases" element={<RepairDatabasesPage />} />
          <Route path="/edit-db-config" element={<EditDatabaseConfigPage />} />
          <Route path="/edit-mongo-config" element={<EditMongoDBConfigPage />} />
          <Route path="/multiphp-ini" element={<MultiPhpIniEditorPage />} />
          <Route path="/shell-access" element={<ShellAccessPage />} />
          <Route path="/bandwidth-limit" element={<BandwidthLimitPage />} />
          <Route path="/profile" element={<ProfilePage />} />
          <Route path="/sessions" element={<SessionsPage />} />
          <Route path="/developer" element={<DeveloperPage />} />
          <Route path="/mail-suite" element={<MailSuitePage />} />
          <Route path="/help" element={<HelpPage />} />
        </Route>
        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </>
  );
}
