import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api } from '@/api/client'
import { MessageHeader, SentMessage, Folder } from '@/api/types'
import { useAccounts } from '@/store/accounts'
import { Star, Paperclip, MailOpen, MousePointerClick } from 'lucide-react'
import clsx from 'clsx'

// Cache each account's folder list so we can resolve a logical sidebar folder
// (Sent/Spam/Trash/…) to that account's REAL mailbox name — e.g. Gmail's Sent is
// "[Gmail]/Sent Mail", not "Sent", so selecting the literal "Sent" returned
// nothing. Resolved via the RFC 6154 special-use classification from /folders.
const folderCache: Record<string, Folder[]> = {}
async function loadFolders(accId: string): Promise<Folder[]> {
  if (folderCache[accId]) return folderCache[accId]
  try {
    const r = await api.get<{ data: Folder[] }>(`/mail/${accId}/folders`)
    folderCache[accId] = r.data.data || []
  } catch {
    folderCache[accId] = []
  }
  return folderCache[accId]
}

// logical sidebar folder name -> RFC 6154 special key
const SPECIAL: Record<string, string> = {
  sent: 'sent', spam: 'spam', trash: 'trash', drafts: 'drafts', starred: 'starred',
}

// Message-IDs may or may not carry the surrounding <...>; normalise so the Sent
// IMAP header and the stored SentMessage record match regardless.
function normId(s: string) {
  return s.replace(/[<>]/g, '').trim()
}

function formatDate(s: string) {
  if (!s) return ''
  const d = new Date(s)
  const now = new Date()
  if (d.toDateString() === now.toDateString()) {
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }
  return d.toLocaleDateString([], { month: 'short', day: 'numeric' })
}

export default function Inbox() {
  const { folder } = useParams<{ folder?: string }>()
  const folderName = folder || 'INBOX'
  const acc = useAccounts((s) => s.current())
  const [items, setItems] = useState<MessageHeader[]>([])
  const [loading, setLoading] = useState(false)
  const [realFolder, setRealFolder] = useState(folderName)
  const nav = useNavigate()

  // In the Sent folder we overlay per-message open/click tracking, keyed by
  // Message-ID (the SentMessage record shares the id we stamped on the mail).
  const isSent = /sent/i.test(folderName)
  const [tracking, setTracking] = useState<Record<string, SentMessage>>({})

  // Resolve the logical folder (from the URL) to the account's real mailbox name.
  useEffect(() => {
    if (!acc) return
    const special = SPECIAL[folderName.toLowerCase()]
    if (!special) { setRealFolder(folderName); return }
    let cancelled = false
    void loadFolders(acc.id).then((fs) => {
      if (cancelled) return
      const match = fs.find((f) => f.special === special)
      setRealFolder(match ? match.name : folderName)
    })
    return () => { cancelled = true }
  }, [acc?.id, folderName])

  useEffect(() => {
    if (!acc) return
    setLoading(true)
    api
      .get(`/mail/${acc.id}/threads`, { params: { folder: realFolder, limit: 50 } })
      .then((r) => setItems(r.data?.data?.items || []))
      .catch(() => setItems([]))
      .finally(() => setLoading(false))
  }, [acc?.id, realFolder])

  useEffect(() => {
    if (!acc || !isSent) {
      setTracking({})
      return
    }
    api
      .get<{ data: SentMessage[] }>('/tracking/sent', { params: { account_id: acc.id, limit: 200 } })
      .then((r) => {
        const map: Record<string, SentMessage> = {}
        for (const s of r.data.data || []) {
          if (s.message_id) map[normId(s.message_id)] = s
        }
        setTracking(map)
      })
      .catch(() => setTracking({}))
  }, [acc?.id, folderName, isSent])

  if (!acc) {
    return (
      <div className="p-8">
        <div className="card p-6 max-w-lg">
          <h2 className="font-semibold text-ink-800 mb-1">No mail account connected</h2>
          <p className="text-sm text-ink-500 mb-3">
            Add a mailbox to start reading and sending mail.
          </p>
          <a className="btn-primary" href="/mail/settings/accounts">Add account</a>
        </div>
      </div>
    )
  }

  return (
    <div className="p-3">
      <div className="card overflow-hidden">
        <div className="px-4 py-2 border-b border-ink-100 text-sm text-ink-500">
          {folderName} {loading && <span className="ml-2">loading…</span>}
        </div>
        <ul className="divide-y divide-ink-100">
          {items.map((m) => {
            const rec = isSent && m.message_id ? tracking[normId(m.message_id)] : undefined
            return (
            <li
              key={`${m.folder}-${m.uid}`}
              onClick={() => nav(`/thread/${m.uid}?folder=${encodeURIComponent(realFolder)}`)}
              className={clsx(
                'grid items-center gap-3 px-4 py-3 cursor-pointer hover:bg-ink-50',
                isSent ? 'grid-cols-[24px,170px,1fr,150px]' : 'grid-cols-[24px,200px,1fr,90px]',
                m.unread && 'bg-brand-50/40',
              )}
            >
              <Star size={14} className={m.starred ? 'fill-yellow-400 text-yellow-400' : 'text-ink-300'} />
              <div className={clsx('truncate text-sm', m.unread && 'font-semibold')}>
                {m.from?.[0]?.name || m.from?.[0]?.address || '(unknown)'}
              </div>
              <div className="truncate text-sm">
                <span className={clsx(m.unread && 'font-semibold')}>{m.subject || '(no subject)'}</span>
                <span className="text-ink-500"> — {m.snippet}</span>
              </div>
              <div className="flex items-center justify-end gap-2 text-xs text-ink-500">
                {rec?.track_open && (
                  <span
                    className={clsx('flex items-center gap-0.5', rec.open_count > 0 ? 'text-green-600 font-medium' : 'text-ink-300')}
                    title={rec.open_count > 0
                      ? `Opened ${rec.open_count}×${rec.last_open_at ? ' · last ' + new Date(rec.last_open_at).toLocaleString() : ''}`
                      : 'Not opened yet'}
                  >
                    <MailOpen size={13} /> {rec.open_count}
                  </span>
                )}
                {rec?.track_click && rec.click_count > 0 && (
                  <span className="flex items-center gap-0.5 text-blue-600 font-medium" title={`${rec.click_count} link click${rec.click_count === 1 ? '' : 's'}`}>
                    <MousePointerClick size={13} /> {rec.click_count}
                  </span>
                )}
                {m.has_attach && <Paperclip size={12} />}
                {formatDate(m.date)}
              </div>
            </li>
          )})}
          {items.length === 0 && !loading && (
            <li className="px-4 py-10 text-center text-ink-500">No messages.</li>
          )}
        </ul>
      </div>
    </div>
  )
}
