import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import DOMPurify from 'dompurify'
import { api } from '@/api/client'
import { MessageBody, SentMessage, TrackingEvent } from '@/api/types'
import { useAccounts } from '@/store/accounts'
import { useCompose } from '@/store/compose'
import { ArrowLeft, Reply, Forward, Trash2, MailOpen, MousePointerClick, Send as SendIcon, Truck, Activity as ActivityIcon } from 'lucide-react'
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

  const [activity, setActivity] = useState<{ message: SentMessage; events: TrackingEvent[] } | null>(null)

  useEffect(() => {
    if (!acc || !msg?.message_id) {
      setActivity(null)
      return
    }
    api
      .get<{ data: { message: SentMessage; events: TrackingEvent[] } }>('/tracking/message', {
        params: { account_id: acc.id, message_id: msg.message_id },
      })
      .then((r) => setActivity(r.data.data || null))
      .catch(() => setActivity(null))
  }, [acc?.id, msg?.message_id])

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

      {activity && <ActivityPanel a={activity} />}
    </div>
  )
}

function ActivityPanel({ a }: { a: { message: SentMessage; events: TrackingEvent[] } }) {
  const m = a.message
  const evLabel: Record<string, { label: string; icon: JSX.Element; cls: string }> = {
    open: { label: 'Opened', icon: <MailOpen size={13} />, cls: 'text-green-600' },
    click: { label: 'Clicked', icon: <MousePointerClick size={13} />, cls: 'text-blue-600' },
    delivered: { label: 'Delivered', icon: <Truck size={13} />, cls: 'text-green-600' },
    bounced: { label: 'Bounced', icon: <Truck size={13} />, cls: 'text-red-600' },
  }
  return (
    <div className="card p-5 mt-3">
      <div className="flex items-center gap-2 mb-3">
        <ActivityIcon size={16} className="text-brand-600" />
        <h2 className="font-semibold">Activity</h2>
      </div>

      <div className="flex flex-wrap gap-4 text-sm mb-4">
        <div className="flex items-center gap-1.5 text-ink-600"><SendIcon size={14} /> Sent {new Date(m.sent_at).toLocaleString()}</div>
        <div className="flex items-center gap-1.5"><span className="px-2 py-0.5 rounded-full bg-ink-100 text-ink-600 capitalize text-xs">{m.status}</span></div>
        {m.track_open && (
          <div className={`flex items-center gap-1.5 ${m.open_count > 0 ? 'text-green-600 font-medium' : 'text-ink-400'}`}>
            <MailOpen size={14} /> {m.open_count} open{m.open_count === 1 ? '' : 's'}
          </div>
        )}
        {m.track_click && (
          <div className={`flex items-center gap-1.5 ${m.click_count > 0 ? 'text-blue-600 font-medium' : 'text-ink-400'}`}>
            <MousePointerClick size={14} /> {m.click_count} click{m.click_count === 1 ? '' : 's'}
          </div>
        )}
      </div>

      {!m.track_open && !m.track_click && (
        <p className="text-sm text-ink-400">Open/click tracking was off for this message.</p>
      )}

      {a.events.length > 0 ? (
        <ul className="divide-y divide-ink-100 border border-ink-100 rounded-lg text-sm">
          {a.events.map((ev) => {
            const meta = evLabel[ev.type] || { label: ev.type, icon: <ActivityIcon size={13} />, cls: 'text-ink-600' }
            return (
              <li key={ev.id} className="p-2.5 flex items-center gap-2">
                <span className={`flex items-center gap-1 font-medium w-24 shrink-0 ${meta.cls}`}>{meta.icon} {meta.label}</span>
                {ev.url && <a href={ev.url} target="_blank" rel="noreferrer" className="text-brand-600 truncate flex-1 hover:underline">{ev.url}</a>}
                <span className="text-ink-400 ml-auto shrink-0">{new Date(ev.at).toLocaleString()}</span>
              </li>
            )
          })}
        </ul>
      ) : (
        (m.track_open || m.track_click) && <p className="text-sm text-ink-400">No opens or clicks recorded yet.</p>
      )}
    </div>
  )
}
