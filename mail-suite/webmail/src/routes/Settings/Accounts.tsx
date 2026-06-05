import { FormEvent, useEffect, useState } from 'react'
import toast from 'react-hot-toast'
import { useAccounts } from '@/store/accounts'
import { Trash2, Star } from 'lucide-react'
import { api } from '@/api/client'

export default function AccountsPage() {
  const { accounts, load, remove } = useAccounts()
  const [provider, setProvider] = useState<'betazen' | 'imap'>('betazen')
  const [form, setForm] = useState({
    display_name: '', address: '', password: '',
    imap_host: '', imap_port: 993, imap_ssl: true,
    smtp_host: '', smtp_port: 465, smtp_ssl: true,
    username: '', color: '#4f46e5',
  })

  useEffect(() => { void load() }, [load])

  async function setPrimary(id: string) {
    try { await api.post(`/accounts/${id}/primary`); toast.success('Primary set'); load() } catch { toast.error('Failed') }
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    try {
      await api.post('/accounts', { ...form, provider })
      toast.success('Account added')
      setForm({ ...form, address: '', password: '', display_name: '' })
      void load()
    } catch (err: any) {
      toast.error(err?.response?.data?.error || 'Failed')
    }
  }

  return (
    <div className="space-y-6">
      <section>
        <h3 className="font-medium mb-3">Connected mailboxes</h3>
        <ul className="divide-y divide-ink-100 border border-ink-100 rounded-lg">
          {accounts.map((a) => (
            <li key={a.id} className="flex items-center gap-3 p-3">
              <span className="inline-block h-8 w-8 rounded-full text-white grid place-items-center font-semibold" style={{ background: a.color || '#4f46e5' }}>
                {a.address[0].toUpperCase()}
              </span>
              <div className="flex-1 min-w-0">
                <div className="font-medium truncate">{a.display_name || a.address}</div>
                <div className="text-xs text-ink-500 truncate">{a.address} · {a.provider}</div>
              </div>
              {a.is_primary && <span className="text-xs text-brand-700 bg-brand-50 px-2 py-0.5 rounded-full">Primary</span>}
              {!a.is_primary && (
                <button onClick={() => setPrimary(a.id)} className="btn-ghost text-xs" title="Set primary"><Star size={14} /></button>
              )}
              <button onClick={() => void remove(a.id).then(() => toast.success('Removed'))} className="btn-ghost text-red-600"><Trash2 size={14} /></button>
            </li>
          ))}
          {accounts.length === 0 && <li className="p-4 text-sm text-ink-500">No mailboxes yet.</li>}
        </ul>
      </section>

      <section>
        <h3 className="font-medium mb-3">Add a mailbox</h3>
        <form onSubmit={onSubmit} className="grid grid-cols-2 gap-3">
          <div className="col-span-2 flex gap-2">
            <button type="button" onClick={() => setProvider('betazen')} className={provider === 'betazen' ? 'btn-primary' : 'btn-ghost'}>Betazen mailbox</button>
            <button type="button" onClick={() => setProvider('imap')} className={provider === 'imap' ? 'btn-primary' : 'btn-ghost'}>External IMAP</button>
          </div>
          <input className="input" placeholder="Display name" value={form.display_name} onChange={(e) => setForm({ ...form, display_name: e.target.value })} required />
          <input className="input" placeholder="Email address" type="email" value={form.address} onChange={(e) => setForm({ ...form, address: e.target.value })} required />
          <input className="input col-span-2" placeholder="Password" type="password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} required />
          {provider === 'imap' && (
            <>
              <input className="input" placeholder="IMAP host" value={form.imap_host} onChange={(e) => setForm({ ...form, imap_host: e.target.value })} />
              <input className="input" placeholder="IMAP port" type="number" value={form.imap_port} onChange={(e) => setForm({ ...form, imap_port: +e.target.value })} />
              <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={form.imap_ssl} onChange={(e) => setForm({ ...form, imap_ssl: e.target.checked })} /> IMAP SSL</label>
              <input className="input" placeholder="SMTP host" value={form.smtp_host} onChange={(e) => setForm({ ...form, smtp_host: e.target.value })} />
              <input className="input" placeholder="SMTP port" type="number" value={form.smtp_port} onChange={(e) => setForm({ ...form, smtp_port: +e.target.value })} />
              <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={form.smtp_ssl} onChange={(e) => setForm({ ...form, smtp_ssl: e.target.checked })} /> SMTP SSL</label>
              <input className="input col-span-2" placeholder="Username (defaults to email)" value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} />
            </>
          )}
          <input className="input" placeholder="Color (hex)" value={form.color} onChange={(e) => setForm({ ...form, color: e.target.value })} />
          <div className="col-span-2"><button type="submit" className="btn-primary">Add mailbox</button></div>
        </form>
      </section>
    </div>
  )
}
