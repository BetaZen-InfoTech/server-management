import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import DOMPurify from 'dompurify'
import { api } from '@/api/client'
import { MessageBody } from '@/api/types'
import { useAccounts } from '@/store/accounts'
import { useCompose } from '@/store/compose'
import { ArrowLeft, Reply, Forward, Trash2 } from 'lucide-react'
import toast from 'react-hot-toast'

function escapeHtml(s: string) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

// quote wraps the original message as a reply/forward blockquote.
function quote(m: MessageBody) {
  const when = new Date(m.date).toLocaleString()
  const who = m.from?.[0]?.name || m.from?.[0]?.address || ''
  const orig = m.html || (m.text ? `<pre>${escapeHtml(m.text)}</pre>` : '')
  return `<p><br/></p><p>On ${escapeHtml(when)}, ${escapeHtml(who)} wrote:</p><blockquote style="margin:0 0 0 .8ex;border-left:2px solid #ccc;padding-left:1ex">${orig}</blockquote>`
}

export default function Thread() {
  const { uid } = useParams<{ uid: string }>()
  const [params] = useSearchParams()
  const folder = params.get('folder') || 'INBOX'
  const acc = useAccounts((s) => s.current())
  const openCompose = useCompose((s) => s.open)
  const [msg, setMsg] = useState<MessageBody | null>(null)
  const [loading, setLoading] = useState(true)
  const nav = useNavigate()

  useEffect(() => {
    if (!acc || !uid) return
    setLoading(true)
    api
      .get(`/mail/${acc.id}/messages/${uid}`, { params: { folder } })
      .then((r) => setMsg(r.data?.data || null))
      .catch(() => setMsg(null))
      .finally(() => setLoading(false))
  }, [acc?.id, uid, folder])

  const safeHTML = useMemo(() => {
    if (!msg?.html) return ''
    return DOMPurify.sanitize(msg.html, { USE_PROFILES: { html: true } })
  }, [msg?.html])

  async function trash() {
    if (!acc || !uid) return
    try {
      await api.patch(`/mail/${acc.id}/messages/${uid}`, { folder: 'Trash' }, { params: { folder } })
      toast.success('Moved to Trash')
      nav(-1)
    } catch {
      toast.error('Could not move')
    }
  }

  function reply() {
    if (!acc || !msg) return
    const subject = /^re:/i.test(msg.subject) ? msg.subject : `Re: ${msg.subject}`
    openCompose({
      accountId: acc.id,
      to: msg.reply_to?.[0]?.address || msg.from?.[0]?.address || '',
      subject,
      html: quote(msg),
      inReplyTo: msg.message_id,
      references: msg.message_id ? [msg.message_id] : undefined,
    })
  }
  function forward() {
    if (!acc || !msg) return
    const subject = /^fwd:/i.test(msg.subject) ? msg.subject : `Fwd: ${msg.subject}`
    openCompose({ accountId: acc.id, subject, html: quote(msg) })
  }

  if (!acc) return <div className="p-8 text-ink-500">No account selected.</div>
  if (loading) return <div className="p-8 text-ink-500">Loading…</div>
  if (!msg) return <div className="p-8 text-ink-500">Message not found.</div>

  return (
    <div className="p-3 max-w-4xl mx-auto">
      <div className="flex items-center gap-2 mb-3">
        <button className="btn-ghost" onClick={() => nav(-1)} aria-label="Back"><ArrowLeft size={16} /></button>
        <button className="btn-ghost" onClick={reply}><Reply size={14} /> Reply</button>
        <button className="btn-ghost" onClick={forward}><Forward size={14} /> Forward</button>
        <button className="btn-ghost text-red-600" onClick={trash}><Trash2 size={14} /> Trash</button>
      </div>
      <div className="card p-6">
        <h1 className="text-xl font-semibold mb-2">{msg.subject || '(no subject)'}</h1>
        <div className="flex items-center justify-between text-sm text-ink-500 mb-4">
          <div>
            <div>
              <span className="font-medium text-ink-800">{msg.from?.[0]?.name || msg.from?.[0]?.address}</span>
              {msg.from?.[0]?.name && <span className="ml-1">&lt;{msg.from?.[0]?.address}&gt;</span>}
            </div>
            <div>To: {msg.to?.map((a) => a.address).join(', ')}</div>
          </div>
          <div>{new Date(msg.date).toLocaleString()}</div>
        </div>
        {safeHTML ? (
          <div className="prose max-w-none" dangerouslySetInnerHTML={{ __html: safeHTML }} />
        ) : (
          <pre className="whitespace-pre-wrap font-sans text-sm text-ink-800">{msg.text}</pre>
        )}
        {!!msg.attachments?.length && (
          <div className="mt-6">
            <div className="text-sm font-medium mb-2">Attachments</div>
            <ul className="text-sm space-y-1">
              {msg.attachments.map((a) => (
                <li key={a.id}>{a.filename} <span className="text-ink-500">({a.content_type}, {a.size} bytes)</span></li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </div>
  )
}
