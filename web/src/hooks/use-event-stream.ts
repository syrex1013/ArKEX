"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  type EventStreamHandle,
  openEventStream,
  type StreamEvent,
  type StreamState,
  type StreamStatus,
} from "@/lib/sse";

export interface UseEventStreamOptions<T> {
  /** App path, e.g. `/api/logs/stream`. Falsy disables the stream. */
  path: string | null | undefined;
  query?: Record<string, string | number | null | undefined>;
  enabled?: boolean;
  /** Ring-buffer size. Older events are dropped from the returned array. */
  maxEvents?: number;
  parse?: (raw: string) => T | null;
  /** Called for every accepted (non-duplicate) event, outside React state. */
  onEvent?: (event: StreamEvent<T>) => void;
  /**
   * Stable identity for endpoints whose frames carry no `id:` line (the log
   * stream sends `{DBID,seq,...}` with no id). Replays after a reconnect or a
   * StrictMode double-mount would otherwise append the same rows twice.
   */
  dedupeKey?: (data: T) => string | null | undefined;
  /** Render coalescing window for `events`, in ms. */
  flushMs?: number;
}

export interface UseEventStreamResult<T> {
  events: StreamEvent<T>[];
  status: StreamStatus;
  attempt: number;
  lastEventAt: number | null;
  lastError: string | null;
  /** Events received since mount (not capped). */
  total: number;
  clear: () => void;
  /** Manually drop the connection and reconnect from scratch. */
  restart: () => void;
}

/**
 * Subscribe to one backend SSE endpoint.
 *
 * Events are funnelled through a pending buffer and committed to React state on
 * a short timer, so a burst of hundreds of frames per second costs one render
 * per window instead of one render per frame.
 */
export function useEventStream<T>({
  path,
  query,
  enabled = true,
  maxEvents = 400,
  parse,
  onEvent,
  dedupeKey,
  flushMs = 120,
}: UseEventStreamOptions<T>): UseEventStreamResult<T> {
  const [events, setEvents] = useState<StreamEvent<T>[]>([]);
  const [state, setState] = useState<StreamState>({
    status: "idle",
    attempt: 0,
    lastEventAt: null,
    lastError: null,
  });
  const [total, setTotal] = useState(0);
  const [generation, setGeneration] = useState(0);

  const pendingRef = useRef<StreamEvent<T>[]>([]);
  const flushTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const onEventRef = useRef(onEvent);
  const parseRef = useRef(parse);
  const dedupeKeyRef = useRef(dedupeKey);
  const seenRef = useRef(new Set<string>());
  const seenOrderRef = useRef<string[]>([]);

  onEventRef.current = onEvent;
  parseRef.current = parse;
  dedupeKeyRef.current = dedupeKey;

  // Survives reconnects and StrictMode remounts: the server always replays a
  // backlog on connect, so identity must outlive the connection.
  const isDuplicate = useCallback((data: T) => {
    const key = dedupeKeyRef.current?.(data);
    if (!key) return false;
    if (seenRef.current.has(key)) return true;
    seenRef.current.add(key);
    seenOrderRef.current.push(key);
    if (seenOrderRef.current.length > 8000) {
      const dropped = seenOrderRef.current.splice(0, 2000);
      for (const old of dropped) seenRef.current.delete(old);
    }
    return false;
  }, []);

  // Serialize the query so a fresh object literal does not force a reconnect.
  const queryKey = useMemo(() => JSON.stringify(query ?? {}), [query]);

  const flush = useCallback(() => {
    flushTimerRef.current = null;
    const batch = pendingRef.current;
    if (batch.length === 0) return;
    pendingRef.current = [];
    setEvents((prev) => {
      const next = prev.concat(batch);
      return next.length > maxEvents ? next.slice(next.length - maxEvents) : next;
    });
  }, [maxEvents]);

  const scheduleFlush = useCallback(() => {
    if (flushTimerRef.current !== null) return;
    flushTimerRef.current = setTimeout(flush, flushMs);
  }, [flush, flushMs]);

  useEffect(() => {
    if (!path || !enabled) return;
    // Read only to re-subscribe when `restart()` bumps the generation counter.
    void generation;
    let handle: EventStreamHandle | null = null;

    handle = openEventStream<T>({
      path,
      query: queryKey ? (JSON.parse(queryKey) as Record<string, string | number | null | undefined>) : undefined,
      parse: parseRef.current,
      onEvent: (event) => {
        if (isDuplicate(event.data)) return;
        onEventRef.current?.(event);
        pendingRef.current.push(event);
        setTotal((n) => n + 1);
        scheduleFlush();
      },
      onState: (next) => setState(next),
    });

    return () => {
      handle?.close();
      handle = null;
      if (flushTimerRef.current !== null) {
        clearTimeout(flushTimerRef.current);
        flushTimerRef.current = null;
      }
      pendingRef.current = [];
      setState({ status: "closed", attempt: 0, lastEventAt: null, lastError: null });
    };
  }, [path, enabled, queryKey, scheduleFlush, isDuplicate, generation]);

  const clear = useCallback(() => {
    pendingRef.current = [];
    seenRef.current.clear();
    seenOrderRef.current = [];
    setEvents([]);
    setTotal(0);
  }, []);

  const restart = useCallback(() => setGeneration((g) => g + 1), []);

  return {
    events,
    status: state.status,
    attempt: state.attempt,
    lastEventAt: state.lastEventAt,
    lastError: state.lastError,
    total,
    clear,
    restart,
  };
}
