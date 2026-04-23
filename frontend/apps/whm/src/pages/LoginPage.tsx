import { useEffect, useMemo, useState } from "react";
import { useNavigate, Navigate, Link } from "react-router-dom";
import { Button, adminEmailPlaceholder } from "@serverpanel/ui";
import { useAuthStore } from "@/store/auth";
import axios from "axios";
import toast from "react-hot-toast";
import { LogIn, Server, Eye, EyeOff, Copy, Check, KeyRound } from "lucide-react";

export default function LoginPage() {
  const navigate = useNavigate();
  const { setAuth, isAuthenticated } = useAuthStore();

  // If already logged in, redirect declaratively. Calling useNavigate() during
  // render triggers a React warning and in some cases results in a blank page
  // because the component returns null before the navigation actually happens.
  if (isAuthenticated) {
    return <Navigate to="/dashboard" replace />;
  }
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [rememberMe, setRememberMe] = useState(false);
  const [copied, setCopied] = useState(false);
  const [version, setVersion] = useState("");
  // Email placeholder mirrors the panel's actual hostname so operators
  // see admin@<their-domain> instead of a hardcoded admin@serverpanel.io.
  // useMemo so the value is stable across renders (window.location is
  // constant for the page lifetime).
  const emailPlaceholder = useMemo(() => adminEmailPlaceholder(), []);
  // Owner-controlled flag — hide the demo-credentials card on
  // production deploys where leaking admin@betazeninfotech.com /
  // admin123 on a public login page isn't desirable. Starts null so we
  // don't flash the block before the public-settings fetch resolves.
  const [showDemo, setShowDemo] = useState<boolean | null>(null);

  // Fetch the live panel version from the public /api/v1/version endpoint
  // so the login-page footer tracks every deploy without us editing the
  // page on each release.
  useEffect(() => {
    axios
      .get("/api/v1/version")
      .then((r) => setVersion(r?.data?.data?.version ?? ""))
      .catch(() => {});
    axios
      .get("/api/v1/public-settings")
      .then((r) => setShowDemo(r?.data?.data?.show_demo_login_credentials !== false))
      .catch(() => setShowDemo(true));
  }, []);

  const demoCredentials = { email: "admin@betazeninfotech.com", password: "admin123" };

  const handleDemoFill = () => {
    setEmail(demoCredentials.email);
    setPassword(demoCredentials.password);
    setCopied(true);
    toast.success("Demo credentials filled");
    setTimeout(() => setCopied(false), 2000);
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email || !password) {
      toast.error("Please enter both email and password");
      return;
    }

    setLoading(true);
    try {
      const res = await axios.post("/api/v1/auth/login", { email, password, remember_me: rememberMe });
      const data = res.data.data;
      // WHM is the platform-owner workspace. Anyone else who manages
      // to authenticate here (vendor_admin, vendor_staff, developer,
      // support, customer) gets bounced to the user-panel login —
      // their tokens are never stored client-side, so no half-session
      // is left lying around. Backend keeps a single /auth/login so
      // password reset / forgot flows stay symmetric across surfaces.
      const role = data?.user?.role;
      if (role !== "vendor_owner") {
        toast.error("This account belongs on the User Panel — redirecting…");
        // Hard nav so the user-panel SPA boots fresh, no React state
        // bleed-through. The user-panel LoginPage will recognise an
        // already-issued refresh token via the standard refresh path.
        setTimeout(() => { window.location.href = "/user-panel/login"; }, 800);
        return;
      }
      setAuth(data.user, data.access_token, data.refresh_token);
      toast.success("Login successful");
      navigate("/dashboard", { replace: true });
    } catch (err: any) {
      const message = err.response?.data?.error?.message || "Invalid credentials";
      toast.error(message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-panel-bg p-4">
      <div className="w-full max-w-md">
        {/* Brand Header */}
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center w-16 h-16 rounded-2xl bg-blue-600/10 border border-blue-600/20 mb-4">
            <Server className="text-blue-500" size={32} />
          </div>
          <h1 className="text-2xl font-bold text-panel-text">Betazen Server Panel WHM</h1>
          <p className="text-panel-muted mt-1">Vendor & Admin Control Panel</p>
        </div>

        {/* Login Card */}
        <div className="bg-panel-surface border border-panel-border rounded-xl p-8">
          <h2 className="text-lg font-semibold text-panel-text mb-6">Sign in to your account</h2>

          <form onSubmit={handleSubmit} className="space-y-5">
            <div>
              <label htmlFor="email" className="block text-sm font-medium text-panel-muted mb-1.5">
                Email Address
              </label>
              <input
                id="email"
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder={emailPlaceholder}
                className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors"
                autoComplete="email"
                required
              />
            </div>

            <div>
              <label htmlFor="password" className="block text-sm font-medium text-panel-muted mb-1.5">
                Password
              </label>
              <div className="relative">
                <input
                  id="password"
                  type={showPassword ? "text" : "password"}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="Enter your password"
                  className="w-full px-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors pr-12"
                  autoComplete="current-password"
                  required
                />
                <button
                  type="button"
                  onClick={() => setShowPassword(!showPassword)}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-panel-muted hover:text-panel-text transition-colors"
                >
                  {showPassword ? <EyeOff size={18} /> : <Eye size={18} />}
                </button>
              </div>
            </div>

            <div className="flex items-center justify-between">
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={rememberMe}
                  onChange={(e) => setRememberMe(e.target.checked)}
                  className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-500 focus:ring-blue-500/40"
                />
                <span className="text-sm text-panel-muted">Remember me for 30 days</span>
              </label>
              <Link to="/forgot-password" className="text-sm text-blue-500 hover:text-blue-400 transition-colors">
                Forgot password?
              </Link>
            </div>

            <Button
              onClick={() => {}}
              className="w-full flex items-center justify-center gap-2 py-2.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              disabled={loading}
            >
              {loading ? (
                <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
              ) : (
                <LogIn size={18} />
              )}
              {loading ? "Signing in..." : "Sign In"}
            </Button>
          </form>

          {/* Passwordless alternative — lands on /otp which handles both
              "enter email → email me a code" and "verify code" in one
              page. Matches the password-reset-link pattern so the two
              flows feel related. */}
          <div className="relative my-5">
            <div className="absolute inset-0 flex items-center" aria-hidden="true">
              <div className="w-full border-t border-panel-border"></div>
            </div>
            <div className="relative flex justify-center">
              <span className="bg-panel-surface px-2 text-[11px] uppercase tracking-wider text-panel-muted">or</span>
            </div>
          </div>
          <Link
            to="/otp"
            className="w-full flex items-center justify-center gap-2 py-2.5 border border-panel-border rounded-lg font-medium text-panel-text hover:bg-panel-bg transition-colors"
          >
            <KeyRound size={16} />
            Sign in with an email code
          </Link>
        </div>

        {/* Demo Credentials — gated on the owner-controlled public flag */}
        {showDemo && (
          <div className="mt-4 bg-panel-surface border border-dashed border-blue-500/30 rounded-xl p-4">
            <div className="flex items-center justify-between mb-3">
              <span className="text-xs font-semibold uppercase tracking-wider text-blue-400">Demo Admin Login</span>
              <button
                type="button"
                onClick={handleDemoFill}
                className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium rounded-lg bg-blue-600/10 border border-blue-500/20 text-blue-400 hover:bg-blue-600/20 hover:border-blue-500/40 transition-colors"
              >
                {copied ? <Check size={12} /> : <Copy size={12} />}
                {copied ? "Filled!" : "Copy & Fill"}
              </button>
            </div>
            <div className="space-y-2">
              <div className="flex items-center justify-between bg-panel-bg rounded-lg px-3 py-2">
                <span className="text-xs text-panel-muted">Email</span>
                <span className="text-sm text-panel-text font-mono">{demoCredentials.email}</span>
              </div>
              <div className="flex items-center justify-between bg-panel-bg rounded-lg px-3 py-2">
                <span className="text-xs text-panel-muted">Password</span>
                <span className="text-sm text-panel-text font-mono">{demoCredentials.password}</span>
              </div>
            </div>
          </div>
        )}

        {/* Footer */}
        <p className="text-center text-panel-muted text-xs mt-6">
          Betazen Server Panel WHM{version ? ` v${version}` : ""} &middot; Secure admin access only
        </p>
      </div>
    </div>
  );
}
