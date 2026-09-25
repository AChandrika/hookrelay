import { useState } from "react";
import { api } from "../api";
import { ErrorNote, Loading, notify, PageHeader, StatusBadge } from "../components";
import { absoluteTime, prettyBody, relativeTime } from "../lib/format";
import { useAsync } from "../lib/hooks";
import type { Attempt, DeliveryRow } from "../types";
import DiagnosisPanel from "./DiagnosisPanel";

// The delivery page reads like a parcel tracking page: the current state at
// the top, every attempt below it, newest first, back to when the event arrived.
export default function DeliveryDetail({ id }: { id: string }) {
  const { data, error, reload } = useAsync(() => api.delivery(id), [id], 3000);
  const [busy, setBusy] = useState(false);
  const [openAttempt, setOpenAttempt] = useState<number | null>(null);

  if (!data) return error ? <ErrorNote error={error} onRetry={reload} /> : <Loading />;
  const { delivery: d, event, attempts } = data;

  async function replay() {
    setBusy(true);
    try {
      await api.replay(d.id);
      notify("Replay queued. The first new attempt starts within a few seconds.");
      reload();
    } catch (e) {
      notify(e instanceof Error ? e.message : "Replay failed.", "bad");
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <p className="crumb">
        <a href={`#/events/${event.id}`}>{event.event_type} event</a>
      </p>
      <PageHeader title={d.endpoint_url}>
        {d.status === "dead" && (
          <button type="button" className="button-primary" onClick={replay} disabled={busy}>
            {busy ? "Replaying…" : "Replay delivery"}
          </button>
        )}
      </PageHeader>
      <p className="meta">
        <StatusBadge status={d.status} attempts={d.attempt_count} />
        <span className="meta-sep">
          {d.attempt_count} {d.attempt_count === 1 ? "attempt" : "attempts"}
        </span>
      </p>
      {error && <ErrorNote error={error} onRetry={reload} />}

      <DiagnosisPanel delivery={d} hasAttempts={attempts.length > 0} />

      <section className="section" aria-labelledby="history-title">
        <h2 id="history-title">Delivery history</h2>
        <ol className="track">
          <CurrentState d={d} />
          {attempts.map((a) => (
            <AttemptStop
              key={a.attempt_number}
              a={a}
              open={openAttempt === a.attempt_number}
              onToggle={() => setOpenAttempt(openAttempt === a.attempt_number ? null : a.attempt_number)}
            />
          ))}
          <li className="stop stop-origin">
            <span className="stop-dot" aria-hidden="true" />
            <div className="stop-body">
              <p className="stop-title">Event received</p>
              <p className="muted">{absoluteTime(event.created_at)}</p>
            </div>
          </li>
        </ol>
      </section>
    </>
  );
}

function CurrentState({ d }: { d: DeliveryRow }) {
  let tone = "info";
  let title = "Queued";
  let detail = "Waiting for a worker to pick it up.";

  if (d.status === "succeeded") {
    tone = "ok";
    title = "Delivered";
    detail = `The receiver accepted it on attempt ${d.attempt_count}.`;
  } else if (d.status === "dead") {
    tone = "bad";
    title = "Stopped retrying";
    detail = `${d.last_error ?? "All attempts failed."} Replaying sends it again with a fresh set of retries.`;
  } else if (d.status === "in_flight") {
    title = "Sending now";
    detail = "A worker is waiting for the receiver to respond.";
  } else if (d.attempt_count > 0) {
    tone = "warn";
    title = `Next attempt ${relativeTime(d.next_attempt_at)}`;
    detail = `Retrying because: ${d.last_error ?? "the last attempt failed"}.`;
  }

  return (
    <li className={`stop stop-current tone-border-${tone}`}>
      <span className={`stop-dot tone-${tone}`} aria-hidden="true" />
      <div className="stop-body">
        <p className="stop-title">{title}</p>
        <p className="muted">{detail}</p>
      </div>
    </li>
  );
}

function AttemptStop({ a, open, onToggle }: { a: Attempt; open: boolean; onToggle: () => void }) {
  const ok = a.response_status !== undefined && a.response_status >= 200 && a.response_status < 300;
  const panelId = `attempt-${a.attempt_number}`;
  const headers = a.response_headers ? Object.entries(a.response_headers) : [];

  return (
    <li className={`stop ${ok ? "stop-ok" : "stop-bad"}`}>
      <span className="stop-dot" aria-hidden="true" />
      <div className="stop-body">
        <button type="button" className="stop-toggle" aria-expanded={open} aria-controls={panelId} onClick={onToggle}>
          <span className="stop-title">Attempt {a.attempt_number}</span>
          <span className="stop-outcome">{a.response_status ? `HTTP ${a.response_status}` : "No response"}</span>
          <span className="muted">{a.duration_ms} ms</span>
          <span className="muted stop-time" title={absoluteTime(a.started_at)}>
            {relativeTime(a.started_at)}
          </span>
        </button>
        {a.error && <p className="stop-error">{a.error}</p>}
        {open && (
          <div id={panelId} className="inspector">
            <h3>Response headers</h3>
            {headers.length ? (
              <table className="kv">
                <tbody>
                  {headers.map(([k, v]) => (
                    <tr key={k}>
                      <th scope="row">{k}</th>
                      <td>{v}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              <p className="muted">None recorded.</p>
            )}
            <h3>Response body</h3>
            {a.response_body ? (
              <pre className="code">{prettyBody(a.response_body)}</pre>
            ) : (
              <p className="muted">Empty.</p>
            )}
          </div>
        )}
      </div>
    </li>
  );
}
