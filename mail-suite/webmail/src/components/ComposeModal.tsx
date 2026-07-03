import { X } from 'lucide-react'
import { useComposeModal } from '@/store/compose'
import { ComposeForm } from '@/routes/Compose'

// Gmail-style docked composer: a card pinned to the bottom-right on desktop and
// a bottom sheet on mobile, so you can write while the inbox stays visible
// behind it. Only mounts ComposeForm (and its tiptap editor) while open, so a
// fresh, empty draft is created each time it's popped.
export default function ComposeModal() {
  const { open, close } = useComposeModal()
  if (!open) return null
  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center sm:justify-end sm:p-4 pointer-events-none">
      {/* Backdrop — dims + closes on tap (mainly for the mobile bottom sheet). */}
      <div className="absolute inset-0 bg-black/20 sm:bg-transparent pointer-events-auto" onClick={close} />
      <div className="relative pointer-events-auto w-full sm:w-[620px] max-h-[92vh] bg-white rounded-t-2xl sm:rounded-xl shadow-2xl border border-ink-200 flex flex-col overflow-hidden">
        <div className="flex items-center justify-between px-4 py-2.5 bg-ink-800 text-white shrink-0">
          <span className="text-sm font-medium">New message</span>
          <button onClick={close} className="p-1 rounded hover:bg-white/10" aria-label="Close">
            <X size={16} />
          </button>
        </div>
        <div className="p-4 overflow-auto">
          <ComposeForm onDone={close} />
        </div>
      </div>
    </div>
  )
}
