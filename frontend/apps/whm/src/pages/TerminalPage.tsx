import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import {
  RefreshCw, User, ChevronDown, Copy, Trash2, Maximize2, Minimize2,
  Shield, Home, FolderTree, Globe, KeyRound,
  Minus, Plus, Keyboard, Command, Download, Zap,
} from "lucide-react";
import { Card, Modal, copyToClipboard } from "@serverpanel/ui";
import toast from "react-hot-toast";
import { useAuthStore } from "@/store/auth";
import api from "@/lib/api";

// COMMAND_PRESETS groups one-liner admin commands the operator runs
// daily. The palette drops them into the active shell as if typed —
// they still have to press Enter, so a misfire can be edited before
// execution. Keys are human-readable labels; values are the literal
// command string (no trailing newline).
const COMMAND_PRESETS: { group: string; items: { label: string; cmd: string; desc?: string }[] }[] = [
  {
    group: "System",
    items: [
      { label: "df -h", cmd: "df -h", desc: "Disk usage per mount" },
      { label: "free -h", cmd: "free -h", desc: "Memory usage" },
      { label: "uptime", cmd: "uptime", desc: "Load average + uptime" },
      { label: "top (htop)", cmd: "htop", desc: "Interactive process viewer" },
      { label: "journalctl --since '10 min ago'", cmd: "journalctl --since '10 min ago' --no-pager | tail -200", desc: "Recent kernel/systemd logs" },
    ],
  },
  {
    group: "ServerPanel",
    items: [
      { label: "systemctl status serverpanel", cmd: "systemctl status serverpanel --no-pager -n 20" },
      { label: "Restart serverpanel", cmd: "systemctl restart serverpanel" },
      { label: "Tail serverpanel logs", cmd: "journalctl -u serverpanel -f" },
      { label: "PM2 list", cmd: "pm2 list" },
      { label: "nginx -t", cmd: "nginx -t && systemctl reload nginx", desc: "Validate + reload nginx" },
    ],
  },
  {
    group: "Networking",
    items: [
      { label: "ss -tlnp", cmd: "ss -tlnp", desc: "Listening TCP sockets + process" },
      { label: "iptables -L", cmd: "iptables -L -n --line-numbers | head -50" },
      { label: "curl localhost:8080/api/v1/health", cmd: "curl -s http://127.0.0.1:8080/api/v1/health && echo" },
    ],
  },
  {
    group: "Files",
    items: [
      { label: "ls -lah", cmd: "ls -lah" },
      { label: "du -sh * | sort -h", cmd: "du -sh * 2>/dev/null | sort -h | tail -20", desc: "Largest items in cwd" },
      { label: "find . -mtime -1", cmd: "find . -mtime -1 -type f 2>/dev/null | head -30", desc: "Files modified in last 24h" },
    ],
  },
];

interface SystemUser {
  id: string;
  username: string;
  name: string;
  email: string;
  role: string;
  status: string;
}

export default function TerminalPage() {
  const termRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const [connected, setConnected] = useState(false);
  const [users, setUsers] = useState<SystemUser[]>([]);
  // Only the platform owner is allowed to pick a shell user — they get
  // a root shell by default. Everyone else is hard-pinned to their own
  // linux account; the backend enforces the same rule via JWT claims.
  //
  // The canonical role string is "vendor_owner" (matches backend +
  // packages/types). "admin" is kept here for legacy seeds that may
  // still be carrying the old role value.
  const currentUser = useAuthStore((s) => s.user);
  const isOwner = currentUser?.role === "vendor_owner" || currentUser?.role === "admin";
  const ownUsername = currentUser?.username || currentUser?.email?.split("@")[0] || "";
  const [selectedUser, setSelectedUser] = useState<string>(isOwner ? "root" : ownUsername);
  const [dropdownOpen, setDropdownOpen] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const [sessionStart, setSessionStart] = useState<number | null>(null);
  const [uptime, setUptime] = useState("00:00");
  // Persist the font size across reloads — operators who bump it once
  // for a demo or readability preference shouldn't have to do it every
  // time they open the terminal.
  const [fontSize, setFontSize] = useState<number>(() => {
    const v = Number(localStorage.getItem("sp-term-fontsize"));
    return Number.isFinite(v) && v >= 10 && v <= 24 ? v : 14;
  });
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);

  const fetchUsers = async () => {
    // Only the platform owner can switch between users — so don't even
    // bother fetching the user list for tenant-scoped roles (they'd get a
    // filtered view anyway).
    if (!isOwner) return;
    try {
      // Merge platform staff (/users) and tenant-root vendors
      // (/admin/vendors). /users uses strict-tenant scoping so it
      // hides vendor_admin accounts — without the merge the owner
      // couldn't SSH as any vendor, only as their own staff.
      const [staffRes, vendorRes] = await Promise.all([
        api.get("/users?limit=200").catch(() => null),
        api.get("/admin/vendors?limit=500").catch(() => null),
      ]);
      const staff = (staffRes?.data?.data || []) as SystemUser[];
      const vendorRows = (vendorRes?.data?.data || []) as Array<{ id: string; username: string; name: string; status?: string }>;
      const vendors: SystemUser[] = vendorRows
        .filter((r) => r.username)
        .map((r) => ({
          id: r.id,
          username: r.username,
          name: r.name,
          email: "",
          role: "vendor",
          status: r.status ?? "active",
        }));
      const seen = new Set<string>();
      const merged: SystemUser[] = [];
      for (const u of [...vendors, ...staff]) {
        if (!u.username || seen.has(u.username)) continue;
        seen.add(u.username);
        merged.push(u);
      }
      setUsers(merged);
    } catch {
      // keep empty
    }
  };

  useEffect(() => {
    fetchUsers();
  }, []);

  const connectTerminal = (user?: string) => {
    const connectAs = user ?? selectedUser;

    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    if (terminalRef.current) {
      terminalRef.current.dispose();
      terminalRef.current = null;
    }

    const token = useAuthStore.getState().accessToken || localStorage.getItem("access_token");
    if (!token) return;

    const term = new Terminal({
      cursorBlink: true,
      fontSize,
      fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', Menlo, monospace",
      theme: {
        background: "#1e1e2e",
        foreground: "#cdd6f4",
        cursor: "#f5e0dc",
        selectionBackground: "#585b7066",
        black: "#45475a",
        red: "#f38ba8",
        green: "#a6e3a1",
        yellow: "#f9e2af",
        blue: "#89b4fa",
        magenta: "#f5c2e7",
        cyan: "#94e2d5",
        white: "#bac2de",
        brightBlack: "#585b70",
        brightRed: "#f38ba8",
        brightGreen: "#a6e3a1",
        brightYellow: "#f9e2af",
        brightBlue: "#89b4fa",
        brightMagenta: "#f5c2e7",
        brightCyan: "#94e2d5",
        brightWhite: "#a6adc8",
      },
      allowProposedApi: true,
    });

    const fitAddon = new FitAddon();
    const webLinksAddon = new WebLinksAddon();
    term.loadAddon(fitAddon);
    term.loadAddon(webLinksAddon);

    terminalRef.current = term;
    fitAddonRef.current = fitAddon;

    if (termRef.current) {
      term.open(termRef.current);
      fitAddon.fit();
    }

    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    const wsUrl = `${proto}//${window.location.host}/ws/terminal?token=${encodeURIComponent(token)}&user=${encodeURIComponent(connectAs)}`;
    const ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    ws.onopen = () => {
      setConnected(true);
      setSessionStart(Date.now());
      const resizePayload = JSON.stringify({ cols: term.cols, rows: term.rows });
      const buf = new Uint8Array(1 + resizePayload.length);
      buf[0] = 1;
      for (let i = 0; i < resizePayload.length; i++) buf[i + 1] = resizePayload.charCodeAt(i);
      ws.send(buf);
    };

    ws.onmessage = (event) => {
      if (event.data instanceof ArrayBuffer) {
        term.write(new Uint8Array(event.data));
      } else {
        term.write(event.data);
      }
    };

    ws.onclose = () => {
      setConnected(false);
      setSessionStart(null);
      term.write("\r\n\x1b[33mConnection closed.\x1b[0m\r\n");
    };

    ws.onerror = () => {
      setConnected(false);
      setSessionStart(null);
      term.write("\r\n\x1b[31mConnection error.\x1b[0m\r\n");
    };

    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        const buf = new Uint8Array(1 + data.length);
        buf[0] = 0;
        for (let i = 0; i < data.length; i++) buf[i + 1] = data.charCodeAt(i);
        ws.send(buf);
      }
    });

    term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) {
        const resizePayload = JSON.stringify({ cols, rows });
        const buf = new Uint8Array(1 + resizePayload.length);
        buf[0] = 1;
        for (let i = 0; i < resizePayload.length; i++) buf[i + 1] = resizePayload.charCodeAt(i);
        ws.send(buf);
      }
    });
  };

  const handleUserSelect = (username: string) => {
    setSelectedUser(username);
    setDropdownOpen(false);
    connectTerminal(username);
  };

  // sendToTerminal injects text straight into the PTY as if the operator
  // typed it — used by the quick-nav buttons below to cd into common
  // directories. Wrapping in a helper keeps the button handlers tiny and
  // means the keystroke protocol (byte 0 = stdin) lives in one place.
  const sendToTerminal = (text: string) => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      toast.error("Terminal not connected");
      return;
    }
    const buf = new Uint8Array(1 + text.length);
    buf[0] = 0;
    for (let i = 0; i < text.length; i++) buf[i + 1] = text.charCodeAt(i);
    ws.send(buf);
    terminalRef.current?.focus();
  };

  // becomeRoot is the one-click "I want a root shell" action for
  // platform owners. Reconnects the WS with user=root so the backend
  // spawns /bin/bash --login at /root, bypassing the dropdown entirely.
  // Tenant-scoped roles never see this button — the backend would
  // reject the request anyway.
  const becomeRoot = () => {
    if (!isOwner) {
      toast.error("Root shell is restricted to the platform owner.");
      return;
    }
    handleUserSelect("root");
  };

  // Quick cd shortcuts. They write literal `cd ~/apps\n` to the PTY,
  // which means they flow through whatever shell is active — works
  // identically whether the session is root or a tenant user. The
  // trailing \r is what an Enter keypress sends; without it the
  // command just sits on the prompt unexecuted.
  const goHome = () => sendToTerminal("cd ~\r");
  const goApps = () => sendToTerminal("cd ~/apps\r");
  const goDomains = () => sendToTerminal("cd ~/domains\r");

  // Adjust the terminal font size + persist. Clamp to a legible range
  // so operators can't make it unreadable either direction. Re-fitting
  // after the size change keeps the col/row count in sync with the
  // pane's pixel dimensions.
  const adjustFont = (delta: number) => {
    setFontSize((prev) => {
      const next = Math.max(10, Math.min(24, prev + delta));
      localStorage.setItem("sp-term-fontsize", String(next));
      if (terminalRef.current) {
        terminalRef.current.options.fontSize = next;
        // Fit addon doesn't automatically recalculate after font change —
        // bump it so the row count updates to match the new cell size.
        requestAnimationFrame(() => fitAddonRef.current?.fit());
      }
      return next;
    });
  };

  // insertCommand drops a preset command into the shell's input buffer
  // WITHOUT pressing Enter — so the operator can edit before running.
  // Closes the palette afterwards. The palette UI shows the raw command
  // so there's no surprise about what gets typed.
  const insertCommand = (cmd: string) => {
    sendToTerminal(cmd);
    setPaletteOpen(false);
    toast.success("Inserted — press Enter to run, or edit first");
  };

  // saveSession collects every line currently in the xterm scrollback
  // buffer (visible + off-screen) and offers it as a .txt download.
  // Matches what an operator would get from "copy all + paste into a
  // file" but without the manual steps. No ANSI is stored because
  // xterm's buffer model exposes the post-render text, not raw bytes.
  const saveSession = () => {
    const term = terminalRef.current;
    if (!term) {
      toast.error("Terminal not ready");
      return;
    }
    const buf = term.buffer.active;
    const lines: string[] = [];
    for (let i = 0; i < buf.length; i++) {
      const line = buf.getLine(i);
      if (!line) continue;
      lines.push(line.translateToString(true));
    }
    // Trim trailing empties so the file doesn't end with 500 blank lines.
    while (lines.length && lines[lines.length - 1] === "") lines.pop();
    const content = lines.join("\n") + "\n";
    const blob = new Blob([content], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    const ts = new Date().toISOString().replace(/[:.]/g, "-").slice(0, 19);
    a.href = url;
    a.download = `terminal-${selectedUser}-${ts}.txt`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
    toast.success("Session saved");
  };

  const copySelection = async () => {
    const term = terminalRef.current;
    if (!term) return;
    const text = term.getSelection();
    if (!text) {
      toast.error("Nothing selected");
      return;
    }
    if (await copyToClipboard(text)) toast.success("Copied to clipboard");
    else toast.error("Copy failed");
  };

  const clearTerminal = () => {
    terminalRef.current?.clear();
  };

  useEffect(() => {
    connectTerminal();

    const handleResize = () => {
      if (fitAddonRef.current) {
        fitAddonRef.current.fit();
      }
    };
    window.addEventListener("resize", handleResize);

    return () => {
      window.removeEventListener("resize", handleResize);
      if (wsRef.current) wsRef.current.close();
      if (terminalRef.current) terminalRef.current.dispose();
    };
  }, []);

  useEffect(() => {
    if (fitAddonRef.current) {
      setTimeout(() => fitAddonRef.current?.fit(), 150);
    }
  }, [fullscreen]);

  // F11 toggles fullscreen without having to click the button. Registered
  // at window level so the shortcut works even when the terminal has
  // focus (xterm otherwise eats every keystroke). Capture phase so
  // xterm doesn't get to cancel it first.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "F11") {
        e.preventDefault();
        setFullscreen((f) => !f);
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, []);

  useEffect(() => {
    if (!sessionStart) {
      setUptime("00:00");
      return;
    }
    const tick = () => {
      const s = Math.floor((Date.now() - sessionStart) / 1000);
      const hh = String(Math.floor(s / 3600)).padStart(2, "0");
      const mm = String(Math.floor((s % 3600) / 60)).padStart(2, "0");
      const ss = String(s % 60).padStart(2, "0");
      setUptime(s >= 3600 ? `${hh}:${mm}:${ss}` : `${mm}:${ss}`);
    };
    tick();
    const id = setInterval(tick, 1000);
    return () => clearInterval(id);
  }, [sessionStart]);

  const currentLabel = isOwner && selectedUser === "root"
    ? "root (Server)"
    : users.find((u) => u.username === selectedUser)?.name || selectedUser || "—";

  return (
    <div className={`flex flex-col ${fullscreen ? "fixed inset-0 z-50 bg-panel-bg p-2" : "h-full"}`}>
      {/*
        Header block is removed in the upgraded UI — the terminal's own
        window chrome already carries identity (`root@serverpanel`),
        status (connection dot), and all controls. Dropping the page
        title gives the shell ~80px more of vertical space, which is
        what operators actually care about.

        For tenant users the sandbox note appears as a slim amber
        ribbon above the terminal so they know the boundary exists
        without wasting a full header row on it.
      */}
      {!fullscreen && !isOwner && (
        <div className="flex items-start gap-2 px-3 py-2 mb-2 rounded-lg border border-amber-500/30 bg-amber-500/5 text-xs text-amber-200">
          <KeyRound size={12} className="mt-0.5 shrink-0" />
          <span>
            Shell sandboxed to <code className="font-mono text-amber-100">{`~ (/home/${ownUsername})`}</code> — <code className="font-mono">cd /</code> and paths outside your home are blocked. Contact the platform owner for broader access.
          </span>
        </div>
      )}

      <Card className="flex-1 p-0 overflow-hidden border border-panel-border bg-[#1e1e2e] rounded-xl shadow-2xl flex flex-col">
        {/* Window chrome / toolbar */}
        <div className="flex items-center justify-between px-4 py-2.5 bg-gradient-to-b from-[#2a2a3e] to-[#1e1e2e] border-b border-panel-border">
          <div className="flex items-center gap-3">
            <div className="flex items-center gap-1.5">
              <span className="w-3 h-3 rounded-full bg-[#ff5f57] border border-[#e0443e]" />
              <span className="w-3 h-3 rounded-full bg-[#febc2e] border border-[#dea123]" />
              <span className="w-3 h-3 rounded-full bg-[#28c840] border border-[#1aab29]" />
            </div>
            <div className="h-4 w-px bg-panel-border mx-1" />
            <div className="flex items-center gap-1.5 text-xs text-panel-muted font-mono">
              <span className={`w-1.5 h-1.5 rounded-full ${connected ? "bg-green-400 animate-pulse" : "bg-red-400"}`} />
              <span className="text-panel-text">{selectedUser}@serverpanel</span>
              <span className="text-panel-muted">:~</span>
            </div>
          </div>

          <div className="flex items-center gap-1.5">
            {/* User selector — owner-only. Tenant-scoped roles see a
                read-only badge showing their own pinned linux user. */}
            {isOwner ? (
              <div className="relative">
                <button
                  onClick={() => setDropdownOpen(!dropdownOpen)}
                  className="flex items-center gap-2 px-2.5 py-1.5 bg-[#313244] text-panel-text border border-panel-border/60 rounded-md text-xs font-medium hover:bg-[#3b3b52] hover:border-blue-500/40 transition-all"
                >
                  <User size={12} />
                  <span className="max-w-[140px] truncate">{currentLabel}</span>
                  <ChevronDown size={12} className={`transition-transform ${dropdownOpen ? "rotate-180" : ""}`} />
                </button>
                {dropdownOpen && (
                  <>
                    <div className="fixed inset-0 z-10" onClick={() => setDropdownOpen(false)} />
                    <div className="absolute right-0 top-full mt-1 z-20 w-64 bg-[#1e1e2e] border border-panel-border rounded-lg shadow-2xl max-h-72 overflow-y-auto backdrop-blur">
                      <div className="px-3 py-2 text-[10px] uppercase tracking-wider text-panel-muted border-b border-panel-border font-semibold">
                        Switch user
                      </div>
                      <button
                        onClick={() => handleUserSelect("root")}
                        className={`w-full text-left px-3 py-2.5 text-sm hover:bg-[#313244] transition-colors flex items-center gap-2.5 ${selectedUser === "root" ? "bg-[#313244] text-blue-400" : "text-panel-text"}`}
                      >
                        <span className="w-2 h-2 rounded-full bg-red-400 shadow-lg shadow-red-400/50" />
                        <span className="font-medium">root</span>
                        <span className="text-panel-muted text-xs ml-auto">Server owner</span>
                      </button>
                      {users.filter((u) => u.status === "active").map((u) => (
                        <button
                          key={u.id}
                          onClick={() => handleUserSelect(u.username)}
                          className={`w-full text-left px-3 py-2.5 text-sm hover:bg-[#313244] transition-colors flex items-center gap-2.5 ${selectedUser === u.username ? "bg-[#313244] text-blue-400" : "text-panel-text"}`}
                        >
                          <span className="w-2 h-2 rounded-full bg-green-400 shadow-lg shadow-green-400/50" />
                          <span className="font-medium truncate">{u.username}</span>
                          <span className="text-panel-muted text-xs ml-auto truncate">{u.name}</span>
                        </button>
                      ))}
                      {users.filter((u) => u.status === "active").length === 0 && (
                        <div className="px-3 py-4 text-xs text-panel-muted text-center">No additional users</div>
                      )}
                    </div>
                  </>
                )}
              </div>
            ) : (
              <div
                title="Tenant-scoped roles always get their own linux account. Only the platform owner can switch users."
                className="flex items-center gap-2 px-2.5 py-1.5 bg-[#313244] text-panel-muted border border-panel-border/60 rounded-md text-xs font-medium cursor-default select-none"
              >
                <User size={12} />
                <span className="max-w-[140px] truncate">{ownUsername || "—"}</span>
              </div>
            )}

            <div className="h-4 w-px bg-panel-border mx-1" />

            {/* Quick-jump to $HOME. Safe for every role — the jail
                allows cd ~ regardless. Works by writing literal
                `cd ~<Enter>` into the PTY so the active shell executes
                it, independent of whether it's an interactive bash or
                the sandboxed jail-bash. */}
            <button
              onClick={goHome}
              title="Go to home directory (cd ~)"
              className="p-1.5 text-panel-muted hover:text-panel-text hover:bg-[#313244] rounded-md transition-colors"
            >
              <Home size={14} />
            </button>
            {isOwner && (
              <>
                {/* Convenience shortcuts only owners need — tenants don't
                    manage the per-user apps or domains dirs directly. */}
                <button
                  onClick={goApps}
                  title="Go to ~/apps"
                  className="p-1.5 text-panel-muted hover:text-panel-text hover:bg-[#313244] rounded-md transition-colors"
                >
                  <FolderTree size={14} />
                </button>
                <button
                  onClick={goDomains}
                  title="Go to ~/domains"
                  className="p-1.5 text-panel-muted hover:text-panel-text hover:bg-[#313244] rounded-md transition-colors"
                >
                  <Globe size={14} />
                </button>
                {/* Become root — one-click reconnect as root shell. Only
                    visible to platform owner; backend still enforces the
                    same role check, so a client-side DOM tweak can't
                    escalate. The button dims and shows a different
                    label when already root to confirm the current
                    privilege tier. */}
                <button
                  onClick={becomeRoot}
                  title={selectedUser === "root" ? "You're already root" : "Open a fresh root shell"}
                  disabled={selectedUser === "root"}
                  className={`flex items-center gap-1.5 px-2.5 py-1.5 rounded-md text-xs font-medium transition-all ${
                    selectedUser === "root"
                      ? "bg-red-500/10 border border-red-500/30 text-red-300 cursor-default"
                      : "bg-gradient-to-r from-red-500/80 to-red-600/80 hover:from-red-500 hover:to-red-600 text-white border border-red-500/40"
                  }`}
                >
                  <Shield size={12} />
                  <span>{selectedUser === "root" ? "ROOT" : "Become root"}</span>
                </button>
              </>
            )}

            <div className="h-4 w-px bg-panel-border mx-1" />

            {/* Command palette — dropdown of one-liner admin commands.
                Clicking inserts the command into the prompt without
                auto-running, so the operator can edit before Enter.
                Grouped by category so it doesn't become an alphabet-
                soup list as we add more. */}
            <div className="relative">
              <button
                onClick={() => setPaletteOpen(!paletteOpen)}
                title="Command palette — insert a common command"
                className={`p-1.5 rounded-md transition-colors ${
                  paletteOpen
                    ? "bg-[#313244] text-blue-300"
                    : "text-panel-muted hover:text-panel-text hover:bg-[#313244]"
                }`}
              >
                <Zap size={14} />
              </button>
              {paletteOpen && (
                <>
                  <div className="fixed inset-0 z-10" onClick={() => setPaletteOpen(false)} />
                  <div className="absolute right-0 top-full mt-1 z-20 w-80 bg-[#1e1e2e] border border-panel-border rounded-lg shadow-2xl max-h-[28rem] overflow-y-auto">
                    <div className="px-3 py-2 text-[10px] uppercase tracking-wider text-panel-muted border-b border-panel-border font-semibold flex items-center gap-1.5">
                      <Command size={11} /> Quick commands
                    </div>
                    {COMMAND_PRESETS.map((group) => (
                      <div key={group.group} className="py-1">
                        <div className="px-3 pt-2 pb-1 text-[10px] uppercase tracking-wider text-panel-muted/70">
                          {group.group}
                        </div>
                        {group.items.map((it) => (
                          <button
                            key={it.label}
                            onClick={() => insertCommand(it.cmd)}
                            className="w-full text-left px-3 py-2 hover:bg-[#313244] transition-colors text-xs"
                          >
                            <div className="font-mono text-panel-text truncate">{it.label}</div>
                            {it.desc && (
                              <div className="text-[10px] text-panel-muted/70 mt-0.5 truncate">{it.desc}</div>
                            )}
                          </button>
                        ))}
                      </div>
                    ))}
                  </div>
                </>
              )}
            </div>

            {/* Font size controls. The target pixel size sits in the
                middle so it reads as a single compound control even
                though it's three DOM elements. */}
            <div className="flex items-center rounded-md border border-panel-border/60 bg-[#181825]">
              <button
                onClick={() => adjustFont(-1)}
                title="Decrease font size"
                className="px-1.5 py-1 text-panel-muted hover:text-panel-text disabled:opacity-30"
                disabled={fontSize <= 10}
              >
                <Minus size={12} />
              </button>
              <span className="px-1.5 text-[10px] font-mono text-panel-muted/70 select-none min-w-[1.75rem] text-center">
                {fontSize}
              </span>
              <button
                onClick={() => adjustFont(1)}
                title="Increase font size"
                className="px-1.5 py-1 text-panel-muted hover:text-panel-text disabled:opacity-30"
                disabled={fontSize >= 24}
              >
                <Plus size={12} />
              </button>
            </div>

            <div className="h-4 w-px bg-panel-border mx-1" />

            <button
              onClick={copySelection}
              title="Copy selection"
              className="p-1.5 text-panel-muted hover:text-panel-text hover:bg-[#313244] rounded-md transition-colors"
            >
              <Copy size={14} />
            </button>
            <button
              onClick={saveSession}
              title="Download session buffer as .txt"
              className="p-1.5 text-panel-muted hover:text-panel-text hover:bg-[#313244] rounded-md transition-colors"
            >
              <Download size={14} />
            </button>
            <button
              onClick={clearTerminal}
              title="Clear terminal (Ctrl-L)"
              className="p-1.5 text-panel-muted hover:text-panel-text hover:bg-[#313244] rounded-md transition-colors"
            >
              <Trash2 size={14} />
            </button>
            <button
              onClick={() => connectTerminal()}
              title="Reconnect"
              className="p-1.5 text-panel-muted hover:text-panel-text hover:bg-[#313244] rounded-md transition-colors"
            >
              <RefreshCw size={14} />
            </button>
            <button
              onClick={() => setHelpOpen(true)}
              title="Keyboard shortcuts"
              className="p-1.5 text-panel-muted hover:text-panel-text hover:bg-[#313244] rounded-md transition-colors"
            >
              <Keyboard size={14} />
            </button>
            <button
              onClick={() => setFullscreen(!fullscreen)}
              title={fullscreen ? "Exit fullscreen (F11)" : "Fullscreen (F11)"}
              className="p-1.5 text-panel-muted hover:text-panel-text hover:bg-[#313244] rounded-md transition-colors"
            >
              {fullscreen ? <Minimize2 size={14} /> : <Maximize2 size={14} />}
            </button>
          </div>
        </div>

        {/* Terminal body */}
        <div ref={termRef} className="flex-1 w-full px-3 py-2 bg-[#1e1e2e]" style={{ minHeight: "500px" }} />

        {/* Status bar */}
        <div className="flex items-center justify-between px-4 py-1.5 bg-[#181825] border-t border-panel-border text-[11px] font-mono text-panel-muted">
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-1.5">
              <span className={`w-1.5 h-1.5 rounded-full ${connected ? "bg-green-400" : "bg-red-400"}`} />
              <span className={connected ? "text-green-400" : "text-red-400"}>
                {connected ? "CONNECTED" : "DISCONNECTED"}
              </span>
            </div>
            <span>user: <span className="text-panel-text">{selectedUser}</span></span>
            <span>shell: <span className="text-panel-text">bash</span></span>
            <span>
              tier: <span className={
                selectedUser === "root" && isOwner ? "text-red-300 font-semibold" :
                isOwner ? "text-blue-300" : "text-amber-300"
              }>
                {selectedUser === "root" && isOwner ? "root" : isOwner ? "owner" : "sandbox"}
              </span>
            </span>
          </div>
          <div className="flex items-center gap-4">
            {connected && <span>uptime: <span className="text-panel-text">{uptime}</span></span>}
            <span>{fontSize}px</span>
            <span>UTF-8</span>
            <span className="hidden sm:inline">⌘C copy · ⌘V paste · ? for help</span>
          </div>
        </div>
      </Card>

      <Modal
        isOpen={helpOpen}
        onClose={() => setHelpOpen(false)}
        title="Terminal keyboard shortcuts"
        size="md"
      >
        <div className="space-y-4 text-sm">
          <section>
            <h3 className="text-xs uppercase tracking-wider text-panel-muted font-semibold mb-2">Panel shortcuts</h3>
            <div className="space-y-1.5 text-panel-text">
              <Shortcut keys="F11" label="Toggle fullscreen" />
              <Shortcut keys="?" label="Open this help (toolbar button)" />
              <Shortcut keys="⌘C / Ctrl-C" label="Copy selected text" />
              <Shortcut keys="⌘V / Ctrl-V" label="Paste at cursor" />
            </div>
          </section>
          <section>
            <h3 className="text-xs uppercase tracking-wider text-panel-muted font-semibold mb-2">Shell shortcuts</h3>
            <div className="space-y-1.5 text-panel-text">
              <Shortcut keys="Ctrl-C" label="Interrupt (SIGINT) the running command" />
              <Shortcut keys="Ctrl-D" label="Logout / EOF" />
              <Shortcut keys="Ctrl-L" label="Clear screen" />
              <Shortcut keys="Ctrl-R" label="Reverse search command history" />
              <Shortcut keys="Ctrl-A / Ctrl-E" label="Jump to start / end of line" />
              <Shortcut keys="Ctrl-W" label="Delete previous word" />
              <Shortcut keys="Ctrl-U" label="Clear line before cursor" />
              <Shortcut keys="Tab" label="Autocomplete path / command" />
              <Shortcut keys="↑ / ↓" label="Previous / next command in history" />
            </div>
          </section>
          <section>
            <h3 className="text-xs uppercase tracking-wider text-panel-muted font-semibold mb-2">This panel</h3>
            <div className="text-panel-muted text-xs space-y-1">
              <p>• The <strong className="text-panel-text">⚡ lightning</strong> icon opens a command palette of common one-liners (restart serverpanel, df -h, …). Clicking inserts the command without pressing Enter, so you can edit before running.</p>
              <p>• <strong className="text-panel-text">↓ download</strong> saves the rendered session buffer as a <code className="font-mono">.txt</code> file.</p>
              <p>• Font size changes are remembered per browser (<code className="font-mono">localStorage</code>).</p>
              {!isOwner && (
                <p>• Your shell is sandboxed to <code className="font-mono">~</code>. <code className="font-mono">cd</code> outside home is blocked at the shell layer; deeper isolation comes from jailkit at the linux-account level.</p>
              )}
            </div>
          </section>
        </div>
      </Modal>
    </div>
  );
}

function Shortcut({ keys, label }: { keys: string; label: string }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-panel-muted">{label}</span>
      <kbd className="px-1.5 py-0.5 rounded bg-panel-bg border border-panel-border text-[11px] font-mono text-panel-text">
        {keys}
      </kbd>
    </div>
  );
}
