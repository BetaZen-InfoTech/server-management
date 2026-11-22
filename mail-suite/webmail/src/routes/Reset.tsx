import { FormEvent, useMemo, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import toast from 'react-hot-toast'
import { api } from '@/api/client'

export default function Reset() {
  const [params] = useSearchParams()
  const token = useMemo(() => params.get('token') || '', [params])
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [loading, setLoading] = useState(false)
  const nav = useNavigate()

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (password.length < 8) {
      toast.error('Password must be at least 8 characters')
      return
    }
    if (password !== confirm) {
      toast.error('Passwords do not match')
      return
    }
    setLoading(true)
    try {
      await api.post('/mail-reset/reset', { token, password })
      toast.success('Password reset — please sign in with your new password')
      nav('/login')
    } catch (err: any) {
      // The proxy relays the panel's {success:false, error:{message}} shape.
      const msg = err?.response?.data?.error?.message || 'This reset link is invalid or has expired.'
      toast.error(msg)
    } finally {
      setLoading(false)
    }
  }

  if (!token) {
    return (
      <div className="min-h-full grid place-items-center bg-gradient-to-br from-brand-50 to-white p-6">
        <div className="card w-full max-w-sm p-6 flex flex-col gap-3">
          <h1 className="text-xl font-semibold text-brand-700">Reset link missing</h1>
          <p className="text-sm text-ink-500">
            This page needs a valid reset link. Request a new one from the sign-in screen.
          </p>
          <Link className="btn-primary text-center" to="/forgot">Request a reset link</Link>
        </div>
      </div>
    )
  }

  return (
    <div className="min-h-full grid place-items-center bg-gradient-to-br from-brand-50 to-white p-6">
      <form onSubmit={onSubmit} className="card w-full max-w-sm p-6 flex flex-col gap-3">
        <h1 className="text-xl font-semibold text-brand-700">Set a new password</h1>
        <p className="text-sm text-ink-500 mb-1">Choose a new password for your mailbox.</p>
        <input
          className="input"
          type="password"
          placeholder="New password (min 8 characters)"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
        />
        <input
          className="input"
          type="password"
          placeholder="Confirm new password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          required
        />
        <button className="btn-primary" disabled={loading} type="submit">
          {loading ? 'Saving…' : 'Set new password'}
        </button>
        <Link className="text-xs text-brand-600 text-center hover:underline" to="/login">
          Back to sign in
        </Link>
      </form>
    </div>
  )
}
