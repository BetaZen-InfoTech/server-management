import { Outlet, useLocation } from 'react-router-dom'
import Sidebar from './Sidebar'
import TopBar from './TopBar'
import ComposeDock from './ComposeDock'
import { useCompose } from '@/store/compose'
import { useAccounts } from '@/store/accounts'

export default function Shell() {
  const open = useCompose((s) => s.open)
  const current = useAccounts((s) => s.current())
  const loc = useLocation()
  const onCompose = () => open({ accountId: current?.id })
  return (
    <div className="flex h-full">
      <Sidebar onCompose={onCompose} />
      <div className="flex-1 flex flex-col min-w-0">
        <TopBar />
        <main className="flex-1 overflow-auto bg-transparent">
          {/* keyed on the path so each route gently fades up on navigation
              (unchanged on same-route re-renders like the stats poll) */}
          <div key={loc.pathname} className="animate-fade-up h-full">
            <Outlet />
          </div>
        </main>
      </div>
      <ComposeDock />
    </div>
  )
}
