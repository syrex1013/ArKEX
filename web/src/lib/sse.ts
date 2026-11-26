/**
 * Server-sent-events client.
 *
 * The Go backend exposes three real SSE endpoints, each with its own cursor
 * convention but a shared framing contract (`id:` + `data:` frames, 20s `: ping`
 * heartbeats, token auth):
 *
 *   /api/logs/stream                    ?since=<seq>   LOG    {DBID,TS,Level,Tag,Text}
 *   /api/exploration/activity/stream    ?task=&intent=&since=<id>   activity DTO
 *   /api/tasks/{id}/swarm/stream        (replays last 50, id-deduped)  SystemEvent
 *
 * EventSource cannot set headers, so the bearer token travels as `?token=`,
 * which `extractToken` on the server accepts alongside the auth cookie.
 *
 * Responsibilities kept here (not in components):
 *  - reconnect with jittered exponential backoff
 *  - resume from the last seen cursor (`since`) because a fresh EventSource
 *    does not inherit the browser's Last-Event-ID
 *  - drop replayed frames after reconnect (all endpoints replay a backlog)
 *  - surface an explicit connection state machine instead of guessing
 */

import { sseUrl } from "@/lib/api";

export type StreamStatus = "idle" | "connecting" | "open" | "reconnecting" | "closed";

export interface StreamEvent<T> {
  /** Frame id when the server sent one; null for frames without `id:`. */
  id: string | null;
  data: T;
  /** Client receive timestamp (ms) — server timestamps stay inside `data`. */
  receivedAt: number;
}

export interface StreamState {
  status: StreamStatus;
  attempt: number;
  lastEventAt: number | null;
  lastError: string | null;
}

export interface EventStreamOptions<T> {
  /** Absolute app path, e.g. `/api/logs/stream`. */
  path: string;
  query?: Record<string, string | number | null | undefined>;
  /** Parse a raw frame payload. Return null to skip the frame. Defaults to JSON.parse. */
  parse?: (raw: string) => T | null;
  onEvent: (event: StreamEvent<T>) => void;
  onState?: (state: StreamState) => void;
  /** How many event ids to remember for replay suppression. Default 4000. */
  dedupeCapacity?: number;
  baseDelayMs?: number;
  maxDelayMs?: number;
  /** Give up after this many consecutive failures. Default: never. */
  maxAttempts?: number;
}

export interface EventStreamHandle {
  close: () => void;
  state: () => StreamState;
}

export function buildStreamUrl(path: string, query: EventStreamOptions<unknown>["query"]): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value === null || value === undefined || value === "") continue;
    params.set(key, String(value));
  }
  const qs = params.toString();
  // sseUrl owns the base resolution (same-origin in production, direct backend in
  // dev, NEXT_PUBLIC_SSE_BASE override) and attaches the auth token when the
  // resolved origin is cross-site.
  return sseUrl(qs ? `${path}?${qs}` : path);
}

/**
 * Open a resilient SSE connection. Returns a handle whose `close()` is safe to
 * call at any time (including from a React cleanup during a pending retry).
 */
export function openEventStream<T>(options: EventStreamOptions<T>): EventStreamHandle {
  const {
    path,
    query,
    parse = (raw: string) => JSON.parse(raw) as T,
    onEvent,
    onState,
    dedupeCapacity = 4000,
    baseDelayMs = 800,
    maxDelayMs = 20_000,
    maxAttempts = Number.POSITIVE_INFINITY,
  } = options;

  const seen = new Set<string>();
  const seenOrder: string[] = [];

  let source: EventSource | null = null;
  let retryTimer: ReturnType<typeof setTimeout> | null = null;
  let attempt = 0;
  let cursor: string | null = null;
  let closed = false;

  const state: StreamState = { status: "idle", attempt: 0, lastEventAt: null, lastError: null };

  const emitState = (patch: Partial<StreamState>) => {
    Object.assign(state, patch);
    state.attempt = attempt;
    onState?.({ ...state });
  };

  const remember = (id: string) => {
    if (seen.has(id)) return false;
    seen.add(id);
    seenOrder.push(id);
    if (seenOrder.length > dedupeCapacity) {
      const evicted = seenOrder.shift();
      if (evicted) seen.delete(evicted);
    }
    return true;
  };

  const scheduleReconnect = () => {
    if (closed) return;
    attempt += 1;
    if (attempt > maxAttempts) {
      emitState({ status: "closed", lastError: state.lastError ?? "stream retries exhausted" });
      return;
    }
    const exponential = Math.min(maxDelayMs, baseDelayMs * 2 ** Math.min(attempt - 1, 6));
    const jittered = Math.round(exponential * (0.7 + Math.random() * 0.6));
    emitState({ status: "reconnecting" });
    retryTimer = setTimeout(connect, jittered);
  };

  const handleFrame = (event: MessageEvent<string>, explicitId: string | null) => {
    const id = explicitId && explicitId !== "" ? explicitId : null;
    if (id) {
      if (!remember(id)) return;
      cursor = id;
    }
    let data: T | null;
    try {
      data = parse(event.data);
    } catch {
      return;
    }
    if (data === null) return;
    state.lastEventAt = Date.now();
    onEvent({ id, data, receivedAt: state.lastEventAt });
  };

  function connect() {
    if (closed) return;
    retryTimer = null;
    emitState({ status: attempt === 0 ? "connecting" : "reconnecting" });

    const url = buildStreamUrl(path, { ...query, since: cursor ?? query?.since });

    let es: EventSource;
    try {
      es = new EventSource(url);
    } catch (error) {
      emitState({ lastError: error instanceof Error ? error.message : String(error) });
      scheduleReconnect();
      return;
    }
    source = es;

    es.onopen = () => {
      // A successful handshake means the transport works; the cursor is only
      // reset after data arrives, so a flapping proxy still resumes correctly.
      attempt = 0;
      emitState({ status: "open", lastError: null });
    };

    es.onmessage = (event) => handleFrame(event, event.lastEventId);

    es.onerror = () => {
      if (closed) return;
      // 401 is terminal: retrying with the same dead session just spins.
      if (es.readyState === EventSource.CLOSED) {
        emitState({ status: "closed", lastError: state.lastError });
        return;
      }
      emitState({ lastError: "stream interrupted" });
      es.close();
      source = null;
      scheduleReconnect();
    };
  }

  connect();

  return {
    close: () => {
      closed = true;
      if (retryTimer) clearTimeout(retryTimer);
      source?.close();
      source = null;
      emitState({ status: "closed" });
    },
    state: () => ({ ...state }),
  };
}

/** SSE endpoint paths, kept in one place so call sites cannot drift. */
export const STREAMS = {
  logs: (since?: number | string) => ({ path: "/api/logs/stream", query: { since } }),
  activity: (task: string, intent?: string, since?: number | string) => ({
    path: "/api/exploration/activity/stream",
    query: { task, intent, since },
  }),
  swarm: (task: string) => ({ path: `/api/tasks/${encodeURIComponent(task)}/swarm/stream` }),
} as const;
