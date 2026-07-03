import { FormEvent, useEffect, useState } from 'react'
import toast from 'react-hot-toast'
import { Users, Plus, Upload, Trash2, Pencil, Search, X, Tag } from 'lucide-react'
import clsx from 'clsx'
import { api } from '@/api/client'
import { Contact, ContactGroup, ContactImportResult } from '@/api/types'

const STATUSES = ['subscribed', 'unsubscribed', 'bounced', 'complained'] as const

export default function Contacts() {
  const [groups, setGroups] = useState<ContactGroup[]>([])
  const [contacts, setContacts] = useState<Contact[]>([])
  const [total, setTotal] = useState(0)
  const [groupFilter, setGroupFilter] = useState('')
  const [status, setStatus] = useState('')
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const [editing, setEditing] = useState<Contact | 'new' | null>(null)
  const [importing, setImporting] = useState(false)
  const limit = 50

  async function loadGroups() {
    try {
      const r = await api.get<{ data: ContactGroup[] }>('/contact-groups')
      setGroups(r.data.data || [])
    } catch { /* ignore */ }
  }
  async function loadContacts() {
    try {
      const r = await api.get<{ data: { items: Contact[]; total: number } }>('/contacts', {
        params: { group_id: groupFilter || undefined, status: status || undefined, search: search || undefined, page, limit },
      })
      setContacts(r.data.data?.items || [])
      setTotal(r.data.data?.total || 0)
    } catch {
      setContacts([]); setTotal(0)
    }
  }
  useEffect(() => { void loadGroups() }, [])
  useEffect(() => { void loadContacts() }, [groupFilter, status, page])
  // debounce search
  useEffect(() => {
    const t = setTimeout(() => { setPage(1); void loadContacts() }, 350)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [search])

  async function newGroup() {
    const name = window.prompt('New group name')
    if (!name?.trim()) return
    try {
      await api.post('/contact-groups', { name: name.trim() })
      toast.success('Group created'); void loadGroups()
    } catch (e: any) { toast.error(e?.response?.data?.error || 'Failed') }
  }
  async function deleteGroup(g: ContactGroup) {
    if (!window.confirm(`Delete group "${g.name}"? Contacts stay, only the group is removed.`)) return
    try {
      await api.delete(`/contact-groups/${g.id}`)
      toast.success('Group deleted')
      if (groupFilter === g.id) setGroupFilter('')
      void loadGroups(); void loadContacts()
    } catch { toast.error('Failed') }
  }
  async function removeContact(c: Contact) {
    if (!window.confirm(`Delete ${c.email}?`)) return
    try { await api.delete(`/contacts/${c.id}`); toast.success('Deleted'); void loadContacts(); void loadGroups() }
    catch { toast.error('Failed') }
  }

  const pages = Math.max(1, Math.ceil(total / limit))

  return (
    <div className="p-3 max-w-6xl mx-auto">
      <div className="flex items-center gap-2 mb-3">
        <h1 className="text-xl font-semibold flex items-center gap-2"><Users size={20} /> Contacts</h1>
        <span className="text-sm text-ink-400">{total}</span>
        <div className="flex-1" />
        <button className="btn-ghost" onClick={() => setImporting(true)}><Upload size={15} /> Import</button>
        <button className="btn-primary" onClick={() => setEditing('new')}><Plus size={15} /> Add contact</button>
      </div>

      <div className="grid grid-cols-[220px,1fr] gap-3">
        {/* Groups rail */}
        <aside className="card p-2 h-max">
          <button
            onClick={() => { setGroupFilter(''); setPage(1) }}
            className={clsx('w-full text-left px-3 py-2 rounded-lg text-sm flex justify-between', !groupFilter ? 'bg-brand-50 text-brand-700 font-medium' : 'hover:bg-ink-100')}
          >All contacts</button>
          {groups.map((g) => (
            <div key={g.id} className={clsx('group flex items-center rounded-lg', groupFilter === g.id ? 'bg-brand-50' : 'hover:bg-ink-100')}>
              <button onClick={() => { setGroupFilter(g.id); setPage(1) }} className={clsx('flex-1 text-left px-3 py-2 text-sm flex items-center gap-2 min-w-0', groupFilter === g.id && 'text-brand-700 font-medium')}>
                <Tag size={13} className="shrink-0" style={{ color: g.color || undefined }} />
                <span className="truncate">{g.name}</span>
                <span className="ml-auto text-xs text-ink-400">{g.contact_count}</span>
              </button>
              <button onClick={() => deleteGroup(g)} className="opacity-0 group-hover:opacity-100 px-2 text-ink-400 hover:text-red-600" title="Delete group"><Trash2 size={13} /></button>
            </div>
          ))}
          <button onClick={newGroup} className="w-full text-left px-3 py-2 rounded-lg text-sm text-ink-500 hover:bg-ink-100 flex items-center gap-2"><Plus size={13} /> New group</button>
        </aside>

        {/* Contacts table */}
        <section className="card overflow-hidden">
          <div className="flex items-center gap-2 p-2 border-b border-ink-100">
            <div className="flex items-center gap-2 flex-1 px-2">
              <Search size={15} className="text-ink-400" />
              <input className="flex-1 bg-transparent text-sm focus:outline-none py-1.5" placeholder="Search email or name" value={search} onChange={(e) => setSearch(e.target.value)} />
            </div>
            <select className="input max-w-[150px] text-sm" value={status} onChange={(e) => { setStatus(e.target.value); setPage(1) }}>
              <option value="">All statuses</option>
              {STATUSES.map((s) => <option key={s} value={s}>{s}</option>)}
            </select>
          </div>
          <ul className="divide-y divide-ink-100">
            {contacts.map((c) => (
              <li key={c.id} className="grid grid-cols-[1fr,120px,80px] items-center gap-3 px-4 py-2.5 hover:bg-ink-50 group">
                <div className="min-w-0">
                  <div className="text-sm font-medium truncate">{c.email}</div>
                  {c.name && <div className="text-xs text-ink-500 truncate">{c.name}</div>}
                </div>
                <StatusPill status={c.status} />
                <div className="flex items-center justify-end gap-1 opacity-0 group-hover:opacity-100">
                  <button onClick={() => setEditing(c)} className="p-1.5 rounded hover:bg-ink-100 text-ink-500" title="Edit"><Pencil size={14} /></button>
                  <button onClick={() => removeContact(c)} className="p-1.5 rounded hover:bg-ink-100 text-red-600" title="Delete"><Trash2 size={14} /></button>
                </div>
              </li>
            ))}
            {contacts.length === 0 && <li className="p-10 text-center text-sm text-ink-500">No contacts. Add one or import a list.</li>}
          </ul>
          {pages > 1 && (
            <div className="flex items-center justify-between p-2 border-t border-ink-100 text-sm">
              <button className="btn-ghost" disabled={page <= 1} onClick={() => setPage((p) => p - 1)}>Prev</button>
              <span className="text-ink-500">Page {page} / {pages}</span>
              <button className="btn-ghost" disabled={page >= pages} onClick={() => setPage((p) => p + 1)}>Next</button>
            </div>
          )}
        </section>
      </div>

      {editing && (
        <ContactModal
          contact={editing === 'new' ? null : editing}
          groups={groups}
          onClose={() => setEditing(null)}
          onSaved={() => { setEditing(null); void loadContacts(); void loadGroups() }}
        />
      )}
      {importing && (
        <ImportModal groups={groups} onClose={() => setImporting(false)} onDone={() => { setImporting(false); void loadContacts(); void loadGroups() }} />
      )}
    </div>
  )
}

function StatusPill({ status }: { status: string }) {
  const cls = status === 'subscribed' ? 'bg-green-100 text-green-700'
    : status === 'unsubscribed' ? 'bg-ink-100 text-ink-500'
    : 'bg-amber-100 text-amber-700'
  return <span className={clsx('px-2 py-0.5 rounded-full text-xs font-medium justify-self-start', cls)}>{status}</span>
}

function ContactModal({ contact, groups, onClose, onSaved }: {
  contact: Contact | null; groups: ContactGroup[]; onClose: () => void; onSaved: () => void
}) {
  const [email, setEmail] = useState(contact?.email || '')
  const [name, setName] = useState(contact?.name || '')
  const [status, setStatus] = useState(contact?.status || 'subscribed')
  const [gids, setGids] = useState<string[]>(contact?.group_ids || [])
  const [saving, setSaving] = useState(false)

  function toggle(id: string) {
    setGids((xs) => xs.includes(id) ? xs.filter((x) => x !== id) : [...xs, id])
  }
  async function submit(e: FormEvent) {
    e.preventDefault()
    setSaving(true)
    try {
      const body = { email, name, status, group_ids: gids, fields: contact?.fields || {} }
      if (contact) await api.put(`/contacts/${contact.id}`, body)
      else await api.post('/contacts', body)
      toast.success('Saved'); onSaved()
    } catch (e: any) { toast.error(e?.response?.data?.error || 'Failed') }
    finally { setSaving(false) }
  }
  return (
    <Overlay onClose={onClose} title={contact ? 'Edit contact' : 'Add contact'}>
      <form onSubmit={submit} className="flex flex-col gap-3">
        <input className="input" placeholder="Email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
        <input className="input" placeholder="Name (optional)" value={name} onChange={(e) => setName(e.target.value)} />
        <select className="input" value={status} onChange={(e) => setStatus(e.target.value as any)}>
          {STATUSES.map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
        {groups.length > 0 && (
          <div>
            <div className="text-xs text-ink-500 mb-1">Groups</div>
            <div className="flex flex-wrap gap-1.5">
              {groups.map((g) => (
                <button key={g.id} type="button" onClick={() => toggle(g.id)}
                  className={clsx('px-2.5 py-1 rounded-full text-xs border', gids.includes(g.id) ? 'bg-brand-50 border-brand-300 text-brand-700' : 'border-ink-200 text-ink-500')}>
                  {g.name}
                </button>
              ))}
            </div>
          </div>
        )}
        <div className="flex justify-end gap-2 pt-1">
          <button type="button" className="btn-ghost" onClick={onClose}>Cancel</button>
          <button type="submit" className="btn-primary" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
        </div>
      </form>
    </Overlay>
  )
}

function ImportModal({ groups, onClose, onDone }: { groups: ContactGroup[]; onClose: () => void; onDone: () => void }) {
  const [text, setText] = useState('')
  const [gids, setGids] = useState<string[]>([])
  const [busy, setBusy] = useState(false)

  function parse() {
    // one per line: "email" or "email,name" or "email<TAB>name"
    return text.split('\n').map((l) => l.trim()).filter(Boolean).map((l) => {
      const [email, ...rest] = l.split(/[,\t]/)
      return { email: email.trim(), name: rest.join(' ').trim() }
    }).filter((r) => r.email.includes('@'))
  }
  async function run() {
    const rows = parse()
    if (!rows.length) { toast.error('Add at least one email'); return }
    setBusy(true)
    try {
      const r = await api.post<{ data: ContactImportResult }>('/contacts/import', { group_ids: gids, rows })
      const d = r.data.data
      toast.success(`Imported: ${d.created} new, ${d.updated} updated${d.skipped ? `, ${d.skipped} skipped` : ''}`)
      onDone()
    } catch (e: any) { toast.error(e?.response?.data?.error || 'Import failed') }
    finally { setBusy(false) }
  }
  return (
    <Overlay onClose={onClose} title="Import contacts">
      <p className="text-sm text-ink-500 mb-2">Paste one email per line. Optionally <code className="text-xs">email, Name</code> per line.</p>
      <textarea className="input min-h-[160px] font-mono text-sm" placeholder={"amit@example.com, Amit Roy\npriya@example.com"} value={text} onChange={(e) => setText(e.target.value)} />
      {groups.length > 0 && (
        <div className="mt-2">
          <div className="text-xs text-ink-500 mb-1">Add to groups</div>
          <div className="flex flex-wrap gap-1.5">
            {groups.map((g) => (
              <button key={g.id} type="button" onClick={() => setGids((xs) => xs.includes(g.id) ? xs.filter((x) => x !== g.id) : [...xs, g.id])}
                className={clsx('px-2.5 py-1 rounded-full text-xs border', gids.includes(g.id) ? 'bg-brand-50 border-brand-300 text-brand-700' : 'border-ink-200 text-ink-500')}>{g.name}</button>
            ))}
          </div>
        </div>
      )}
      <div className="flex justify-end gap-2 pt-3">
        <button className="btn-ghost" onClick={onClose}>Cancel</button>
        <button className="btn-primary" onClick={run} disabled={busy}>{busy ? 'Importing…' : 'Import'}</button>
      </div>
    </Overlay>
  )
}

function Overlay({ title, children, onClose }: { title: string; children: React.ReactNode; onClose: () => void }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/30" onClick={onClose} />
      <div className="relative bg-white rounded-xl shadow-2xl border border-ink-200 w-full max-w-md max-h-[90vh] overflow-auto">
        <div className="flex items-center justify-between px-5 py-3 border-b border-ink-100">
          <h3 className="font-semibold">{title}</h3>
          <button onClick={onClose} className="p-1 rounded hover:bg-ink-100"><X size={16} /></button>
        </div>
        <div className="p-5">{children}</div>
      </div>
    </div>
  )
}
