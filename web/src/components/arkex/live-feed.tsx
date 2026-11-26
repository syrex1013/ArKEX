"use client";

import { useEffect, useMemo, useRef, useState } from "react";

import { ArrowDownToLine, Pause, Play, Trash2 } from "lucide-react";

import { StreamStatusPill } from "@/components/arkex/connection-indicator";
import { useLiveLog } from "@/components/arkex/live-stream-provider";
import { Panel, StatusDot } from "@/components/arkex/primitives";
import { Button } from "@/components/ui/button";
import type { StreamStatus } from "@/lib/sse";
import type { LogLine } from "@/lib/types";
import { cn } from "@/lib/utils";

type LevelFilter = "all" | "info" | "warn" | "error";

const LEVEL_FILTERS: { id: LevelFilter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "info", label: "Info" },
  { id: "warn", label: "Warn" },
  { id: "error", label: "Error" },
];

const LEVEL_RANK: Record<string, number> = { info: 0, warn: 1, error: 2 };

function levelClass(level: string): string {
  switch (level) {
    case "error":
      return "text-destructive";
    case "warn":
      return "text-warning";
    default:
      return "text-muted-foreground";
  }
}

function streamHint(status: StreamStatus): { title: string; detail: string } {
  if (status === "closed") {
    return {
      title: "Stream disconnected",
      detail: "The backend rejected or dropped the connection. Reconnect to resume the tail.",
    };
  }
  if (status === "open") {
    return { title: "Waiting for events", detail: "Live output appears here as the server emits it." };
  }
  return { title: "Connecting to log stream", detail: "Opening the server-sent event channel." };
}

function clock(ts: string | undefined): string {
  if (!ts) return "--:--:--";
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return "--:--:--";
  return d.toLocaleTimeString([], { hour12: false, hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

/**
 * Live tail of the backend log (`GET /api/logs/stream`).
 * Renders only real server events — nothing is simulated when the stream is down.
 */
export function LiveFeed({
  height = "h-72",
  className,
  title = "Backend log",
}: {
  height?: string;
  className?: string;
  title?: string;
}) {
  const { events, status, attempt, lastEventAt, lastError, total, clear, restart } = useLiveLog();

  const [level, setLevel] = useState<LevelFilter>("all");
  const [paused, setPaused] = useState(false);
  const [follow, setFollow] = useState(true);
  const [frozen, setFrozen] = useState<LogLine[]>([]);

  const scrollRef = useRef<HTMLDivElement | null>(null);

  // Freeze the visible list while paused without dropping the stream.
  useEffect(() => {
    if (paused) setFrozen(events.map((e) => e.data));
  }, [paused, events]);

  const source = paused ? frozen : events.map((e) => e.data);

  const rows = useMemo(() => {
    const minRank = level === "all" ? -1 : (LEVEL_RANK[level] ?? 0);
    const filtered = source.filter((line) => (LEVEL_RANK[line.level] ?? 0) >= minRank);
    return filtered.slice(-400);
  }, [source, level]);

  useEffect(() => {
    if (!follow || paused) return;
    const el = scrollRef.current;
    if (!el) return;
    el.scrollTop = el.scrollHeight;
  }, [follow, paused]);

  const onScroll = () => {
    const el = scrollRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
    if (atBottom !== follow) setFollow(atBottom);
  };

  const errorCount = source.reduce((n, line) => n + (line.level === "error" ? 1 : 0), 0);
  const empty = streamHint(status);

  return (
    <Panel
      title={title}
      flush
      className={className}
      aside={
        <>
          <span className="tnum hidden text-[0.6875rem] text-muted-foreground sm:inline">
            {total.toLocaleString()} events
          </span>
          {errorCount > 0 ? (
            <span className="tnum inline-flex items-center gap-1 text-[0.6875rem] text-destructive">
              <StatusDot tone="error" />
              {errorCount}
            </span>
          ) : null}
          <StreamStatusPill status={status} attempt={attempt} lastEventAt={lastEventAt} />
        </>
      }
    >
      <div className="flex items-center justify-between gap-2 border-hairline border-b px-2 py-1.5">
        <div className="flex items-center gap-0.5">
          {LEVEL_FILTERS.map((f) => (
            <button
              key={f.id}
              type="button"
              onClick={() => setLevel(f.id)}
              className={cn(
                "rounded-sm px-1.5 py-1 font-medium text-[0.6875rem] transition-colors",
                level === f.id
                  ? "bg-surface-3 text-foreground"
                  : "text-muted-foreground hover:bg-surface-2 hover:text-foreground",
              )}
              aria-pressed={level === f.id}
            >
              {f.label}
            </button>
          ))}
        </div>
        <div className="flex items-center gap-0.5">
          <Button
            size="icon-xs"
            variant="ghost"
            onClick={() => setFollow((v) => !v)}
            title={follow ? "Stop following" : "Follow latest"}
            aria-pressed={follow}
          >
            <ArrowDownToLine className={cn(!follow && "opacity-40")} />
          </Button>
          <Button
            size="icon-xs"
            variant="ghost"
            onClick={() => setPaused((v) => !v)}
            title={paused ? "Resume rendering" : "Pause rendering"}
          >
            {paused ? <Play /> : <Pause />}
          </Button>
          <Button
            size="icon-xs"
            variant="ghost"
            onClick={() => {
              clear();
              setFrozen([]);
            }}
            title="Clear buffer"
          >
            <Trash2 />
          </Button>
        </div>
      </div>

      {lastError ? (
        <div className="flex items-center justify-between gap-3 border-destructive/30 border-b bg-destructive/10 px-2.5 py-1.5">
          <span className="min-w-0 truncate text-[0.6875rem] text-destructive">{lastError}</span>
          <Button size="xs" variant="outline" onClick={restart}>
            Reconnect
          </Button>
        </div>
      ) : null}

      <div
        ref={scrollRef}
        onScroll={onScroll}
        className={cn(
          "scroll-fade-b overflow-y-auto overflow-x-hidden bg-surface-1 font-mono text-[0.6875rem] leading-[1.6]",
          height,
        )}
      >
        {rows.length === 0 ? (
          <div className="flex h-full min-h-24 flex-col items-center justify-center gap-2 text-center">
            <div className="flex items-center gap-2 text-muted-foreground">
              {status === "connecting" || status === "reconnecting" ? (
                <span className="inline-flex size-1.5 animate-pulse rounded-full bg-warning" aria-hidden />
              ) : (
                <StatusDot tone={status === "closed" ? "error" : "idle"} />
              )}
              <span className="text-[0.6875rem]">{empty.title}</span>
            </div>
            <span className="max-w-xs text-[0.625rem] text-muted-foreground/70">{empty.detail}</span>
          </div>
        ) : (
          <ul className="py-1">
            {rows.map((line) => (
              <li
                key={`${line.seq}:${line.db_id ?? "-"}`}
                className="group flex items-baseline gap-2 px-2.5 py-[1px] hover:bg-surface-2"
              >
                <span className="tnum shrink-0 text-muted-foreground/60">{clock(line.ts)}</span>
                <span className={cn("w-9 shrink-0 uppercase", levelClass(line.level))}>{line.level}</span>
                {line.tag ? <span className="shrink-0 text-primary/80">[{line.tag}]</span> : null}
                <span className="min-w-0 flex-1 truncate text-foreground/90" title={line.text}>
                  {line.text}
                </span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Panel>
  );
}
