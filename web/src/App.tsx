import { Suspense, lazy, useEffect, useState } from "react";
import { NavLink, Navigate, Route, Routes } from "react-router-dom";
import { Flame, Moon, Sun, KeyRound, LayoutDashboard, Users, Boxes, ScrollText, BarChart3, Settings2, LogOut, Menu, X, Activity, Send, Pause, Play } from "lucide-react";
import { Toaster, toast } from "sonner";
import { login, logout, TOTPRequiredError, type SessionInfo, tryBootstrapSession } from "./lib/auth";
import { getTheme, toggleTheme } from "./lib/theme";
import { cn } from "./lib/utils";
import { get, post } from "./lib/api";
import ErrorBoundary from "./components/ErrorBoundary";
import { ConfirmProvider, useConfirmDialog } from "./components/Dialog";
import CommandPalette from "./components/CommandPalette";

// Lazy-load each page so the initial bundle stays small. Users that
// never leave Overview don't download Accounts/Workers/Logs chunks.
const Overview = lazy(() => import("./pages/Overview"));
const Accounts = lazy(() => import("./pages/Accounts"));
const Workers = lazy(() => import("./pages/Workers"));
const Logs = lazy(() => import("./pages/Logs"));
const Quota = lazy(() => import("./pages/Quota"));
const APIKeys = lazy(() => import("./pages/APIKeys"));
const ConfigPage = lazy(() => import("./pages/Config"));
const Audit = lazy(() => import("./pages/Audit"));
const TestRequest = lazy(() => import("./pages/TestRequest"));

export default function App() {
  const [session, setSession] = useState<SessionInfo | null | "loading">("loading");

  useEffect(() => {
    // Probe the server: if we already have a live session cookie, /status
    // returns 200 and we skip the login screen.
    tryBootstrapSession().then(setSession);
  }, []);

  if (session === "loading") {
    return (
      <div className="flex h-full items-center justify-center bg-background">
        <div className="text-sm text-muted-foreground">Connecting…</div>
      </div>
    );
  }
  if (!session) return <LoginScreen onAuthed={(s) => setSession(s)} />;

  return (
    <ConfirmProvider>
      <div className="flex h-full flex-col">
        <Toaster theme={getTheme()} richColors closeButton position="top-right" />
        <CommandPalette />
        <TopBar who={session.who} onLogout={async () => { await logout(); setSession(null); }} />
        <div className="flex flex-1 overflow-hidden">
          <SideNav />
          <main className="flex-1 overflow-auto bg-background">
            <div className="mx-auto w-full max-w-[108rem] p-4 md:p-6">
              <ErrorBoundary>
                <Suspense fallback={<PageFallback />}>
                  <Routes>
                    <Route path="/" element={<Navigate to="/overview" replace />} />
                    <Route path="/overview" element={<Overview />} />
                    <Route path="/accounts" element={<Accounts />} />
                    <Route path="/workers" element={<Workers />} />
                    <Route path="/logs" element={<Logs />} />
                    <Route path="/quota" element={<Quota />} />
                    <Route path="/apikeys" element={<APIKeys />} />
                    <Route path="/audit" element={<Audit />} />
                    <Route path="/test" element={<TestRequest />} />
                    <Route path="/config" element={<ConfigPage />} />
                    <Route path="*" element={<Navigate to="/overview" replace />} />
                  </Routes>
                </Suspense>
              </ErrorBoundary>
            </div>
          </main>
        </div>
      </div>
    </ConfirmProvider>
  );
}

function PageFallback() {
  return (
    <div className="flex h-full items-center justify-center">
      <div className="text-sm text-muted-foreground">Loading…</div>
    </div>
  );
}

function LoginScreen({ onAuthed }: { onAuthed: (s: SessionInfo) => void }) {
  const [val, setVal] = useState("");
  const [totp, setTotp] = useState("");
  const [totpRequired, setTotpRequired] = useState(false);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const s = await login(val, totp || undefined);
      toast.success(`Welcome, ${s.who}`);
      onAuthed(s);
    } catch (e: unknown) {
      if (e instanceof TOTPRequiredError) {
        setTotpRequired(true);
        if (e.wrong) toast.error("Invalid TOTP code");
        else toast.info("TOTP code required");
      } else {
        toast.error(e instanceof Error ? e.message : String(e));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex h-full items-center justify-center bg-background p-4">
      <Toaster theme={getTheme()} richColors closeButton position="top-right" />
      <form onSubmit={submit} className="card w-full max-w-sm p-6 space-y-4">
        <div className="flex items-center gap-3">
          <Flame className="text-accent h-8 w-8" />
          <div>
            <h1 className="text-lg font-semibold">FlareX</h1>
            <p className="text-xs text-muted-foreground">admin dashboard — sign in with your API key</p>
          </div>
        </div>
        <input
          type="password"
          placeholder="API key (flx_…, admin.api_key bootstrap, or bearer)"
          className="input font-mono"
          value={val}
          onChange={(e) => setVal(e.target.value)}
          autoFocus={!totpRequired}
        />
        {totpRequired && (
          <input
            type="text"
            inputMode="numeric"
            autoComplete="one-time-code"
            pattern="[0-9]{6}"
            maxLength={6}
            placeholder="6-digit TOTP code"
            className="input font-mono tracking-[0.4em] text-center"
            value={totp}
            onChange={(e) => setTotp(e.target.value.replace(/\D/g, ""))}
            autoFocus
          />
        )}
        <button type="submit" className="btn-accent w-full" disabled={busy || !val || (totpRequired && totp.length !== 6)}>
          {busy ? "Signing in…" : totpRequired ? "Verify & sign in" : "Sign in"}
        </button>
        <p className="text-xs text-muted-foreground">
          First-time setup: use <code className="text-accent">admin.api_key</code> from{" "}
          <code className="text-accent">config.yaml</code>. After login, mint named
          keys with scoped access under the <em>API keys</em> tab.
        </p>
      </form>
    </div>
  );
}

function TopBar({ who, onLogout }: { who: string; onLogout: () => void }) {
  const [mode, setMode] = useState(getTheme());
  return (
    <header className="relative z-[70] flex items-center gap-3 border-b bg-card px-4 py-3 md:px-6">
      <MobileMenu />
      <div className="flex items-center gap-2">
        <Flame className="text-accent h-6 w-6" />
        <span className="font-semibold tracking-wide">FlareX</span>
        <span className="ml-1 hidden text-xs text-muted-foreground sm:inline">admin</span>
      </div>
      <ProxyModePill />
      <div className="flex-1" />
      <GlobalKillSwitch />
      <span className="hidden font-mono text-xs text-muted-foreground sm:inline">{who}</span>
      <button type="button" onClick={() => setMode(toggleTheme())} className="btn-ghost" title="Toggle theme" aria-label="Toggle theme">
        {mode === "dark" ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
      </button>
      <button type="button" onClick={onLogout} className="btn-ghost" title="Sign out" aria-label="Sign out">
        <LogOut className="h-4 w-4" />
      </button>
    </header>
  );
}

function ProxyModePill() {
  const confirm = useConfirmDialog();
  const [mode, setMode] = useState<string | null>(null);
  useEffect(() => {
    get<{ pool?: { proxy_mode?: string } }>("/config")
      .then((c) => setMode(c?.pool?.proxy_mode || null))
      .catch(() => setMode(null));
  }, []);
  async function cycle() {
    if (!mode) return;
    const order = ["hybrid", "socket", "fetch"];
    const next = order[(order.indexOf(mode) + 1) % order.length];
    const ok = await confirm({
      title: `Switch proxy mode to "${next}"?`,
      description: (
        <>
          Takes effect immediately for new dials. Active connections finish on the current mode.
          <br />
          <span className="font-mono text-[11px]">socket</span> = raw TCP only.
          {" "}
          <span className="font-mono text-[11px]">fetch</span> = HTTP via Worker <code>fetch()</code>.
          {" "}
          <span className="font-mono text-[11px]">hybrid</span> = socket first, sniff → fetch for CF-hosted HTTP.
        </>
      ),
      confirmLabel: `Switch to ${next}`,
    });
    if (!ok) return;
    try {
      await post<{ mode: string }>("/config/proxy-mode", { mode: next });
      setMode(next);
      toast.success(`Proxy mode now: ${next}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    }
  }
  if (!mode) return null;
  const tone = mode === "fetch" ? "pill-warn" : mode === "socket" ? "pill-neutral" : "pill-ok";
  return (
    <button
      type="button"
      onClick={cycle}
      className={`pill ${tone} hidden cursor-pointer font-mono md:inline-flex hover:opacity-80`}
      title={`proxy_mode=${mode}. Click to cycle (hybrid → socket → fetch).`}
    >
      mode: {mode}
    </button>
  );
}

function GlobalKillSwitch() {
  const confirm = useConfirmDialog();
  const [busy, setBusy] = useState(false);

  async function act(paused: boolean) {
    const ok = await confirm({
      title: paused ? "Pause all accounts?" : "Resume all accounts?",
      description: paused
        ? "Marks every worker on every account as quota-paused. New dials will fail with network-unreachable until you resume. Use this as an emergency kill switch — in-flight connections keep running."
        : "Clears the quota-paused flag on every worker so dials resume.",
      confirmLabel: paused ? "Pause all" : "Resume all",
      variant: paused ? "danger" : undefined,
    });
    if (!ok) return;
    setBusy(true);
    try {
      const accts = await get<{ accounts: { id: string }[] }>("/accounts");
      const verb = paused ? "pause" : "resume";
      let affected = 0;
      for (const a of accts.accounts || []) {
        const r = await post<{ affected: number }>(`/accounts/${encodeURIComponent(a.id)}/${verb}`);
        affected += r.affected || 0;
      }
      toast.success(`${paused ? "Paused" : "Resumed"} ${affected} worker(s) across ${accts.accounts?.length ?? 0} account(s)`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="hidden items-center gap-1 md:flex">
      <button
        type="button"
        onClick={() => act(true)}
        disabled={busy}
        className="btn-ghost text-xs border-destructive/40 text-destructive hover:bg-destructive/10"
        title="Pause every worker (kill switch)"
      >
        <Pause className="h-3.5 w-3.5" /> kill
      </button>
      <button
        type="button"
        onClick={() => act(false)}
        disabled={busy}
        className="btn-ghost text-xs"
        title="Resume every worker"
      >
        <Play className="h-3.5 w-3.5" /> resume
      </button>
    </div>
  );
}

const NAV = [
  { to: "/overview", label: "Overview", icon: LayoutDashboard },
  { to: "/accounts", label: "Accounts", icon: Users },
  { to: "/workers", label: "Workers", icon: Boxes },
  { to: "/test", label: "Test request", icon: Send },
  { to: "/logs", label: "Logs", icon: ScrollText },
  { to: "/quota", label: "Quota", icon: BarChart3 },
  { to: "/apikeys", label: "API keys", icon: KeyRound },
  { to: "/audit", label: "Audit log", icon: Activity },
  { to: "/config", label: "Config", icon: Settings2 },
] as const;

function SideNav() {
  return (
    <nav className="hidden w-64 shrink-0 border-r bg-card/50 p-3 space-y-1 md:block">
      {NAV.map((n) => (
        <NavLink
          key={n.to}
          to={n.to}
          className={({ isActive }) =>
            cn(
              "flex items-center gap-2 rounded-md px-3 py-2 text-sm transition",
              isActive
                ? "bg-accent/10 text-accent"
                : "text-muted-foreground hover:bg-muted hover:text-foreground"
            )
          }
        >
          <n.icon className="h-4 w-4" />
          <span>{n.label}</span>
        </NavLink>
      ))}
    </nav>
  );
}

function MobileMenu() {
  const [open, setOpen] = useState(false);
  if (!open) {
    return (
      <button type="button" onClick={() => setOpen(true)} className="btn-ghost md:hidden" aria-label="Open menu">
        <Menu className="h-4 w-4" />
      </button>
    );
  }
  return (
    <>
      <div className="fixed inset-0 z-40 bg-black/50 md:hidden" onClick={() => setOpen(false)} />
      <aside className="fixed left-0 top-0 z-50 h-full w-64 border-r bg-card p-3 md:hidden">
        <div className="mb-3 flex items-center justify-between">
          <span className="font-semibold text-accent">Menu</span>
          <button type="button" onClick={() => setOpen(false)} className="btn-ghost" aria-label="Close menu">
            <X className="h-4 w-4" />
          </button>
        </div>
        {NAV.map((n) => (
          <NavLink
            key={n.to}
            to={n.to}
            onClick={() => setOpen(false)}
            className={({ isActive }) =>
              cn(
                "flex items-center gap-2 rounded-md px-3 py-2 text-sm",
                isActive ? "bg-accent/10 text-accent" : "text-muted-foreground hover:bg-muted hover:text-foreground"
              )
            }
          >
            <n.icon className="h-4 w-4" />
            <span>{n.label}</span>
          </NavLink>
        ))}
      </aside>
    </>
  );
}
