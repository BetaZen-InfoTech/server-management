import { useEffect, useMemo, useState } from "react";
import { useNavigate, Navigate, Link } from "react-router-dom";
import { Button, adminEmailPlaceholder, OfflineOverlay } from "@serverpanel/ui";
import { useAuthStore } from "@/store/auth";
import axios from "axios";
import toast from "react-hot-toast";
import {
  LogIn,
  Server,
  Eye,
  EyeOff,
  Copy,
  Check,
  KeyRound,
  Sparkles,
  Github,
  Globe,
  Database,
  Mail,
  Shield,
  Activity,
  Boxes,
  ArrowRight,
  BookOpen,
  Users,
} from "lucide-react";

export default function LoginPage() {
  const navigate = useNavigate();
  const { setAuth, isAuthenticated } = useAuthStore();

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [rememberMe, setRememberMe] = useState(false);
  const [copied, setCopied] = useState(false);
  const [version, setVersion] = useState("");
  // Email placeholder mirrors the panel's actual hostname so operators
  // see admin@<their-domain> instead of a hardcoded admin@serverpanel.io.
  const emailPlaceholder = useMemo(() => adminEmailPlaceholder(), []);
  // Owner-controlled flag — hide the demo-credentials card on
  // production deploys where leaking admin creds isn't desirable.
  const [showDemo, setShowDemo] = useState<boolean | null>(null);
  const [brandName, setBrandName] = useState("Betazen Server Panel");
  const [brandLogo, setBrandLogo] = useState("");

  useEffect(() => {
    axios
      .get("/api/v1/version")
      .then((r) => setVersion(r?.data?.data?.version ?? ""))
      .catch(() => {});
    axios
      .get("/api/v1/public-settings")
      .then((r) => setShowDemo(r?.data?.data?.show_demo_login_credentials !== false))
      .catch(() => setShowDemo(true));
    axios
      .get("/api/v1/branding")
      .then((r) => {
        const d = r?.data?.data || {};
        const name = d.panel_name || "Betazen Server Panel";
        setBrandName(name);
        setBrandLogo(d.logo_data_url || "");
        document.title = name + " — WHM Login";
        if (d.favicon_data_url) {
          let link = document.querySelector<HTMLLinkElement>("link[rel='icon']");
          if (!link) {
            link = document.createElement("link");
            link.rel = "icon";
            document.head.appendChild(link);
          }
          link.href = d.favicon_data_url;
        }
      })
      .catch(() => {});
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
      // to authenticate here gets bounced to the user-panel login —
      // their tokens are never stored client-side, so no half-session
      // is left lying around.
      const role = data?.user?.role;
      if (role !== "vendor_owner") {
        toast.error("This account belongs on the User Panel — redirecting…");
        setTimeout(() => {
          window.location.href = "/user-panel/login";
        }, 800);
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

  const features: Array<{ icon: typeof Globe; title: string; desc: string }> = [
    { icon: Globe, title: "Domains & DNS", desc: "Add domains, manage records, wildcard SSL" },
    { icon: Boxes, title: "Deploy Apps", desc: "PHP, Node, Python, Vue, Nuxt, WordPress" },
    { icon: Database, title: "Databases", desc: "MySQL, PostgreSQL, MongoDB at one click" },
    { icon: Mail, title: "Mail Server", desc: "Postfix + Dovecot + SpamAssassin, ready to go" },
    { icon: Shield, title: "Firewall & SSL", desc: "UFW rules + auto Let's Encrypt renewals" },
    { icon: Activity, title: "Live Monitoring", desc: "CPU, RAM, disk, bandwidth & service health" },
  ];

  // If already logged in, redirect declaratively. Calling useNavigate() during
  // render triggers a React warning and in some cases results in a blank page
  // because the component returns null before the navigation actually happens.
  //
  // This guard MUST sit below every hook above. It used to be the first
  // statement in the component, which meant the render right after a
  // successful login (isAuthenticated flips false -> true) returned before
  // reaching any of the ten hooks the previous render had run. React counts
  // hooks per render and treats a shrinking list as corruption, so it threw
  // minified error #300 ("Rendered fewer hooks than expected") and unmounted
  // the tree — the operator saw a blank panel right after signing in.
  if (isAuthenticated) {
    return <Navigate to="/dashboard" replace />;
  }

  return (
    <div className="relative min-h-screen overflow-hidden bg-panel-bg text-panel-text">
      <OfflineOverlay />

      {/* Scoped animation keyframes — kept inline so this page stays
          self-contained and the shared tailwind preset doesn't need to
          carry login-only animation utilities. */}
      <style>{`
        @keyframes orb-float-a { 0%,100% { transform: translate3d(0,0,0) scale(1);} 50% { transform: translate3d(40px,-30px,0) scale(1.08);} }
        @keyframes orb-float-b { 0%,100% { transform: translate3d(0,0,0) scale(1);} 50% { transform: translate3d(-50px,40px,0) scale(1.12);} }
        @keyframes orb-float-c { 0%,100% { transform: translate3d(0,0,0) scale(1);} 50% { transform: translate3d(30px,50px,0) scale(0.94);} }
        @keyframes grid-drift { 0% { background-position: 0 0;} 100% { background-position: 60px 60px;} }
        @keyframes shimmer { 0% { background-position: -200% 0;} 100% { background-position: 200% 0;} }
        @keyframes rise { from { opacity: 0; transform: translateY(12px);} to { opacity: 1; transform: translateY(0);} }
        @keyframes pulse-ring { 0% { box-shadow: 0 0 0 0 rgba(59,130,246,0.45);} 70% { box-shadow: 0 0 0 18px rgba(59,130,246,0);} 100% { box-shadow: 0 0 0 0 rgba(59,130,246,0);} }
        .orb-a { animation: orb-float-a 14s ease-in-out infinite; }
        .orb-b { animation: orb-float-b 18s ease-in-out infinite; }
        .orb-c { animation: orb-float-c 22s ease-in-out infinite; }
        .grid-bg { background-image: linear-gradient(rgba(148,163,184,0.07) 1px, transparent 1px), linear-gradient(90deg, rgba(148,163,184,0.07) 1px, transparent 1px); background-size: 60px 60px; animation: grid-drift 30s linear infinite; }
        .shimmer-text { background: linear-gradient(90deg, #93c5fd 0%, #ffffff 50%, #93c5fd 100%); background-size: 200% 100%; -webkit-background-clip: text; background-clip: text; color: transparent; animation: shimmer 6s linear infinite; }
        .rise-in { animation: rise 0.6s cubic-bezier(0.16,1,0.3,1) both; }
        .pulse-ring { animation: pulse-ring 2.6s ease-out infinite; }
        @media (prefers-reduced-motion: reduce) {
          .orb-a, .orb-b, .orb-c, .grid-bg, .shimmer-text, .rise-in, .pulse-ring { animation: none !important; }
        }
      `}</style>

      {/* Background layers */}
      <div className="pointer-events-none absolute inset-0">
        {/* Top gradient wash */}
        <div className="absolute inset-0 bg-[radial-gradient(ellipse_at_top,_rgba(37,99,235,0.18),_transparent_55%)]" />
        {/* Bottom violet wash */}
        <div className="absolute inset-0 bg-[radial-gradient(ellipse_at_bottom_right,_rgba(139,92,246,0.14),_transparent_55%)]" />
        {/* Drifting grid */}
        <div className="absolute inset-0 grid-bg opacity-60" />
        {/* Floating colour orbs */}
        <div className="orb-a absolute -top-32 -left-24 h-[420px] w-[420px] rounded-full bg-blue-600/25 blur-3xl" />
        <div className="orb-b absolute top-1/3 -right-32 h-[460px] w-[460px] rounded-full bg-indigo-500/20 blur-3xl" />
        <div className="orb-c absolute -bottom-40 left-1/3 h-[380px] w-[380px] rounded-full bg-violet-600/20 blur-3xl" />
      </div>

      <div className="relative z-10 mx-auto grid min-h-screen w-full max-w-7xl grid-cols-1 gap-8 px-4 py-8 lg:grid-cols-[1.05fr_minmax(380px,460px)] lg:gap-12 lg:px-8 lg:py-12">
        {/* LEFT: hero / open-source banner / features */}
        <aside className="hidden flex-col justify-between lg:flex">
          <div className="rise-in">
            <div className="inline-flex items-center gap-2 rounded-full border border-blue-400/30 bg-blue-500/10 px-3 py-1.5 text-xs font-medium text-blue-300 backdrop-blur">
              <Sparkles size={13} className="text-blue-300" />
              100% Open Source · Self-Hosted · No Lock-In
            </div>
            <h1 className="mt-6 text-4xl font-bold leading-tight tracking-tight text-white xl:text-5xl">
              The <span className="shimmer-text">modern WHM/cPanel</span>
              <br />
              for your own VPS.
            </h1>
            <p className="mt-4 max-w-xl break-words text-base leading-relaxed text-panel-muted">
              <span className="font-semibold text-white">{brandName}</span> is a free,
              source-available control panel that lets you run domains, apps, databases, email, DNS,
              SSL, backups and firewall from one beautiful dashboard — without monthly licenses or
              vendor lock-in.
            </p>

            <div className="mt-6 flex flex-wrap items-center gap-3">
              <a
                href="https://github.com/BetaZen-InfoTech/server-management"
                target="_blank"
                rel="noopener noreferrer"
                className="group inline-flex items-center gap-2 rounded-lg border border-panel-border bg-panel-surface/70 px-4 py-2 text-sm font-medium text-panel-text backdrop-blur transition-all hover:-translate-y-0.5 hover:border-blue-400/40 hover:bg-panel-surface"
              >
                <Github size={16} />
                Star on GitHub
                <ArrowRight size={14} className="opacity-60 transition-transform group-hover:translate-x-0.5" />
              </a>
              <a
                href="https://github.com/BetaZen-InfoTech/server-management/blob/main/FEATURES_VENDOR_WHM.md"
                target="_blank"
                rel="noopener noreferrer"
                className="group inline-flex items-center gap-2 rounded-lg border border-blue-500/30 bg-blue-600/10 px-4 py-2 text-sm font-medium text-blue-300 transition-all hover:-translate-y-0.5 hover:border-blue-400/60 hover:bg-blue-600/20"
              >
                <BookOpen size={16} />
                Feature Docs
                <ArrowRight size={14} className="opacity-60 transition-transform group-hover:translate-x-0.5" />
              </a>
            </div>
          </div>

          {/* Feature grid */}
          <div className="mt-10 grid grid-cols-2 gap-3 xl:grid-cols-3">
            {features.map((f, i) => {
              const Icon = f.icon;
              return (
                <div
                  key={f.title}
                  className="group rise-in rounded-xl border border-panel-border/70 bg-panel-surface/40 p-4 backdrop-blur transition-all hover:-translate-y-0.5 hover:border-blue-400/40 hover:bg-panel-surface/70"
                  style={{ animationDelay: `${0.08 + i * 0.06}s` }}
                >
                  <div className="mb-2 inline-flex h-9 w-9 items-center justify-center rounded-lg bg-blue-500/10 text-blue-300 ring-1 ring-blue-400/20 transition-colors group-hover:bg-blue-500/20 group-hover:text-blue-200">
                    <Icon size={18} />
                  </div>
                  <div className="text-sm font-semibold text-white">{f.title}</div>
                  <div className="mt-0.5 text-xs leading-snug text-panel-muted">{f.desc}</div>
                </div>
              );
            })}
          </div>

          <div className="mt-8 flex items-center justify-between text-xs text-panel-muted">
            <span>
              © {new Date().getFullYear()} BetaZen InfoTech — released under a source-available license.
            </span>
          </div>
        </aside>

        {/* RIGHT: login card */}
        <main className="flex items-center justify-center">
          <div className="w-full max-w-md rise-in">
            {/* Brand header */}
            <div className="mb-6 text-center lg:text-left">
              <div className="relative mx-auto inline-flex h-16 w-16 items-center justify-center overflow-hidden rounded-2xl border border-blue-400/30 bg-gradient-to-br from-blue-500/20 to-indigo-600/20 backdrop-blur lg:mx-0">
                <div className="pulse-ring absolute inset-0 rounded-2xl" aria-hidden />
                {brandLogo ? (
                  <img src={brandLogo} alt="" className="max-h-full max-w-full object-contain" />
                ) : (
                  <Server className="text-blue-300" size={30} />
                )}
              </div>
              <h2 className="mt-4 break-words text-2xl font-bold text-white">{brandName} WHM</h2>
              <p className="mt-1 text-sm text-panel-muted">Vendor &amp; Admin Control Panel</p>
            </div>

            {/* Card */}
            <div className="relative overflow-hidden rounded-2xl border border-panel-border bg-panel-surface/80 p-7 shadow-2xl backdrop-blur-xl">
              {/* Subtle top sheen */}
              <div className="pointer-events-none absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-blue-400/40 to-transparent" />

              <h3 className="text-base font-semibold text-white">Sign in to your account</h3>
              <p className="mt-0.5 text-xs text-panel-muted">Owner-only access. Other roles use the User Panel.</p>

              <form onSubmit={handleSubmit} className="mt-5 space-y-4">
                <div>
                  <label htmlFor="email" className="mb-1.5 block text-sm font-medium text-panel-muted">
                    Email Address
                  </label>
                  <input
                    id="email"
                    type="email"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    placeholder={emailPlaceholder}
                    className="w-full rounded-lg border border-panel-border bg-panel-bg/70 px-4 py-2.5 text-panel-text placeholder-panel-muted/50 transition-all focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/40"
                    autoComplete="email"
                    required
                  />
                </div>

                <div>
                  <label htmlFor="password" className="mb-1.5 block text-sm font-medium text-panel-muted">
                    Password
                  </label>
                  <div className="relative">
                    <input
                      id="password"
                      type={showPassword ? "text" : "password"}
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                      placeholder="Enter your password"
                      className="w-full rounded-lg border border-panel-border bg-panel-bg/70 px-4 py-2.5 pr-12 text-panel-text placeholder-panel-muted/50 transition-all focus:border-blue-500 focus:outline-none focus:ring-2 focus:ring-blue-500/40"
                      autoComplete="current-password"
                      required
                    />
                    <button
                      type="button"
                      onClick={() => setShowPassword(!showPassword)}
                      className="absolute right-3 top-1/2 -translate-y-1/2 text-panel-muted transition-colors hover:text-panel-text"
                      aria-label={showPassword ? "Hide password" : "Show password"}
                    >
                      {showPassword ? <EyeOff size={18} /> : <Eye size={18} />}
                    </button>
                  </div>
                </div>

                <div className="flex items-center justify-between">
                  <label className="flex cursor-pointer items-center gap-2">
                    <input
                      type="checkbox"
                      checked={rememberMe}
                      onChange={(e) => setRememberMe(e.target.checked)}
                      className="h-4 w-4 rounded border-panel-border bg-panel-bg text-blue-500 focus:ring-blue-500/40"
                    />
                    <span className="text-sm text-panel-muted">Remember me for 30 days</span>
                  </label>
                  <Link to="/forgot-password" className="text-sm text-blue-400 transition-colors hover:text-blue-300">
                    Forgot password?
                  </Link>
                </div>

                <Button
                  type="submit"
                  variant="ghost"
                  className="group relative flex w-full items-center justify-center gap-2 overflow-hidden !rounded-lg !bg-gradient-to-r !from-blue-600 !to-indigo-600 !py-2.5 !text-white !shadow-lg !shadow-blue-600/20 transition-all hover:!from-blue-500 hover:!to-indigo-500 hover:!bg-transparent hover:!shadow-blue-600/40 hover:!text-white focus:!ring-blue-500/40"
                  disabled={loading}
                >
                  {loading ? (
                    <div className="h-5 w-5 animate-spin rounded-full border-2 border-white/30 border-t-white" />
                  ) : (
                    <LogIn size={18} />
                  )}
                  {loading ? "Signing in..." : "Sign In"}
                </Button>
              </form>

              {/* Passwordless alternative */}
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
                className="flex w-full items-center justify-center gap-2 rounded-lg border border-panel-border bg-panel-bg/50 py-2.5 font-medium text-panel-text transition-all hover:border-blue-400/40 hover:bg-panel-bg"
              >
                <KeyRound size={16} />
                Sign in with an email code
              </Link>

              {/* Vendor / User Panel switch */}
              <div className="mt-4 rounded-lg border border-dashed border-violet-400/30 bg-violet-500/5 p-3">
                <div className="flex items-center gap-3">
                  <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-violet-500/15 text-violet-300 ring-1 ring-violet-400/20">
                    <Users size={16} />
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="text-sm font-medium text-white">Vendor or customer?</div>
                    <div className="truncate text-xs text-panel-muted">Sign in on the User Panel instead.</div>
                  </div>
                  <a
                    href="/user-panel/login"
                    className="inline-flex shrink-0 items-center gap-1.5 rounded-lg border border-violet-400/30 bg-violet-500/10 px-3 py-1.5 text-xs font-medium text-violet-200 transition-all hover:border-violet-300/60 hover:bg-violet-500/20"
                  >
                    Vendor Login
                    <ArrowRight size={12} />
                  </a>
                </div>
              </div>
            </div>

            {/* Demo Credentials */}
            {showDemo && (
              <div className="mt-4 rounded-xl border border-dashed border-blue-500/30 bg-panel-surface/70 p-4 backdrop-blur">
                <div className="mb-3 flex items-center justify-between">
                  <span className="text-xs font-semibold uppercase tracking-wider text-blue-400">Demo Admin Login</span>
                  <button
                    type="button"
                    onClick={handleDemoFill}
                    className="flex items-center gap-1.5 rounded-lg border border-blue-500/20 bg-blue-600/10 px-3 py-1.5 text-xs font-medium text-blue-400 transition-colors hover:border-blue-500/40 hover:bg-blue-600/20"
                  >
                    {copied ? <Check size={12} /> : <Copy size={12} />}
                    {copied ? "Filled!" : "Copy & Fill"}
                  </button>
                </div>
                <div className="space-y-2">
                  <div className="flex items-center justify-between rounded-lg bg-panel-bg px-3 py-2">
                    <span className="text-xs text-panel-muted">Email</span>
                    <span className="font-mono text-sm text-panel-text">{demoCredentials.email}</span>
                  </div>
                  <div className="flex items-center justify-between rounded-lg bg-panel-bg px-3 py-2">
                    <span className="text-xs text-panel-muted">Password</span>
                    <span className="font-mono text-sm text-panel-text">{demoCredentials.password}</span>
                  </div>
                </div>
              </div>
            )}

            {/* Mobile-only open-source banner — desktop already has the
                left hero column so we only render this on small screens. */}
            <div className="mt-4 rounded-xl border border-blue-400/20 bg-blue-500/5 p-3 text-center text-xs text-blue-200 lg:hidden">
              <Sparkles size={12} className="mr-1 inline" />
              Open source · Self-hosted ·{" "}
              <a
                href="https://github.com/BetaZen-InfoTech/server-management"
                target="_blank"
                rel="noopener noreferrer"
                className="underline decoration-blue-400/40 underline-offset-2 hover:decoration-blue-300"
              >
                GitHub
              </a>{" "}
              ·{" "}
              <a
                href="https://github.com/BetaZen-InfoTech/server-management/blob/main/FEATURES_VENDOR_WHM.md"
                target="_blank"
                rel="noopener noreferrer"
                className="underline decoration-blue-400/40 underline-offset-2 hover:decoration-blue-300"
              >
                Feature Docs
              </a>
            </div>

            <p className="mt-6 text-center text-xs text-panel-muted">
              {brandName} WHM{version ? ` v${version}` : ""} &middot; Secure admin access only
            </p>
          </div>
        </main>
      </div>
    </div>
  );
}
