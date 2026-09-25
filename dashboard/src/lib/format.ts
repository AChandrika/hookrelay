import type { DeliveryStatus } from "../types";

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

export function relativeTime(iso: string, now = Date.now()): string {
  const diff = (new Date(iso).getTime() - now) / 1000;
  const abs = Math.abs(diff);
  if (abs < 45) return rtf.format(Math.round(diff), "second");
  if (abs < 45 * 60) return rtf.format(Math.round(diff / 60), "minute");
  if (abs < 22 * 3600) return rtf.format(Math.round(diff / 3600), "hour");
  return rtf.format(Math.round(diff / 86400), "day");
}

export function absoluteTime(iso: string): string {
  return new Date(iso).toLocaleString();
}

export function shortId(id: string): string {
  return id.slice(0, 8);
}

/** Status names as a user thinks about them, not as the database stores them. */
export function statusLabel(status: DeliveryStatus, attempts: number): string {
  switch (status) {
    case "succeeded":
      return "Delivered";
    case "in_flight":
      return "Sending";
    case "dead":
      return "Failed";
    case "pending":
      return attempts > 0 ? "Retrying" : "Queued";
  }
}

export function prettyJson(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

/** Pretty-print a response body if it's JSON; otherwise show it as sent. */
export function prettyBody(body: string): string {
  try {
    return JSON.stringify(JSON.parse(body), null, 2);
  } catch {
    return body;
  }
}
