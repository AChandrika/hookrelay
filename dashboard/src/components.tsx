import { useEffect, useState, type ReactNode } from "react";
import type { DeliveryStatus } from "./types";
import { statusLabel } from "./lib/format";

export function StatusBadge({ status, attempts }: { status: DeliveryStatus; attempts: number }) {
  const tone =
    status === "succeeded" ? "ok" : status === "dead" ? "bad" : status === "pending" && attempts > 0 ? "warn" : "info";
  return <span className={`badge badge-${tone}`}>{statusLabel(status, attempts)}</span>;
}

export function PageHeader({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <header className="page-header">
      <h1>{title}</h1>
      {children && <div className="page-actions">{children}</div>}
    </header>
  );
}

export function ErrorNote({ error, onRetry }: { error: Error; onRetry?: () => void }) {
  return (
    <div className="note note-error" role="alert">
      <p>{error.message}</p>
      {onRetry && (
        <button type="button" className="button-quiet" onClick={onRetry}>
          Try again
        </button>
      )}
    </div>
  );
}

export function Loading() {
  return <p className="muted">Loading…</p>;
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>;
}

export function LoadMore({ hasMore, loading, onClick }: { hasMore: boolean; loading: boolean; onClick: () => void }) {
  if (!hasMore) return null;
  return (
    <div className="load-more">
      <button type="button" className="button-quiet" onClick={onClick} disabled={loading}>
        {loading ? "Loading…" : "Show older"}
      </button>
    </div>
  );
}

// ---------- Toasts ----------

type Toast = { id: number; text: string; tone: "ok" | "bad" };
let nextToastId = 1;

export function notify(text: string, tone: Toast["tone"] = "ok") {
  window.dispatchEvent(new CustomEvent<Toast>("hookrelay:toast", { detail: { id: nextToastId++, text, tone } }));
}

export function Toaster() {
  const [toasts, setToasts] = useState<Toast[]>([]);
  useEffect(() => {
    const onToast = (e: Event) => {
      const t = (e as CustomEvent<Toast>).detail;
      setToasts((ts) => [...ts, t]);
      window.setTimeout(() => setToasts((ts) => ts.filter((x) => x.id !== t.id)), 5000);
    };
    window.addEventListener("hookrelay:toast", onToast);
    return () => window.removeEventListener("hookrelay:toast", onToast);
  }, []);
  return (
    <div className="toasts" role="status" aria-live="polite">
      {toasts.map((t) => (
        <div key={t.id} className={`toast toast-${t.tone}`}>
          {t.text}
        </div>
      ))}
    </div>
  );
}
