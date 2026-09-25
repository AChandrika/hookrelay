import type {
  DeliveryDetail,
  DeliveryRow,
  DeliveryStatus,
  Diagnosis,
  Endpoint,
  EventRecord,
  EventSummary,
  Page,
  Stats,
} from "./types";

// The API key lives in sessionStorage: it survives a page refresh but is
// cleared when the tab closes. Any script running on the page could read it
// (XSS), which is acceptable for a self-hosted admin tool; a multi-user product
// would use an HttpOnly session cookie issued by a login endpoint instead.
const KEY = "hookrelay.apiKey";

export const auth = {
  get: () => sessionStorage.getItem(KEY),
  set: (key: string) => sessionStorage.setItem(KEY, key),
  clear: () => sessionStorage.removeItem(KEY),
};

export class ApiError extends Error {
  status: number;
  code: string;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function request<T>(path: string, method = "GET"): Promise<T> {
  const headers: Record<string, string> = {};
  const key = auth.get();
  if (key) headers.Authorization = `Bearer ${key}`;

  const res = await fetch(path, { method, headers });
  if (!res.ok) {
    let code = "http_error";
    let message = `Request failed with status ${res.status}.`;
    try {
      const body = await res.json();
      code = body.error?.code ?? code;
      message = body.error?.message ?? message;
    } catch {
      // Not JSON (e.g. the proxy couldn't reach the API). Keep the generic message.
    }
    if (res.status === 401) {
      auth.clear();
      window.dispatchEvent(new Event("hookrelay:signed-out"));
    }
    throw new ApiError(res.status, code, message);
  }
  return (await res.json()) as T;
}

type Query = Record<string, string | undefined>;

function qs(params: Query): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v) p.set(k, v);
  const s = p.toString();
  return s ? `?${s}` : "";
}

export type DeliveryQuery = { status?: DeliveryStatus; event_id?: string; cursor?: string };

export const api = {
  stats: () => request<Stats>("/v1/stats"),
  events: (cursor?: string) => request<Page<EventSummary>>(`/v1/events${qs({ cursor })}`),
  event: (id: string) => request<{ event: EventRecord }>(`/v1/events/${id}`),
  deliveries: (q: DeliveryQuery) => request<Page<DeliveryRow>>(`/v1/deliveries${qs(q)}`),
  delivery: (id: string) => request<DeliveryDetail>(`/v1/deliveries/${id}`),
  replay: (id: string) => request<{ status: string }>(`/v1/deliveries/${id}/replay`, "POST"),
  endpoints: () => request<{ data: Endpoint[] }>("/v1/endpoints"),
  enableEndpoint: (id: string) => request<Endpoint>(`/v1/endpoints/${id}/enable`, "POST"),
  replayFailed: (id: string) => request<{ replayed: number }>(`/v1/endpoints/${id}/replay-failed`, "POST"),
  diagnose: (id: string) => request<Diagnosis>(`/v1/deliveries/${id}/diagnose`, "POST"),
  /** The latest diagnosis, or null if the delivery has never been diagnosed. */
  diagnosis: async (id: string): Promise<Diagnosis | null> => {
    try {
      return await request<Diagnosis>(`/v1/deliveries/${id}/diagnosis`);
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) return null;
      throw e;
    }
  },
};
