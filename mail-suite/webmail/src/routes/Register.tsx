import { FormEvent, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import toast from 'react-hot-toast'
import { useAuth } from '@/store/auth'

export default function Register() {
  const { register } = useAuth()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [name, setName] = useState('')
  const [loading, setLoading] = useState(false)
  const nav = useNavigate()

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setLoading(true)
    try {
      await register(email, password, name)
      nav('/settings/accounts')
    } catch (err: any) {
      toast.error(err?.response?.data?.error || 'Sign up failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-full grid place-items-center bg-gradient-to-br from-brand-50 to-white p-6">
      <form onSubmit={onSubmit} className="card w-full max-w-sm p-6 flex flex-col gap-3">
        <h1 className="text-xl font-semibold text-brand-700">Create account</h1>
        <input className="input" placeholder="Your name" value={name} onChange={(e) => setName(e.target.value)} required />
        <input className="input" type="email" placeholder="Email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        <input className="input" type="password" placeholder="Password (min 8)" value={password} onChange={(e) => setPassword(e.target.value)} required minLength={8} />
        <button className="btn-primary" disabled={loading} type="submit">{loading ? 'Creating…' : 'Sign up'}</button>
        <div className="text-sm text-ink-500 text-center">
          Already have an account? <Link to="/login" className="text-brand-600">Sign in</Link>
        </div>
      </form>
    </div>
  )
}
