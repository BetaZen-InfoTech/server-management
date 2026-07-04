import { api } from './client'

// Web Push (browser/PWA notifications). The flow:
//   1. GET /push/vapid          → server's VAPID public key
//   2. Notification.requestPermission()
//   3. register /mail/sw.js, pushManager.subscribe(applicationServerKey)
//   4. POST /push/subscribe     → persist the subscription server-side
// The service worker (sw.js) then shows notifications the backend pushes.

const SW_URL = '/mail/sw.js'
const SW_SCOPE = '/mail/'

export function pushSupported(): boolean {
  return (
    typeof window !== 'undefined' &&
    'serviceWorker' in navigator &&
    'PushManager' in window &&
    'Notification' in window
  )
}

export type PushState = {
  supported: boolean
  permission: NotificationPermission | 'unsupported'
  subscribed: boolean
  serverEnabled: boolean
}

function urlBase64ToUint8Array(base64String: string): Uint8Array<ArrayBuffer> {
  const padding = '='.repeat((4 - (base64String.length % 4)) % 4)
  const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/')
  const raw = atob(base64)
  const out = new Uint8Array(new ArrayBuffer(raw.length))
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

async function getRegistration(): Promise<ServiceWorkerRegistration> {
  const existing = await navigator.serviceWorker.getRegistration(SW_SCOPE)
  if (existing) return existing
  return navigator.serviceWorker.register(SW_URL, { scope: SW_SCOPE })
}

// registerServiceWorker is called once at app start so an already-subscribed
// browser keeps a live, up-to-date worker (best-effort, never throws).
export async function registerServiceWorker(): Promise<void> {
  if (!pushSupported()) return
  try {
    await getRegistration()
  } catch {
    /* ignore — push just won't be available */
  }
}

async function fetchVapid(): Promise<{ public_key: string; enabled: boolean }> {
  const r = await api.get<{ data: { public_key: string; enabled: boolean } }>('/push/vapid')
  return r.data.data
}

export async function getPushState(): Promise<PushState> {
  if (!pushSupported()) {
    return { supported: false, permission: 'unsupported', subscribed: false, serverEnabled: false }
  }
  let subscribed = false
  try {
    const reg = await navigator.serviceWorker.getRegistration(SW_SCOPE)
    if (reg) subscribed = !!(await reg.pushManager.getSubscription())
  } catch {
    /* ignore */
  }
  let serverEnabled = false
  try {
    serverEnabled = (await fetchVapid()).enabled
  } catch {
    /* ignore */
  }
  return { supported: true, permission: Notification.permission, subscribed, serverEnabled }
}

// enablePush requests permission, subscribes, and registers the subscription
// with the backend. Returns true on success. Throws with a human message on a
// hard failure so the caller can toast it.
export async function enablePush(): Promise<boolean> {
  if (!pushSupported()) throw new Error('This browser does not support notifications.')
  const { public_key, enabled } = await fetchVapid()
  if (!enabled || !public_key) throw new Error('Push is not configured on the server yet.')

  const perm = await Notification.requestPermission()
  if (perm !== 'granted') throw new Error('Notification permission was blocked.')

  const reg = await getRegistration()
  await navigator.serviceWorker.ready

  let sub = await reg.pushManager.getSubscription()
  if (!sub) {
    sub = await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: urlBase64ToUint8Array(public_key),
    })
  }
  const j = sub.toJSON() as { endpoint?: string; keys?: { p256dh?: string; auth?: string } }
  await api.post('/push/subscribe', {
    endpoint: j.endpoint,
    keys: { p256dh: j.keys?.p256dh, auth: j.keys?.auth },
  })
  return true
}

export async function sendTestPush(): Promise<number> {
  const r = await api.post<{ data: { sent_to: number } }>('/push/test', {})
  return r.data.data?.sent_to ?? 0
}

export async function disablePush(): Promise<void> {
  if (!pushSupported()) return
  const reg = await navigator.serviceWorker.getRegistration(SW_SCOPE)
  if (!reg) return
  const sub = await reg.pushManager.getSubscription()
  if (!sub) return
  const endpoint = sub.endpoint
  try {
    await sub.unsubscribe()
  } catch {
    /* ignore */
  }
  try {
    await api.post('/push/unsubscribe', { endpoint })
  } catch {
    /* ignore */
  }
}
