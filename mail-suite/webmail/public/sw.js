/* Betazen Mail — Web Push service worker.
 * Receives push messages (even when the webmail tab is closed) and shows a
 * notification; clicking it focuses/opens the inbox. Served at /mail/sw.js. */

self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()))

self.addEventListener('push', (event) => {
  let data = {}
  try {
    data = event.data ? event.data.json() : {}
  } catch (e) {
    data = { title: 'Betazen Mail', body: event.data ? event.data.text() : '' }
  }
  const title = data.title || 'Betazen Mail'
  const options = {
    body: data.body || '',
    icon: data.icon || '/mail/favicon.svg',
    badge: '/mail/favicon.svg',
    tag: data.tag || 'betazen-mail',
    renotify: true,
    data: { url: data.url || '/mail/inbox' },
  }
  event.waitUntil(self.registration.showNotification(title, options))
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const url = (event.notification.data && event.notification.data.url) || '/mail/inbox'
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((wins) => {
      for (const w of wins) {
        if (w.url.includes('/mail') && 'focus' in w) {
          w.focus()
          if ('navigate' in w) {
            try { w.navigate(url) } catch (e) { /* cross-origin/navigation guard */ }
          }
          return
        }
      }
      if (self.clients.openWindow) return self.clients.openWindow(url)
    }),
  )
})
