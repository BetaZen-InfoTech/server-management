import { FormEvent, useEffect, useState } from 'react'
import toast from 'react-hot-toast'
import { api } from '@/api/client'
import { Signature } from '@/api/types'
import { Trash2 } from 'lucide-react'
import RichEditor from '@/components/RichEditor'

const DEFAULT_SIG = '<p>Best regards,<br/>Your name</p>'

export default function SignaturesPage() {
  const [items, setItems] = useState<Signature[]>([])
  const [name, setName] = useState('')
  const [html, setHtml] = useState(DEFAULT_SIG)
  const [isDefault, setIsDefault] = useState(false)

  async function load() {
    const r = await api.get<{ data: Signature[] }>('/signatures')
    setItems(r.data.data || [])
  }

  useEffect(() => { void load() }, [])

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    try {
      await api.post('/signatures', { name, html, is_default: isDefault })
      toast.success('Saved')
      setName(''); setHtml(DEFAULT_SIG); setIsDefault(false)
      void load()
    } catch (err: any) {
      toast.error(err?.response?.data?.error || 'Failed')
    }
  }

  async function remove(id: string) {
    try { await api.delete(`/signatures/${id}`); toast.success('Deleted'); void load() } catch { toast.error('Failed') }
  }

  return (
    <div className="space-y-6">
      <section>
        <h3 className="font-medium mb-3">Your signatures</h3>
        <ul className="divide-y divide-ink-100 border border-ink-100 rounded-lg">
          {items.map((s) => (
            <li key={s.id} className="p-3 flex items-start gap-3">
              <div className="flex-1">
                <div className="font-medium">{s.name} {s.is_default && <span className="text-xs text-brand-700 bg-brand-50 px-2 py-0.5 rounded-full ml-1">Default</span>}</div>
                <div className="prose mt-2" dangerouslySetInnerHTML={{ __html: s.html }} />
              </div>
              <button onClick={() => remove(s.id)} className="btn-ghost text-red-600"><Trash2 size={14} /></button>
            </li>
          ))}
          {items.length === 0 && <li className="p-4 text-sm text-ink-500">No signatures yet.</li>}
        </ul>
      </section>

      <section>
        <h3 className="font-medium mb-3">Add signature</h3>
        <form onSubmit={onSubmit} className="flex flex-col gap-3">
          <input className="input" placeholder="Name" value={name} onChange={(e) => setName(e.target.value)} required />
          <RichEditor value={html} onChange={setHtml} minHeight={160} />
          <label className="text-sm flex items-center gap-2"><input type="checkbox" checked={isDefault} onChange={(e) => setIsDefault(e.target.checked)} /> Use as default</label>
          <button type="submit" className="btn-primary self-start">Save signature</button>
        </form>
      </section>
    </div>
  )
}
