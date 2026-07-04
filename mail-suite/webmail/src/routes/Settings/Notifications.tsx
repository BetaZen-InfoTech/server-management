import { useEffect, useState } from 'react'
import toast from 'react-hot-toast'
import { Bell, BellOff, Loader2 } from 'lucide-react'
import { disablePush, enablePush, getPushState, PushState, sendTestPush } from '@/api/push'

export default function NotificationsPage() {
  const [state, setState] = useState<PushState | null>(null)
  const [busy, setBusy] = useState(false)

  const refresh = () => getPushState().then(setState).catch(() => setState(null))
  useEffect(() => { void refresh() }, [])

  const onEnable = async () => {
    setBusy(true)
    try {
      await enablePush()
      const n = await sendTestPush()
      toast.success(n > 0 ? 'Notifications enabled — sent a test to this device.' : 'Notifications enabled.')
      await refresh()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Could not enable notifications.')
    } finally {
      setBusy(false)
    }
  }

  const onDisable = async () => {
    setBusy(true)
    try {
      await disablePush()
      toast.success('Notifications turned off on this device.')
      await refresh()
    } finally {
      setBusy(false)
    }
  }

  const onTest = async () => {
    setBusy(true)
    try {
      const n = await sendTestPush()
      toast.success(n > 0 ? `Test sent to ${n} device${n === 1 ? '' : 's'}.` : 'No subscribed devices.')
    } catch {
      toast.error('Could not send test.')
    } finally {
      setBusy(false)
    }
  }

  if (!state) {
    return <div className="text-sm text-ink-500 flex items-center gap-2"><Loader2 size={14} className="animate-spin" /> Checking…</div>
  }

  if (!state.supported) {
    return (
      <div className="space-y-2 text-sm text-ink-700">
        <h3 className="font-medium">Push notifications</h3>
        <p className="text-ink-500">This browser doesn't support Web Push. On iPhone, add this site to your Home Screen first, then enable notifications from the installed app.</p>
      </div>
    )
  }

  if (!state.serverEnabled) {
    return (
      <div className="space-y-2 text-sm text-ink-700">
        <h3 className="font-medium">Push notifications</h3>
        <p className="text-ink-500">Push isn't configured on this server yet.</p>
      </div>
    )
  }

  const blocked = state.permission === 'denied'

  return (
    <div className="space-y-4 text-sm text-ink-700 max-w-xl">
      <div>
        <h3 className="font-medium mb-1">Push notifications</h3>
        <p className="text-ink-500">
          Get a notification on this device the moment new mail lands in your inbox — even when the
          webmail tab is closed. Works on your phone too: open this site in Chrome (or add it to your
          Home Screen), then enable it here.
        </p>
      </div>

      <div className="flex items-center gap-3 rounded-lg border border-ink-100 p-3">
        <div className={state.subscribed ? 'text-green-600' : 'text-ink-400'}>
          {state.subscribed ? <Bell size={20} /> : <BellOff size={20} />}
        </div>
        <div className="flex-1">
          <div className="font-medium">
            {state.subscribed ? 'Notifications are on for this device' : 'Notifications are off'}
          </div>
          <div className="text-xs text-ink-500">
            {blocked
              ? 'Blocked in your browser settings — allow notifications for this site, then try again.'
              : state.subscribed
                ? 'You’ll be alerted when new mail arrives.'
                : 'Enable to start receiving new-mail alerts.'}
          </div>
        </div>
        {state.subscribed ? (
          <div className="flex gap-2">
            <button className="px-3 py-1.5 rounded-md border border-ink-200 text-ink-700 hover:bg-ink-50 disabled:opacity-50" disabled={busy} onClick={onTest}>Send test</button>
            <button className="px-3 py-1.5 rounded-md border border-ink-200 text-ink-700 hover:bg-ink-50 disabled:opacity-50" disabled={busy} onClick={onDisable}>Turn off</button>
          </div>
        ) : (
          <button className="btn-primary" disabled={busy || blocked} onClick={onEnable}>
            {busy ? <Loader2 size={14} className="animate-spin" /> : 'Enable'}
          </button>
        )}
      </div>
    </div>
  )
}
