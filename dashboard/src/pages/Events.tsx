import { api } from "../api";
import { Empty, ErrorNote, LoadMore, Loading, PageHeader } from "../components";
import { relativeTime, absoluteTime, shortId } from "../lib/format";
import { usePaged } from "../lib/hooks";
import type { EventSummary } from "../types";

function summary(e: EventSummary): string {
  if (e.total === 0) return "No endpoint subscribed";
  const parts = [`${e.succeeded} of ${e.total} delivered`];
  if (e.failed) parts.push(`${e.failed} failed`);
  if (e.in_progress) parts.push(`${e.in_progress} in progress`);
  return parts.join(", ");
}

export default function Events() {
  const { items, error, hasMore, loadingMore, loadMore, refresh } = usePaged((c) => api.events(c), []);

  return (
    <>
      <PageHeader title="Events">
        <button type="button" className="button-quiet" onClick={() => void refresh()}>
          Refresh
        </button>
      </PageHeader>
      {error && <ErrorNote error={error} onRetry={() => void refresh()} />}
      {items === null ? (
        !error && <Loading />
      ) : items.length === 0 ? (
        <Empty>
          <p>No events yet.</p>
          <p className="muted">Events appear here as soon as your app publishes them with POST /v1/events.</p>
        </Empty>
      ) : (
        <>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th scope="col">Event</th>
                  <th scope="col">Deliveries</th>
                  <th scope="col">Received</th>
                  <th scope="col">ID</th>
                </tr>
              </thead>
              <tbody>
                {items.map((e) => (
                  <tr key={e.id} className={e.failed ? "row-attention" : undefined}>
                    <td>
                      <a href={`#/events/${e.id}`}>{e.event_type}</a>
                    </td>
                    <td>{summary(e)}</td>
                    <td title={absoluteTime(e.created_at)}>{relativeTime(e.created_at)}</td>
                    <td>
                      <code>{shortId(e.id)}</code>
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
