import { useCallback, useEffect, useRef, useState } from "react";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { LoaderCircle, Play, Square } from "lucide-react";
import {
  closeDesktopUserTerminal,
  desktopErrorMessage,
  desktopUserTerminalEnabled,
  getDesktopUserTerminal,
  readDesktopUserTerminal,
  resizeDesktopUserTerminal,
  startDesktopUserTerminal,
  writeDesktopUserTerminal,
  type DesktopTerminalSession,
} from "../lib/desktop-bridge";

const terminalPollMilliseconds = 120;

export function UserTerminalPanel({ runID, sessionID, onSession }: {
  runID: string;
  sessionID: string;
  onSession: (sessionID: string) => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const sessionRef = useRef(sessionID);
  const runRef = useRef(runID);
  const mountedRef = useRef(true);
  const cursorRef = useRef(0);
  const writeQueueRef = useRef(Promise.resolve());
  const [session, setSession] = useState<DesktopTerminalSession | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const enabled = desktopUserTerminalEnabled();

  useEffect(() => {
    sessionRef.current = sessionID;
  }, [sessionID]);

  runRef.current = runID;

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    if (!hostRef.current) return;
    const terminal = new Terminal({
      allowProposedApi: false,
      convertEol: false,
      cursorBlink: true,
      cursorStyle: "bar",
      fontFamily: "\"Cascadia Mono\", Consolas, monospace",
      fontSize: 13,
      lineHeight: 1.18,
      scrollback: 5000,
      theme: {
        background: "#171512",
        foreground: "#f2e4cc",
        cursor: "#f58231",
        cursorAccent: "#171512",
        selectionBackground: "#a94b2473",
        black: "#171512",
        red: "#ef6b5d",
        green: "#8fbf71",
        yellow: "#e2b45f",
        blue: "#78a9d4",
        magenta: "#c792b6",
        cyan: "#74b6b0",
        white: "#f2e4cc",
      },
    });
    const fit = new FitAddon();
    terminal.loadAddon(fit);
    terminal.open(hostRef.current);
    fit.fit();
    terminalRef.current = terminal;
    fitRef.current = fit;
    const dataSubscription = terminal.onData((data) => {
      const current = sessionRef.current;
      if (!current) return;
      writeQueueRef.current = writeQueueRef.current
        .then(() => writeDesktopUserTerminal(current, data))
        .then(() => undefined)
        .catch((error) => setMessage(desktopErrorMessage(error)));
    });
    const resize = () => {
      fit.fit();
      const current = sessionRef.current;
      if (current && terminal.cols >= 20 && terminal.rows >= 5) {
        void resizeDesktopUserTerminal(current, terminal.cols, terminal.rows)
          .catch((error) => setMessage(desktopErrorMessage(error)));
      }
    };
    const ResizeObserverType = window.ResizeObserver;
    const resizeObserver = ResizeObserverType ? new ResizeObserverType(resize) : null;
    if (resizeObserver) {
      resizeObserver.observe(hostRef.current);
    } else {
      window.addEventListener("resize", resize);
    }
    return () => {
      resizeObserver?.disconnect();
      if (!resizeObserver) window.removeEventListener("resize", resize);
      dataSubscription.dispose();
      terminal.dispose();
      terminalRef.current = null;
      fitRef.current = null;
    };
  }, []);

  const writePage = useCallback((encoded: string) => {
    const binary = window.atob(encoded);
    const bytes = Uint8Array.from(binary, (current) => current.charCodeAt(0));
    terminalRef.current?.write(bytes);
  }, []);

  useEffect(() => {
    if (!sessionID || !enabled) {
      setSession(null);
      cursorRef.current = 0;
      return;
    }
    let cancelled = false;
    let timer = 0;
    const poll = async () => {
      try {
        const page = await readDesktopUserTerminal(sessionID, cursorRef.current);
        if (cancelled) return;
        if (page.dropped) {
          terminalRef.current?.clear();
        }
        if (page.data_bytes > 0) {
          writePage(page.data_base64);
        }
        cursorRef.current = page.next_cursor;
        const current = await getDesktopUserTerminal(sessionID);
        if (cancelled) return;
        setSession(current);
        if (current.state === "running") {
          timer = window.setTimeout(() => void poll(), terminalPollMilliseconds);
        }
      } catch (error) {
        if (!cancelled) setMessage(desktopErrorMessage(error));
      }
    };
    void getDesktopUserTerminal(sessionID).then((current) => {
      if (cancelled) return;
      setSession(current);
      cursorRef.current = current.output_base_cursor;
      void poll();
    }).catch((error) => {
      if (!cancelled) {
        onSession("");
        setMessage(desktopErrorMessage(error));
      }
    });
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [enabled, onSession, sessionID, writePage]);

  const start = async () => {
    if (!enabled || !runID) return;
    const requestedRunID = runID;
    setBusy(true);
    setMessage("");
    try {
      fitRef.current?.fit();
      const terminal = terminalRef.current;
      const created = await startDesktopUserTerminal(runID,
        terminal?.cols ?? 120, terminal?.rows ?? 32, Boolean(sessionID));
      if (!mountedRef.current || runRef.current !== requestedRunID) {
        await closeDesktopUserTerminal(created.session_id).catch(() => undefined);
        return;
      }
      terminal?.reset();
      cursorRef.current = created.output_base_cursor;
      sessionRef.current = created.session_id;
      setSession(created);
      onSession(created.session_id);
      terminal?.focus();
    } catch (error) {
      if (mountedRef.current && runRef.current === requestedRunID) {
        setMessage(desktopErrorMessage(error));
      }
    } finally {
      if (mountedRef.current) setBusy(false);
    }
  };

  const stop = async () => {
    if (!sessionID) return;
    setBusy(true);
    setMessage("");
    try {
      await closeDesktopUserTerminal(sessionID);
      if (!mountedRef.current) return;
      onSession("");
      setSession(null);
      sessionRef.current = "";
      cursorRef.current = 0;
    } catch (error) {
      if (mountedRef.current) setMessage(desktopErrorMessage(error));
    } finally {
      if (mountedRef.current) setBusy(false);
    }
  };

  return <div className="user-terminal-panel">
    <div aria-label="用户终端控制" className="user-terminal-controls" role="toolbar">
      {!sessionID && <button disabled={!enabled || !runID || busy}
        onClick={() => void start()} title="启动 Debug 终端" type="button">
        {busy ? <LoaderCircle aria-hidden="true" className="spin" size={14} /> :
          <Play aria-hidden="true" size={14} />}
        <span>启动</span>
      </button>}
      {sessionID && <button disabled={busy} onClick={() => void stop()}
        title="关闭终端" type="button">
        {busy ? <LoaderCircle aria-hidden="true" className="spin" size={14} /> :
          <Square aria-hidden="true" size={13} />}
        <span>关闭</span>
      </button>}
      <span className={`user-terminal-state ${session?.state ?? "idle"}`}>
        {session?.state ?? (enabled ? "idle" : "disabled")}
      </span>
      {message && <span className="user-terminal-message" role="status">{message}</span>}
    </div>
    <div aria-label="用户终端" className="user-terminal-host" ref={hostRef} />
  </div>;
}
