import { useEffect, useState } from "react";
import { Card, Button } from "@serverpanel/ui";
import { apiClient } from "@serverpanel/api-client";
import { useAuthStore } from "@/store/auth";
import toast from "react-hot-toast";
import { User, Mail, KeyRound, ShieldCheck, Save, Eye, EyeOff } from "lucide-react";

const inputClass =
  "w-full px-3 py-2 bg-panel-bg border border-panel-border rounded-lg text-panel-text placeholder-panel-muted/50 focus:outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-colors text-sm";
const labelClass = "block text-sm font-medium text-panel-text mb-1";

export default function ProfilePage() {
  const { user, setAuth, accessToken, refreshToken } = useAuthStore();

  const [name, setName] = useState(user?.name ?? "");
  const [email, setEmail] = useState(user?.email ?? "");
  const [username, setUsername] = useState(user?.username ?? "");
  const [loadingProfile, setLoadingProfile] = useState(true);
  const [savingProfile, setSavingProfile] = useState(false);

  const [currentPwd, setCurrentPwd] = useState("");
  const [newPwd, setNewPwd] = useState("");
  const [confirmPwd, setConfirmPwd] = useState("");
  const [showCurrent, setShowCurrent] = useState(false);
  const [showNew, setShowNew] = useState(false);
  const [savingPwd, setSavingPwd] = useState(false);

  useEffect(() => {
    apiClient
      .get("/api/v1/auth/me")
      .then((res) => {
        const d = res.data?.data || {};
        setName(d.name ?? "");
        setEmail(d.email ?? "");
        setUsername(d.username ?? "");
      })
      .catch(() => {})
      .finally(() => setLoadingProfile(false));
  }, []);

  const saveProfile = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim() || !email.trim()) {
      toast.error("Name and email are required");
      return;
    }
    setSavingProfile(true);
    try {
      const res = await apiClient.patch("/api/v1/auth/me", { name, email });
      const d = res.data?.data || {};
      if (user && accessToken && refreshToken) {
        setAuth(
          { ...user, name: d.name ?? name, email: d.email ?? email },
          accessToken,
          refreshToken,
        );
      }
      toast.success("Profile updated");
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update profile");
    } finally {
      setSavingProfile(false);
    }
  };

  const changePassword = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!currentPwd || !newPwd) {
      toast.error("Current and new password are required");
      return;
    }
    if (newPwd.length < 8) {
      toast.error("New password must be at least 8 characters");
      return;
    }
    if (newPwd !== confirmPwd) {
      toast.error("Passwords do not match");
      return;
    }
    setSavingPwd(true);
    try {
      await apiClient.post("/api/v1/auth/me/password", {
        current_password: currentPwd,
        new_password: newPwd,
      });
      toast.success("Password updated — other active sessions have been signed out");
      setCurrentPwd("");
      setNewPwd("");
      setConfirmPwd("");
    } catch (err: any) {
      toast.error(err?.response?.data?.error?.message || "Failed to update password");
    } finally {
      setSavingPwd(false);
    }
  };

  return (
    <div className="max-w-3xl space-y-6">
      <div>
        <h1 className="text-xl font-bold text-panel-text">My Profile</h1>
        <p className="text-panel-muted text-sm mt-1">
          Manage your login identity and password.
        </p>
      </div>

      <Card>
        <form onSubmit={saveProfile} className="p-6 space-y-4">
          <div className="flex items-center gap-2 text-panel-text">
            <User size={16} className="text-blue-400" />
            <h2 className="text-sm font-semibold uppercase tracking-wider">Account details</h2>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>Full name</label>
              <input
                type="text"
                value={name}
                disabled={loadingProfile}
                onChange={(e) => setName(e.target.value)}
                className={inputClass}
                placeholder="Your name"
              />
            </div>
            <div>
              <label className={labelClass}>Username</label>
              <input
                type="text"
                value={username}
                disabled
                className={inputClass + " opacity-60 cursor-not-allowed"}
              />
              <p className="text-xs text-panel-muted mt-1">
                Linux username — fixed.
              </p>
            </div>
          </div>

          <div>
            <label className={labelClass}>
              <span className="inline-flex items-center gap-1.5">
                <Mail size={12} /> Email
              </span>
            </label>
            <input
              type="email"
              value={email}
              disabled={loadingProfile}
              onChange={(e) => setEmail(e.target.value)}
              className={inputClass}
              placeholder="you@example.com"
            />
            <p className="text-xs text-panel-muted mt-1">
              Used for sign-in and password recovery.
            </p>
          </div>

          <div className="flex justify-end pt-2">
            <Button
              type="submit"
              disabled={savingProfile || loadingProfile}
              className="inline-flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium"
            >
              <Save size={14} />
              {savingProfile ? "Saving..." : "Save changes"}
            </Button>
          </div>
        </form>
      </Card>

      <Card>
        <form onSubmit={changePassword} className="p-6 space-y-4">
          <div className="flex items-center gap-2 text-panel-text">
            <KeyRound size={16} className="text-purple-400" />
            <h2 className="text-sm font-semibold uppercase tracking-wider">Change password</h2>
          </div>

          <div>
            <label className={labelClass}>Current password</label>
            <div className="relative">
              <input
                type={showCurrent ? "text" : "password"}
                value={currentPwd}
                onChange={(e) => setCurrentPwd(e.target.value)}
                className={inputClass + " pr-10"}
                autoComplete="current-password"
              />
              <button
                type="button"
                onClick={() => setShowCurrent(!showCurrent)}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-panel-muted hover:text-panel-text"
                tabIndex={-1}
              >
                {showCurrent ? <EyeOff size={16} /> : <Eye size={16} />}
              </button>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className={labelClass}>New password</label>
              <div className="relative">
                <input
                  type={showNew ? "text" : "password"}
                  value={newPwd}
                  onChange={(e) => setNewPwd(e.target.value)}
                  className={inputClass + " pr-10"}
                  autoComplete="new-password"
                  minLength={8}
                />
                <button
                  type="button"
                  onClick={() => setShowNew(!showNew)}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-panel-muted hover:text-panel-text"
                  tabIndex={-1}
                >
                  {showNew ? <EyeOff size={16} /> : <Eye size={16} />}
                </button>
              </div>
              <p className="text-xs text-panel-muted mt-1">At least 8 characters.</p>
            </div>
            <div>
              <label className={labelClass}>Confirm new password</label>
              <input
                type={showNew ? "text" : "password"}
                value={confirmPwd}
                onChange={(e) => setConfirmPwd(e.target.value)}
                className={inputClass}
                autoComplete="new-password"
                minLength={8}
              />
            </div>
          </div>

          <div className="flex items-start gap-2 p-3 rounded-lg bg-amber-500/5 border border-amber-500/20 text-xs text-panel-muted">
            <ShieldCheck size={14} className="text-amber-400 mt-0.5 shrink-0" />
            <div>
              Changing your password signs out every other active session. Your SSH / FTP password
              rotates automatically.
            </div>
          </div>

          <div className="flex justify-end pt-2">
            <Button
              type="submit"
              disabled={savingPwd}
              className="inline-flex items-center gap-2 px-4 py-2 bg-purple-600 hover:bg-purple-700 text-white rounded-lg text-sm font-medium"
            >
              <KeyRound size={14} />
              {savingPwd ? "Updating..." : "Update password"}
            </Button>
          </div>
        </form>
      </Card>
    </div>
  );
}
