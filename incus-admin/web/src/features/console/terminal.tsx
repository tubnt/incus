import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";

interface ConsoleTerminalProps {
  vmName: string;
  project: string;
  cluster: string;
}

export function ConsoleTerminal({ vmName, project, cluster }: ConsoleTerminalProps) {
  const termRef = useRef<HTMLDivElement>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const terminalRef = useRef<Terminal | null>(null);

  useEffect(() => {
    if (!termRef.current) return;

    const terminal = new Terminal({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: "'JetBrains Mono', 'Fira Code', monospace",
      theme: {
        background: "#0d1117",
        foreground: "#c9d1d9",
        cursor: "#58a6ff",
        selectionBackground: "#264f78",
      },
    });

    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.loadAddon(new WebLinksAddon());
    terminal.open(termRef.current);
    fitAddon.fit();
    terminalRef.current = terminal;

    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const wsUrl = `${protocol}//${window.location.host}/api/console?vm=${encodeURIComponent(vmName)}&project=${encodeURIComponent(project)}&cluster=${encodeURIComponent(cluster)}`;

    terminal.writeln("Connecting to " + vmName + "...");

    const ws = new WebSocket(wsUrl);
    wsRef.current = ws;

    ws.onopen = () => {
      terminal.writeln("Connected.\r\n");
    };

    ws.onmessage = (event) => {
      if (typeof event.data === "string") {
        terminal.write(event.data);
      } else if (event.data instanceof Blob) {
        event.data.text().then((text) => terminal.write(text));
      }
    };

    ws.onerror = () => {
      terminal.writeln("\r\n\x1b[31mConnection error.\x1b[0m");
    };

    ws.onclose = () => {
      terminal.writeln("\r\n\x1b[33mDisconnected.\x1b[0m");
    };

    terminal.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(data);
      }
    });

    const handleResize = () => {
      fitAddon.fit();
    };
    window.addEventListener("resize", handleResize);

    return () => {
      window.removeEventListener("resize", handleResize);
      ws.close();
      terminal.dispose();
    };
  }, [vmName, project, cluster]);

  return (
    <div
      ref={termRef}
      className="w-full rounded-lg overflow-hidden border border-border"
      style={{ height: "500px" }}
    />
  );
}
