"use client";

import * as React from "react";

import { Globe } from "lucide-react";

import { t } from "@/i18n";
import { api, sseUrl } from "@/lib/api";
import { egressStatus } from "@/lib/egress-status";
import type { MullvadEgress } from "@/lib/types";
import { cn } from "@/lib/utils";

export function MullvadIndicator() {
  const [egress, setEgress] = React.useState<MullvadEgress>();
  const [unreachable, setUnreachable] = React.useState(false);

  React.useEffect(() => {
    let alive = true;
    const apply = (result: MullvadEgress) => {
      if (!alive) return;
      setEgress(result);
      setUnreachable(false);
    };
    // First paint before the SSE handshake finishes.
    void api
      .mullvadEgress()
      .then(apply)
      .catch(() => {
        if (alive) setUnreachable(true);
      });

    const es = new EventSource(sseUrl("/api/mullvad/egress/stream"));
    es.onmessage = (e) => {
      try {
        apply(JSON.parse(e.data) as MullvadEgress);
      } catch {
        /* ignore malformed frame */
      }
    };
    // EventSource auto-reconnects; keep the last known pill state across gaps.
    return () => {
      alive = false;
      es.close();
    };
  }, []);

  const { route, state, ip } = egressStatus(egress, unreachable);
  let routeLabel = t("Egress");
  if (route === "mullvad") routeLabel = t("Mullvad");
  else if (route === "wg") routeLabel = t("WireGuard");
  else if (route === "proxy") routeLabel = t("Proxy");
  const stateLabel = {
    checking: t("Checking…"),
    working: t("Working"),
    failed: t("Not working"),
    off: t("Off"),
    unverified: t("Health unverified"),
    unavailable: t("Status unavailable"),
  }[state];
  const label = [routeLabel, stateLabel, ip].filter(Boolean).join(" · ");
  const description = [label, state === "working" && egress?.egress_relay, route === "wg" && egress?.global_wg_endpoint]
    .filter(Boolean)
    .join(" · ");

  return (
    <span
      className="inline-flex max-w-44 items-center gap-1.5 rounded-full border bg-background px-2 py-0.5 font-medium text-xs tabular-nums sm:max-w-none"
      title={description}
    >
      <Globe
        className={cn(
          "size-3.5 shrink-0",
          state === "working" && "text-emerald-600 dark:text-emerald-400",
          state === "failed" && "text-destructive",
          state === "unverified" && "text-amber-600 dark:text-amber-400",
        )}
        aria-hidden
      />
      <span className="truncate">{label}</span>
    </span>
  );
}
