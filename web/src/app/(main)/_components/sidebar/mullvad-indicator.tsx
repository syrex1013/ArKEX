"use client";

import * as React from "react";

import { Globe } from "lucide-react";

import { t } from "@/i18n";
import { api } from "@/lib/api";
import { egressStatus } from "@/lib/egress-status";
import { setVisibleInterval } from "@/lib/polling";
import type { MullvadEgress } from "@/lib/types";
import { cn } from "@/lib/utils";

export function MullvadIndicator() {
  const [egress, setEgress] = React.useState<MullvadEgress>();
  const [unreachable, setUnreachable] = React.useState(false);

  React.useEffect(() => {
    let alive = true;
    let pending: Promise<void> | undefined;
    const load = () => {
      pending ??= api
        .mullvadEgress()
        .then((result) => {
          if (!alive) return;
          setEgress(result);
          setUnreachable(false);
        })
        .catch(() => {
          if (alive) setUnreachable(true);
        })
        .finally(() => {
          pending = undefined;
        });
      return pending;
    };
    void load();
    const stop = setVisibleInterval(load, 60_000);
    return () => {
      alive = false;
      stop();
    };
  }, []);

  const { route, state, ip } = egressStatus(egress, unreachable);
  const routeLabel = route === "mullvad" ? t("Mullvad") : route === "proxy" ? t("Proxy") : t("Egress");
  const stateLabel = {
    checking: t("Checking…"),
    working: t("Working"),
    failed: t("Not working"),
    off: t("Off"),
    unverified: t("Health unverified"),
    unavailable: t("Status unavailable"),
  }[state];
  const label = [routeLabel, stateLabel, ip].filter(Boolean).join(" · ");
  const description = [label, state === "working" && egress?.egress_relay].filter(Boolean).join(" · ");

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
