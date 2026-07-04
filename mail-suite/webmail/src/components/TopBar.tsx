import { useState } from 'react'
import { Search, LogOut } from 'lucide-react'
import AccountSwitcher from './AccountSwitcher'
import { useAuth } from '@/store/auth'

export default function TopBar() {
  const [q, setQ] = useState('')
  const { user, logout } = useAuth()
  return (
    <header className="h-14 border-b border-brand-100/70 glass px-4 flex items-center gap-4 shadow-[0_1px_0_rgba(255,255,255,.6),0_6px_16px_-14px_rgba(2,132,199,.5)] z-10">
      <div className="font-bold text-lg tracking-tight bg-gradient-to-r from-brand-600 via-brand-500 to-brand-400 bg-clip-text text-transparent">Betazen&nbsp;Mail</div>
      <div className="flex-1 max-w-2xl">
        <div className="relative">
          <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-ink-500" />
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search mail"
            className="input pl-9"
          />
        </div>
      </div>
      <AccountSwitcher />
      <button title={user?.email} onClick={() => void logout()} className="btn-ghost" aria-label="Logout">
        <LogOut size={16} />
      </button>
    </header>
  )
}
