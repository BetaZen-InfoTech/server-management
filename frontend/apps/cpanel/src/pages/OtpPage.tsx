import { useEffect, useState } from "react";
import { useNavigate, useSearchParams, Link } from "react-router-dom";
import axios from "axios";
import toast, { Toaster } from "react-hot-toast";
import { useAuthStore } from "@/store/auth";
import { Mail, KeyRound, LogIn, Server, Copy, ArrowLeft, ShieldOff, AlertTriangle } from "lucide-react";

// AXIOS_OPTS — same explicit-credentials shape as the WHM OtpPage.
// Forces axios to ride the bz_otp_bind cookie set by /auth/otp/request
// onto subsequent /auth/otp/{verify,cancel} calls.
const AXIOS_OPTS = { withCredentials: true } as const;

/**
 * Email-OTP login page (User Panel side).
 *
 * Symmetric to the WHM OtpPage but bounces vendor_owner accounts back
 * to /whm/otp so the two surfaces stay strictly separated.
 */
export default function OtpPage() {
  const navigate = useNavigate();
  const { setAuth } = useAuthStore();
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
  const [cancelling, setCancelling] = useState(false);
  const [autoTried, setAutoTried] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  // Set when the backend returns the typed "wrong browser" rejection
  // so we can render an inline explainer instead of a generic toast.
  const [wrongBrowser, setWrongBrowser] = useState(false);

  useEffect(() => {
    if (!autoTried && prefilledEmail && prefilledCode) {
      setAutoTried(true);
      void submitVerify(prefilledEmail, prefilledCode);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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
    setWrongBrowser(false);
    try {
      await axios.post("/api/v1/auth/otp/request", { email, surface: "user-panel" }, AXIOS_OPTS);
      toast.success("Check your inbox for a login code");
      setStage("verify");
      setCooldown(60);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Could not send code");
    } finally {
      setSending(false);
    }
  }

  async function submitVerify(emailVal: string, codeVal: string, e?: React.FormEvent) {
    e?.preventDefault();
    if (!emailVal || !codeVal) return toast.error("Enter the 10-character code");
    setVerifying(true);
    setWrongBrowser(false);
    try {
      const res = await axios.post("/api/v1/auth/otp/verify", {
        email: emailVal,
        code: codeVal.trim(),
      }, AXIOS_OPTS);
      const data = res.data.data;
      const role = data?.user?.role;
      if (role === "vendor_owner") {
        // Owner accounts must not land on the user-panel — bounce to
        // /whm/otp carrying the code so the WHM page can consume it.
        toast.error("Platform owner accounts use WHM — redirecting…");
        setTimeout(() => {
          window.location.href = `/whm/otp?email=${encodeURIComponent(emailVal)}&code=${encodeURIComponent(codeVal)}`;
        }, 800);
        return;
      }
      setAuth(data.user, data.access_token, data.refresh_token);
      toast.success("Welcome back!");
      navigate("/dashboard", { replace: true });
    } catch (err: any) {
      const msg = err?.response?.data?.error?.message || "Invalid or expired code";
      if (msg.includes("must be opened in the browser")) {
        setWrongBrowser(true);
      }
      toast.error(msg);
    } finally {
      setVerifying(false);
    }
  }

  // Revoke any pending OTP for this email so the magic URL + code
  // emailed earlier can no longer be redeemed. Only the originating
  // browser (one with the bz_otp_bind cookie) can do this.
  async function submitCancel() {
    if (!email) return;
    setCancelling(true);
    try {
      await axios.post("/api/v1/auth/otp/cancel", { email }, AXIOS_OPTS);
      toast.success("Pending login code dismissed");
      setStage("request");
      setCode("");
      setCooldown(0);
      setWrongBrowser(false);
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Could not dismiss code");
    } finally {
      setCancelling(false);
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
    <div className="min-h-screen bg-panel-bg flex items-center justify-center px-4">
      <Toaster
        position="top-right"
        toastOptions={{
          style: { background: "#1e1e2e", color: "#cdd6f4", border: "1px solid #313244" },
        }}
      />
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center w-16 h-16 bg-brand-600/10 rounded-2xl mb-4">
            <Server className="text-brand-400" size={32} />
          </div>
          <h1 className="text-2xl font-bold text-white">Sign in with a code</h1>
          <p className="text-panel-muted mt-2">We'll email you a one-time code</p>
        </div>

        <div className="bg-panel-surface border border-panel-border rounded-xl p-6">
          {stage === "request" ? (
            <form onSubmit={submitRequest} className="space-y-4">
              <div>
                <label className="block text-sm font-medium text-panel-text mb-1.5">
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
                    className="w-full pl-10 pr-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent text-sm"
                    required
                  />
                </div>
              </div>
              <button
                type="submit"
                disabled={sending}
                className="w-full flex items-center justify-center gap-2 py-2.5 bg-brand-600 hover:bg-brand-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50"
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
            <form onSubmit={(e) => submitVerify(email, code, e)} className="space-y-4">
              <div className="text-sm text-panel-muted">
                Sent a code to <span className="font-mono text-panel-text">{email || "your email"}</span>.
                It expires in 10 minutes.
              </div>
              {wrongBrowser && (
                <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 text-xs text-amber-200/90 flex items-start gap-2">
                  <AlertTriangle size={14} className="text-amber-400 mt-0.5 shrink-0" />
                  <div>
                    The link you clicked was issued for a different browser.
                    For security, the magic URL only works in the browser
                    that requested it. Either request a fresh code below
                    on this browser, or open the original email link from
                    the browser you started in.
                  </div>
                </div>
              )}
              <div>
                <label className="block text-sm font-medium text-panel-text mb-1.5">
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
                    className="w-full pl-10 pr-12 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-panel-text font-mono tracking-[0.2em] uppercase placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent text-sm"
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
                className="w-full flex items-center justify-center gap-2 py-2.5 bg-brand-600 hover:bg-brand-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50"
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
                  className="text-brand-400 hover:text-brand-300 transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
                >
                  {cooldown > 0 ? `Resend in ${cooldown}s` : "Resend code"}
                </button>
              </div>

              {/* Dismiss this session — kills the pending OTP so the
                  emailed magic URL + code can't be redeemed. The
                  backend's binding gate guarantees only this browser
                  can do it. */}
              <button
                type="button"
                onClick={submitCancel}
                disabled={cancelling}
                className="w-full flex items-center justify-center gap-1.5 py-2 text-xs text-red-400 hover:text-red-300 hover:bg-red-500/5 border border-red-500/20 rounded-lg transition-colors disabled:opacity-50"
                title="Revoke the pending login code so the emailed link can't be used"
              >
                <ShieldOff size={12} />
                {cancelling ? "Dismissing…" : "Dismiss this session (revoke code)"}
              </button>
            </form>
          )}

          <div className="mt-5 pt-4 border-t border-panel-border text-center">
            <Link to="/login" className="text-sm text-panel-muted hover:text-panel-text transition-colors">
              Prefer a password? Sign in the usual way →
            </Link>
          </div>
        </div>

        <p className="text-center text-sm text-panel-muted mt-6">
          Need an account?{" "}
          <a href="#" className="text-brand-400 hover:text-brand-300">
            Contact your administrator
          </a>
        </p>
      </div>
    </div>
  );
}
