import { Outlet, useNavigate } from 'react-router-dom'
import Sidebar from './Sidebar'
import TopBar from './TopBar'

export default function Shell() {
  const nav = useNavigate()
  return (
    <div className="flex h-full">
      <Sidebar onCompose={() => nav('/compose')} />
      <div className="flex-1 flex flex-col min-w-0">
        <TopBar />
        <main className="flex-1 overflow-auto bg-ink-50">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
