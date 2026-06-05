import { useState } from 'react'
import { ChevronDown, Check, Plus } from 'lucide-react'
import { useAccounts } from '@/store/accounts'
import { useNavigate } from 'react-router-dom'

export default function AccountSwitcher() {
  const { accounts, selectedId, select } = useAccounts()
  const [open, setOpen] = useState(false)
  const nav = useNavigate()
  const current = accounts.find((a) => a.id === selectedId)

  return (
    <div className="relative">
      <button
        onClick={() => setOpen((o) => !o)}
        className="btn-ghost min-w-[180px] justify-between"
      >
        <div className="flex items-center gap-2 truncate">
          <span
            className="inline-block h-6 w-6 rounded-full text-xs font-semibold text-white grid place-items-center"
            style={{ background: current?.color || '#4f46e5' }}
          >
            {current?.address?.[0]?.toUpperCase() || '?'}
          </span>
          <span className="truncate">{current?.address || 'No account'}</span>
        </div>
        <ChevronDown size={14} />
      </button>
      {open && (
        <div className="absolute right-0 mt-1 w-72 rounded-xl border border-ink-100 bg-white shadow-lg z-10">
          <div className="p-2 max-h-80 overflow-auto">
            {accounts.map((a) => (
              <button
                key={a.id}
                onClick={() => { select(a.id); setOpen(false) }}
                className="w-full flex items-center gap-2 px-2 py-2 rounded-lg hover:bg-ink-100 text-sm"
              >
                <span
                  className="inline-block h-6 w-6 rounded-full text-xs font-semibold text-white grid place-items-center"
                  style={{ background: a.color || '#4f46e5' }}
                >
                  {a.address[0].toUpperCase()}
                </span>
                <div className="flex-1 text-left truncate">
                  <div className="font-medium truncate">{a.display_name || a.address}</div>
                  <div className="text-xs text-ink-500 truncate">{a.address}</div>
                </div>
                {a.id === selectedId && <Check size={14} className="text-brand-600" />}
              </button>
            ))}
            {accounts.length === 0 && (
              <div className="px-2 py-3 text-sm text-ink-500">No accounts yet.</div>
            )}
          </div>
          <button
            onClick={() => { setOpen(false); nav('/settings/accounts') }}
            className="w-full flex items-center gap-2 px-3 py-2 text-sm border-t border-ink-100 hover:bg-ink-50 rounded-b-xl"
          >
            <Plus size={14} /> Add account
          </button>
        </div>
      )}
    </div>
  )
}
