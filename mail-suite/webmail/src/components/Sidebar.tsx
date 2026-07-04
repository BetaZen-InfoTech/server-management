import { NavLink } from 'react-router-dom'
import {
  Pencil, Inbox as InboxIcon, Send, FileText, Trash2, AlertOctagon, Settings, Star, Users, Megaphone,
} from 'lucide-react'
import clsx from 'clsx'

const items = [
  { to: '/inbox', label: 'Inbox', icon: InboxIcon },
  { to: '/folder/Sent', label: 'Sent', icon: Send },
  { to: '/drafts', label: 'Drafts', icon: FileText },
  { to: '/folder/Starred', label: 'Starred', icon: Star },
  { to: '/folder/Spam', label: 'Spam', icon: AlertOctagon },
  { to: '/folder/Trash', label: 'Trash', icon: Trash2 },
  { to: '/contacts', label: 'Contacts', icon: Users },
  { to: '/campaigns', label: 'Campaigns', icon: Megaphone },
]

export default function Sidebar({ onCompose }: { onCompose: () => void }) {
  return (
    <aside className="w-60 shrink-0 border-r border-brand-100/60 bg-gradient-to-b from-white via-white to-brand-50/40 p-3 flex flex-col gap-2">
      <button onClick={onCompose} className="btn-primary mb-2">
        <Pencil size={16} /> Compose
      </button>
      <nav className="flex flex-col gap-0.5">
        {items.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) => clsx(
              'flex items-center gap-3 rounded-lg px-3 py-2 text-sm',
              isActive ? 'bg-brand-100/80 text-brand-700 font-semibold shadow-sm ring-1 ring-inset ring-brand-200/50' : 'text-ink-600 hover:bg-brand-50/70 hover:text-brand-700',
            )}
          >
            <Icon size={16} /> {label}
          </NavLink>
        ))}
      </nav>
      <div className="mt-auto pt-2 border-t border-ink-100">
        <NavLink
          to="/settings"
          className={({ isActive }) => clsx(
            'flex items-center gap-3 rounded-lg px-3 py-2 text-sm',
            isActive ? 'bg-brand-100/80 text-brand-700 font-semibold shadow-sm ring-1 ring-inset ring-brand-200/50' : 'text-ink-600 hover:bg-brand-50/70 hover:text-brand-700',
          )}
        >
          <Settings size={16} /> Settings
        </NavLink>
      </div>
    </aside>
  )
}
