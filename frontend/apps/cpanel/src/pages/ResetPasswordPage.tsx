import { useState, useMemo } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import axios from "axios";
import toast from "react-hot-toast";
import { KeyRound, Eye, EyeOff, Loader2, ArrowLeft, ShieldCheck } from "lucide-react";

// ResetPasswordPage (User Panel) — public landing for the reset link
// the mailer sends to vendor / staff / customer accounts. Expects
// ?token=<hex> in the URL. POSTs to /api/v1/auth/reset-password with
// the token + new password; on success redirects to /login.
//
// Uses bare axios for the same reason ForgotPasswordPage does — the
// shared /api/v1/cpanel client is auth-gated and would 401 anonymous
// callers, force-logging them out instead of completing the reset.
export default function ResetPasswordPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const token = useMemo(() => params.get("token") || "", [params]);

  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [reveal, setReveal] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const tooShort = password.length > 0 && password.length < 8;
  const mismatch = confirm.length > 0 && confirm !== password;
  const canSubmit = token.length > 0 && password.length >= 8 && confirm === password;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    setSubmitting(true);
    try {
      await axios.post("/api/v1/auth/reset-password", { token, new_password: password });
      toast.success("Password updated — sign in with your new password");
      navigate("/login");
    } catch (err: any) {
      toast.error(
        err?.response?.data?.error?.message || "Reset failed — the link may have expired"
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="min-h-screen bg-panel-bg flex items-center justify-center p-4">
      <div className="w-full max-w-md">
        <div className="bg-panel-surface border border-panel-border rounded-xl p-8 shadow-xl">
          {!token ? (
            <div className="text-center space-y-4">
              <h1 className="text-xl font-semibold text-panel-text">Missing reset link</h1>
              <p className="text-sm text-panel-muted">
                This page expects a token in the URL. Use the link we emailed you, or request a new one.
              </p>
              <Link
                to="/forgot-password"
                className="inline-flex items-center gap-2 px-4 py-2 bg-brand-600 hover:bg-brand-700 text-white rounded-lg text-sm font-medium"
              >
                Request a new link
              </Link>
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="space-y-5">
              <div className="text-center space-y-2">
                <div className="inline-flex p-3 rounded-full bg-brand-500/10 border border-brand-500/20">
                  <KeyRound size={28} className="text-brand-400" />
                </div>
                <h1 className="text-xl font-semibold text-panel-text mt-2">Set a new password</h1>
                <p className="text-sm text-panel-muted">
                  Pick something at least 8 characters. Any existing sessions get signed out.
                </p>
              </div>

              <div>
                <label className="block text-sm font-medium text-panel-text mb-1.5">New password</label>
                <div className="relative">
                  <input
                    type={reveal ? "text" : "password"}
                    required
                    autoFocus
                    minLength={8}
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="At least 8 characters"
                    className="w-full pr-10 px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-brand-500/40 focus:border-brand-500 text-sm"
                  />
                  <button
                    type="button"
                    onClick={() => setReveal((r) => !r)}
                    className="absolute right-2 top-1/2 -translate-y-1/2 p-1 text-panel-muted hover:text-panel-text"
                    aria-label={reveal ? "Hide password" : "Show password"}
                  >
                    {reveal ? <EyeOff size={14} /> : <Eye size={14} />}
                  </button>
                </div>
                {tooShort && <p className="text-[11px] text-red-400 mt-1">Must be at least 8 characters.</p>}
              </div>

              <div>
                <label className="block text-sm font-medium text-panel-text mb-1.5">Confirm password</label>
                <input
                  type={reveal ? "text" : "password"}
                  required
                  minLength={8}
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  placeholder="Type the new password again"
                  className="w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-brand-500/40 focus:border-brand-500 text-sm"
                />
                {mismatch && <p className="text-[11px] text-red-400 mt-1">Passwords don't match.</p>}
              </div>

              <button
                type="submit"
                disabled={!canSubmit || submitting}
                className="w-full flex items-center justify-center gap-2 px-4 py-2 bg-brand-600 hover:bg-brand-700 text-white rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
              >
                {submitting ? <Loader2 size={14} className="animate-spin" /> : <ShieldCheck size={14} />}
                {submitting ? "Updating…" : "Set new password"}
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
