import { useEffect, useState } from 'react'
import toast from 'react-hot-toast'
import { Trash2, FileText } from 'lucide-react'
import { api } from '@/api/client'
import { Draft } from '@/api/types'
import { useCompose } from '@/store/compose'

// Drafts lists the server-side auto-saved drafts. Clicking one reopens it in a
// docked compose window (carrying its draftId so further edits update the same
// draft and sending deletes it).
export default function Drafts() {
  const [items, setItems] = useState<Draft[]>([])
  const [loading, setLoading] = useState(true)
  const open = useCompose((s) => s.open)

  async function load() {
    setLoading(true)
    try {
      const r = await api.get<{ data: Draft[] }>('/drafts')
      setItems(r.data.data || [])
    } catch {
      setItems([])
    } finally {
      setLoading(false)
    }
  }
  useEffect(() => {
    void load()
  }, [])

  function openDraft(d: Draft) {
    open({
      accountId: d.account_id,
      to: d.to,
      cc: d.cc,
      bcc: d.bcc,
      subject: d.subject,
      html: d.html,
      signatureId: d.signature_id,
      inReplyTo: d.in_reply_to,
      references: d.references,
      draftId: d.id,
    })
  }

  async function del(e: React.MouseEvent, id: string) {
    e.stopPropagation()
    try {
      await api.delete(`/drafts/${id}`)
      toast.success('Draft deleted')
      setItems((xs) => xs.filter((x) => x.id !== id))
    } catch {
      toast.error('Could not delete')
    }
  }

  function preview(d: Draft) {
    return d.html.replace(/<[^>]*>/g, ' ').replace(/&nbsp;/g, ' ').replace(/\s+/g, ' ').trim()
  }

  if (loading) return <div className="p-8 text-ink-500">Loading…</div>

  return (
    <div className="p-3 max-w-4xl mx-auto">
      <h1 className="text-xl font-semibold mb-3">Drafts</h1>
      <ul className="card divide-y divide-ink-100 overflow-hidden">
        {items.map((d) => (
          <li
            key={d.id}
            onClick={() => openDraft(d)}
            className="p-3 flex items-center gap-3 cursor-pointer hover:bg-ink-50"
          >
            <FileText size={16} className="text-ink-400 shrink-0" />
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2">
                <span className="font-medium truncate">{d.subject || '(no subject)'}</span>
                <span className="text-xs text-ink-400 shrink-0">to {d.to || '—'}</span>
              </div>
              <div className="text-sm text-ink-500 truncate">{preview(d) || 'Empty draft'}</div>
            </div>
            <span className="text-xs text-ink-400 shrink-0">{new Date(d.updated_at).toLocaleString()}</span>
            <button onClick={(e) => del(e, d.id)} className="btn-ghost text-red-600 shrink-0" title="Delete draft">
              <Trash2 size={14} />
            </button>
          </li>
        ))}
        {items.length === 0 && <li className="p-8 text-center text-sm text-ink-500">No drafts.</li>}
      </ul>
    </div>
  )
}
