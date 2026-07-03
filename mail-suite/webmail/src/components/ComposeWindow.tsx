import { FormEvent, useEffect, useRef, useState } from 'react'
import { useEditor, EditorContent } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import Link from '@tiptap/extension-link'
import Image from '@tiptap/extension-image'
import toast from 'react-hot-toast'
import { Minus, X, Trash2, Maximize2 } from 'lucide-react'
import { api } from '@/api/client'
import { useAccounts } from '@/store/accounts'
import { useCompose, ComposeWindow as Win } from '@/store/compose'
import { Signature } from '@/api/types'
import { MenuBar } from '@/components/EditorMenuBar'

function parseAddrs(s: string) {
  return s
    .split(',')
    .map((x) => x.trim())
    .filter(Boolean)
    .map((address) => ({ address }))
}

// hasBody strips tags to decide whether the editor holds real content — so we
// don't autosave/keep an empty "<p></p>" draft.
function hasBody(html: string) {
  return html.replace(/<[^>]*>/g, '').replace(/&nbsp;/g, ' ').trim().length > 0
}

// One docked compose window. Its field state is owned by the compose store so
// minimizing (which keeps the window mounted but collapsed) never loses input,
// and a debounced autosave persists it as a server draft.
export default function ComposeWindow({ win }: { win: Win }) {
  const accounts = useAccounts((s) => s.accounts)
  const current = useAccounts((s) => s.current())
  const patch = useCompose((s) => s.patch)
  const close = useCompose((s) => s.close)
  const minimize = useCompose((s) => s.minimize)

  const acc = accounts.find((a) => a.id === win.accountId) || current
  const accountId = acc?.id || ''

  const [signatures, setSignatures] = useState<Signature[]>([])
  const [sending, setSending] = useState(false)
  const savingRef = useRef(false)

  const editor = useEditor({
    extensions: [StarterKit, Link, Image],
    content: win.html || '<p></p>',
    onUpdate: ({ editor }) => patch(win.id, { html: editor.getHTML(), dirty: true }),
  })

  useEffect(() => {
    api.get<{ data: Signature[] }>('/signatures').then((r) => {
      const list = r.data.data || []
      setSignatures(list)
      if (!win.signatureId) {
        const def = list.find((s) => s.is_default)
        if (def) patch(win.id, { signatureId: def.id })
      }
    })
    // once on mount
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  async function saveDraft() {
    if (savingRef.current || !accountId) return
    if (!(win.to.trim() || win.subject.trim() || hasBody(win.html))) return
    savingRef.current = true
    const body = {
      account_id: accountId,
      to: win.to,
      cc: win.cc,
      bcc: win.bcc,
      subject: win.subject,
      html: win.html,
      signature_id: win.signatureId,
      in_reply_to: win.inReplyTo,
      references: win.references,
    }
    try {
      if (win.draftId) {
        await api.put(`/drafts/${win.draftId}`, body)
      } else {
        const r = await api.post<{ data: { id: string } }>('/drafts', body)
        patch(win.id, { draftId: r.data?.data?.id })
      }
      patch(win.id, { dirty: false, savedAt: Date.now() })
    } catch {
      /* keep dirty; retried on next change */
    } finally {
      savingRef.current = false
    }
  }

  // Debounced autosave whenever a dirty window has real content.
  useEffect(() => {
    if (!win.dirty) return
    if (!(win.to.trim() || win.subject.trim() || hasBody(win.html))) return
    const t = setTimeout(() => void saveDraft(), 1500)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [win.to, win.cc, win.bcc, win.subject, win.html, win.dirty])

  async function send(e: FormEvent) {
    e.preventDefault()
    if (!editor || !accountId) return
    const to = parseAddrs(win.to)
    if (!to.length) {
      toast.error('Add at least one recipient')
      return
    }
    setSending(true)
    try {
      await api.post(`/mail/${accountId}/send`, {
        to,
        cc: win.cc ? parseAddrs(win.cc) : undefined,
        bcc: win.bcc ? parseAddrs(win.bcc) : undefined,
        subject: win.subject,
        html: editor.getHTML(),
        signature_id: win.signatureId || undefined,
        in_reply_to: win.inReplyTo || undefined,
        references: win.references,
      })
      toast.success('Sent')
      if (win.draftId) {
        try {
          await api.delete(`/drafts/${win.draftId}`)
        } catch {
          /* draft cleanup best-effort */
        }
      }
      close(win.id)
    } catch (err: any) {
      toast.error(err?.response?.data?.error || 'Send failed')
    } finally {
      setSending(false)
    }
  }

  // Closing keeps the draft (Gmail behaviour); discarding deletes it.
  async function handleClose() {
    if (win.dirty && (win.to.trim() || win.subject.trim() || hasBody(win.html))) {
      await saveDraft()
    }
    close(win.id)
  }
  async function discard() {
    if (win.draftId) {
      try {
        await api.delete(`/drafts/${win.draftId}`)
      } catch {
        /* best-effort */
      }
    }
    close(win.id)
  }

  const title = win.subject.trim() || 'New message'

  if (win.minimized) {
    return (
      <div className="pointer-events-auto w-[240px] bg-ink-800 text-white rounded-t-lg shadow-2xl flex items-center justify-between px-3 py-2">
        <button className="text-sm font-medium truncate text-left flex-1" onClick={() => minimize(win.id, false)} title={title}>
          {title}
        </button>
        <div className="flex items-center gap-1 shrink-0">
          <button onClick={() => minimize(win.id, false)} className="p-1 rounded hover:bg-white/10" aria-label="Restore"><Maximize2 size={14} /></button>
          <button onClick={handleClose} className="p-1 rounded hover:bg-white/10" aria-label="Close"><X size={14} /></button>
        </div>
      </div>
    )
  }

  return (
    <div className="pointer-events-auto w-[calc(100vw-1.5rem)] sm:w-[500px] max-h-[80vh] bg-white rounded-t-xl shadow-2xl border border-ink-200 flex flex-col overflow-hidden">
      <div className="flex items-center justify-between px-4 py-2 bg-ink-800 text-white shrink-0">
        <span className="text-sm font-medium truncate">{title}</span>
        <div className="flex items-center gap-1 shrink-0">
          <button onClick={() => minimize(win.id, true)} className="p-1 rounded hover:bg-white/10" aria-label="Minimize"><Minus size={16} /></button>
          <button onClick={handleClose} className="p-1 rounded hover:bg-white/10" aria-label="Close"><X size={16} /></button>
        </div>
      </div>

      <form onSubmit={send} className="flex flex-col min-h-0 flex-1">
        <div className="px-4 pt-3 flex flex-col gap-2 shrink-0">
          <div className="flex items-center gap-2 text-sm border-b border-ink-100 pb-1.5">
            <span className="text-ink-500">From</span>
            {accounts.length > 1 ? (
              <select
                className="flex-1 bg-transparent focus:outline-none text-ink-800"
                value={accountId}
                onChange={(e) => patch(win.id, { accountId: e.target.value })}
              >
                {accounts.map((a) => (
                  <option key={a.id} value={a.id}>{a.address}</option>
                ))}
              </select>
            ) : (
              <span className="text-ink-800">{acc?.address}</span>
            )}
          </div>

          <div className="flex items-center gap-2 text-sm border-b border-ink-100 pb-1.5">
            <span className="text-ink-500">To</span>
            <input
              className="flex-1 bg-transparent focus:outline-none"
              placeholder="Recipients (comma separated)"
              value={win.to}
              onChange={(e) => patch(win.id, { to: e.target.value, dirty: true })}
            />
            <div className="flex items-center gap-2 text-xs text-ink-500">
              {!win.showCc && <button type="button" onClick={() => patch(win.id, { showCc: true })}>Cc</button>}
              {!win.showBcc && <button type="button" onClick={() => patch(win.id, { showBcc: true })}>Bcc</button>}
            </div>
          </div>

          {win.showCc && (
            <div className="flex items-center gap-2 text-sm border-b border-ink-100 pb-1.5">
              <span className="text-ink-500">Cc</span>
              <input className="flex-1 bg-transparent focus:outline-none" value={win.cc} onChange={(e) => patch(win.id, { cc: e.target.value, dirty: true })} />
            </div>
          )}
          {win.showBcc && (
            <div className="flex items-center gap-2 text-sm border-b border-ink-100 pb-1.5">
              <span className="text-ink-500">Bcc</span>
              <input className="flex-1 bg-transparent focus:outline-none" value={win.bcc} onChange={(e) => patch(win.id, { bcc: e.target.value, dirty: true })} />
            </div>
          )}

          <input
            className="text-sm font-medium border-b border-ink-100 pb-1.5 focus:outline-none"
            placeholder="Subject"
            value={win.subject}
            onChange={(e) => patch(win.id, { subject: e.target.value, dirty: true })}
          />
        </div>

        <div className="flex-1 min-h-0 overflow-auto px-1">
          <MenuBar editor={editor} />
          <EditorContent editor={editor} className="min-h-[180px] p-3 prose max-w-none focus:outline-none" />
        </div>

        <div className="flex items-center gap-2 px-4 py-2.5 border-t border-ink-100 shrink-0">
          <button type="submit" className="btn-primary" disabled={sending}>{sending ? 'Sending…' : 'Send'}</button>
          <select className="input max-w-[150px] text-sm" value={win.signatureId} onChange={(e) => patch(win.id, { signatureId: e.target.value, dirty: true })}>
            <option value="">No signature</option>
            {signatures.map((s) => (
              <option key={s.id} value={s.id}>{s.name}{s.is_default ? ' (default)' : ''}</option>
            ))}
          </select>
          <div className="flex-1" />
          {win.savedAt && !win.dirty && <span className="text-xs text-ink-400">Saved</span>}
          <button type="button" onClick={discard} className="btn-ghost text-red-600" title="Discard draft"><Trash2 size={16} /></button>
        </div>
      </form>
    </div>
  )
}
