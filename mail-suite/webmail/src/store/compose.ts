import { create } from 'zustand'

// Controls the Gmail-style docked compose popup. Kept tiny + global so the
// Compose button in the Sidebar (any route) can pop it open without navigating
// away from the inbox.
type ComposeState = {
  open: boolean
  openCompose: () => void
  close: () => void
}

export const useComposeModal = create<ComposeState>((set) => ({
  open: false,
  openCompose: () => set({ open: true }),
  close: () => set({ open: false }),
}))
