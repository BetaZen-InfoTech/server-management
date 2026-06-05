import { FormEvent, useState } from 'react'
import toast from 'react-hot-toast'
import { api } from '@/api/client'

type DNSRecord = { type: string; name: string; expected: string; found?: string; ok: boolean }
type DNSStatus = { domain: string; records: DNSRecord[]; all_ok: boolean }

export default function DomainsPage() {
  const [domain, setDomain] = useState('')
  const [status, setStatus] = useState<DNSStatus | null>(null)
  const [busy, setBusy] = useState(false)

  async function enable(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      const r = await api.post<{ data: DNSStatus }>(`/dns/${encodeURIComponent(domain)}/enable-mail`)
      setStatus(r.data.data)
      toast.success('Records upserted — verify below')
    } catch (err: any) {
      toast.error(err?.response?.data?.error || 'Failed')
    } finally {
      setBusy(false)
    }
  }

  async function verify() {
    if (!domain) return
    setBusy(true)
    try {
      const r = await api.get<{ data: DNSStatus }>(`/dns/${encodeURIComponent(domain)}/status`)
      setStatus(r.data.data)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-6">
      <section>
        <h3 className="font-medium mb-2">Mail DNS setup</h3>
        <p className="text-sm text-ink-500 mb-3">
          Enabling mail for a domain upserts MX, A (mail.&lt;domain&gt;), SPF, DKIM, and DMARC records through your Betazen panel.
        </p>
        <form onSubmit={enable} className="flex gap-2 items-center">
          <input className="input flex-1" placeholder="example.com" value={domain} onChange={(e) => setDomain(e.target.value)} required />
          <button type="submit" className="btn-primary" disabled={busy}>Enable mail</button>
          <button type="button" className="btn-ghost" disabled={busy || !domain} onClick={verify}>Verify</button>
        </form>
      </section>

      {status && (
        <section>
          <h3 className="font-medium mb-2">
            Verification {status.all_ok ? <span className="text-green-600">— all OK</span> : <span className="text-amber-600">— not yet propagated</span>}
          </h3>
          <div className="overflow-auto rounded-lg border border-ink-100">
            <table className="w-full text-sm">
              <thead className="bg-ink-50 text-ink-500">
                <tr>
                  <th className="text-left px-3 py-2">Type</th>
                  <th className="text-left px-3 py-2">Name</th>
                  <th className="text-left px-3 py-2">Expected</th>
                  <th className="text-left px-3 py-2">Found</th>
                  <th className="text-left px-3 py-2">OK</th>
                </tr>
              </thead>
              <tbody>
                {status.records.map((r, i) => (
                  <tr key={i} className="border-t border-ink-100">
                    <td className="px-3 py-2">{r.type}</td>
                    <td className="px-3 py-2">{r.name}</td>
                    <td className="px-3 py-2 text-ink-600">{r.expected}</td>
                    <td className="px-3 py-2 text-ink-600">{r.found || '—'}</td>
                    <td className="px-3 py-2">{r.ok ? '✅' : '⏳'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </div>
  )
}
