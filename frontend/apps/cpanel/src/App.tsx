import React from "react";
import { Routes, Route, Navigate } from "react-router-dom";
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
import DnsPage from "@/pages/DnsPage";
import SslPage from "@/pages/SslPage";
import BackupsPage from "@/pages/BackupsPage";
import WordPressPage from "@/pages/WordPressPage";
import FilesPage from "@/pages/FilesPage";
import SshKeysPage from "@/pages/SshKeysPage";
import CronPage from "@/pages/CronPage";
import DeployPage from "@/pages/DeployPage";
import DeploySoftwarePage from "@/pages/DeploySoftwarePage";
import TerminalPage from "@/pages/TerminalPage";
import PackagesPage from "@/pages/PackagesPage";
import UsersPage from "@/pages/UsersPage";
import ShellAccessPage from "@/pages/ShellAccessPage";
import ProfilePage from "@/pages/ProfilePage";

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { isAuthenticated } = useAuthStore();
  if (!isAuthenticated) return <Navigate to="/login" replace />;
  return <>{children}</>;
}

export default function App() {
  return (
    <>
    <ConfirmHost />
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      {/* Public password recovery — outside the ProtectedRoute wrapper
          so anonymous callers can actually reach them. The pages POST
          straight to /api/v1/auth/* with bare axios, bypassing the
          /api/v1/cpanel-prefixed auth-gated client. */}
      <Route path="/forgot-password" element={<ForgotPasswordPage />} />
      <Route path="/reset-password" element={<ResetPasswordPage />} />
      {/* Passwordless email-OTP login — public. Lands here from the
          Login page's "Sign in with a code" link or from the one-
          click magic URL in the OTP email (prefilled email+code). */}
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
        <Route path="/deploy-software" element={<DeploySoftwarePage />} />
        <Route path="/terminal" element={<TerminalPage />} />
        <Route path="/packages" element={<PackagesPage />} />
        <Route path="/team" element={<UsersPage />} />
        <Route path="/shell-access" element={<ShellAccessPage />} />
        <Route path="/profile" element={<ProfilePage />} />
        <Route path="/sessions" element={<SessionsPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
    </>
  );
}
