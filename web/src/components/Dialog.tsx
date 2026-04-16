import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { AlertTriangle, X } from "lucide-react";

type Variant = "default" | "danger";

export type ConfirmOptions = {
  title: string;
  description?: React.ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  variant?: Variant;
};

type Resolver = (ok: boolean) => void;
type ConfirmFn = (opts: ConfirmOptions) => Promise<boolean>;

const ConfirmCtx = createContext<ConfirmFn | null>(null);

export function ConfirmProvider({ children }: { children: React.ReactNode }) {
  const [state, setState] = useState<ConfirmOptions | null>(null);
  const resolverRef = useRef<Resolver | null>(null);

  const confirm = useCallback<ConfirmFn>((opts) => {
    return new Promise<boolean>((resolve) => {
      resolverRef.current = resolve;
      setState(opts);
    });
  }, []);

  const close = useCallback((ok: boolean) => {
    resolverRef.current?.(ok);
    resolverRef.current = null;
    setState(null);
  }, []);

  return (
    <ConfirmCtx.Provider value={confirm}>
      {children}
      {state && <ConfirmModal opts={state} onResolve={close} />}
    </ConfirmCtx.Provider>
  );
}

export function useConfirmDialog(): ConfirmFn {
  const ctx = useContext(ConfirmCtx);
  if (!ctx) throw new Error("useConfirmDialog must be used inside <ConfirmProvider>");
  return ctx;
}

function ConfirmModal({ opts, onResolve }: { opts: ConfirmOptions; onResolve: (ok: boolean) => void }) {
  const confirmRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    confirmRef.current?.focus();
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onResolve(false);
      else if (e.key === "Enter") onResolve(true);
    }
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      prev?.focus?.();
    };
  }, [onResolve]);

  const danger = opts.variant === "danger";
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      role="dialog"
      aria-modal="true"
      aria-labelledby="confirm-title"
      onMouseDown={(e) => { if (e.target === e.currentTarget) onResolve(false); }}
    >
      <div className="card w-full max-w-md animate-[fadeIn_150ms_ease-out] p-5">
        <div className="flex items-start gap-3">
          {danger && <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-destructive" />}
          <div className="flex-1">
            <h2 id="confirm-title" className="text-sm font-semibold">{opts.title}</h2>
            {opts.description && (
              <div className="mt-1.5 text-xs text-muted-foreground">{opts.description}</div>
            )}
          </div>
          <button
            type="button"
            onClick={() => onResolve(false)}
            className="rounded p-1 text-muted-foreground hover:bg-muted"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="mt-5 flex justify-end gap-2">
          <button type="button" className="btn-ghost text-xs" onClick={() => onResolve(false)}>
            {opts.cancelLabel || "Cancel"}
          </button>
          <button
            ref={confirmRef}
            type="button"
            className={danger ? "btn-danger text-xs" : "btn-accent text-xs"}
            onClick={() => onResolve(true)}
          >
            {opts.confirmLabel || "Confirm"}
          </button>
        </div>
      </div>
    </div>
  );
}
