// These mirror the JSON returned by the Go API (internal/store).

export type DeliveryStatus = "pending" | "in_flight" | "succeeded" | "dead";

export interface Page<T> {
  data: T[];
  next_cursor?: string;
}

export interface Endpoint {
  id: string;
  url: string;
  event_types: string[];
  enabled: boolean;
  disabled_reason?: string;
  created_at: string;
}

export interface EventRecord {
  id: string;
  event_type: string;
  payload: unknown;
  idempotency_key?: string;
  created_at: string;
}

export interface EventSummary {
  id: string;
  event_type: string;
  created_at: string;
  total: number;
  succeeded: number;
  failed: number;
  in_progress: number;
}

export interface DeliveryRow {
  id: string;
  event_id: string;
  event_type: string;
  endpoint_id: string;
  endpoint_url: string;
  status: DeliveryStatus;
  attempt_count: number;
  next_attempt_at: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
}

export interface Attempt {
  attempt_number: number;
  started_at: string;
  duration_ms: number;
  response_status?: number;
  response_headers?: Record<string, string>;
  response_body?: string;
  error?: string;
}

export interface DeliveryDetail {
  delivery: DeliveryRow;
  event: EventRecord;
  attempts: Attempt[]; // newest first
}

export interface Stats {
  hourly: { hour: string; delivered: number; failed: number }[];
  by_status: Record<DeliveryStatus, number>;
}

export interface DiagnosisResult {
  category: string;
  summary: string;
  likely_cause: string;
  suggested_fix: string;
  confidence: "low" | "medium" | "high";
  evidence: string[];
}

export interface Diagnosis {
  id: string;
  delivery_id: string;
  status: "pending" | "running" | "done" | "failed";
  model?: string;
  prompt_version?: string;
  result?: DiagnosisResult;
  note?: string;
  cached: boolean;
  duration_ms?: number;
  prompt_tokens?: number;
  completion_tokens?: number;
  created_at: string;
  updated_at: string;
}
