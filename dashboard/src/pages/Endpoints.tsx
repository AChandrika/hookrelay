import { useState } from "react";
import { api } from "../api";
import { Empty, ErrorNote, Loading, notify, PageHeader } from "../components";
import { useAsync } from "../lib/hooks";

export default function Endpoints() {
  const { data, error, reload } = useAsync(() => api.endpoints(), []);
  const [busy, setBusy] = useState<string | null>(null);

  async function run(id: string, action: () => Promise<string>) {
    setBusy(id);
    try {
      notify(await action());
      reload();
    } catch (e) {
      notify(e instanceof Error ? e.message : "Something went wrong.", "bad");
    } finally {
      setBusy(null);
    }
  }

  const enable = (id: string) =>
    run(id, async () => {
      await api.enableEndpoint(id);
      return "Endpoint re-enabled. Replay its failed deliveries when the receiver is ready.";
    });

  const replayFailed = (id: string) =>
    run(id, async () => {
      const { replayed } = await api.replayFailed(id);
      if (replayed === 0) return "This endpoint has no failed deliveries.";
      return replayed === 1 ? "Replay queued for 1 delivery." : `Replay queued for ${replayed} deliveries.`;
    });

  if (!data) return error ? <ErrorNote error={error} onRetry={reload} /> : <Loading />;

  return (
    <>
      <PageHeader title="Endpoints" />
      {data.data.length === 0 ? (
        <Empty>
          <p>No endpoints yet.</p>
          <p className="muted">Register one with POST /v1/endpoints and it will be listed here.</p>
        </Empty>
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th scope="col">URL</th>
                <th scope="col">Receives</th>
                <th scope="col">Status</th>
                <th scope="col">
                  <span className="visually-hidden">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {data.data.map((ep) => (
                <tr key={ep.id}>
                  <td className="cell-url">{ep.url}</td>
                  <td>{ep.event_types.length ? ep.event_types.join(", ") : "All events"}</td>
                  <td>
                    {ep.enabled ? (
                      <span className="badge badge-ok">Active</span>
                    ) : (
                      <>
                        <span className="badge badge-bad">Disabled</span>
                        {ep.disabled_reason && <p className="cell-note">{ep.disabled_reason}</p>}
                      </>
                    )}
                  </td>
                  <td className="cell-action">
                    {ep.enabled ? (
                      <button
                        type="button"
                        className="button-quiet"
                        onClick={() => void replayFailed(ep.id)}
                        disabled={busy === ep.id}
                      >
                        Replay failed deliveries
                      </button>
                    ) : (
                      <button
                        type="button"
                        className="button-quiet"
                        onClick={() => void enable(ep.id)}
                        disabled={busy === ep.id}
                      >
                        Re-enable
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
