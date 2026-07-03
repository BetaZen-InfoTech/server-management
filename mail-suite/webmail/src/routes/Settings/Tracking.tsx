import { useEffect, useMemo, useState } from 'react'
import toast from 'react-hot-toast'
import { MailOpen, MousePointerClick, Truck, Eye } from 'lucide-react'
import { api } from '@/api/client'
import { useAccounts } from '@/store/accounts'
import { SentMessage, TrackingEvent, TrackingSettings } from '@/api/types'

// effective mirrors the server's MailAccount.EffectiveTracking: an unconfigured
// mailbox reports all-ON so the UI matches what actually happens on send.
function effective(t?: TrackingSettings): TrackingSettings {
  if (!t || !t.configured) return { configured: true, delivery: true, open: true, click: true }
  return t
}

function Toggle({ label, hint, icon, checked, onChange }: {
  label: string; hint: string; icon: React.ReactNode; checked: boolean; onChange: (v: boolean) => void
}) {
  return (
    <label className="flex items-start gap-3 p-3 border border-ink-100 rounded-lg cursor-pointer hover:bg-ink-50">
      <input type="checkbox" className="mt-1" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      <div className="flex-1">
        <div className="flex items-center gap-2 font-medium text-sm">{icon} {label}</div>
        <div className="text-xs text-ink-500">{hint}</div>
      </div>
    </label>
  )
}

export default function TrackingPage() {
  const { accounts, load } = useAccounts()
  const [accountId, setAccountId] = useState('')
  const [t, setT] = useState<TrackingSettings>(effective())
  const [saving, setSaving] = useState(false)

  const [sent, setSent] = useState<SentMessage[]>([])
  const [openId, setOpenId] = useState<string | null>(null)
  const [events, setEvents] = useState<TrackingEvent[]>([])

  const acc = useMemo(() => accounts.find((a) => a.id === accountId), [accounts, accountId])

  useEffect(() => {
    if (!accountId && accounts[0]) setAccountId(accounts[0].id)
  }, [accounts, accountId])

  useEffect(() => {
    if (acc) setT(effective(acc.tracking))
  }, [acc])

  async function loadSent(id: string) {
    try {
      const r = await api.get<{ data: SentMessage[] }>('/tracking/sent', { params: { account_id: id, limit: 50 } })
      setSent(r.data.data || [])
    } catch {
      setSent([])
    }
  }
  useEffect(() => {
    if (accountId) void loadSent(accountId)
    setOpenId(null)
  }, [accountId])

  async function save() {
    if (!accountId) return
    setSaving(true)
    try {
      await api.patch(`/accounts/${accountId}`, { tracking: { ...t, configured: true } })
      toast.success('Tracking settings saved')
      await load()
    } catch (err: any) {
      toast.error(err?.response?.data?.error || 'Failed to save')
    } finally {
      setSaving(false)
    }
  }

  async function toggleDetail(trackId: string) {
    if (openId === trackId) {
      setOpenId(null)
      return
    }
    setOpenId(trackId)
    setEvents([])
    try {
      const r = await api.get<{ data: { events: TrackingEvent[] } }>(`/tracking/sent/${trackId}`)
      setEvents(r.data.data?.events || [])
    } catch {
      setEvents([])
    }
  }

  return (
    <div className="space-y-8">
      <section>
        <h3 className="font-medium mb-1">Email tracking</h3>
        <p className="text-sm text-ink-500 mb-3">
          Choose what to track per mailbox. New mailboxes track everything by default. Note: open tracking is
          best-effort — some mail apps (e.g. Apple Mail) pre-load or block images, so opens can be over- or under-counted.
        </p>
        <div className="flex items-center gap-2 mb-3">
          <span className="text-sm text-ink-500">Mailbox</span>
          <select className="input max-w-xs" value={accountId} onChange={(e) => setAccountId(e.target.value)}>
            {accounts.map((a) => <option key={a.id} value={a.id}>{a.address}</option>)}
          </select>
        </div>
        <div className="grid sm:grid-cols-3 gap-3">
          <Toggle label="Delivery" hint="Record sent / delivered / bounced status" icon={<Truck size={14} />} checked={t.delivery} onChange={(v) => setT({ ...t, delivery: v })} />
          <Toggle label="Open" hint="Insert a tracking pixel to detect opens" icon={<Eye size={14} />} checked={t.open} onChange={(v) => setT({ ...t, open: v })} />
          <Toggle label="Link / button click" hint="Route links through a redirect to count clicks" icon={<MousePointerClick size={14} />} checked={t.click} onChange={(v) => setT({ ...t, click: v })} />
        </div>
        <div className="mt-3">
          <button className="btn-primary" onClick={save} disabled={saving}>{saving ? 'Saving…' : 'Save tracking settings'}</button>
        </div>
      </section>

      <section>
        <h3 className="font-medium mb-3">Recent sent — tracking status</h3>
        <ul className="border border-ink-100 rounded-lg divide-y divide-ink-100">
          {sent.map((m) => (
            <li key={m.id}>
              <button className="w-full text-left p-3 flex items-center gap-3 hover:bg-ink-50" onClick={() => toggleDetail(m.track_id)}>
                <div className="flex-1 min-w-0">
                  <div className="font-medium truncate">{m.subject || '(no subject)'}</div>
                  <div className="text-xs text-ink-500 truncate">to {m.to?.map((a) => a.address).join(', ')}</div>
                </div>
                <div className="flex items-center gap-3 text-xs shrink-0">
                  <span className="px-2 py-0.5 rounded-full bg-ink-100 text-ink-600 capitalize">{m.status}</span>
                  <span className="flex items-center gap-1 text-ink-600" title="Opens"><MailOpen size={13} /> {m.open_count}</span>
                  <span className="flex items-center gap-1 text-ink-600" title="Clicks"><MousePointerClick size={13} /> {m.click_count}</span>
                  <span className="text-ink-400">{new Date(m.sent_at).toLocaleString()}</span>
                </div>
              </button>
              {openId === m.track_id && (
                <div className="px-4 pb-3 bg-ink-50">
                  <div className="text-xs text-ink-500 mb-1">
                    Tracking: {[m.track_delivery && 'delivery', m.track_open && 'open', m.track_click && 'click'].filter(Boolean).join(', ') || 'none'}
                    {m.first_open_at && ` · first open ${new Date(m.first_open_at).toLocaleString()}`}
                  </div>
                  <ul className="text-xs divide-y divide-ink-100">
                    {events.map((ev) => (
                      <li key={ev.id} className="py-1 flex items-center gap-2">
                        <span className="capitalize font-medium w-16">{ev.type}</span>
                        {ev.url && <span className="text-brand-600 truncate flex-1">{ev.url}</span>}
                        <span className="text-ink-400 ml-auto">{new Date(ev.at).toLocaleString()}</span>
                      </li>
                    ))}
                    {events.length === 0 && <li className="py-2 text-ink-400">No opens or clicks recorded yet.</li>}
                  </ul>
                </div>
              )}
            </li>
          ))}
          {sent.length === 0 && <li className="p-8 text-center text-sm text-ink-500">Nothing sent from this mailbox yet.</li>}
        </ul>
      </section>
    </div>
  )
}
