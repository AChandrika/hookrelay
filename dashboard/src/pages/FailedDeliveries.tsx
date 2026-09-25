import { useState } from "react";
import { api } from "../api";
import { Empty, ErrorNote, LoadMore, Loading, notify, PageHeader } from "../components";
import { absoluteTime, relativeTime } from "../lib/format";
import { usePaged } from "../lib/hooks";

// This is the dead-letter queue: deliveries that used up every retry.
export default function FailedDeliveries() {
  const { items, setItems, error, hasMore, loadingMore, loadMore, refresh } = usePaged(
    (cursor) => api.deliveries({ status: "dead", cursor }),
    [],
  );
  const [replaying, setReplaying] = useState<string | null>(null);

  async function replay(id: string) {
    setReplaying(id);
    try {
      await api.replay(id);
      setItems((prev) => (prev ? prev.filter((d) => d.id !== id) : prev));
      notify("Replay queued.");
    } catch (e) {
      notify(e instanceof Error ? e.message : "Replay failed.", "bad");
    } finally {
      setReplaying(null);
    }
  }

  return (
    <>
      <PageHeader title="Failed deliveries">
        <button type="button" className="button-quiet" onClick={() => void refresh()}>
          Refresh
        </button>
      </PageHeader>
      <p className="lede">
        These deliveries used up every retry. Fix the receiver, then replay them to send each one again.
      </p>
      {error && <ErrorNote error={error} onRetry={() => void refresh()} />}
      {items === null ? (
        !error && <Loading />
      ) : items.length === 0 ? (
        <Empty>
          <p>No failed deliveries. Everything that was sent either arrived or is still retrying.</p>
        </Empty>
      ) : (
        <>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th scope="col">Endpoint</th>
                  <th scope="col">Event</th>
                  <th scope="col">Why it failed</th>
                  <th scope="col">Last tried</th>
                  <th scope="col">
                    <span className="visually-hidden">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {items.map((d) => (
                  <tr key={d.id}>
                    <td className="cell-url">
                      <a href={`#/deliveries/${d.id}`}>{d.endpoint_url}</a>
                    </td>
                    <td>{d.event_type}</td>
                    <td className="cell-note">{d.last_error}</td>
                    <td title={absoluteTime(d.updated_at)}>{relativeTime(d.updated_at)}</td>
                    <td className="cell-action">
                      <button
                        type="button"
                        className="button-quiet"
                        onClick={() => void replay(d.id)}
                        disabled={replaying === d.id}
                      >
                        {replaying === d.id ? "Replaying…" : "Replay"}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <LoadMore hasMore={hasMore} loading={loadingMore} onClick={() => void loadMore()} />
        </>
      )}
    </>
  );
}
