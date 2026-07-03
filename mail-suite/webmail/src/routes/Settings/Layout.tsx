import { NavLink, Outlet } from 'react-router-dom'
import clsx from 'clsx'

const tabs = [
  { to: 'accounts', label: 'Accounts' },
  { to: 'signatures', label: 'Signatures' },
  { to: 'tracking', label: 'Tracking' },
  { to: 'forwarders', label: 'Forwarders' },
  { to: 'devices', label: 'Devices' },
  { to: 'domains', label: 'Domains' },
]

export default function SettingsLayout() {
  return (
    <div className="p-3 max-w-5xl mx-auto">
      <h1 className="text-xl font-semibold mb-3">Settings</h1>
      <div className="card overflow-hidden">
        <div className="flex border-b border-ink-100">
          {tabs.map((t) => (
            <NavLink
              key={t.to}
              to={t.to}
              className={({ isActive }) =>
                clsx('px-4 py-2 text-sm', isActive ? 'border-b-2 border-brand-600 text-brand-700 font-medium' : 'text-ink-500 hover:text-ink-800')
              }
            >
              {t.label}
            </NavLink>
          ))}
        </div>
        <div className="p-4">
          <Outlet />
        </div>
      </div>
    </div>
  )
}
