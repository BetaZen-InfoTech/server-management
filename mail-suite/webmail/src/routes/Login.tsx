import { FormEvent, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import toast from 'react-hot-toast'
import { useAuth } from '@/store/auth'

export default function Login() {
  const { login } = useAuth()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const nav = useNavigate()

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setLoading(true)
    try {
      await login(email, password)
      nav('/inbox')
    } catch (err: any) {
      toast.error(err?.response?.data?.error || 'Login failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-full grid place-items-center bg-gradient-to-br from-brand-50 to-white p-6">
      <form onSubmit={onSubmit} className="card w-full max-w-sm p-6 flex flex-col gap-3">
        <h1 className="text-xl font-semibold text-brand-700">Betazen Mail</h1>
        <p className="text-sm text-ink-500 mb-2">Sign in with your email address and mailbox password</p>
        <input className="input" type="email" placeholder="you@yourdomain.com" value={email} onChange={(e) => setEmail(e.target.value)} required />
        <input className="input" type="password" placeholder="Mailbox password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        <button className="btn-primary" disabled={loading} type="submit">{loading ? 'Signing in…' : 'Sign in'}</button>
        <p className="text-xs text-ink-400 text-center">
          Use the same email and password you use for webmail. Your inbox is set up automatically on first sign-in.
        </p>
      </form>
    </div>
  )
}
