import { useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams, Link } from "react-router-dom";
import axios from "axios";
import toast from "react-hot-toast";
import { useAuthStore } from "@/store/auth";
import { Mail, KeyRound, LogIn, Server, Copy, ArrowLeft, ShieldOff, CheckCircle2 } from "lucide-react";

// AXIOS_OPTS — withCredentials forces axios to include the
// bz_otp_bind cookie set by /auth/otp/request on subsequent
// /auth/otp/{verify,poll,complete,cancel} calls. Same-origin requests
// usually do this anyway, but stating it explicitly removes any
// ambiguity if the browser ever treats the call as cross-origin
// (e.g. a future CDN-fronted deploy).
const AXIOS_OPTS = { withCredentials: true } as const;

// Poll cadence for the originating browser's "has anyone clicked the
// magic link?" check. 2.5s is frequent enough that the handoff feels
// instant but not so fast that the backend sees a stampede if a user
// walks away from the tab.
const POLL_INTERVAL_MS = 2500;

/**
 * Email-OTP login page (WHM side).
 *
 * Three cooperating flows:
 *   1. Request — user enters an email, clicks "Send code". We POST
 *      /api/v1/auth/otp/request (backend stamps bz_otp_bind cookie)
 *      and switch to Verify mode.
 *   2. Verify — two sub-flows that the same page handles:
 *        a. Same-browser: user types (or the magic-link query string
 *           prefills) the 10-char code. POST /api/v1/auth/otp/verify
 *           returns the JWT pair.
 *        b. Cross-browser: the user clicked the emailed magic URL in
 *           a DIFFERENT browser than the one that requested the code.
 *           /verify on that browser returns {status:"approved_in_other_browser"},
 *           and the originating browser — which has been polling
 *           /api/v1/auth/otp/poll — sees handoff_approved:true and
 *           auto-finishes via /api/v1/auth/otp/complete. The clicking
 *           browser only shows a confirmation screen; it never gets
 *           a session.
 *   3. Cancel — one-click "Dismiss this session" that revokes any
 *      pending OTP. Binding-cookie gated, so only the originating
 *      browser can kill its own pending login.
 */
export default function OtpPage() {
  const navigate = useNavigate();
  const { setAuth, isAuthenticated } = useAuthStore();
  const [params] = useSearchParams();

  const prefilledEmail = params.get("email") || "";
  const prefilledCode = params.get("code") || "";

  const [email, setEmail] = useState(prefilledEmail);
  const [code, setCode] = useState(prefilledCode);
  const [stage, setStage] = useState<"request" | "verify" | "handoff-done">(
    prefilledEmail && prefilledCode ? "verify" : "request",
  );
  const [sending, setSending] = useState(false);
  const [verifying, setVerifying] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [autoTried, setAutoTried] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  // Ref-guarded one-shot so the cross-browser auto-complete doesn't
  // fire twice if the poll and a user click race.
  const completingRef = useRef(false);

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

  // Originating-browser polling loop. Only runs on the "verify" stage
  // so a user staring at the request form or the handoff-done screen
  // doesn't generate traffic. When the backend reports
  // handoff_approved=true we call /auth/otp/complete to finish the
  // login without making the user retype the code.
  useEffect(() => {
    if (stage !== "verify") return;
    let cancelled = false;
    const tick = async () => {
      if (cancelled || completingRef.current) return;
      try {
        const res = await axios.post("/api/v1/auth/otp/poll", {}, AXIOS_OPTS);
        const data = res.data?.data;
        if (!data?.pending) return; // no OTP tied to this cookie — stop silently
        if (data?.handoff_approved) {
          completingRef.current = true;
          await completeHandoff();
        }
      } catch {
        // Ignore — the page is still usable via manual code entry and
        // the next tick will retry. Errors here are almost always
        // rate-limit bumps which we don't want to surface as toasts.
      }
    };
    const id = setInterval(tick, POLL_INTERVAL_MS);
    // Fire once immediately so a user who arrives at /whm/otp with the
    // OTP already approved (rare race) doesn't wait a full tick.
    void tick();
    return () => {
      cancelled = true;
      clearInterval(id);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stage]);

  async function submitRequest(e?: React.FormEvent) {
    e?.preventDefault();
    if (!email) return toast.error("Please enter your email");
    if (cooldown > 0) return;
    setSending(true);
    try {
      await axios.post("/api/v1/auth/otp/request", { email, surface: "whm" }, AXIOS_OPTS);
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

  // Completes a handoff-approved login. Only runs on the originating
  // browser (the one holding the bz_otp_bind cookie); a browser that
  // triggered the approval by clicking a forwarded URL gets nothing.
  async function completeHandoff() {
    try {
      const res = await axios.post("/api/v1/auth/otp/complete", {}, AXIOS_OPTS);
      const data = res.data.data;
      const role = data?.user?.role;
      if (role !== "vendor_owner") {
        toast.error("This account belongs on the User Panel — redirecting…");
        setTimeout(() => {
          window.location.href = `/user-panel/otp`;
        }, 800);
        return;
      }
      setAuth(data.user, data.access_token, data.refresh_token);
      toast.success("Signed in from your other browser's click");
      navigate("/dashboard", { replace: true });
    } catch (err: any) {
      // Fell through — the approval window expired or was cancelled.
      // Reset the latch so the next tick can try again if the user
      // taps Resend.
      completingRef.current = false;
      toast.error(err?.response?.data?.error?.message || "Could not complete sign-in");
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
      }, AXIOS_OPTS);
      const data = res.data.data;
      // Cross-browser handoff path — backend wrote handoff_approved on
      // the OTP doc instead of issuing tokens. Show a friendly
      // confirmation; the originating browser's poll will complete.
      if (data?.status === "approved_in_other_browser") {
        setStage("handoff-done");
        return;
      }
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
      const msg = err?.response?.data?.error?.message || "Invalid or expired code";
      toast.error(msg);
    } finally {
      setVerifying(false);
    }
  }

  // Revoke any pending OTP for this email so the magic URL + code
  // emailed earlier can no longer be redeemed. Only the originating
  // browser (one with the bz_otp_bind cookie) can do this — the
  // backend's binding gate makes calls from anywhere else a no-op.
  async function submitCancel() {
    if (!email) return;
    setCancelling(true);
    try {
      await axios.post("/api/v1/auth/otp/cancel", { email }, AXIOS_OPTS);
      toast.success("Pending login code dismissed");
      setStage("request");
      setCode("");
      setCooldown(0);
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
          {stage === "handoff-done" ? (
            // Cross-browser click success screen. This browser does NOT
            // get a session — it only signalled the originating browser
            // (which holds the binding cookie) to finish signing in.
            <div className="space-y-5">
              <div className="flex flex-col items-center text-center gap-3">
                <div className="w-14 h-14 rounded-full bg-green-500/10 border border-green-500/30 flex items-center justify-center">
                  <CheckCircle2 className="text-green-400" size={28} />
                </div>
                <div>
                  <h2 className="text-lg font-semibold text-panel-text">Sign-in approved</h2>
                  <p className="text-sm text-panel-muted mt-1.5 leading-relaxed">
                    Return to the browser where you started the login —
                    it will finish signing in for you automatically.
                    For your security, this browser will not be signed
                    in from a link alone.
                  </p>
                </div>
              </div>
              <Link
                to="/login"
                className="w-full flex items-center justify-center gap-2 py-2.5 bg-panel-bg border border-panel-border hover:border-panel-border-hover text-panel-text rounded-lg font-medium transition-colors"
              >
                <ArrowLeft size={14} /> Back to sign in
              </Link>
            </div>
          ) : stage === "request" ? (
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

              <p className="text-xs text-panel-muted/80 leading-relaxed text-center">
                Tip: if your email is open in another browser, clicking
                the magic link there will finish sign-in here automatically.
              </p>

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

              {/* Dismiss this session — kills the pending OTP so the
                  emailed magic URL + code can't be redeemed. The
                  backend's binding gate guarantees only the
                  originating browser can do this. */}
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

        <p className="text-center text-panel-muted text-xs mt-6">
          Betazen Server Panel WHM &middot; Secure admin access only
        </p>
      </div>
    </div>
  );
}
