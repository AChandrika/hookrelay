import { api } from "../api";
import { Empty, ErrorNote, Loading, PageHeader, StatusBadge } from "../components";
import { absoluteTime, prettyJson, relativeTime } from "../lib/format";
import { useAsync } from "../lib/hooks";
import type { DeliveryRow } from "../types";

function nextStep(d: DeliveryRow): string {
  switch (d.status) {
    case "succeeded":
      return "Done";
    case "in_flight":
      return "Sending now";
    case "dead":
      return d.last_error ?? "Stopped retrying";
    case "pending":
      return d.attempt_count > 0 ? `Next attempt ${relativeTime(d.next_attempt_at)}` : "Waiting for a worker";
  }
}

export default function EventDetail({ id }: { id: string }) {
  const ev = useAsync(() => api.event(id), [id]);
  const ds = useAsync(() => api.deliveries({ event_id: id }), [id], 3000);

  if (!ev.data) return ev.error ? <ErrorNote error={ev.error} onRetry={ev.reload} /> : <Loading />;
  const event = ev.data.event;

  return (
    <>
      <p className="crumb">
        <a href="#/events">Events</a>
      </p>
      <PageHeader title={event.event_type} />
      <p className="meta">
        Received <span title={absoluteTime(event.created_at)}>{relativeTime(event.created_at)}</span>
        <span className="meta-sep">ID <code>{event.id}</code></span>
        {event.idempotency_key && (
          <span className="meta-sep">
            Idempotency key <code>{event.idempotency_key}</code>
          </span>
        )}
      </p>

      <section className="section" aria-labelledby="deliveries-title">
        <h2 id="deliveries-title">Deliveries</h2>
        {ds.error && <ErrorNote error={ds.error} onRetry={ds.reload} />}
        {!ds.data ? (
          !ds.error && <Loading />
        ) : ds.data.data.length === 0 ? (
          <Empty>
            <p>No endpoint is subscribed to {event.event_type}, so nothing was sent.</p>
          </Empty>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th scope="col">Endpoint</th>
                  <th scope="col">Status</th>
                  <th scope="col">Attempts</th>
                  <th scope="col">Next step</th>
                </tr>
              </thead>
              <tbody>
                {ds.data.data.map((d) => (
                  <tr key={d.id}>
                    <td className="cell-url">
                      <a href={`#/deliveries/${d.id}`}>{d.endpoint_url}</a>
                    </td>
                    <td>
                      <StatusBadge status={d.status} attempts={d.attempt_count} />
                    </td>
                    <td className="num">{d.attempt_count}</td>
                    <td className="cell-note">{nextStep(d)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section className="section" aria-labelledby="payload-title">
        <h2 id="payload-title">Payload</h2>
        <pre className="code">{prettyJson(event.payload)}</pre>
      </section>
    </>
  );
}
