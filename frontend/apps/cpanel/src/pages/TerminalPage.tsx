import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import {
  RefreshCw, User, Copy, Trash2, Maximize2, Minimize2,
  KeyRound, Home, FolderTree, Globe,
  Minus, Plus, Keyboard, Command, Download, Zap,
} from "lucide-react";
import { Card, Modal } from "@serverpanel/ui";
import toast from "react-hot-toast";
import { useAuthStore } from "@/store/auth";

// COMMAND_PRESETS is a curated, vendor-safe subset of the WHM palette.
// Every entry must work inside the jailkit sandbox that the backend
// drops non-vendor_admin callers into — so anything that needs root
// (systemctl, iptables, ufw, journalctl across the box, apt, nginx
// reload, …) is intentionally absent. Commands are inserted into the
// PTY input buffer without pressing Enter, so the operator can edit
// before running — matches the WHM behaviour.
const COMMAND_PRESETS: { group: string; items: { label: string; cmd: string; desc?: string }[] }[] = [
  {
    group: "System",
    items: [
      { label: "uname -a", cmd: "uname -a", desc: "Kernel + arch" },
      { label: "free -h", cmd: "free -h", desc: "Memory usage (whole host)" },
      { label: "df -h ~", cmd: "df -h ~", desc: "Disk usage for your home" },
      { label: "uptime", cmd: "uptime", desc: "Load average + uptime" },
    ],
  },
  {
    group: "Files",
    items: [
      { label: "ls -la ~", cmd: "ls -la ~", desc: "List your home directory" },
      { label: "ls -lah", cmd: "ls -lah", desc: "List current directory" },
      { label: "du -sh ~", cmd: "du -sh ~", desc: "Total size of your home" },
      { label: "du -sh * | sort -h", cmd: "du -sh * 2>/dev/null | sort -h | tail -20", desc: "Largest items in cwd" },
      { label: "find . -mtime -1", cmd: "find . -mtime -1 -type f 2>/dev/null | head -30", desc: "Files modified in last 24h" },
    ],
  },
  {
    group: "Networking",
    items: [
      { label: "curl -I", cmd: "curl -I ", desc: "Fetch response headers (append URL)" },
      { label: "ping -c 4", cmd: "ping -c 4 ", desc: "Ping host 4 times (append host)" },
      { label: "dig +short", cmd: "dig +short ", desc: "DNS lookup (append hostname)" },
    ],
  },
  {
    group: "Apps",
    items: [
      { label: "pm2 list", cmd: "pm2 list", desc: "List PM2-managed apps (if installed)" },
      { label: "node -v", cmd: "node -v", desc: "Node.js version" },
      { label: "python3 --version", cmd: "python3 --version", desc: "Python version" },
    ],
  },
];

export default function TerminalPage() {
  const termRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const [connected, setConnected] = useState(false);
  // The cpanel surface is strictly tenant-scoped — callers are always
  // pinned by the backend to their own linux account (vendor_admin
  // gets full bash; vendor_staff/developer/support/customer get a
  // jailkit'd shell confined to ~). There's no user picker, no root
  // button, no `/users` fetch — all of that lives in the WHM panel.
  const currentUser = useAuthStore((s) => s.user);
  const role = currentUser?.role ?? "";
  // Tenant root (vendor_admin) gets a full bash — no sandbox ribbon.
  // Everyone else on the cpanel side (vendor_staff, developer,
  // support, customer) runs inside a jailkit shell so we show the
  // amber notice explaining the cd /etc etc boundary.
  const isTenantRoot = role === "vendor_admin";
  const ownUsername = currentUser?.username || currentUser?.email?.split("@")[0] || "";

  const [fullscreen, setFullscreen] = useState(false);
  const [sessionStart, setSessionStart] = useState<number | null>(null);
  const [uptime, setUptime] = useState("00:00");
  // Persist the font size across reloads under a cpanel-specific
  // key so it doesn't collide with the WHM panel's preference in
  // the same browser profile.
  const [fontSize, setFontSize] = useState<number>(() => {
    const v = Number(localStorage.getItem("cpanel.terminal.font-size"));
    return Number.isFinite(v) && v >= 10 && v <= 24 ? v : 14;
  });
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);

  const connectTerminal = () => {
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
    // Note: no &user= param — the backend derives the target linux
    // user from the JWT itself for cpanel callers, so even if the
    // frontend forgot the param it would still be the right user.
    const wsUrl = `${proto}//${window.location.host}/ws/terminal?token=${encodeURIComponent(token)}`;
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

  const reconnect = () => {
    connectTerminal();
    toast.success("Reconnecting…");
  };

  // sendToTerminal injects text straight into the PTY as if the
  // operator typed it — used by quick-nav buttons and the palette.
  // Byte 0 prefix = stdin; matches the protocol the backend expects.
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

  // Quick cd shortcuts — trailing \r = Enter, so these run
  // immediately. All three are safe inside the jailkit sandbox
  // (everything is relative to ~), which is why we don't gate
  // ~/apps and ~/domains on role the way the WHM panel does.
  const goHome = () => sendToTerminal("cd ~\r");
  const goApps = () => sendToTerminal("cd ~/apps\r");
  const goDomains = () => sendToTerminal("cd ~/domains\r");

  // Adjust + persist terminal font size; clamp to a legible range
  // and re-fit afterwards so col/row count tracks the new cell size.
  const adjustFont = (delta: number) => {
    setFontSize((prev) => {
      const next = Math.max(10, Math.min(24, prev + delta));
      localStorage.setItem("cpanel.terminal.font-size", String(next));
      if (terminalRef.current) {
        terminalRef.current.options.fontSize = next;
        requestAnimationFrame(() => fitAddonRef.current?.fit());
      }
      return next;
    });
  };

  // insertCommand drops a preset into the input buffer WITHOUT
  // pressing Enter — lets the operator review/edit (especially
  // useful for palette entries that need a URL or host argument).
  const insertCommand = (cmd: string) => {
    sendToTerminal(cmd);
    setPaletteOpen(false);
    toast.success("Inserted — press Enter to run, or edit first");
  };

  // saveSession grabs the rendered xterm buffer (visible +
  // scrollback) and offers it as a timestamped .txt download.
  // The buffer stores post-render text, not raw bytes — so no
  // ANSI codes end up in the file.
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
    while (lines.length && lines[lines.length - 1] === "") lines.pop();
    const content = lines.join("\n") + "\n";
    const blob = new Blob([content], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    const ts = new Date().toISOString().replace(/[:.]/g, "-").slice(0, 19);
    a.href = url;
    a.download = `terminal-${ownUsername || "session"}-${ts}.txt`;
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (fitAddonRef.current) {
      setTimeout(() => fitAddonRef.current?.fit(), 150);
    }
  }, [fullscreen]);

  // F11 toggles fullscreen without having to click the button.
  // Capture phase so xterm doesn't eat it first — xterm otherwise
  // claims every keystroke when focused.
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

  const displayUser = ownUsername || "—";

  return (
    <div className={`flex flex-col ${fullscreen ? "fixed inset-0 z-50 bg-panel-bg p-2" : "h-full"}`}>
      {/*
        Sandbox notice — shown for every cpanel role except
        vendor_admin (tenant root, full bash). The backend's jail
        rcfile blocks `cd /etc`, `cd /` and paths outside the user's
        home, so this banner tells the operator up front why those
        commands fail rather than having them trip over it later.
      */}
      {!fullscreen && !isTenantRoot && (
        <div className="flex items-start gap-2 px-3 py-2 mb-2 rounded-lg border border-amber-500/30 bg-amber-500/5 text-xs text-amber-200">
          <KeyRound size={12} className="mt-0.5 shrink-0" />
          <span>
            Your shell is sandboxed to <code className="font-mono text-amber-100">{`~ (/home/${ownUsername || "you"})`}</code> — <code className="font-mono">cd /</code>, <code className="font-mono">cd /etc</code>, and paths outside your home are blocked. Contact your admin if you need broader access.
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
              <span className="text-panel-text">{displayUser}@serverpanel</span>
              <span className="text-panel-muted">:~</span>
            </div>
          </div>

          <div className="flex items-center gap-1.5">
            {/* Read-only linux user badge — cpanel callers can't
                switch accounts (backend enforces this via JWT), so
                the badge just confirms who they're connected as. */}
            <div
              title="Tenant-scoped roles always connect as their own linux account."
              className="flex items-center gap-2 px-2.5 py-1.5 bg-[#313244] text-panel-muted border border-panel-border/60 rounded-md text-xs font-medium cursor-default select-none"
            >
              <User size={12} />
              <span className="max-w-[140px] truncate">{displayUser}</span>
            </div>

            <div className="h-4 w-px bg-panel-border mx-1" />

            {/* Quick-jump: cd ~, ~/apps, ~/domains — all three are
                safely inside the jailkit sandbox so they work for
                every cpanel role. */}
            <button
              onClick={goHome}
              title="Go to home directory (cd ~)"
              className="p-1.5 text-panel-muted hover:text-panel-text hover:bg-[#313244] rounded-md transition-colors"
            >
              <Home size={14} />
            </button>
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

            <div className="h-4 w-px bg-panel-border mx-1" />

            {/* Command palette — vendor-safe preset drop-in. Inserts
                into the prompt without pressing Enter so the
                operator can edit before running. */}
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

            {/* Font size controls. Reads as one compound control
                even though it's three elements; the pixel size
                sits visibly in the middle. */}
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
              onClick={reconnect}
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
            <span>user: <span className="text-panel-text">{displayUser}</span></span>
            <span>shell: <span className="text-panel-text">bash</span></span>
            <span>
              tier: <span className={isTenantRoot ? "text-blue-300" : "text-amber-300"}>
                {isTenantRoot ? "tenant" : "sandbox"}
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
              <p>• The <strong className="text-panel-text">⚡ lightning</strong> icon opens a palette of vendor-safe one-liners (df -h ~, ls -la ~, pm2 list, …). Clicking inserts the command without pressing Enter, so you can edit before running.</p>
              <p>• <strong className="text-panel-text">↓ download</strong> saves the rendered session buffer as a <code className="font-mono">.txt</code> file.</p>
              <p>• Font size changes are remembered per browser (<code className="font-mono">localStorage</code>).</p>
              {!isTenantRoot && (
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
