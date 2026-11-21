"use client";

import * as React from "react";

import { ShieldCheck, ShieldX } from "lucide-react";

import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { t } from "@/i18n";
import { api } from "@/lib/api";
import type { MullvadEgress } from "@/lib/types";
import { cn } from "@/lib/utils";

// Compact navbar indicator: Mullvad tunnel state plus the public egress IP.
// The backend caches the outbound IP probe for 60s, so a 60s poll is cheap.
export function MullvadIndicator() {
  const [egress, setEgress] = React.useState<MullvadEgress | null>(null);

  React.useEffect(() => {
    let alive = true;
    const load = () => {
      api
        .mullvadEgress()
        .then((e) => alive && setEgress(e))
        .catch(() => undefined);
    };
    load();
    const stop = setInterval(load, 60000);
    return () => {
      alive = false;
      clearInterval(stop);
    };
  }, []);

  if (!egress) return null;
  const up = !!egress.up && !egress.task_egress_blocked;
  const tunneled = up && egress.egress_tunneled;
  const label = up
    ? tunneled
      ? t("Mullvad · {ip}", { ip: egress.egress_ip || "…" })
      : t("Mullvad up · egress {ip}", { ip: egress.egress_ip || "…" })
    : egress.task_egress_blocked
      ? t("Mullvad · egress blocked")
      : t("Mullvad down");

  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <span
            className={cn(
              "flex items-center gap-1.5 font-medium text-xs tabular-nums",
              tunneled ? "text-emerald-600 dark:text-emerald-400" : up ? "text-muted-foreground" : "text-destructive",
            )}
            aria-label={label}
          >
            {tunneled ? <ShieldCheck className="size-3.5" /> : <ShieldX className="size-3.5" />}
            <span className="hidden sm:inline">{label}</span>
          </span>
        </TooltipTrigger>
        <TooltipContent side="bottom">
          <p>
            {t("WireGuard {mode} · interface {iface}", {
              mode: egress.mode || egress.mode_setting || "-",
              iface: egress.interface || "-",
            })}
          </p>
          {egress.egress_relay ? <p>{t("Relay {relay}", { relay: egress.egress_relay })}</p> : null}
          <p>{label}</p>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}
