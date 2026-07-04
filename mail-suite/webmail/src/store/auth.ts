import { create } from 'zustand'
import { api, clearTokens, getTokens, setTokens } from '@/api/client'
import { TokenPair, User } from '@/api/types'

// Guarded read of the persisted user: a stored literal 'undefined' (which
// JSON.parse throws on) or any corrupt value must not blank the whole app at
// store-construction time.
function loadPersistedUser(): User | null {
  try {
    const raw = localStorage.getItem('mailsuite.user')
    if (!raw || raw === 'undefined') return null
    return JSON.parse(raw) as User
  } catch {
    return null
  }
}

type AuthState = {
  user: User | null
  ready: boolean
  login: (email: string, password: string) => Promise<void>
  register: (email: string, password: string, name: string) => Promise<void>
  logout: () => Promise<void>
  bootstrap: () => Promise<void>
}

export const useAuth = create<AuthState>((set) => ({
  user: loadPersistedUser(),
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
    // Send the refresh token so the backend actually revokes the server-side
    // session — otherwise it stays valid for the full refresh TTL after logout.
    try { await api.post('/auth/logout', { refresh_token: getTokens().refresh }) } catch {}
    clearTokens()
    localStorage.removeItem('mailsuite.user')
    set({ user: null })
  },
}))
