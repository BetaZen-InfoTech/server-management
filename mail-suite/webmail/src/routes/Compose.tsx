import { FormEvent, ReactNode, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useEditor, EditorContent, Editor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import Link from '@tiptap/extension-link'
import Image from '@tiptap/extension-image'
import {
  Bold, Italic, Strikethrough, Code, Heading1, Heading2, List, ListOrdered,
  Quote, Link2, Link2Off, Minus, Undo2, Redo2, Image as ImageIcon,
} from 'lucide-react'
import toast from 'react-hot-toast'
import { api } from '@/api/client'
import { useAccounts } from '@/store/accounts'
import { Signature } from '@/api/types'

// MenuBar — rich-text formatting toolbar over the tiptap editor. Every button
// maps to an HTML-producing command (editor.getHTML() is what we send), so the
// composer covers real formatting: bold / italic / strike / inline code,
// H1-H2, bullet + numbered lists, blockquote, horizontal rule, links, and
// inline images by URL — no extra dependency (all from StarterKit + the Link /
// Image extensions already loaded).
function MenuBar({ editor }: { editor: Editor | null }) {
  if (!editor) return null

  const Btn = ({ onClick, active, disabled, title, children }: {
    onClick: () => void; active?: boolean; disabled?: boolean; title: string; children: ReactNode
  }) => (
    <button
      type="button"
      title={title}
      disabled={disabled}
      onClick={onClick}
      className={`p-1.5 rounded transition-colors disabled:opacity-40 hover:bg-ink-100 ${
        active ? 'bg-brand-100 text-brand-700' : 'text-ink-600'
      }`}
    >
      {children}
    </button>
  )
  const Sep = () => <span className="w-px self-stretch bg-ink-200 mx-1" />

  const setLink = () => {
    const prev = editor.getAttributes('link').href as string | undefined
    const url = window.prompt('Link URL', prev || 'https://')
    if (url === null) return
    if (url === '') {
      editor.chain().focus().extendMarkRange('link').unsetLink().run()
      return
    }
    editor.chain().focus().extendMarkRange('link').setLink({ href: url }).run()
  }
  const addImage = () => {
    const url = window.prompt('Image URL', 'https://')
    if (url) editor.chain().focus().setImage({ src: url }).run()
  }

  return (
    <div className="flex flex-wrap items-center gap-0.5 border-b border-ink-200 bg-ink-50 px-2 py-1">
      <Btn title="Bold (Ctrl+B)" active={editor.isActive('bold')} onClick={() => editor.chain().focus().toggleBold().run()}><Bold size={16} /></Btn>
      <Btn title="Italic (Ctrl+I)" active={editor.isActive('italic')} onClick={() => editor.chain().focus().toggleItalic().run()}><Italic size={16} /></Btn>
      <Btn title="Strikethrough" active={editor.isActive('strike')} onClick={() => editor.chain().focus().toggleStrike().run()}><Strikethrough size={16} /></Btn>
      <Btn title="Inline code" active={editor.isActive('code')} onClick={() => editor.chain().focus().toggleCode().run()}><Code size={16} /></Btn>
      <Sep />
      <Btn title="Heading 1" active={editor.isActive('heading', { level: 1 })} onClick={() => editor.chain().focus().toggleHeading({ level: 1 }).run()}><Heading1 size={16} /></Btn>
      <Btn title="Heading 2" active={editor.isActive('heading', { level: 2 })} onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()}><Heading2 size={16} /></Btn>
      <Btn title="Bullet list" active={editor.isActive('bulletList')} onClick={() => editor.chain().focus().toggleBulletList().run()}><List size={16} /></Btn>
      <Btn title="Numbered list" active={editor.isActive('orderedList')} onClick={() => editor.chain().focus().toggleOrderedList().run()}><ListOrdered size={16} /></Btn>
      <Btn title="Quote" active={editor.isActive('blockquote')} onClick={() => editor.chain().focus().toggleBlockquote().run()}><Quote size={16} /></Btn>
      <Btn title="Divider" onClick={() => editor.chain().focus().setHorizontalRule().run()}><Minus size={16} /></Btn>
      <Sep />
      <Btn title="Link" active={editor.isActive('link')} onClick={setLink}><Link2 size={16} /></Btn>
      <Btn title="Remove link" disabled={!editor.isActive('link')} onClick={() => editor.chain().focus().unsetLink().run()}><Link2Off size={16} /></Btn>
      <Btn title="Image by URL" onClick={addImage}><ImageIcon size={16} /></Btn>
      <Sep />
      <Btn title="Undo" disabled={!editor.can().undo()} onClick={() => editor.chain().focus().undo().run()}><Undo2 size={16} /></Btn>
      <Btn title="Redo" disabled={!editor.can().redo()} onClick={() => editor.chain().focus().redo().run()}><Redo2 size={16} /></Btn>
    </div>
  )
}

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
          <MenuBar editor={editor} />
          <EditorContent editor={editor} className="min-h-[260px] p-3 prose max-w-none focus:outline-none" />
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
