import { Outlet } from 'react-router-dom'
import Sidebar from './Sidebar'
import TopBar from './TopBar'
import ComposeModal from './ComposeModal'
import { useComposeModal } from '@/store/compose'

export default function Shell() {
  const openCompose = useComposeModal((s) => s.openCompose)
  return (
    <div className="flex h-full">
      <Sidebar onCompose={openCompose} />
      <div className="flex-1 flex flex-col min-w-0">
        <TopBar />
        <main className="flex-1 overflow-auto bg-ink-50">
          <Outlet />
        </main>
      </div>
      <ComposeModal />
    </div>
  )
}
