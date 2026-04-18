import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import { RefreshCw, User, ChevronDown, TerminalSquare, Copy, Trash2, Maximize2, Minimize2 } from "lucide-react";
import { Card, Button } from "@serverpanel/ui";
import toast from "react-hot-toast";
import { useAuthStore } from "@/store/auth";
import api from "@/lib/api";

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

  const fetchUsers = async () => {
    // Only the platform owner can switch between users — so don't even
    // bother fetching the user list for tenant-scoped roles (they'd get a
    // filtered view anyway).
    if (!isOwner) return;
    try {
      const res = await api.get("/users?limit=200");
      setUsers(res.data.data || []);
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
      fontSize: 14,
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

  const copySelection = async () => {
    const term = terminalRef.current;
    if (!term) return;
    const text = term.getSelection();
    if (!text) {
      toast.error("Nothing selected");
      return;
    }
    try {
      await navigator.clipboard.writeText(text);
      toast.success("Copied to clipboard");
    } catch {
      toast.error("Copy failed");
    }
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
    <div className={`flex flex-col ${fullscreen ? "fixed inset-0 z-50 bg-panel-bg p-4" : "space-y-4 h-full"}`}>
      {!fullscreen && (
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="flex items-center justify-center w-10 h-10 rounded-lg bg-gradient-to-br from-blue-500/20 to-purple-500/20 border border-blue-500/30">
              <TerminalSquare size={20} className="text-blue-400" />
            </div>
            <div>
              <h1 className="text-2xl font-bold text-panel-text">Terminal</h1>
              <p className="text-panel-muted text-sm">
                Secure shell session as <span className="text-panel-text font-medium">{selectedUser || ownUsername || "—"}</span>
                {!isOwner && <span className="ml-2 text-xs text-panel-muted/70">(pinned to your account — no root access)</span>}
              </p>
            </div>
          </div>
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

            <button
              onClick={copySelection}
              title="Copy selection"
              className="p-1.5 text-panel-muted hover:text-panel-text hover:bg-[#313244] rounded-md transition-colors"
            >
              <Copy size={14} />
            </button>
            <button
              onClick={clearTerminal}
              title="Clear terminal"
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
              onClick={() => setFullscreen(!fullscreen)}
              title={fullscreen ? "Exit fullscreen" : "Fullscreen"}
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
          </div>
          <div className="flex items-center gap-4">
            {connected && <span>uptime: <span className="text-panel-text">{uptime}</span></span>}
            <span>UTF-8</span>
            <span className="hidden sm:inline">⌘C copy · ⌘V paste</span>
          </div>
        </div>
      </Card>
    </div>
  );
}
