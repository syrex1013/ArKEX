"use client";

import { createContext, type ReactNode, useContext } from "react";

import { type UseEventStreamResult, useEventStream } from "@/hooks/use-event-stream";
import type { LogLine } from "@/lib/types";

interface LiveStreamContextValue {
  log: UseEventStreamResult<LogLine>;
}

const LiveStreamContext = createContext<LiveStreamContextValue | null>(null);

/** Reconnect window shared by every console view so a page swap never drops the tail. */
export const LOG_STREAM_MAX_EVENTS = 800;
export const LOG_STREAM_FLUSH_MS = 150;

/**
 * Owns the console's shared backend-log subscription (`/api/logs/stream`).
 * Mounted once per authenticated shell; views read it through `useLiveLog()`.
 */
export function LiveStreamProvider({ children }: { children: ReactNode }) {
  const log = useEventStream<LogLine>({
    path: "/api/logs/stream",
    maxEvents: LOG_STREAM_MAX_EVENTS,
    flushMs: LOG_STREAM_FLUSH_MS,
    // Log frames carry no `id:` line; `seq` is the sink's monotonic counter and
    // is present on replays and live frames alike, so it is the safer identity.
    dedupeKey: (line) => (line.seq != null ? `seq:${line.seq}` : `db:${line.db_id}`),
  });

  return <LiveStreamContext.Provider value={{ log }}>{children}</LiveStreamContext.Provider>;
}

export function useLiveLog(): UseEventStreamResult<LogLine> {
  const ctx = useContext(LiveStreamContext);
  if (!ctx) throw new Error("useLiveLog must be used inside <LiveStreamProvider>");
  return ctx.log;
}
