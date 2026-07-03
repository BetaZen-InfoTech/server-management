import { create } from 'zustand'

// A single Gmail-style docked compose window. Field state lives here (not inside
// the component) so it survives minimize/restore and can be auto-saved as a
// draft. Multiple windows can be open at once.
export type ComposeWindow = {
  id: string
  minimized: boolean
  accountId: string
  to: string
  cc: string
  bcc: string
  showCc: boolean
  showBcc: boolean
  subject: string
  html: string
  signatureId: string
  inReplyTo?: string
  references?: string[]
  draftId?: string // server-side draft id once autosaved
  savedAt?: number
  dirty: boolean
}

// What a caller can seed a new window with (reply/forward/resume-draft).
export type ComposeInit = Partial<
  Pick<
    ComposeWindow,
    'accountId' | 'to' | 'cc' | 'bcc' | 'subject' | 'html' | 'signatureId' | 'inReplyTo' | 'references' | 'draftId'
  >
>

type ComposeStore = {
  windows: ComposeWindow[]
  open: (init?: ComposeInit) => string
  close: (id: string) => void
  minimize: (id: string, minimized: boolean) => void
  patch: (id: string, patch: Partial<ComposeWindow>) => void
}

let seq = 0
function nextId() {
  seq += 1
  return `cw_${seq}_${Date.now()}`
}

export const useCompose = create<ComposeStore>((set) => ({
  windows: [],
  open: (init) => {
    const id = nextId()
    const win: ComposeWindow = {
      id,
      minimized: false,
      accountId: init?.accountId || '',
      to: init?.to || '',
      cc: init?.cc || '',
      bcc: init?.bcc || '',
      showCc: !!init?.cc,
      showBcc: !!init?.bcc,
      subject: init?.subject || '',
      html: init?.html || '',
      signatureId: init?.signatureId || '',
      inReplyTo: init?.inReplyTo,
      references: init?.references,
      draftId: init?.draftId,
      dirty: false,
    }
    set((s) => ({ windows: [...s.windows, win] }))
    return id
  },
  close: (id) => set((s) => ({ windows: s.windows.filter((w) => w.id !== id) })),
  minimize: (id, minimized) =>
    set((s) => ({ windows: s.windows.map((w) => (w.id === id ? { ...w, minimized } : w)) })),
  patch: (id, patch) =>
    set((s) => ({ windows: s.windows.map((w) => (w.id === id ? { ...w, ...patch } : w)) })),
}))
