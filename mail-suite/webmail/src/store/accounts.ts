import { create } from 'zustand'
import { api } from '@/api/client'
import { MailAccount } from '@/api/types'

type S = {
  accounts: MailAccount[]
  selectedId: string | null
  loading: boolean
  load: () => Promise<void>
  add: (req: Partial<MailAccount> & { password: string }) => Promise<void>
  remove: (id: string) => Promise<void>
  select: (id: string) => void
  current: () => MailAccount | null
}

export const useAccounts = create<S>((set, get) => ({
  accounts: [],
  selectedId: localStorage.getItem('mailsuite.selectedAccount') || null,
  loading: false,

  async load() {
    set({ loading: true })
    try {
      const r = await api.get<{ data: MailAccount[] }>('/accounts')
      const list = r.data.data || []
      let selectedId = get().selectedId
      if (!selectedId || !list.find((a) => a.id === selectedId)) {
        const primary = list.find((a) => a.is_primary) || list[0]
        selectedId = primary?.id || null
      }
      if (selectedId) localStorage.setItem('mailsuite.selectedAccount', selectedId)
      set({ accounts: list, selectedId, loading: false })
    } catch {
      set({ loading: false })
    }
  },

  async add(req) {
    await api.post('/accounts', req)
    await get().load()
  },

  async remove(id) {
    await api.delete(`/accounts/${id}`)
    await get().load()
  },

  select(id) {
    localStorage.setItem('mailsuite.selectedAccount', id)
    set({ selectedId: id })
  },

  current() {
    const { accounts, selectedId } = get()
    return accounts.find((a) => a.id === selectedId) || null
  },
}))
