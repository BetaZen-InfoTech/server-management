import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Button } from "@serverpanel/ui";
import { useAuthStore } from "@/store/auth";
import axios from "axios";
import toast from "react-hot-toast";
import { LogIn, Server, Eye, EyeOff, Copy, Check } from "lucide-react";

export default function LoginPage() {
  const navigate = useNavigate();
  const { setAuth, isAuthenticated } = useAuthStore();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [copied, setCopied] = useState(false);

  const demoCredentials = { email: "admin@betazeninfotech.com", password: "admin123" };

  const handleDemoFill = () => {
    setEmail(demoCredentials.email);
    setPassword(demoCredentials.password);
    setCopied(true);
    toast.success("Demo credentials filled");
    setTimeout(() => setCopied(false), 2000);
  };

  if (isAuthenticated) {
    navigate("/dashboard", { replace: true });
    return null;
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email || !password) {
      toast.error("Please enter both email and password");
      return;
    }

    setLoading(true);
    try {
      const res = await axios.post("/api/v1/auth/login", { email, password });
      const data = res.data.data;
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
          <h1 className="text-2xl font-bold text-panel-text">ServerPanel WHM</h1>
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
                placeholder="admin@serverpanel.io"
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
                  className="w-4 h-4 rounded border-panel-border bg-panel-bg text-blue-500 focus:ring-blue-500/40"
                />
                <span className="text-sm text-panel-muted">Remember me</span>
              </label>
              <a href="#" className="text-sm text-blue-500 hover:text-blue-400 transition-colors">
                Forgot password?
              </a>
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
        </div>

        {/* Demo Credentials */}
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

        {/* Footer */}
        <p className="text-center text-panel-muted text-xs mt-6">
          ServerPanel WHM v1.0.0 &middot; Secure admin access only
        </p>
      </div>
    </div>
  );
}
