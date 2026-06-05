import axios, { AxiosError, AxiosInstance, InternalAxiosRequestConfig } from 'axios'

const LS_ACCESS = 'mailsuite.access'
const LS_REFRESH = 'mailsuite.refresh'

export function getTokens() {
  return {
    access: localStorage.getItem(LS_ACCESS) || '',
    refresh: localStorage.getItem(LS_REFRESH) || '',
  }
}

export function setTokens(access: string, refresh: string) {
  localStorage.setItem(LS_ACCESS, access)
  localStorage.setItem(LS_REFRESH, refresh)
}

export function clearTokens() {
  localStorage.removeItem(LS_ACCESS)
  localStorage.removeItem(LS_REFRESH)
}

const baseURL = (import.meta.env.VITE_API_BASE_URL as string) || '/api/v1'

export const api: AxiosInstance = axios.create({ baseURL })

api.interceptors.request.use((cfg: InternalAxiosRequestConfig) => {
  const { access } = getTokens()
  if (access) {
    cfg.headers.set('Authorization', `Bearer ${access}`)
  }
  return cfg
})

let refreshPromise: Promise<string> | null = null

async function refreshAccess(): Promise<string> {
  const { refresh } = getTokens()
  if (!refresh) throw new Error('no_refresh')
  const res = await axios.post(`${baseURL}/auth/refresh`, { refresh_token: refresh })
  const data = res.data?.data
  if (!data?.access_token) throw new Error('refresh_failed')
  setTokens(data.access_token, data.refresh_token)
  return data.access_token
}

api.interceptors.response.use(
  (r) => r,
  async (err: AxiosError) => {
    const original = err.config as InternalAxiosRequestConfig & { _retried?: boolean }
    if (err.response?.status === 401 && !original?._retried && getTokens().refresh) {
      original._retried = true
      try {
        if (!refreshPromise) refreshPromise = refreshAccess().finally(() => (refreshPromise = null))
        const tok = await refreshPromise
        original.headers!.set('Authorization', `Bearer ${tok}`)
        return api.request(original)
      } catch {
        clearTokens()
        window.location.href = '/mail/login'
      }
    }
    return Promise.reject(err)
  },
)
