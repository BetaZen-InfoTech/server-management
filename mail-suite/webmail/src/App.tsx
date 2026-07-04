import { useEffect } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { registerServiceWorker } from '@/api/push'
import { useAuth } from '@/store/auth'
import { useAccounts } from '@/store/accounts'
import Login from '@/routes/Login'
import Register from '@/routes/Register'
import Inbox from '@/routes/Inbox'
import Thread from '@/routes/Thread'
import Drafts from '@/routes/Drafts'
import Contacts from '@/routes/Contacts'
import Campaigns from '@/routes/Campaigns'
import CampaignDetail from '@/routes/CampaignDetail'
import SettingsLayout from '@/routes/Settings/Layout'
import SignaturesPage from '@/routes/Settings/Signatures'
import AccountsPage from '@/routes/Settings/Accounts'
import ForwardersPage from '@/routes/Settings/Forwarders'
import DevicesPage from '@/routes/Settings/Devices'
import NotificationsPage from '@/routes/Settings/Notifications'
import DomainsPage from '@/routes/Settings/Domains'
import TrackingPage from '@/routes/Settings/Tracking'
import Shell from '@/components/Shell'

function Protected({ children }: { children: React.ReactNode }) {
  const { user, ready, bootstrap } = useAuth()
  const loc = useLocation()
  useEffect(() => { if (!ready) void bootstrap() }, [ready, bootstrap])
  if (!ready) return <div className="p-8 text-ink-500">Loading…</div>
  if (!user) return <Navigate to="/login" replace state={{ from: loc.pathname }} />
  return <>{children}</>
}

function Bootstrap({ children }: { children: React.ReactNode }) {
  const { load } = useAccounts()
  useEffect(() => { void load() }, [load])
  return <>{children}</>
}

export default function App() {
  // Keep an up-to-date service worker so already-subscribed browsers receive
  // pushes even after a redeploy (best-effort; no-op where unsupported).
  useEffect(() => { void registerServiceWorker() }, [])
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/register" element={<Register />} />

      <Route
        path="/"
        element={
          <Protected>
            <Bootstrap>
              <Shell />
            </Bootstrap>
          </Protected>
        }
      >
        <Route index element={<Navigate to="/inbox" replace />} />
        <Route path="inbox" element={<Inbox />} />
        <Route path="folder/:folder" element={<Inbox />} />
        <Route path="thread/:uid" element={<Thread />} />
        <Route path="drafts" element={<Drafts />} />
        <Route path="contacts" element={<Contacts />} />
        <Route path="campaigns" element={<Campaigns />} />
        <Route path="campaigns/:id" element={<CampaignDetail />} />
        <Route path="settings" element={<SettingsLayout />}>
          <Route index element={<Navigate to="accounts" replace />} />
          <Route path="accounts" element={<AccountsPage />} />
          <Route path="signatures" element={<SignaturesPage />} />
          <Route path="tracking" element={<TrackingPage />} />
          <Route path="forwarders" element={<ForwardersPage />} />
          <Route path="devices" element={<DevicesPage />} />
          <Route path="notifications" element={<NotificationsPage />} />
          <Route path="domains" element={<DomainsPage />} />
        </Route>
      </Route>

      <Route path="*" element={<Navigate to="/inbox" replace />} />
    </Routes>
  )
}
