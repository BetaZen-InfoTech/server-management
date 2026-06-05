import { FormEvent, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useEditor, EditorContent } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import Link from '@tiptap/extension-link'
import Image from '@tiptap/extension-image'
import toast from 'react-hot-toast'
import { api } from '@/api/client'
import { useAccounts } from '@/store/accounts'
import { Signature } from '@/api/types'

export default function Compose() {
  const acc = useAccounts((s) => s.current())
  const [to, setTo] = useState('')
  const [cc, setCc] = useState('')
  const [subject, setSubject] = useState('')
  const [signatures, setSignatures] = useState<Signature[]>([])
  const [sigId, setSigId] = useState('')
  const [sending, setSending] = useState(false)
  const nav = useNavigate()

  const editor = useEditor({
    extensions: [StarterKit, Link, Image],
    content: '<p></p>',
  })

  useEffect(() => {
    api.get<{ data: Signature[] }>('/signatures').then((r) => {
      setSignatures(r.data.data || [])
      const def = (r.data.data || []).find((s) => s.is_default)
      if (def) setSigId(def.id)
    })
  }, [])

  if (!acc) return <div className="p-8 text-ink-500">No account selected.</div>

  function parseAddrs(s: string) {
    return s
      .split(',')
      .map((x) => x.trim())
      .filter(Boolean)
      .map((address) => ({ address }))
  }

  async function send(e: FormEvent) {
    e.preventDefault()
    if (!editor) return
    setSending(true)
    try {
      await api.post(`/mail/${acc!.id}/send`, {
        to: parseAddrs(to),
        cc: cc ? parseAddrs(cc) : undefined,
        subject,
        html: editor.getHTML(),
        signature_id: sigId || undefined,
      })
      toast.success('Sent')
      nav('/inbox')
    } catch (err: any) {
      toast.error(err?.response?.data?.error || 'Send failed')
    } finally {
      setSending(false)
    }
  }

  return (
    <div className="p-3 max-w-3xl mx-auto">
      <form onSubmit={send} className="card p-4 flex flex-col gap-3">
        <div className="text-sm text-ink-500">From: <span className="text-ink-800">{acc.address}</span></div>
        <input className="input" placeholder="To (comma separated)" value={to} onChange={(e) => setTo(e.target.value)} required />
        <input className="input" placeholder="Cc" value={cc} onChange={(e) => setCc(e.target.value)} />
        <input className="input" placeholder="Subject" value={subject} onChange={(e) => setSubject(e.target.value)} />
        <div className="border border-ink-200 rounded-lg overflow-hidden">
          <EditorContent editor={editor} className="min-h-[260px] p-3 prose max-w-none" />
        </div>
        <div className="flex items-center gap-2">
          <select className="input max-w-xs" value={sigId} onChange={(e) => setSigId(e.target.value)}>
            <option value="">No signature</option>
            {signatures.map((s) => (
              <option key={s.id} value={s.id}>{s.name}{s.is_default ? ' (default)' : ''}</option>
            ))}
          </select>
          <div className="flex-1" />
          <button type="button" className="btn-ghost" onClick={() => nav(-1)}>Cancel</button>
          <button type="submit" className="btn-primary" disabled={sending}>{sending ? 'Sending…' : 'Send'}</button>
        </div>
      </form>
    </div>
  )
}
