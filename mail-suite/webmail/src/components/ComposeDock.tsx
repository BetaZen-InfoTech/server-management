import { useCompose } from '@/store/compose'
import ComposeWindow from './ComposeWindow'

// Renders every open compose window in a row anchored to the bottom-right, so
// several can sit side-by-side like Gmail. The container ignores pointer events
// (so it never blocks the page) while each window re-enables them.
export default function ComposeDock() {
  const windows = useCompose((s) => s.windows)
  if (!windows.length) return null
  return (
    <div className="fixed bottom-0 right-0 z-50 flex items-end gap-3 p-3 pointer-events-none max-w-full overflow-x-auto">
      {windows.map((w) => (
        <ComposeWindow key={w.id} win={w} />
      ))}
    </div>
  )
}
