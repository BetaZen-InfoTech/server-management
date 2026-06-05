import { create } from 'zustand'
import { api, clearTokens, setTokens } from '@/api/client'
import { TokenPair, User } from '@/api/types'

type AuthState = {
  user: User | null
  ready: boolean
  login: (email: string, password: string) => Promise<void>
  register: (email: string, password: string, name: string) => Promise<void>
  logout: () => Promise<void>
  bootstrap: () => Promise<void>
}

export const useAuth = create<AuthState>((set) => ({
  user: JSON.parse(localStorage.getItem('mailsuite.user') || 'null'),
  ready: false,

  async bootstrap() {
    try {
      const r = await api.get('/auth/me')
      const u = r.data?.data as User
      localStorage.setItem('mailsuite.user', JSON.stringify(u))
      set({ user: u, ready: true })
    } catch {
      set({ user: null, ready: true })
    }
  },

  async login(email, password) {
    const r = await api.post<{ data: TokenPair }>('/auth/login', { email, password })
    const t = r.data.data
    setTokens(t.access_token, t.refresh_token)
    localStorage.setItem('mailsuite.user', JSON.stringify(t.user))
    set({ user: t.user, ready: true })
  },

  async register(email, password, name) {
    const r = await api.post<{ data: TokenPair }>('/auth/register', { email, password, name })
    const t = r.data.data
    setTokens(t.access_token, t.refresh_token)
    localStorage.setItem('mailsuite.user', JSON.stringify(t.user))
    set({ user: t.user, ready: true })
  },

  async logout() {
    try { await api.post('/auth/logout', {}) } catch {}
    clearTokens()
    localStorage.removeItem('mailsuite.user')
    set({ user: null })
  },
}))
