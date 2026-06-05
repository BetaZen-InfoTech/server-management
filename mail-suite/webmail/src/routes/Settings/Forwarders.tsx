import { FormEvent, useEffect, useState } from 'react'
import toast from 'react-hot-toast'
import { api } from '@/api/client'
import { Forwarder } from '@/api/types'
import { Trash2 } from 'lucide-react'
import { useAccounts } from '@/store/accounts'

export default function ForwardersPage() {
  const { accounts } = useAccounts()
  const [items, setItems] = useState<Forwarder[]>([])
  const [source, setSource] = useState('')
  const [destinations, setDestinations] = useState('')
  const [keepCopy, setKeepCopy] = useState(true)
  const [accountId, setAccountId] = useState('')

  async function load() {
    const r = await api.get<{ data: Forwarder[] }>('/forwarders')
    setItems(r.data.data || [])
  }
  useEffect(() => { void load() }, [])
  useEffect(() => { if (!accountId && accounts[0]) setAccountId(accounts[0].id) }, [accounts])

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    try {
      await api.post('/forwarders', {
        account_id: accountId,
        source,
        destinations: destinations.split(',').map((s) => s.trim()).filter(Boolean),
        keep_copy: keepCopy,
      })
      toast.success('Forwarder added')
      setSource(''); setDestinations('')
      void load()
    } catch (err: any) {
      toast.error(err?.response?.data?.error || 'Failed')
    }
  }
  async function remove(id: string) {
    try { await api.delete(`/forwarders/${id}`); toast.success('Removed'); void load() } catch { toast.error('Failed') }
  }

  return (
    <div className="space-y-6">
      <section>
        <h3 className="font-medium mb-3">Active forwarders</h3>
        <ul className="divide-y divide-ink-100 border border-ink-100 rounded-lg">
          {items.map((f) => (
            <li key={f.id} className="p-3 flex items-center gap-3">
              <div className="flex-1 min-w-0">
                <div className="font-medium truncate">{f.source}</div>
                <div className="text-xs text-ink-500">→ {f.destinations.join(', ')} {f.keep_copy && '(keep copy)'}</div>
              </div>
              <button onClick={() => remove(f.id)} className="btn-ghost text-red-600"><Trash2 size={14} /></button>
            </li>
          ))}
          {items.length === 0 && <li className="p-4 text-sm text-ink-500">No forwarders.</li>}
        </ul>
      </section>

      <section>
        <h3 className="font-medium mb-3">Add forwarder</h3>
        <form onSubmit={onSubmit} className="grid grid-cols-2 gap-3">
          <select className="input" value={accountId} onChange={(e) => setAccountId(e.target.value)} required>
            {accounts.map((a) => <option key={a.id} value={a.id}>{a.address}</option>)}
          </select>
          <input className="input" placeholder="Source email" type="email" value={source} onChange={(e) => setSource(e.target.value)} required />
          <input className="input col-span-2" placeholder="Destinations (comma separated)" value={destinations} onChange={(e) => setDestinations(e.target.value)} required />
          <label className="text-sm flex items-center gap-2 col-span-2"><input type="checkbox" checked={keepCopy} onChange={(e) => setKeepCopy(e.target.checked)} /> Keep a copy in inbox</label>
          <div className="col-span-2"><button type="submit" className="btn-primary">Add forwarder</button></div>
        </form>
      </section>
    </div>
  )
}
