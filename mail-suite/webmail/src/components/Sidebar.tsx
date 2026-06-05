import { NavLink } from 'react-router-dom'
import {
  Pencil, Inbox as InboxIcon, Send, FileText, Trash2, AlertOctagon, Settings, Star,
} from 'lucide-react'
import clsx from 'clsx'

const items = [
  { to: '/inbox', label: 'Inbox', icon: InboxIcon },
  { to: '/folder/Sent', label: 'Sent', icon: Send },
  { to: '/folder/Drafts', label: 'Drafts', icon: FileText },
  { to: '/folder/Starred', label: 'Starred', icon: Star },
  { to: '/folder/Spam', label: 'Spam', icon: AlertOctagon },
  { to: '/folder/Trash', label: 'Trash', icon: Trash2 },
]

export default function Sidebar({ onCompose }: { onCompose: () => void }) {
  return (
    <aside className="w-60 shrink-0 border-r border-ink-100 bg-white p-3 flex flex-col gap-2">
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
              isActive ? 'bg-brand-50 text-brand-700 font-medium' : 'text-ink-700 hover:bg-ink-100',
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
            isActive ? 'bg-brand-50 text-brand-700 font-medium' : 'text-ink-700 hover:bg-ink-100',
          )}
        >
          <Settings size={16} /> Settings
        </NavLink>
      </div>
    </aside>
  )
}
