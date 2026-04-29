import React, { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Button, emailForLocal } from "@serverpanel/ui";
import { useAuthStore } from "@/store/auth";
import { Lock, Mail, Server, Copy, Check, KeyRound } from "lucide-react";
import toast, { Toaster } from "react-hot-toast";
import axios from "axios";

export default function LoginPage() {
  const navigate = useNavigate();
  const { setAuth } = useAuthStore();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [copied, setCopied] = useState(false);
  // Mirror the panel's actual hostname into the placeholder so vendors
  // see you@<their-domain> instead of a generic example.
  const emailPlaceholder = useMemo(() => emailForLocal("you"), []);
  const [version, setVersion] = useState("");
  // Null until the public-settings fetch finishes so we never briefly flash
  // the demo card on a panel where the owner has turned it off. Defaults
  // to true on fetch-miss to preserve historic behaviour (fresh installs
  // show the credentials until the owner explicitly opts out).
  const [showDemo, setShowDemo] = useState<boolean | null>(null);
  const [brandName, setBrandName] = useState("Betazen Server Panel");
  const [brandLogo, setBrandLogo] = useState("");

  // Fetch the live panel version so the login-page header tracks every
  // deploy without us hand-editing the string on each release. Branding
  // follows the same public-fetch pattern.
  useEffect(() => {
    axios
      .get("/api/v1/version")
      .then((r) => setVersion(r?.data?.data?.version ?? ""))
      .catch(() => {});
    axios
      .get("/api/v1/public-settings")
      .then((r) => setShowDemo(r?.data?.data?.show_demo_login_credentials !== false))
      .catch(() => setShowDemo(true));
    axios.get("/api/v1/branding").then((r) => {
      const d = r?.data?.data || {};
      const name = d.panel_name || "Betazen Server Panel";
      setBrandName(name);
      setBrandLogo(d.logo_data_url || "");
      document.title = name + " — User Panel Login";
      if (d.favicon_data_url) {
        let link = document.querySelector<HTMLLinkElement>("link[rel='icon']");
        if (!link) {
          link = document.createElement("link");
          link.rel = "icon";
          document.head.appendChild(link);
        }
        link.href = d.favicon_data_url;
      }
    }).catch(() => {});
  }, []);

  const demoCredentials = { email: "demo@betazeninfotech.com", password: "demo123" };

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
      toast.error("Please fill in all fields");
      return;
    }
    setLoading(true);
    try {
      const res = await axios.post("/api/v1/auth/login", { email, password });
      const data = res.data.data;
      // Symmetric block: the User Panel is for vendors / staff /
      // customers. The platform owner (vendor_owner) belongs in WHM.
      // We never persist their session on this side — bouncing them
      // straight to /whm/login keeps the two surfaces cleanly
      // separated and matches what main.go's root redirect does.
      const role = data?.user?.role;
      if (role === "vendor_owner") {
        toast.error("Platform owner accounts use WHM — redirecting…");
        setTimeout(() => { window.location.href = "/whm/login"; }, 800);
        return;
      }
      setAuth(data.user, data.access_token, data.refresh_token);
      toast.success("Welcome back!");
      navigate("/dashboard");
    } catch (err: any) {
      const msg = err.response?.data?.error?.message || "Invalid credentials";
      toast.error(msg);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen bg-panel-bg flex items-center justify-center px-4">
      <Toaster
        position="top-right"
        toastOptions={{
          style: {
            background: "#1e1e2e",
            color: "#cdd6f4",
            border: "1px solid #313244",
          },
        }}
      />
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center w-16 h-16 bg-brand-600/10 rounded-2xl mb-4 overflow-hidden">
            {brandLogo ? (
              <img src={brandLogo} alt="" className="max-w-full max-h-full object-contain" />
            ) : (
              <Server className="text-brand-400" size={32} />
            )}
          </div>
          <h1 className="text-2xl font-bold text-white">{brandName}{version ? ` v${version}` : ""}</h1>
          <p className="text-panel-muted mt-2">Sign in to your control panel</p>
        </div>

        <div className="bg-panel-surface border border-panel-border rounded-xl p-6">
          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">
                Email Address
              </label>
              <div className="relative">
                <Mail
                  size={18}
                  className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted"
                />
                <input
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder={emailPlaceholder}
                  className="w-full pl-10 pr-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent text-sm"
                />
              </div>
            </div>

            <div>
              <label className="block text-sm font-medium text-panel-text mb-1.5">
                Password
              </label>
              <div className="relative">
                <Lock
                  size={18}
                  className="absolute left-3 top-1/2 -translate-y-1/2 text-panel-muted"
                />
                <input
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="Enter your password"
                  className="w-full pl-10 pr-4 py-2.5 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder:text-panel-muted focus:outline-none focus:ring-2 focus:ring-brand-500 focus:border-transparent text-sm"
                />
              </div>
            </div>

            <div className="flex items-center justify-between text-sm">
              <label className="flex items-center gap-2 text-panel-muted">
                <input
                  type="checkbox"
                  className="rounded border-panel-border bg-panel-bg text-brand-600 focus:ring-brand-500"
                />
                Remember me
              </label>
              <Link to="/forgot-password" className="text-brand-400 hover:text-brand-300">
                Forgot password?
              </Link>
            </div>

            <Button
              type="submit"
              loading={loading}
              className="w-full"
              size="lg"
            >
              Sign In
            </Button>
          </form>

          {/* Passwordless alternative — lands on /otp which handles both
              "enter email → email me a code" and "verify code". */}
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
          <div className="mt-4 bg-panel-surface border border-dashed border-brand-500/30 rounded-xl p-4">
            <div className="flex items-center justify-between mb-3">
              <span className="text-xs font-semibold uppercase tracking-wider text-brand-400">Demo Login</span>
              <button
                type="button"
                onClick={handleDemoFill}
                className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium rounded-lg bg-brand-600/10 border border-brand-500/20 text-brand-400 hover:bg-brand-600/20 hover:border-brand-500/40 transition-colors"
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
