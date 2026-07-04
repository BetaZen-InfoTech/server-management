import { FormEvent, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import toast from 'react-hot-toast'
import { Megaphone, Plus, X, Send, Clock } from 'lucide-react'
import clsx from 'clsx'
import { api } from '@/api/client'
import { Campaign, CampaignTemplate, ContactGroup } from '@/api/types'
import { useAccounts } from '@/store/accounts'
import RichEditor from '@/components/RichEditor'

const INTERVALS: { label: string; secs: number }[] = [
  { label: 'every 1 minute', secs: 60 },
  { label: 'every 2 minutes', secs: 120 },
  { label: 'every 5 minutes', secs: 300 },
  { label: 'every 15 minutes', secs: 900 },
  { label: 'every 30 minutes', secs: 1800 },
  { label: 'every 1 hour', secs: 3600 },
  { label: 'every 2 hours', secs: 7200 },
  { label: 'every 6 hours', secs: 21600 },
  { label: 'every 24 hours', secs: 86400 },
]

export function StatusBadge({ status }: { status: string }) {
  const cls =
    status === 'sending' ? 'bg-blue-100 text-blue-700'
    : status === 'sent' ? 'bg-green-100 text-green-700'
    : status === 'paused' ? 'bg-amber-100 text-amber-700'
    : status === 'failed' ? 'bg-red-100 text-red-700'
    : status === 'canceled' ? 'bg-ink-100 text-ink-500'
    : 'bg-ink-100 text-ink-600'
  return <span className={clsx('px-2 py-0.5 rounded-full text-xs font-medium capitalize', cls)}>{status}</span>
}

export default function Campaigns() {
  const [items, setItems] = useState<Campaign[]>([])
  const [groups, setGroups] = useState<ContactGroup[]>([])
  const [editing, setEditing] = useState<Campaign | 'new' | null>(null)
  const nav = useNavigate()

  async function load() {
    try {
      const r = await api.get<{ data: Campaign[] }>('/campaigns')
      setItems(r.data.data || [])
    } catch { setItems([]) }
  }
  useEffect(() => {
    void load()
    api.get<{ data: ContactGroup[] }>('/contact-groups').then((r) => setGroups(r.data.data || [])).catch(() => {})
  }, [])

  return (
    <div className="p-3 max-w-5xl mx-auto">
      <div className="flex items-center gap-2 mb-3">
        <h1 className="text-xl font-semibold flex items-center gap-2"><Megaphone size={20} /> Campaigns</h1>
        <div className="flex-1" />
        <button className="btn-primary" onClick={() => setEditing('new')}><Plus size={15} /> New campaign</button>
      </div>

      <ul className="card divide-y divide-ink-100 overflow-hidden">
        {items.map((c) => {
          const pct = c.total_recipients ? Math.round((c.sent_count / c.total_recipients) * 100) : 0
          return (
            <li key={c.id} onClick={() => nav(`/campaigns/${c.id}`)} className="p-3 flex items-center gap-3 cursor-pointer hover:bg-ink-50">
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2">
                  <span className="font-medium truncate">{c.name || '(untitled)'}</span>
                  <StatusBadge status={c.status} />
                  {c.mode === 'drip' && <span className="text-xs text-ink-400 flex items-center gap-1"><Clock size={11} /> drip</span>}
                </div>
                <div className="text-xs text-ink-500 truncate">{c.subject}</div>
              </div>
              <div className="text-xs text-ink-500 shrink-0 w-40">
                {(c.status === 'sending' || c.status === 'sent' || c.status === 'paused') && (
                  <>
                    <div className="flex justify-between"><span>{c.sent_count}/{c.total_recipients}</span><span>{pct}%</span></div>
                    <div className="h-1.5 bg-ink-100 rounded mt-1"><div className="h-1.5 bg-brand-500 rounded" style={{ width: `${pct}%` }} /></div>
                  </>
                )}
              </div>
            </li>
          )
        })}
        {items.length === 0 && <li className="p-10 text-center text-sm text-ink-500">No campaigns yet. Create one to email a contact group.</li>}
      </ul>

      {editing && (
        <CampaignEditor
          campaign={editing === 'new' ? null : editing}
          groups={groups}
          onClose={() => setEditing(null)}
          onSaved={() => { setEditing(null); void load() }}
        />
      )}
    </div>
  )
}

function CampaignEditor({ campaign, groups, onClose, onSaved }: {
  campaign: Campaign | null; groups: ContactGroup[]; onClose: () => void; onSaved: () => void
}) {
  const { accounts } = useAccounts()
  const [name, setName] = useState(campaign?.name || '')
  const [accountId, setAccountId] = useState(campaign?.account_id || '')
  const [subject, setSubject] = useState(campaign?.subject || '')
  const [html, setHtml] = useState(campaign?.html || '<p>Hi {{first_name}},</p><p></p>')
  const [gids, setGids] = useState<string[]>(campaign?.group_ids || [])
  const [allContacts, setAllContacts] = useState(campaign?.all_contacts || false)
  const [totalContacts, setTotalContacts] = useState<number | null>(null)
  const [mode, setMode] = useState<'now' | 'drip'>(campaign?.mode || 'now')
  const [intervalSecs, setIntervalSecs] = useState(campaign?.interval_seconds || 300)
  const [batchSize, setBatchSize] = useState(campaign?.batch_size || 50)
  const [busy, setBusy] = useState(false)
  const [templates, setTemplates] = useState<CampaignTemplate[]>([])

  useEffect(() => { if (!accountId && accounts[0]) setAccountId(accounts[0].id) }, [accounts])
  useEffect(() => {
    api.get<{ data: CampaignTemplate[] }>('/campaign-templates').then((r) => setTemplates(r.data.data || [])).catch(() => {})
    // total contact count, for the "All contacts" estimate
    api.get<{ data: { total: number } }>('/contacts', { params: { limit: 1 } })
      .then((r) => setTotalContacts(r.data?.data?.total ?? null)).catch(() => {})
  }, [])

  function loadTemplate(id: string) {
    const t = templates.find((x) => x.id === id)
    if (!t) return
    if (t.subject) setSubject(t.subject)
    setHtml(t.html)
    toast.success(`Loaded "${t.name}"`)
  }
  async function saveTemplate() {
    const nm = window.prompt('Save this body as a template — name?')
    if (!nm?.trim()) return
    try {
      const r = await api.post<{ data: CampaignTemplate }>('/campaign-templates', { name: nm.trim(), subject, html })
      setTemplates((xs) => [r.data.data, ...xs])
      toast.success('Template saved')
    } catch (e: any) { toast.error(e?.response?.data?.error || 'Failed') }
  }

  const recipientEstimate = groups.filter((g) => gids.includes(g.id)).reduce((n, g) => n + g.contact_count, 0)

  function body() {
    return {
      account_id: accountId, name, subject, html,
      group_ids: allContacts ? [] : gids, all_contacts: allContacts,
      mode, batch_size: batchSize, interval_seconds: mode === 'drip' ? intervalSecs : 0,
    }
  }

  async function save(): Promise<string | null> {
    if (!name.trim() || !subject.trim() || !accountId || (!allContacts && gids.length === 0)) {
      toast.error('Add a name, subject, sender, and pick recipients (a group or “All contacts”)')
      return null
    }
    try {
      if (campaign) {
        await api.put(`/campaigns/${campaign.id}`, body())
        return campaign.id
      }
      const r = await api.post<{ data: Campaign }>('/campaigns', body())
      return r.data.data.id
    } catch (e: any) {
      toast.error(e?.response?.data?.error || 'Save failed')
      return null
    }
  }

  async function onSaveDraft(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    const id = await save()
    setBusy(false)
    if (id) { toast.success('Saved'); onSaved() }
  }

  async function onSaveSend() {
    const target = allContacts ? `ALL${totalContacts != null ? ` ~${totalContacts}` : ''}` : `~${recipientEstimate}`
    if (!window.confirm(`Send this campaign to ${target} subscribed contact(s)${mode === 'drip' ? ' as a drip' : ''}?`)) return
    setBusy(true)
    const id = await save()
    if (id) {
      try {
        await api.post(`/campaigns/${id}/start`)
        toast.success(mode === 'drip' ? 'Drip started' : 'Sending started')
        onSaved()
      } catch (e: any) { toast.error(e?.response?.data?.error || 'Could not start') }
    }
    setBusy(false)
  }

  function toggleGroup(id: string) {
    setGids((xs) => xs.includes(id) ? xs.filter((x) => x !== id) : [...xs, id])
  }

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center p-3 overflow-auto bg-black/30">
      <div className="relative bg-white rounded-xl shadow-2xl border border-ink-200 w-full max-w-2xl my-4">
        <div className="flex items-center justify-between px-5 py-3 border-b border-ink-100 sticky top-0 bg-white rounded-t-xl">
          <h3 className="font-semibold">{campaign ? 'Edit campaign' : 'New campaign'}</h3>
          <button onClick={onClose} className="p-1 rounded hover:bg-ink-100"><X size={16} /></button>
        </div>
        <form onSubmit={onSaveDraft} className="p-5 flex flex-col gap-3">
          <input className="input" placeholder="Campaign name (internal)" value={name} onChange={(e) => setName(e.target.value)} />
          <div className="grid grid-cols-2 gap-3">
            <select className="input" value={accountId} onChange={(e) => setAccountId(e.target.value)}>
              {accounts.map((a) => <option key={a.id} value={a.id}>From: {a.address}</option>)}
            </select>
            <input className="input" placeholder="Subject" value={subject} onChange={(e) => setSubject(e.target.value)} />
          </div>

          <div>
            <div className="flex items-center gap-2 mb-1">
              <div className="text-xs text-ink-500 flex-1">Body — use <code className="text-[11px]">{'{{first_name}}'}</code>, <code className="text-[11px]">{'{{name}}'}</code>, <code className="text-[11px]">{'{{email}}'}</code> for personalization</div>
              {templates.length > 0 && (
                <select className="input py-1 text-xs max-w-[150px]" value="" onChange={(e) => { loadTemplate(e.target.value); e.currentTarget.value = '' }}>
                  <option value="">Load template…</option>
                  {templates.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
                </select>
              )}
              <button type="button" className="btn-ghost text-xs py-1 px-2" onClick={saveTemplate}>Save as template</button>
            </div>
            <RichEditor value={html} onChange={setHtml} minHeight={200} />
          </div>

          <div>
            <div className="text-xs text-ink-500 mb-1.5">Recipients</div>
            <div className="flex flex-wrap gap-4 text-sm mb-2">
              <label className="flex items-center gap-1.5"><input type="radio" checked={!allContacts} onChange={() => setAllContacts(false)} /> Selected groups</label>
              <label className="flex items-center gap-1.5">
                <input type="radio" checked={allContacts} onChange={() => setAllContacts(true)} /> All contacts
                {totalContacts != null && <span className="text-ink-400">(~{totalContacts})</span>}
              </label>
            </div>
            {allContacts ? (
              <div className="text-xs text-emerald-700 bg-emerald-50 border border-emerald-200 rounded-lg px-2.5 py-1.5">
                Sends to <b>every subscribed contact</b>{totalContacts != null ? ` (~${totalContacts})` : ''} — groups are ignored. Unsubscribed / bounced contacts are always skipped.
              </div>
            ) : (
              <>
                <div className="flex flex-wrap gap-1.5">
                  {groups.map((g) => (
                    <button key={g.id} type="button" onClick={() => toggleGroup(g.id)}
                      className={clsx('px-2.5 py-1 rounded-full text-xs border', gids.includes(g.id) ? 'bg-brand-50 border-brand-300 text-brand-700' : 'border-ink-200 text-ink-500')}>
                      {g.name} <span className="text-ink-400">{g.contact_count}</span>
                    </button>
                  ))}
                  {groups.length === 0 && <span className="text-xs text-ink-400">No groups yet — create groups in Contacts, or pick “All contacts” above.</span>}
                </div>
                {recipientEstimate > 0 && <div className="text-xs text-ink-400 mt-1">~{recipientEstimate} contacts selected</div>}
              </>
            )}
          </div>

          <div className="border border-ink-100 rounded-lg p-3">
            <div className="flex gap-4 text-sm mb-2">
              <label className="flex items-center gap-1.5"><input type="radio" checked={mode === 'now'} onChange={() => setMode('now')} /> Send now</label>
              <label className="flex items-center gap-1.5"><input type="radio" checked={mode === 'drip'} onChange={() => setMode('drip')} /> Drip (scheduled batches)</label>
            </div>
            <div className="flex items-center gap-2 text-sm text-ink-600">
              <span>Send</span>
              <input type="number" min={1} max={500} className="input w-20 py-1" value={batchSize} onChange={(e) => setBatchSize(Math.max(1, +e.target.value || 1))} />
              <span>emails</span>
              {mode === 'drip' ? (
                <select className="input py-1" value={intervalSecs} onChange={(e) => setIntervalSecs(+e.target.value)}>
                  {INTERVALS.map((i) => <option key={i.secs} value={i.secs}>{i.label}</option>)}
                </select>
              ) : (
                <span className="text-ink-400">per batch, as fast as possible</span>
              )}
            </div>
          </div>

          <div className="flex justify-end gap-2 pt-1">
            <button type="submit" className="btn-ghost" disabled={busy}>Save draft</button>
            <button type="button" className="btn-primary" disabled={busy} onClick={onSaveSend}>
              <Send size={15} /> {mode === 'drip' ? 'Save & start drip' : 'Save & send'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
