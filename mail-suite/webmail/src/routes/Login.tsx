import { FormEvent, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
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
        <p className="text-sm text-ink-500 mb-2">Sign in to continue</p>
        <input className="input" type="email" placeholder="Email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        <input className="input" type="password" placeholder="Password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        <button className="btn-primary" disabled={loading} type="submit">{loading ? 'Signing in…' : 'Sign in'}</button>
        <div className="text-sm text-ink-500 text-center">
          New here? <Link to="/register" className="text-brand-600">Create an account</Link>
        </div>
      </form>
    </div>
  )
}
