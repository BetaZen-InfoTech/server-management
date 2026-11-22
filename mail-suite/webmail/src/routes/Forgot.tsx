import { FormEvent, useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '@/api/client'

export default function Forgot() {
  const [email, setEmail] = useState('')
  const [loading, setLoading] = useState(false)
  const [sent, setSent] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setLoading(true)
    try {
      // Always resolves 200 — the backend never reveals whether the address
      // exists or has a recovery email (no account enumeration).
      await api.post('/mail-reset/forgot', { email })
      setSent(true)
    } catch {
      // Even a transient/proxy error is shown as the same neutral result so
      // nothing leaks; the user can retry.
      setSent(true)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-full grid place-items-center bg-gradient-to-br from-brand-50 to-white p-6">
      <div className="card w-full max-w-sm p-6 flex flex-col gap-3">
        <h1 className="text-xl font-semibold text-brand-700">Reset your password</h1>
        {sent ? (
          <>
            <p className="text-sm text-ink-500">
              If <b>{email}</b> is a Betazen Mail address with a recovery email on file, we've sent a
              reset link to that recovery address. It is valid for 30 minutes.
            </p>
            <p className="text-xs text-ink-400">
              Didn't get it? Check spam, or make sure you added a recovery email in
              <span className="whitespace-nowrap"> Settings → Accounts</span> before you were locked out.
            </p>
            <Link className="btn-primary text-center" to="/login">Back to sign in</Link>
          </>
        ) : (
          <form onSubmit={onSubmit} className="flex flex-col gap-3">
            <p className="text-sm text-ink-500 mb-1">
              Enter your mailbox address. We'll email a reset link to the recovery address you set up.
            </p>
            <input
              className="input"
              type="email"
              placeholder="you@yourdomain.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
            />
            <button className="btn-primary" disabled={loading} type="submit">
              {loading ? 'Sending…' : 'Send reset link'}
            </button>
            <Link className="text-xs text-brand-600 text-center hover:underline" to="/login">
              Back to sign in
            </Link>
          </form>
        )}
      </div>
    </div>
  )
}
