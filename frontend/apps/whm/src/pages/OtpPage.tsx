import { useEffect, useState } from "react";
import { useNavigate, useSearchParams, Link } from "react-router-dom";
import axios from "axios";
import toast from "react-hot-toast";
import { useAuthStore } from "@/store/auth";
import { Mail, KeyRound, LogIn, Server, Copy, ArrowLeft } from "lucide-react";

/**
 * Email-OTP login page (WHM side).
 *
 * Two modes driven by the URL + state:
 *   1. Request — user enters an email, clicks "Send code". We POST
 *      /api/v1/auth/otp/request and switch to Verify mode.
 *   2. Verify — user enters the code (or follows the magic link,
 *      which prefills `email` + `code` via query string). We POST
 *      /api/v1/auth/otp/verify; success stores tokens like password
 *      login and redirects to /dashboard.
 *
 * The magic link from the email lands here with `?email=&code=` and
 * we auto-submit once on mount so the user only clicks once.
 */
export default function OtpPage() {
  const navigate = useNavigate();
  const { setAuth, isAuthenticated } = useAuthStore();
  const [params] = useSearchParams();

  const prefilledEmail = params.get("email") || "";
  const prefilledCode = params.get("code") || "";

  const [email, setEmail] = useState(prefilledEmail);
  const [code, setCode] = useState(prefilledCode);
  const [stage, setStage] = useState<"request" | "verify">(
    prefilledEmail && prefilledCode ? "verify" : "request",
  );
  const [sending, setSending] = useState(false);
  const [verifying, setVerifying] = useState(false);
  const [autoTried, setAutoTried] = useState(false);
  const [cooldown, setCooldown] = useState(0);

  // If the Zustand store already has a valid session, bounce straight
  // to the dashboard — the magic link was clicked from an authed device.
  useEffect(() => {
    if (isAuthenticated) navigate("/dashboard", { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Auto-verify when both email + code arrive via query string. Guarded
  // by `autoTried` so a failed auto-verify doesn't keep retrying in a
  // loop as state changes.
  useEffect(() => {
    if (!autoTried && prefilledEmail && prefilledCode) {
      setAutoTried(true);
      void submitVerify(prefilledEmail, prefilledCode);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Resend cooldown — the backend rate-limits to 60s per email, so we
  // mirror that in the UI instead of letting the user eat a silent
  // rate-limit response.
  useEffect(() => {
    if (cooldown <= 0) return;
    const t = setTimeout(() => setCooldown((c) => c - 1), 1000);
    return () => clearTimeout(t);
  }, [cooldown]);

  async function submitRequest(e?: React.FormEvent) {
    e?.preventDefault();
    if (!email) return toast.error("Please enter your email");
    if (cooldown > 0) return;
    setSending(true);
    try {
      await axios.post("/api/v1/auth/otp/request", { email, surface: "whm" });
      toast.success("Check your inbox for a login code");
      setStage("verify");
      setCooldown(60);
    } catch (err: any) {
      // Server swallows errors to avoid enumeration — a 5xx is the only
      // way this throws.
      toast.error(err?.response?.data?.error?.message || "Could not send code");
    } finally {
      setSending(false);
    }
  }

  async function submitVerify(emailVal: string, codeVal: string, e?: React.FormEvent) {
    e?.preventDefault();
    if (!emailVal || !codeVal) return toast.error("Enter the 10-character code");
    setVerifying(true);
    try {
      const res = await axios.post("/api/v1/auth/otp/verify", {
        email: emailVal,
        code: codeVal.trim(),
      });
      const data = res.data.data;
      const role = data?.user?.role;
      if (role !== "vendor_owner") {
        // Wrong surface — OTP for a vendor/staff/customer account was
        // used on WHM. Bounce to user-panel with the code so the
        // receiving page can consume it.
        toast.error("This account belongs on the User Panel — redirecting…");
        setTimeout(() => {
          window.location.href = `/user-panel/otp?email=${encodeURIComponent(emailVal)}&code=${encodeURIComponent(codeVal)}`;
        }, 800);
        return;
      }
      setAuth(data.user, data.access_token, data.refresh_token);
      toast.success("Signed in");
      navigate("/dashboard", { replace: true });
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Invalid or expired code");
    } finally {
      setVerifying(false);
    }
  }

  async function copyCode() {
    if (!code) return;
    try {
      await navigator.clipboard.writeText(code);
      toast.success("Code copied");
    } catch {
      toast.error("Copy failed");
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-panel-bg p-4">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center w-16 h-16 rounded-2xl bg-blue-600/10 border border-blue-600/20 mb-4">
            <Server className="text-blue-500" size={32} />
          </div>
          <h1 className="text-2xl font-bold text-panel-text">Sign in with a code</h1>
          <p className="text-panel-muted mt-1">We'll email you a one-time code</p>
        </div>

        <div className="bg-panel-surface border border-panel-border rounded-xl p-8">
          {stage === "request" ? (
            <form onSubmit={submitRequest} className="space-y-5">
              <div>
                <label className="block text-sm font-medium text-panel-muted mb-1.5">
                  Email Address
                </label>
                <div className="relative">
                  <Mail size={18} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
                  <input
                    autoFocus
                    type="email"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    placeholder="you@example.com"
                    className="w-full pl-10 pr-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500"
                    autoComplete="email"
                    required
                  />
                </div>
              </div>
              <button
                type="submit"
                disabled={sending}
                className="w-full flex items-center justify-center gap-2 py-2.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50"
              >
                {sending ? (
                  <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                ) : (
                  <Mail size={16} />
                )}
                {sending ? "Sending…" : "Send login code"}
              </button>
            </form>
          ) : (
            <form onSubmit={(e) => submitVerify(email, code, e)} className="space-y-5">
              <div className="text-sm text-panel-muted">
                Sent a code to <span className="font-mono text-panel-text">{email || "your email"}</span>.
                It expires in 10 minutes.
              </div>
              <div>
                <label className="block text-sm font-medium text-panel-muted mb-1.5">
                  Login code
                </label>
                <div className="relative">
                  <KeyRound size={18} className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted" />
                  <input
                    autoFocus
                    type="text"
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                    placeholder="A1B2C3D4E5"
                    maxLength={16}
                    className="w-full pl-10 pr-12 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-panel-text font-mono tracking-[0.2em] uppercase placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500"
                    autoComplete="one-time-code"
                    required
                  />
                  <button
                    type="button"
                    onClick={copyCode}
                    className="absolute right-2 top-1/2 -translate-y-1/2 p-1.5 rounded text-panel-muted hover:text-panel-text hover:bg-panel-bg transition-colors"
                    title="Copy code"
                  >
                    <Copy size={14} />
                  </button>
                </div>
              </div>
              <button
                type="submit"
                disabled={verifying}
                className="w-full flex items-center justify-center gap-2 py-2.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50"
              >
                {verifying ? (
                  <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                ) : (
                  <LogIn size={16} />
                )}
                {verifying ? "Verifying…" : "Sign in"}
              </button>

              <div className="flex items-center justify-between text-xs">
                <button
                  type="button"
                  onClick={() => setStage("request")}
                  className="flex items-center gap-1 text-panel-muted hover:text-panel-text transition-colors"
                >
                  <ArrowLeft size={12} /> Use a different email
                </button>
                <button
                  type="button"
                  onClick={() => submitRequest()}
                  disabled={sending || cooldown > 0}
                  className="text-blue-500 hover:text-blue-400 transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
                >
                  {cooldown > 0 ? `Resend in ${cooldown}s` : "Resend code"}
                </button>
              </div>
            </form>
          )}

          <div className="mt-5 pt-4 border-t border-panel-border text-center">
            <Link to="/login" className="text-sm text-panel-muted hover:text-panel-text transition-colors">
              Prefer a password? Sign in the usual way →
            </Link>
          </div>
        </div>

        <p className="text-center text-panel-muted text-xs mt-6">
          Betazen Server Panel WHM &middot; Secure admin access only
        </p>
      </div>
    </div>
  );
}
