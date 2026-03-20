import React from "react";
import { Routes, Route, Navigate } from "react-router-dom";
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
import FilesPage from "@/pages/FilesPage";
import SshKeysPage from "@/pages/SshKeysPage";
import CronPage from "@/pages/CronPage";
import DeployPage from "@/pages/DeployPage";
import TerminalPage from "@/pages/TerminalPage";

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { isAuthenticated } = useAuthStore();
  if (!isAuthenticated) return <Navigate to="/login" replace />;
  return <>{children}</>;
}

export default function App() {
  return (
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
        <Route path="/files" element={<FilesPage />} />
        <Route path="/ssh-keys" element={<SshKeysPage />} />
        <Route path="/cron" element={<CronPage />} />
        <Route path="/deployments" element={<DeployPage />} />
        <Route path="/terminal" element={<TerminalPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
  );
}
