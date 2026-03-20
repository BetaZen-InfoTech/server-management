import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import { RefreshCw } from "lucide-react";
import { Card, Button } from "@serverpanel/ui";
import { useAuthStore } from "@/store/auth";

export default function TerminalPage() {
  const termRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const [connected, setConnected] = useState(false);

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
    const wsUrl = `${proto}//${window.location.host}/ws/terminal?token=${encodeURIComponent(token)}`;
    const ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    ws.onopen = () => {
      setConnected(true);
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
      term.write("\r\n\x1b[33mConnection closed.\x1b[0m\r\n");
    };

    ws.onerror = () => {
      setConnected(false);
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

  return (
    <div className="space-y-4 h-full flex flex-col">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-panel-text">Terminal</h1>
          <p className="text-panel-muted text-sm mt-1">Your account shell access</p>
        </div>
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <div className={`w-2 h-2 rounded-full ${connected ? "bg-green-400 animate-pulse" : "bg-red-400"}`} />
            <span className="text-sm text-panel-muted">{connected ? "Connected" : "Disconnected"}</span>
          </div>
          <Button className="bg-panel-surface text-panel-text border border-panel-border hover:bg-panel-border flex items-center gap-2 px-3 py-2 rounded-lg text-sm" onClick={connectTerminal}>
            <RefreshCw size={14} />
            Reconnect
          </Button>
        </div>
      </div>

      <Card className="flex-1 p-0 overflow-hidden border border-panel-border bg-[#1e1e2e] rounded-xl">
        <div ref={termRef} className="h-full w-full p-2" style={{ minHeight: "500px" }} />
      </Card>
    </div>
  );
}
