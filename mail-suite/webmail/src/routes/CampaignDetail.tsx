import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import toast from 'react-hot-toast'
import { ArrowLeft, Pause, Play, Ban, Trash2, MailOpen, MousePointerClick, Send } from 'lucide-react'
import clsx from 'clsx'
import { api } from '@/api/client'
import { Campaign, CampaignRecipient, CampaignStats, TrackingEvent } from '@/api/types'
import { StatusBadge } from './Campaigns'

function recipStatusCls(s: string) {
  return s === 'sent' ? 'bg-green-100 text-green-700'
    : s === 'failed' || s === 'bounced' ? 'bg-red-100 text-red-700'
    : s === 'unsubscribed' ? 'bg-ink-100 text-ink-500'
    : 'bg-amber-100 text-amber-700'
}

function Stat({ label, value, sub, className }: { label: string; value: number; sub?: string; className?: string }) {
  return (
    <div className="card p-3 text-center">
      <div className={clsx('text-2xl font-semibold', className)}>{value}</div>
      <div className="text-xs text-ink-500">{label}</div>
      {sub && <div className="text-[10px] text-ink-400 mt-0.5">{sub}</div>}
    </div>
  )
}

function RateCard({ label, value, sub, bar }: { label: string; value: number; sub: string; bar: string }) {
  return (
    <div className="card p-3">
      <div className="flex items-baseline justify-between">
        <span className="text-xs text-ink-500">{label}</span>
        <span className="text-xs text-ink-400">{sub}</span>
      </div>
      <div className="text-2xl font-semibold mt-0.5">{value}%</div>
      <div className="h-1.5 bg-ink-100 rounded mt-1.5 overflow-hidden">
        <div className={clsx('h-1.5 rounded transition-all', bar)} style={{ width: `${Math.min(100, value)}%` }} />
      </div>
    </div>
  )
}

export default function CampaignDetail() {
  const { id } = useParams<{ id: string }>()
  const nav = useNavigate()
  const [c, setC] = useState<Campaign | null>(null)
  const [stats, setStats] = useState<CampaignStats | null>(null)
  const [recips, setRecips] = useState<CampaignRecipient[]>([])
  const [loading, setLoading] = useState(true)
  const [openRid, setOpenRid] = useState<string | null>(null)
  const [events, setEvents] = useState<Record<string, TrackingEvent[] | undefined>>({})

  async function toggleRecipient(r: CampaignRecipient) {
    if (openRid === r.id) { setOpenRid(null); return }
    setOpenRid(r.id)
    setEvents((m) => ({ ...m, [r.id]: undefined })) // show "loading…" until fetched
    try {
      const rr = await api.get<{ data: TrackingEvent[] }>(`/campaigns/${id}/recipients/${r.id}/events`)
      setEvents((m) => ({ ...m, [r.id]: rr.data.data || [] }))
    } catch {
      setEvents((m) => ({ ...m, [r.id]: [] }))
    }
  }

  async function load() {
    if (!id) return
    try {
      const [rc, rs, rr] = await Promise.all([
        api.get<{ data: Campaign }>(`/campaigns/${id}`),
        api.get<{ data: CampaignStats }>(`/campaigns/${id}/stats`),
        api.get<{ data: CampaignRecipient[] }>(`/campaigns/${id}/recipients`, { params: { limit: 200 } }),
      ])
      setC(rc.data.data)
      setStats(rs.data.data)
      setRecips(rr.data.data || [])
    } catch {
      setC(null)
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => { void load() }, [id])
  // live-refresh while the campaign is actively sending
  useEffect(() => {
    if (c?.status !== 'sending') return
    const t = setInterval(() => void load(), 4000)
    return () => clearInterval(t)
  }, [c?.status]) // eslint-disable-line react-hooks/exhaustive-deps

  async function act(path: string, ok: string) {
    try { await api.post(`/campaigns/${id}/${path}`); toast.success(ok); void load() }
    catch (e: any) { toast.error(e?.response?.data?.error || 'Failed') }
  }
  async function remove() {
    if (!window.confirm('Delete this campaign and all its recipient data?')) return
    try { await api.delete(`/campaigns/${id}`); toast.success('Deleted'); nav('/campaigns') }
    catch { toast.error('Failed') }
  }

  if (loading) return <div className="p-8 text-ink-500">Loading…</div>
  if (!c || !stats) return <div className="p-8 text-ink-500">Campaign not found.</div>

  const pct = stats.total ? Math.round((stats.sent / stats.total) * 100) : 0
  const rate = (n: number, d: number) => (d > 0 ? Math.round((n / d) * 100) : 0)

  return (
    <div className="p-3 max-w-4xl mx-auto">
      <div className="flex items-center gap-2 mb-3">
        <button className="btn-ghost" onClick={() => nav('/campaigns')}><ArrowLeft size={16} /></button>
        <h1 className="text-xl font-semibold truncate flex-1">{c.name}</h1>
        <StatusBadge status={c.status} />
      </div>

      <div className="card p-4 mb-3 flex items-center gap-3 flex-wrap">
        <div className="text-sm text-ink-600 flex-1 min-w-0">
          <div className="truncate"><span className="text-ink-400">Subject:</span> {c.subject}</div>
          <div className="text-xs text-ink-400 mt-0.5">
            {c.mode === 'drip' ? `Drip · ${c.batch_size} every ${Math.round(c.interval_seconds / 60)} min` : `Send now · ${c.batch_size}/batch`}
            {c.next_run_at && c.status === 'sending' && c.mode === 'drip' && ` · next ${new Date(c.next_run_at).toLocaleTimeString()}`}
          </div>
        </div>
        {c.status === 'draft' && <button className="btn-primary" onClick={() => act('start', 'Sending started')}><Send size={14} /> Send</button>}
        {c.status === 'sending' && <button className="btn-ghost" onClick={() => act('pause', 'Paused')}><Pause size={14} /> Pause</button>}
        {c.status === 'paused' && <button className="btn-primary" onClick={() => act('start', 'Resumed')}><Play size={14} /> Resume</button>}
        {(c.status === 'sending' || c.status === 'paused') && <button className="btn-ghost text-amber-600" onClick={() => act('cancel', 'Canceled')}><Ban size={14} /> Cancel</button>}
        <button className="btn-ghost text-red-600" onClick={remove}><Trash2 size={14} /></button>
      </div>

      {(c.status === 'sending' || c.status === 'sent' || c.status === 'paused') && (
        <div className="mb-3">
          <div className="flex justify-between text-xs text-ink-500 mb-1"><span>{stats.sent} / {stats.total} sent</span><span>{pct}%</span></div>
          <div className="h-2 bg-ink-100 rounded"><div className="h-2 bg-brand-500 rounded transition-all" style={{ width: `${pct}%` }} /></div>
        </div>
      )}

      <div className="grid grid-cols-3 sm:grid-cols-6 gap-2 mb-4">
        <Stat label="Recipients" value={stats.total} />
        <Stat label="Sent" value={stats.sent} className="text-green-600" />
        <Stat label="Opened" value={stats.opened} sub={`${stats.open_total} total opens`} className="text-green-600" />
        <Stat label="Clicked" value={stats.clicked} sub={`${stats.click_total} total clicks`} className="text-blue-600" />
        <Stat label="Unsub" value={stats.unsubscribed} className="text-ink-500" />
        <Stat label="Failed" value={stats.failed + stats.bounced} className="text-red-600" />
      </div>

      {stats.sent > 0 && (
        <div className="grid grid-cols-3 gap-2 mb-4">
          <RateCard label="Open rate" value={rate(stats.opened, stats.sent)} sub={`${stats.opened}/${stats.sent}`} bar="bg-green-500" />
          <RateCard label="Click rate" value={rate(stats.clicked, stats.sent)} sub={`${stats.clicked}/${stats.sent}`} bar="bg-blue-500" />
          <RateCard label="Click-to-open" value={rate(stats.clicked, stats.opened)} sub={`${stats.clicked}/${stats.opened}`} bar="bg-brand-500" />
        </div>
      )}

      <div className="card overflow-hidden">
        <div className="px-4 py-2 border-b border-ink-100 text-sm font-medium">Recipients <span className="text-ink-400 font-normal">— click a row for its full open/click timeline</span></div>
        <ul className="divide-y divide-ink-100 max-h-[55vh] overflow-auto">
          {recips.map((r) => {
            const evs = events[r.id]
            return (
            <li key={r.id}>
              <button onClick={() => toggleRecipient(r)} className="w-full text-left px-4 py-2 flex items-center gap-3 text-sm hover:bg-ink-50">
                <div className="flex-1 min-w-0">
                  <div className="truncate">{r.email}</div>
                  {r.error && <div className="text-xs text-red-500 truncate">{r.error}</div>}
                </div>
                <span className={clsx('px-2 py-0.5 rounded-full text-xs capitalize shrink-0', recipStatusCls(r.status))}>{r.status}</span>
                {r.open_count > 0 && <span className="text-green-600 flex items-center gap-0.5 shrink-0"><MailOpen size={13} /> {r.open_count}</span>}
                {r.click_count > 0 && <span className="text-blue-600 flex items-center gap-0.5 shrink-0"><MousePointerClick size={13} /> {r.click_count}</span>}
              </button>
              {openRid === r.id && (
                <div className="px-4 pb-3 bg-ink-50/70">
                  {evs === undefined ? (
                    <div className="text-xs text-ink-400 py-2">Loading timeline…</div>
                  ) : evs.length === 0 ? (
                    <div className="text-xs text-ink-400 py-2">No opens or clicks recorded for this recipient yet.</div>
                  ) : (
                    <ul className="text-xs divide-y divide-ink-100 border border-ink-100 rounded-lg bg-white">
                      {evs.map((ev) => (
                        <li key={ev.id} className="px-3 py-1.5 flex items-center gap-2">
                          <span className={clsx('flex items-center gap-1 font-medium w-16 shrink-0 capitalize', ev.type === 'click' ? 'text-blue-600' : 'text-green-600')}>
                            {ev.type === 'click' ? <MousePointerClick size={12} /> : <MailOpen size={12} />} {ev.type}
                          </span>
                          {ev.url && <a href={ev.url} target="_blank" rel="noreferrer" className="text-brand-600 truncate flex-1 hover:underline" title={ev.url}>{ev.url}</a>}
                          <span className="text-ink-400 ml-auto shrink-0">{new Date(ev.at).toLocaleString()}</span>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              )}
            </li>
          )})}
          {recips.length === 0 && <li className="p-6 text-center text-sm text-ink-500">No recipients yet.</li>}
        </ul>
      </div>
    </div>
  )
}
