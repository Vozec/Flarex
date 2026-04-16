import React from "react";

type State = { err: Error | null };

// ErrorBoundary catches React render errors so a bad component can't
// nuke the whole SPA. Fallback is a compact panel with the error + a
// "reload" action. Dev gets a stack trace; prod just the message.
export default class ErrorBoundary extends React.Component<
  { children: React.ReactNode },
  State
> {
  state: State = { err: null };

  static getDerivedStateFromError(err: Error): State {
    return { err };
  }

  componentDidCatch(err: Error, info: React.ErrorInfo) {
    console.error("ui error:", err, info.componentStack);
  }

  render() {
    if (!this.state.err) return this.props.children;
    return (
      <div className="m-6 rounded-lg border border-destructive/40 bg-destructive/5 p-6">
        <h2 className="text-destructive font-semibold">Something broke in the UI.</h2>
        <pre className="mt-3 overflow-auto text-xs text-destructive/80">
          {this.state.err.stack ?? this.state.err.message}
        </pre>
        <button className="btn-ghost mt-4" onClick={() => location.reload()}>
          Reload
        </button>
      </div>
    );
  }
}
