import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import axios from "axios";
import toast from "react-hot-toast";
import { adminEmailPlaceholder } from "@serverpanel/ui";
import { Mail, ArrowLeft, Send, Loader2, CheckCircle2 } from "lucide-react";

// ForgotPasswordPage — public entry for the "I forgot my password" flow.
// POSTs /api/v1/auth/forgot-password with the email. The backend always
// returns success regardless of whether the email matches a user, so
// nothing we render here can leak which addresses are registered.
// Users who typed a matching address get a reset email with a link to
// /whm/reset-password?token=<hex>.
//
// IMPORTANT: this page uses a bare `axios` call to the absolute
// /api/v1/auth/... path on purpose. The shared `@/lib/api` client has
// baseURL `/api/v1/whm` AND auth middleware on every route in that
// group — so routing this through it would 401 on the anonymous
// caller, trigger the response interceptor's refresh-or-logout dance,
// and silently bounce the user back to /login without ever sending a
// mail. (That was the long-standing "forgot password sends nothing"
// bug.) Keep this as plain axios so the public auth endpoints stay
// reachable from the public auth pages.
export default function ForgotPasswordPage() {
  const [email, setEmail] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [sent, setSent] = useState(false);
  // Mirror the panel's actual hostname into the placeholder so
  // operators see admin@<their-domain> instead of a generic example.
  const emailPlaceholder = useMemo(() => adminEmailPlaceholder(), []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email.trim()) {
      toast.error("Enter the email address on your account");
      return;
    }
    setSubmitting(true);
    try {
      // Tell the backend which surface the request originated from so
      // the emailed link points back at /whm/reset-password (owner
      // panel) instead of /user-panel/reset-password.
      await axios.post("/api/v1/auth/forgot-password", {
        email: email.trim(),
        surface: "whm",
      });
      setSent(true);
    } catch {
      // Backend returns 2xx for this endpoint regardless of success.
      // A network-level failure still shows a toast.
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
                className="inline-flex items-center gap-2 text-sm text-blue-400 hover:underline mt-4"
              >
                <ArrowLeft size={14} /> Back to sign in
              </Link>
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="space-y-5">
              <div className="text-center space-y-2">
                <div className="inline-flex p-3 rounded-full bg-blue-500/10 border border-blue-500/20">
                  <Mail size={28} className="text-blue-400" />
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
                  placeholder={emailPlaceholder}
                  className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 text-sm"
                />
                <p className="text-[11px] text-panel-muted mt-1">
                  Works for every account type: platform admin, vendor admin, staff, developer, support.
                </p>
              </div>

              <button
                type="submit"
                disabled={submitting || !email.trim()}
                className="w-full flex items-center justify-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
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
