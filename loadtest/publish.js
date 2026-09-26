// Load test: publish events at a fixed rate and measure the API.
//
//   docker compose run --rm k6 run /scripts/publish.js
//   docker compose run --rm -e RATE=500 -e DURATION=2m k6 run /scripts/publish.js
//
// Start the API with PUBLISH_LIMIT_PER_MINUTE=0 first, or the per-tenant rate
// limit (600/min) turns most requests into 429s. Watch delivery throughput and
// lag in Grafana while it runs: k6 measures accepting events, Grafana shows
// the workers delivering them.
import http from "k6/http";
import { check } from "k6";

const API = __ENV.API_URL || "http://api:8080";
const ADMIN = __ENV.ADMIN_TOKEN || "dev-admin-token";
const RECEIVER = __ENV.RECEIVER_URL || "http://mockreceiver:8090/ok";
const RATE = Number(__ENV.RATE || 200); // events per second
const DURATION = __ENV.DURATION || "1m";

export const options = {
  scenarios: {
    // constant-arrival-rate keeps sending RATE requests per second even if the
    // API slows down, which is how real traffic behaves. (A fixed number of
    // looping users would quietly send less when the server gets slow.)
    publish: {
      executor: "constant-arrival-rate",
      rate: RATE,
      timeUnit: "1s",
      duration: DURATION,
      preAllocatedVUs: 50,
      maxVUs: 500,
    },
  },
  thresholds: {
    "http_req_failed{name:publish}": ["rate<0.01"],
    "http_req_duration{name:publish}": ["p(95)<250", "p(99)<500"],
  },
};

export function setup() {
  const admin = { headers: { "Content-Type": "application/json", Authorization: `Bearer ${ADMIN}` } };
  const t = http.post(`${API}/v1/tenants`, JSON.stringify({ name: `loadtest-${Date.now()}` }), admin);
  if (t.status !== 201) throw new Error(`creating tenant failed: HTTP ${t.status} ${t.body}`);

  const headers = { "Content-Type": "application/json", Authorization: `Bearer ${t.json("api_key")}` };
  const ep = http.post(`${API}/v1/endpoints`, JSON.stringify({ url: RECEIVER }), { headers });
  if (ep.status !== 201) throw new Error(`creating endpoint failed: HTTP ${ep.status} ${ep.body}`);
  return { headers };
}

export default function (data) {
  const body = JSON.stringify({
    event_type: "load.test",
    payload: { vu: __VU, iteration: __ITER, sent_at: Date.now() },
  });
  const res = http.post(`${API}/v1/events`, body, { headers: data.headers, tags: { name: "publish" } });
  check(res, { "status is 201": (r) => r.status === 201 });
}
