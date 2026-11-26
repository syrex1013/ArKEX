"use client";

import { useEffect, useState } from "react";

import { Activity, Radio } from "lucide-react";

import { type DotTone, StatusDot } from "@/components/arkex/primitives";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import type { StreamStatus } from "@/lib/sse";
import { cn } from "@/lib/utils";

export interface FeedHealth {
  id: string;
  label: string;
  status: StreamStatus;
  attempt?: number;
  lastEventAt?: number | null;
}

const STATUS_TONE: Record<StreamStatus, DotTone> = {
  idle: "idle",
  connecting: "warn",
  open: "live",
  reconnecting: "warn",
  closed: "error",
};

export function statusLabel(status: StreamStatus): string {
  switch (status) {
    case "open":
      return "Live";
    case "connecting":
      return "Connecting";
    case "reconnecting":
      return "Reconnecting";
    case "closed":
      return "Offline";
    default:
      return "Idle";
  }
}

/** Tick once a second so relative timestamps stay honest without re-rendering on data. */
function useNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [active]);
  return now;
}

export function formatAge(ts: number | null | undefined, now: number): string {
  if (!ts) return "no data";
  const seconds = Math.max(0, Math.round((now - ts) / 1000));
  if (seconds < 2) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  return `${Math.round(minutes / 60)}h ago`;
}

export function StreamStatusPill({
  status,
  attempt,
  lastEventAt,
  className,
}: {
  status: StreamStatus;
  attempt?: number;
  lastEventAt?: number | null;
  className?: string;
}) {
  const now = useNow(status === "open");
  const tone = STATUS_TONE[status];
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-sm border border-hairline bg-surface-2 px-1.5 py-1 font-medium text-[0.6875rem] text-muted-foreground",
        className,
      )}
      title={lastEventAt ? `Last event ${formatAge(lastEventAt, now)}` : undefined}
    >
      <StatusDot tone={tone} pulse={status === "open"} />
      <span className="text-foreground">{statusLabel(status)}</span>
      {status === "reconnecting" && attempt ? <span className="tnum opacity-70">#{attempt}</span> : null}
    </span>
  );
}

/** Worst-status-wins aggregation across the feeds a view depends on. */
export function aggregateHealth(feeds: FeedHealth[]): { tone: DotTone; label: string } {
  if (feeds.length === 0) return { tone: "off", label: "No feeds" };
  if (feeds.some((f) => f.status === "closed")) return { tone: "error", label: "Degraded" };
  if (feeds.some((f) => f.status === "reconnecting")) return { tone: "warn", label: "Reconnecting" };
  if (feeds.some((f) => f.status === "connecting")) return { tone: "warn", label: "Connecting" };
  if (feeds.every((f) => f.status === "open")) return { tone: "live", label: "Live" };
  return { tone: "idle", label: "Idle" };
}

export function ConnectionIndicator({ feeds, className }: { feeds: FeedHealth[]; className?: string }) {
  const now = useNow(true);
  const { tone, label } = aggregateHealth(feeds);
  const liveCount = feeds.filter((f) => f.status === "open").length;

  return (
    <Popover>
      <PopoverTrigger
        className={cn(
          "inline-flex items-center gap-2 rounded-sm border border-hairline bg-surface-1 px-2 py-1 text-[0.6875rem] transition-colors hover:bg-surface-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
          className,
        )}
        aria-label={`Stream health: ${label}`}
      >
        <StatusDot tone={tone} pulse={tone === "live"} />
        <span className="font-medium text-foreground uppercase tracking-[0.12em]">{label}</span>
        <span className="tnum hidden text-muted-foreground sm:inline">
          {liveCount}/{feeds.length}
        </span>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-72 rounded-sm p-0">
        <div className="flex items-center gap-2 border-hairline border-b px-3 py-2">
          <Radio className="size-3.5 text-muted-foreground" aria-hidden />
          <span className="label-micro text-muted-foreground">Event streams</span>
        </div>
        <ul className="divide-y divide-hairline">
          {feeds.length === 0 ? (
            <li className="px-3 py-3 text-muted-foreground text-xs">No live streams on this view.</li>
          ) : (
            feeds.map((feed) => (
              <li key={feed.id} className="flex items-center justify-between gap-3 px-3 py-2">
                <div className="flex min-w-0 items-center gap-2">
                  <StatusDot tone={STATUS_TONE[feed.status]} pulse={feed.status === "open"} />
                  <span className="truncate text-foreground text-xs">{feed.label}</span>
                </div>
                <span className="shrink-0 text-[0.6875rem] text-muted-foreground">
                  {feed.status === "reconnecting" && feed.attempt
                    ? `retry ${feed.attempt}`
                    : formatAge(feed.lastEventAt, now)}
                </span>
              </li>
            ))
          )}
        </ul>
        <div className="flex items-center gap-2 border-hairline border-t px-3 py-2 text-[0.6875rem] text-muted-foreground">
          <Activity className="size-3" aria-hidden />
          Server-sent events, auto-reconnect with backoff
        </div>
      </PopoverContent>
    </Popover>
  );
}
