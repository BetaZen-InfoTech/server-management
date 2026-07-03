import { FormEvent, useEffect, useState } from 'react'
import toast from 'react-hot-toast'
import { useAccounts } from '@/store/accounts'
import { Trash2, Star, Eye, EyeOff, Loader2, PlugZap } from 'lucide-react'
import { api } from '@/api/client'

// One-click IMAP/SMTP settings for the common external providers, so an
// operator connecting a Gmail/Outlook/Yahoo/Zoho/iCloud mailbox doesn't have to
// know server names + ports. `note` calls out app-password requirements.
type Preset = {
  imap_host: string; imap_port: number; imap_ssl: boolean
  smtp_host: string; smtp_port: number; smtp_ssl: boolean
  note?: string
}
const PRESETS: Record<string, Preset> = {
  Gmail: { imap_host: 'imap.gmail.com', imap_port: 993, imap_ssl: true, smtp_host: 'smtp.gmail.com', smtp_port: 465, smtp_ssl: true, note: 'Gmail needs an App Password (2-Step Verification → App passwords), not your normal password.' },
  Outlook: { imap_host: 'outlook.office365.com', imap_port: 993, imap_ssl: true, smtp_host: 'smtp.office365.com', smtp_port: 587, smtp_ssl: false },
  Yahoo: { imap_host: 'imap.mail.yahoo.com', imap_port: 993, imap_ssl: true, smtp_host: 'smtp.mail.yahoo.com', smtp_port: 465, smtp_ssl: true, note: 'Yahoo needs an App Password (Account Security → Generate app password).' },
  Zoho: { imap_host: 'imap.zoho.com', imap_port: 993, imap_ssl: true, smtp_host: 'smtp.zoho.com', smtp_port: 465, smtp_ssl: true },
  iCloud: { imap_host: 'imap.mail.me.com', imap_port: 993, imap_ssl: true, smtp_host: 'smtp.mail.me.com', smtp_port: 587, smtp_ssl: false, note: 'iCloud needs an app-specific password (appleid.apple.com).' },
}

export default function AccountsPage() {
  const { accounts, load, remove } = useAccounts()
  const [provider, setProvider] = useState<'betazen' | 'imap'>('betazen')
  const [showPassword, setShowPassword] = useState(false)
  const [testing, setTesting] = useState(false)
  const [presetNote, setPresetNote] = useState('')
  const [form, setForm] = useState({
    display_name: '', address: '', password: '',
    imap_host: '', imap_port: 993, imap_ssl: true,
    smtp_host: '', smtp_port: 465, smtp_ssl: true,
    username: '', color: '#4f46e5',
  })

  useEffect(() => { void load() }, [load])

  function applyPreset(name: string) {
    const p = PRESETS[name]
    setForm((f) => ({
      ...f,
      imap_host: p.imap_host, imap_port: p.imap_port, imap_ssl: p.imap_ssl,
      smtp_host: p.smtp_host, smtp_port: p.smtp_port, smtp_ssl: p.smtp_ssl,
    }))
    setPresetNote(p.note || '')
  }

  async function setPrimary(id: string) {
    try { await api.post(`/accounts/${id}/primary`); toast.success('Primary set'); load() } catch { toast.error('Failed') }
  }

  // Verify the credentials against the real IMAP/SMTP servers before saving —
  // catches a wrong password / host / port with a precise IMAP:/SMTP: message.
  async function testConn() {
    setTesting(true)
    try {
      await api.post('/accounts/test', { ...form, provider })
      toast.success('Connection OK — credentials verified')
    } catch (err: any) {
      toast.error(err?.response?.data?.error || 'Connection failed')
    } finally {
      setTesting(false)
    }
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    try {
      await api.post('/accounts', { ...form, provider })
      toast.success('Account added')
      setForm({ ...form, address: '', password: '', display_name: '' })
      setPresetNote('')
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

          {provider === 'imap' && (
            <div className="col-span-2">
              <div className="text-xs text-ink-500 mb-1">Quick setup — fill IMAP/SMTP for a known provider</div>
              <div className="flex flex-wrap gap-1.5">
                {Object.keys(PRESETS).map((name) => (
                  <button key={name} type="button" onClick={() => applyPreset(name)}
                    className="text-xs px-2.5 py-1 rounded-full border border-ink-200 hover:bg-ink-100 text-ink-700">
                    {name}
                  </button>
                ))}
              </div>
              {presetNote && (
                <div className="mt-2 text-xs text-amber-700 bg-amber-50 border border-amber-200 rounded-lg px-2.5 py-1.5">{presetNote}</div>
              )}
            </div>
          )}

          <input className="input" placeholder="Display name" value={form.display_name} onChange={(e) => setForm({ ...form, display_name: e.target.value })} required />
          <input className="input" placeholder="Email address" type="email" value={form.address} onChange={(e) => setForm({ ...form, address: e.target.value })} required />
          <div className="col-span-2 relative">
            <input className="input w-full pr-10" placeholder="Password" type={showPassword ? 'text' : 'password'} value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} required />
            <button type="button" onClick={() => setShowPassword((v) => !v)} className="absolute right-2 top-1/2 -translate-y-1/2 text-ink-400 hover:text-ink-700" aria-label="Toggle password visibility">
              {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
            </button>
          </div>

          {provider === 'imap' && (
            <>
              <input className="input" placeholder="IMAP host" value={form.imap_host} onChange={(e) => setForm({ ...form, imap_host: e.target.value })} />
              <input className="input" placeholder="IMAP port" type="number" value={form.imap_port} onChange={(e) => setForm({ ...form, imap_port: +e.target.value })} />
              <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={form.imap_ssl} onChange={(e) => setForm({ ...form, imap_ssl: e.target.checked, imap_port: e.target.checked ? 993 : 143 })} /> IMAP SSL</label>
              <input className="input" placeholder="SMTP host" value={form.smtp_host} onChange={(e) => setForm({ ...form, smtp_host: e.target.value })} />
              <input className="input" placeholder="SMTP port" type="number" value={form.smtp_port} onChange={(e) => setForm({ ...form, smtp_port: +e.target.value })} />
              <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={form.smtp_ssl} onChange={(e) => setForm({ ...form, smtp_ssl: e.target.checked, smtp_port: e.target.checked ? 465 : 587 })} /> SMTP SSL</label>
              <input className="input col-span-2" placeholder="Username (defaults to email)" value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} />
            </>
          )}
          <input className="input" placeholder="Color (hex)" value={form.color} onChange={(e) => setForm({ ...form, color: e.target.value })} />
          <div className="col-span-2 flex flex-wrap items-center gap-2">
            <button type="submit" className="btn-primary">Add mailbox</button>
            <button type="button" onClick={testConn} disabled={testing || !form.address || !form.password}
              className="btn-ghost inline-flex items-center gap-1.5 disabled:opacity-50">
              {testing ? <Loader2 size={15} className="animate-spin" /> : <PlugZap size={15} />} Test connection
            </button>
            <span className="text-xs text-ink-400">Verifies IMAP/SMTP before saving.</span>
          </div>
        </form>
      </section>
    </div>
  )
}
