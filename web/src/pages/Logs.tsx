import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Play, Square, Trash2 } from "lucide-react";
import { get, stream, type StatusResp } from "../lib/api";
import ErrorBanner from "../components/ErrorBanner";
import { usePageTitle } from "../lib/usePageTitle";

type LogLine = { ts: string; worker: string; msg: string; kind: "ok" | "err" | "info" };

const ALL = "__all__";

export default function Logs() {
  usePageTitle("Logs");
  const [searchParams] = useSearchParams();
  const [workers, setWorkers] = useState<string[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [lines, setLines] = useState<LogLine[]>([]);
  const [err, setErr] = useState<unknown>(null);
  const [running, setRunning] = useState(false);
  const ctrlsRef = useRef<AbortController[]>([]);
  const viewRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    get<StatusResp>("/status").then((r) => {
      const ws = r.workers.map((w) => w.name).sort();
      setWorkers(ws);
      const wantFromURL = searchParams.get("worker");
      if (wantFromURL && ws.includes(wantFromURL)) setSelected(wantFromURL);
      else if (!selected && ws.length) setSelected(ws[0]);
    }).catch(setErr);
  }, [searchParams]);

  useEffect(() => {
    // Auto-scroll to bottom on every new line.
    if (viewRef.current) viewRef.current.scrollTop = viewRef.current.scrollHeight;
  }, [lines]);

  async function streamOne(name: string, ac: AbortController) {
    try {
      const resp = await stream(`/workers/${encodeURIComponent(name)}/logs`, ac.signal);
      const reader = resp.body!.getReader();
      const dec = new TextDecoder();
      let buf = "";
      // eslint-disable-next-line no-constant-condition
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += dec.decode(value);
        const chunks = buf.split("\n");
        buf = chunks.pop() ?? "";
        for (const raw of chunks) {
          if (!raw.startsWith("data:")) continue;
          try {
            const ev = JSON.parse(raw.slice(5).trim());
            const ts = new Date(ev.eventTimestamp || Date.now()).toLocaleTimeString();
            let msg = "", kind: LogLine["kind"] = "info";
            if (ev.exceptions?.length) { msg = `EXCEPTION ${ev.exceptions[0].message}`; kind = "err"; }
            else if (ev.event?.request?.url) { msg = `${ev.event.request.method || "GET"} ${String(ev.event.request.url).slice(0, 180)}`; kind = "ok"; }
            else msg = ev.outcome || "log";
            setLines((cur) => [...cur.slice(-1999), { ts, worker: name, msg, kind }]);
          } catch { /* skip malformed */ }
        }
      }
    } catch (e: unknown) {
      if ((e as { name?: string }).name !== "AbortError") {
        setLines((cur) => [...cur.slice(-1999), { ts: new Date().toLocaleTimeString(), worker: name, msg: `stream error: ${(e as Error).message}`, kind: "err" }]);
      }
    }
  }

  async function start() {
    if (!selected) return;
    stop();
    setLines([]); setErr(null); setRunning(true);
    const targets = selected === ALL ? workers : [selected];
    if (targets.length === 0) { setRunning(false); return; }
    const ctrls: AbortController[] = [];
    for (const name of targets) {
      const ac = new AbortController();
      ctrls.push(ac);
      void streamOne(name, ac);
    }
    ctrlsRef.current = ctrls;
    // running stays true until user stops — each individual stream may end,
    // but we hold the "streaming" UI state until explicit stop.
  }

  function stop() {
    for (const ac of ctrlsRef.current) ac.abort();
    ctrlsRef.current = [];
    setRunning(false);
  }
  useEffect(() => () => stop(), []);

  // Shortcuts for coloring the per-worker prefix in multi-stream mode.
  const workerColor = (name: string) => {
    const palette = ["text-sky-400", "text-emerald-400", "text-amber-400", "text-fuchsia-400", "text-rose-400", "text-indigo-400", "text-lime-400", "text-orange-400", "text-cyan-400", "text-violet-400"];
    let h = 0;
    for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) | 0;
    return palette[Math.abs(h) % palette.length];
  };

  const multi = selected === ALL;

  return (
    <div className="space-y-4">
      <ErrorBanner error={err} />
      <section className="card p-4">
        <div className="flex gap-2 items-center">
          <select
            value={selected}
            onChange={(e) => setSelected(e.target.value)}
            className="input flex-1 font-mono"
            disabled={running}
          >
            <option value={ALL}>— ALL workers ({workers.length}) —</option>
            {workers.map((w) => <option key={w} value={w}>{w}</option>)}
          </select>
          {running ? (
            <button onClick={stop} className="btn-ghost">
              <Square className="h-4 w-4" /> Stop
            </button>
          ) : (
            <button onClick={start} className="btn-accent" disabled={!selected && workers.length === 0}>
              <Play className="h-4 w-4" /> Stream
            </button>
          )}
          {lines.length > 0 && (
            <button onClick={() => setLines([])} className="btn-ghost" title="Clear">
              <Trash2 className="h-4 w-4" />
            </button>
          )}
        </div>
        <p className="mt-2 text-xs text-muted-foreground">
          Streams Cloudflare's Tail API via SSE. {multi
            ? <>One WebSocket per Worker, fan-in to the console below. CF limits ~10 concurrent tails per account — stop one before starting another large pool.</>
            : <>Generate traffic against the selected Worker to see events.</>}
        </p>
      </section>

      <div ref={viewRef} className="card h-[560px] overflow-auto bg-black p-3 font-mono text-xs text-emerald-200">
        {lines.length === 0 && (
          <div className="text-muted-foreground">
            {running ? "streaming… waiting for events" : "No stream active. Pick a worker (or 'ALL') + click Stream."}
          </div>
        )}
        {lines.map((l, i) => (
          <div key={i}>
            <span className="text-muted-foreground">{l.ts}</span>
            {multi && <> <span className={workerColor(l.worker)}>[{l.worker.replace(/^flarex-/, "")}]</span></>}
            <span> </span>
            <span className={l.kind === "err" ? "text-red-400" : l.kind === "ok" ? "text-emerald-300" : "text-foreground"}>{l.msg}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
