// biome-ignore-all lint/nursery/noMisusedPromises: React 19 typings widen ReactNode to include
// Promise, so every truthiness check on a ReactNode prop is flagged. These props are always
// synchronously rendered nodes; no promise is ever passed or awaited here.
import type { ReactNode } from "react";

import type { LucideIcon } from "lucide-react";
import { AlertTriangle, Inbox } from "lucide-react";

import { cn } from "@/lib/utils";

/* -------------------------------------------------------------------------- */
/*  Page header                                                                */
/* -------------------------------------------------------------------------- */

export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
  meta,
  className,
}: {
  eyebrow?: string;
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  meta?: ReactNode;
  className?: string;
}) {
  return (
    <header className={cn("flex flex-col gap-3 border-hairline border-b pb-4", className)}>
      <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-3">
        <div className="min-w-0 space-y-1.5">
          {eyebrow ? <div className="label-micro text-muted-foreground">{eyebrow}</div> : null}
          <h1 className="truncate font-semibold text-foreground text-xl tracking-tight md:text-[1.375rem]">{title}</h1>
          {description ? (
            <p className="max-w-2xl text-[0.8125rem] text-muted-foreground leading-relaxed">{description}</p>
          ) : null}
        </div>
        {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
      </div>
      {meta ? (
        <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-muted-foreground text-xs">{meta}</div>
      ) : null}
    </header>
  );
}

/* -------------------------------------------------------------------------- */
/*  Panel — the single surface primitive for dense console content             */
/* -------------------------------------------------------------------------- */

export function Panel({
  title,
  aside,
  children,
  className,
  bodyClassName,
  dense = false,
  flush = false,
}: {
  title?: ReactNode;
  aside?: ReactNode;
  children: ReactNode;
  className?: string;
  bodyClassName?: string;
  dense?: boolean;
  flush?: boolean;
}) {
  return (
    <section
      className={cn("flex min-w-0 flex-col overflow-hidden rounded-sm border border-hairline bg-surface-1", className)}
    >
      {title || aside ? (
        <div className="flex items-center justify-between gap-3 border-hairline border-b px-3 py-2">
          <div className="label-micro truncate text-muted-foreground">{title}</div>
          {aside ? <div className="flex shrink-0 items-center gap-2">{aside}</div> : null}
        </div>
      ) : null}
      <div className={cn("min-w-0 flex-1", !flush && (dense ? "p-2.5" : "p-3.5"), bodyClassName)}>{children}</div>
    </section>
  );
}

/* -------------------------------------------------------------------------- */
/*  Stat tile                                                                  */
/* -------------------------------------------------------------------------- */

export type StatTone = "neutral" | "primary" | "success" | "warning" | "destructive" | "info";

const TONE_TEXT: Record<StatTone, string> = {
  neutral: "text-foreground",
  primary: "text-primary",
  success: "text-success",
  warning: "text-warning",
  destructive: "text-destructive",
  info: "text-info",
};

const TONE_STROKE: Record<StatTone, string> = {
  neutral: "var(--foreground)",
  primary: "var(--primary)",
  success: "var(--success)",
  warning: "var(--warning)",
  destructive: "var(--destructive)",
  info: "var(--info)",
};

export function Sparkline({
  values,
  tone = "primary",
  className,
  height = 28,
}: {
  values: number[];
  tone?: StatTone;
  className?: string;
  height?: number;
}) {
  if (values.length < 2) return <div className={cn("h-7", className)} aria-hidden />;
  const width = 100;
  const max = Math.max(...values, 1);
  const min = Math.min(...values, 0);
  const span = max - min || 1;
  const step = width / (values.length - 1);
  const points = values.map((v, i) => `${(i * step).toFixed(2)},${(height - ((v - min) / span) * height).toFixed(2)}`);
  const line = `M${points.join(" L")}`;
  const area = `${line} L${width},${height} L0,${height} Z`;
  const gradientId = `spark-${tone}`;

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      className={cn("w-full", className)}
      style={{ height }}
      aria-hidden
    >
      <title>Trend</title>
      <defs>
        <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={TONE_STROKE[tone]} stopOpacity="0.28" />
          <stop offset="100%" stopColor={TONE_STROKE[tone]} stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={area} fill={`url(#${gradientId})`} />
      <path d={line} fill="none" stroke={TONE_STROKE[tone]} strokeWidth="1.25" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}

export function StatTile({
  label,
  value,
  unit,
  hint,
  delta,
  tone = "neutral",
  series,
  icon: Icon,
  className,
}: {
  label: string;
  value: ReactNode;
  unit?: string;
  hint?: ReactNode;
  delta?: { value: number; label?: string };
  tone?: StatTone;
  series?: number[];
  icon?: LucideIcon;
  className?: string;
}) {
  const deltaPositive = !delta || delta.value >= 0;
  return (
    <div
      className={cn(
        "group relative flex min-w-0 flex-col justify-between gap-3 rounded-sm border border-hairline bg-surface-1 p-3.5",
        className,
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="label-micro truncate text-muted-foreground">{label}</div>
        {Icon ? <Icon className="size-3.5 shrink-0 text-muted-foreground/70" aria-hidden /> : null}
      </div>
      <div className="flex items-end justify-between gap-3">
        <div className="min-w-0">
          <div
            className={cn(
              "tnum flex items-baseline gap-1 font-semibold text-[1.5rem] leading-none tracking-tight",
              TONE_TEXT[tone],
            )}
          >
            <span className="truncate">{value}</span>
            {unit ? <span className="font-medium text-muted-foreground text-xs">{unit}</span> : null}
          </div>
          {hint ? <div className="mt-1.5 truncate text-[0.6875rem] text-muted-foreground">{hint}</div> : null}
        </div>
        {delta ? (
          <span
            className={cn(
              "tnum shrink-0 rounded-sm px-1.5 py-0.5 font-medium text-[0.6875rem]",
              deltaPositive ? "bg-success/10 text-success" : "bg-destructive/10 text-destructive",
            )}
          >
            {deltaPositive ? "+" : ""}
            {delta.value}
            {delta.label ? ` ${delta.label}` : ""}
          </span>
        ) : null}
      </div>
      {series && series.length > 1 ? <Sparkline values={series} tone={tone} height={22} /> : null}
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/*  Status dot                                                                 */
/* -------------------------------------------------------------------------- */

export type DotTone = "live" | "idle" | "warn" | "error" | "off";

const DOT_COLOR: Record<DotTone, string> = {
  live: "bg-success",
  idle: "bg-muted-foreground",
  warn: "bg-warning",
  error: "bg-destructive",
  off: "bg-hairline-strong",
};

export function StatusDot({ tone, pulse = false, className }: { tone: DotTone; pulse?: boolean; className?: string }) {
  return (
    <span className={cn("relative inline-flex size-2 shrink-0", className)} aria-hidden>
      {pulse && tone === "live" ? (
        <span className={cn("absolute inset-0 animate-ping rounded-full opacity-60", DOT_COLOR[tone])} />
      ) : null}
      <span className={cn("relative inline-flex size-2 rounded-full", DOT_COLOR[tone])} />
    </span>
  );
}

/* -------------------------------------------------------------------------- */
/*  Empty + error states                                                       */
/* -------------------------------------------------------------------------- */

export function EmptyState({
  icon: Icon = Inbox,
  title,
  description,
  action,
  className,
  field = false,
}: {
  icon?: LucideIcon;
  title: string;
  description?: ReactNode;
  action?: ReactNode;
  className?: string;
  /** Draw the faint structural grid behind the icon. */
  field?: boolean;
}) {
  return (
    <div className={cn("relative flex flex-col items-center justify-center gap-3 px-6 py-12 text-center", className)}>
      {field ? <div className="grid-field pointer-events-none absolute inset-0 opacity-40" aria-hidden /> : null}
      <div className="relative flex size-9 items-center justify-center rounded-sm border border-hairline bg-surface-2">
        <Icon className="size-4 text-muted-foreground" aria-hidden />
      </div>
      <div className="relative space-y-1">
        <div className="font-medium text-foreground text-sm">{title}</div>
        {description ? (
          <p className="mx-auto max-w-sm text-muted-foreground text-xs leading-relaxed">{description}</p>
        ) : null}
      </div>
      {action ? <div className="relative">{action}</div> : null}
    </div>
  );
}

export function ErrorState({
  title = "Something went wrong",
  description,
  action,
  className,
}: {
  title?: string;
  description?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col items-center justify-center gap-3 px-6 py-12 text-center", className)}>
      <div className="flex size-9 items-center justify-center rounded-sm border border-destructive/30 bg-destructive/10">
        <AlertTriangle className="size-4 text-destructive" aria-hidden />
      </div>
      <div className="space-y-1">
        <div className="font-medium text-foreground text-sm">{title}</div>
        {description ? (
          <p className="mx-auto max-w-md text-muted-foreground text-xs leading-relaxed">{description}</p>
        ) : null}
      </div>
      {action ? <div>{action}</div> : null}
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/*  Key/value row used inside dense panels                                     */
/* -------------------------------------------------------------------------- */

export function KV({ k, children, mono }: { k: string; children: ReactNode; mono?: boolean }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-1.5">
      <span className="label-micro shrink-0 text-muted-foreground">{k}</span>
      <span className={cn("min-w-0 truncate text-right text-xs", mono && "tnum font-mono")}>{children}</span>
    </div>
  );
}
