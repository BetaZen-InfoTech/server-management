import React, { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Button } from "@serverpanel/ui";
import { useAuthStore } from "@/store/auth";
import { Lock, Mail, Server, Copy, Check } from "lucide-react";
import toast, { Toaster } from "react-hot-toast";
import axios from "axios";

export default function LoginPage() {
  const navigate = useNavigate();
  const { setAuth } = useAuthStore();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [copied, setCopied] = useState(false);

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
      const { user, accessToken, refreshToken } = res.data.data;
      setAuth(user, accessToken, refreshToken);
      toast.success("Welcome back!");
      navigate("/dashboard");
    } catch (err: any) {
      const msg = err.response?.data?.message || "Invalid credentials";
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
          <div className="inline-flex items-center justify-center w-16 h-16 bg-brand-600/10 rounded-2xl mb-4">
            <Server className="text-brand-400" size={32} />
          </div>
          <h1 className="text-2xl font-bold text-white">ServerPanel</h1>
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
                  placeholder="you@example.com"
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
              <a href="#" className="text-brand-400 hover:text-brand-300">
                Forgot password?
              </a>
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
        </div>

        {/* Demo Credentials */}
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
