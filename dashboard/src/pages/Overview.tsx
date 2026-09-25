import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { api } from "../api";
import { ErrorNote, Loading, PageHeader } from "../components";
import { useAsync } from "../lib/hooks";
import type { DeliveryStatus } from "../types";

// Hex values here because SVG fill attributes don't reliably resolve CSS variables.
const OK = "#177245";
const BAD = "#B42318";

const SEGMENTS: { status: DeliveryStatus; label: string; tone: string }[] = [
  { status: "succeeded", label: "Delivered", tone: "ok" },
  { status: "pending", label: "Queued or retrying", tone: "warn" },
  { status: "in_flight", label: "Sending", tone: "info" },
  { status: "dead", label: "Failed", tone: "bad" },
];

export default function Overview() {
  const { data, error, reload } = useAsync(() => api.stats(), [], 10_000);

  if (!data) return error ? <ErrorNote error={error} onRetry={reload} /> : <Loading />;

  const counts = data.by_status;
  const total = SEGMENTS.reduce((n, s) => n + counts[s.status], 0);
  const hourly = data.hourly.map((h) => ({
    ...h,
    label: new Date(h.hour).toLocaleTimeString([], { hour: "numeric" }),
  }));
  const attempts = data.hourly.reduce((n, h) => n + h.delivered + h.failed, 0);
  const failedAttempts = data.hourly.reduce((n, h) => n + h.failed, 0);

  return (
    <>
      <PageHeader title="Overview" />
      {error && <ErrorNote error={error} onRetry={reload} />}

      <section className="section" aria-labelledby="mix-title">
        <h2 id="mix-title">Where every delivery stands</h2>
        {total === 0 ? (
          <p className="muted">Nothing delivered yet. Publish an event and it will show up here.</p>
        ) : (
          <>
            <div
              className="mix-bar"
              role="img"
              aria-label={SEGMENTS.map((s) => `${s.label}: ${counts[s.status]}`).join(", ")}
            >
              {SEGMENTS.filter((s) => counts[s.status] > 0).map((s) => (
                <span key={s.status} className={`tone-${s.tone}`} style={{ flexGrow: counts[s.status] }} />
              ))}
            </div>
            <dl className="mix-legend">
              {SEGMENTS.map((s) => (
                <div key={s.status}>
                  <dt>
                    <span className={`swatch tone-${s.tone}`} aria-hidden="true" />
                    {s.label}
                  </dt>
                  <dd>{counts[s.status].toLocaleString()}</dd>
                </div>
              ))}
            </dl>
            {counts.dead > 0 && (
              <p>
                <a href="#/failed">
                  Review {counts.dead === 1 ? "the failed delivery" : `${counts.dead} failed deliveries`}
                </a>
              </p>
            )}
          </>
        )}
      </section>

      <section className="section" aria-labelledby="hourly-title">
        <h2 id="hourly-title">Attempts in the last 24 hours</h2>
        <p className="muted">
          {attempts === 0
            ? "No attempts in the last 24 hours."
            : `${attempts.toLocaleString()} attempts, ${failedAttempts.toLocaleString()} of them failed.`}
        </p>
        <div className="chart">
          <ResponsiveContainer width="100%" height={240}>
            <BarChart data={hourly} margin={{ top: 8, right: 0, bottom: 0, left: -20 }}>
              <CartesianGrid vertical={false} stroke="#D5DBE3" />
              <XAxis dataKey="label" tickLine={false} axisLine={false} interval={3} fontSize={12} />
              <YAxis allowDecimals={false} tickLine={false} axisLine={false} fontSize={12} />
              <Tooltip cursor={{ fill: "rgba(31, 79, 209, 0.06)" }} />
              <Bar dataKey="delivered" name="Delivered" stackId="attempts" fill={OK} />
              <Bar dataKey="failed" name="Failed" stackId="attempts" fill={BAD} />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </section>
    </>
  );
}
