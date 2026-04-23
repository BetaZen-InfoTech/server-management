import { useState } from "react";
import { Link } from "react-router-dom";
import axios from "axios";
import toast from "react-hot-toast";
import { Mail, ArrowLeft, Send, Loader2, CheckCircle2 } from "lucide-react";

// ForgotPasswordPage (User Panel) — public entry for vendor / staff /
// customer accounts that have lost their password. POSTs the email to
// /api/v1/auth/forgot-password with surface=user-panel so the emailed
// link points back at /user-panel/reset-password (not /whm/...).
//
// The backend always returns success regardless of whether the email
// matches a user, so this page can never be used to enumerate
// registered addresses.
//
// Uses plain axios instead of the shared `@/lib/api` client because
// that client's baseURL is `/api/v1/cpanel` AND every cpanel route is
// auth-gated — routing the public auth endpoints through it would
// 401 on anonymous callers, trigger the refresh-or-logout interceptor,
// and silently bounce the user back to /login without sending a mail.
export default function ForgotPasswordPage() {
  const [email, setEmail] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [sent, setSent] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email.trim()) {
      toast.error("Enter the email address on your account");
      return;
    }
    setSubmitting(true);
    try {
      await axios.post("/api/v1/auth/forgot-password", {
        email: email.trim(),
        surface: "user-panel",
      });
      setSent(true);
    } catch {
      toast.error("Couldn't reach the server — check your connection and try again");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="min-h-screen bg-panel-bg flex items-center justify-center p-4">
      <div className="w-full max-w-md">
        <div className="bg-panel-surface border border-panel-border rounded-xl p-8 shadow-xl">
          {sent ? (
            <div className="text-center space-y-4">
              <div className="inline-flex p-3 rounded-full bg-green-500/10 border border-green-500/20">
                <CheckCircle2 size={28} className="text-green-400" />
              </div>
              <h1 className="text-xl font-semibold text-panel-text">Check your email</h1>
              <p className="text-sm text-panel-muted leading-relaxed">
                If <b className="text-panel-text">{email}</b> matches an account on this panel, we've sent a password-reset link to it. The link expires in <b>30 minutes</b>.
              </p>
              <p className="text-xs text-panel-muted">
                Didn't receive it? Check your spam folder, or wait a minute and try again (we rate-limit repeat requests per account).
              </p>
              <Link
                to="/login"
                className="inline-flex items-center gap-2 text-sm text-brand-400 hover:underline mt-4"
              >
                <ArrowLeft size={14} /> Back to sign in
              </Link>
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="space-y-5">
              <div className="text-center space-y-2">
                <div className="inline-flex p-3 rounded-full bg-brand-500/10 border border-brand-500/20">
                  <Mail size={28} className="text-brand-400" />
                </div>
                <h1 className="text-xl font-semibold text-panel-text mt-2">Forgot your password?</h1>
                <p className="text-sm text-panel-muted">
                  Enter the email on your account and we'll send a reset link.
                </p>
              </div>

              <div>
                <label className="block text-sm font-medium text-panel-text mb-1.5">Email</label>
                <input
                  type="email"
                  required
                  autoFocus
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="you@example.com"
                  className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-brand-500/40 focus:border-brand-500 text-sm"
                />
                <p className="text-[11px] text-panel-muted mt-1">
                  Works for vendor admin, staff, developer, support, and customer accounts.
                </p>
              </div>

              <button
                type="submit"
                disabled={submitting || !email.trim()}
                className="w-full flex items-center justify-center gap-2 px-4 py-2 bg-brand-600 hover:bg-brand-700 text-white rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
              >
                {submitting ? <Loader2 size={14} className="animate-spin" /> : <Send size={14} />}
                {submitting ? "Sending…" : "Send reset link"}
              </button>

              <div className="text-center pt-2 border-t border-panel-border">
                <Link
                  to="/login"
                  className="inline-flex items-center gap-2 text-sm text-panel-muted hover:text-panel-text"
                >
                  <ArrowLeft size={14} /> Back to sign in
                </Link>
              </div>
            </form>
          )}
        </div>
      </div>
    </div>
  );
}
